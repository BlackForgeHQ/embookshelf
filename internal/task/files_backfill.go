package task

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/hashing"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/storage"
)

// FilesBackfillDeps groups the dependencies the boot-time hashing
// pass needs.
type FilesBackfillDeps struct {
	Files     *repo.FileRepo
	Libraries *repo.LibraryRepo
	Backends  *repo.StorageBackendRepo
	// Resolver maps a library's backend_id to the right Storage (Plan F).
	// When non-nil, it takes precedence over the legacy Storage field.
	Resolver storage.Resolver
	// Storage is the legacy single-backend field kept for backward compat.
	// Used when Resolver is nil.
	Storage   storage.Storage
	BatchSize int           // 0 → 100
	Sleep     time.Duration // pause between batches; 0 → no pause
}

// RunFilesBackfill drains files.content_hash IS NULL by streaming
// each file through sha256 and updating the row. Idempotent: missing
// files (Storage.Get → ErrNotFound) are skipped and logged; the row
// stays NULL so a future run retries when the file reappears.
//
// Returns nil when no rows remain pending. Caller is expected to
// invoke this once at startup; rerunning is safe.
func RunFilesBackfill(ctx context.Context, deps FilesBackfillDeps) error {
	if deps.Files == nil {
		return nil // not yet wired
	}
	if deps.Resolver == nil && deps.Storage == nil {
		return nil // not yet wired
	}
	batchSize := deps.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}

	total := 0
	// skipped tracks IDs that failed in this run so we don't re-fetch them
	// from ListPendingHash on the next iteration (they stay NULL until a
	// future boot).
	skipped := make(map[string]bool)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, err := deps.Files.ListPendingHash(ctx, batchSize+len(skipped))
		if err != nil {
			return err
		}

		// Filter out rows we already attempted this run.
		pending := batch[:0]
		for _, f := range batch {
			if !skipped[f.ID] {
				pending = append(pending, f)
			}
		}
		// Limit to batchSize after filtering.
		if len(pending) > batchSize {
			pending = pending[:batchSize]
		}

		if len(pending) == 0 {
			if total > 0 {
				slog.Info("files backfill complete", "hashed", total)
			}
			return nil
		}
		for _, f := range pending {
			// Resolve key: <library.root>/<location>
			lib, err := deps.Libraries.GetByID(ctx, f.LibraryID)
			if err != nil {
				slog.Warn("files backfill: library lookup failed",
					"file_id", f.ID, "library_id", f.LibraryID, "err", err)
				skipped[f.ID] = true
				continue
			}
			root := ""
			if lib.Root != nil {
				root = *lib.Root
			}
			if root == "" && lib.Path != "" {
				root = lib.Path // fall back during transition (Plan B keeps Path)
			}
			key := joinKey(root, f.Location)

			// Resolve the storage for this library's backend.
			store := deps.Storage
			if deps.Resolver != nil {
				backendID := orZero(lib.BackendID)
				resolved, resolveErr := deps.Resolver.Resolve(backendID)
				if resolveErr != nil {
					slog.Warn("files backfill: backend resolve failed",
						"file_id", f.ID, "backend_id", backendID, "err", resolveErr)
					skipped[f.ID] = true
					continue
				}
				store = resolved
			}

			hash, size, err := hashing.HashFile(ctx, store, key)
			if err != nil {
				slog.Warn("files backfill: hash failed",
					"file_id", f.ID, "key", key, "err", err)
				skipped[f.ID] = true
				continue
			}
			info, headErr := store.Head(ctx, key)
			mtime := time.Now()
			if headErr == nil {
				mtime = info.ModTime
			}

			if err := deps.Files.SetContentHash(ctx, f.ID, hash, size, mtime); err != nil {
				slog.Warn("files backfill: set hash failed",
					"file_id", f.ID, "err", err)
				skipped[f.ID] = true
				continue
			}
			total++
		}
		if deps.Sleep > 0 {
			select {
			case <-time.After(deps.Sleep):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// joinKey concatenates a backend root with a file location into a
// single slash-separated key. Strips trailing slashes from root so
// "<root>" + "/" + "<loc>" never produces a double slash.
func joinKey(root, loc string) string {
	root = strings.TrimRight(root, "/")
	loc = strings.TrimLeft(loc, "/")
	if root == "" {
		return loc
	}
	if loc == "" {
		return root
	}
	return root + "/" + loc
}
