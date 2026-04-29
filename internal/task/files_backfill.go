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
// pass needs. The Storage map is keyed by storage_backend.ID so the
// worker can resolve a file's storage when the project grows beyond
// a single backend (Plan F).
type FilesBackfillDeps struct {
	Files     *repo.FileRepo
	Libraries *repo.LibraryRepo
	Backends  *repo.StorageBackendRepo
	// Storage is the single LocalFS backend constructed in main.go
	// today. Plan F replaces this with a per-backend resolver.
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
	if deps.Storage == nil || deps.Files == nil {
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

			hash, size, err := hashing.HashFile(ctx, deps.Storage, key)
			if err != nil {
				slog.Warn("files backfill: hash failed",
					"file_id", f.ID, "key", key, "err", err)
				skipped[f.ID] = true
				continue
			}
			info, headErr := deps.Storage.Head(ctx, key)
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
