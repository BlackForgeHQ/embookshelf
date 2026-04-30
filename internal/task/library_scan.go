package task

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/riverqueue/river"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/hashing"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/scan"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
)

// LibraryScanArgs is the payload for walking a library's filesystem
// root. The library id also names the scan — each library owns
// exactly one path since migration 000018.
type LibraryScanArgs struct {
	LibraryID string `json:"library_id"`
}

func (LibraryScanArgs) Kind() string { return "library.scan" }

// BookDropEnqueuer is the slice of the queue client that the scan
// task needs. Defined here so the task package doesn't import queue
// (avoids the queue↔task cycle — queue already imports task to
// register workers).
type BookDropEnqueuer interface {
	EnqueueBookDrop(ctx context.Context, itemID string) error
}

// LibraryScanDeps groups the services LibraryScan needs, plus the
// enqueuer used to schedule child BookDropIngest jobs for newly
// discovered files.
type LibraryScanDeps struct {
	BookDrop *service.BookDropService
	Lib      *service.LibraryService
	Queue    BookDropEnqueuer
	// Resolver maps a library's backend_id to the right Storage. Plan F+.
	// When non-nil, LibraryScan resolves the backend per library instead of
	// using the single Storage field.
	Resolver storage.Resolver
	// Storage is the legacy single-backend field. Still used when Resolver
	// is nil (e.g. SQLite queue tests that pass nil for both).
	Storage storage.Storage
	// Files is the storage_v2 file repo. When non-nil the scan uses the
	// two-phase walk+diff pipeline. nil disables the new pipeline and
	// falls back to the legacy BookExistsByPath check.
	Files *repo.FileRepo
}

// LibraryScan walks a library's filesystem root and stages every
// unseen supported file into the bookdrop queue. It does not
// extract metadata — that's BookDropIngest's job, fired for each
// enqueued item. Returning an error from this function asks the
// caller to retry the whole scan; per-file errors are logged and
// skipped without aborting.
func LibraryScan(ctx context.Context, args LibraryScanArgs, deps LibraryScanDeps) error {
	lib, err := deps.Lib.GetByID(ctx, args.LibraryID)
	if err != nil {
		return err
	}
	root := lib.Path
	if lib.Root != nil && *lib.Root != "" {
		root = *lib.Root
	}
	if root == "" {
		slog.Warn("library scan: empty root, skipping", "library_id", lib.ID)
		return nil
	}

	// Resolve the Storage for this library's backend. When a Resolver is
	// configured, use it; otherwise fall back to the legacy single-Storage
	// field. A nil resolver AND nil storage → legacy scan (SQLite queue tests).
	store := deps.Storage
	if deps.Resolver != nil {
		backendID := orZero(lib.BackendID)
		resolved, resolveErr := deps.Resolver.Resolve(backendID)
		if resolveErr != nil {
			slog.Warn("library scan: backend resolve failed, falling back to legacy scan",
				"library_id", lib.ID, "backend_id", backendID, "err", resolveErr)
			return legacyScan(ctx, lib, deps)
		}
		store = resolved
	}

	// Fall back to legacy implementation when storage is missing (e.g. in
	// the SQLite queue tests that pass nil for both).
	if store == nil || deps.Files == nil {
		return legacyScan(ctx, lib, deps)
	}

	// Phase 1: read DB state.
	dbFiles, err := deps.Files.ListByLibrary(ctx, lib.ID)
	if err != nil {
		return errors.New("list db files: " + err.Error())
	}

	// Phase 2: walk storage and collect WalkEntry slice.
	var walked []scan.WalkEntry
	entries, errc := scan.Walk(ctx, store, root)
	for e := range entries {
		// Convert the absolute key (LocalFS rooted at "/") to library-
		// relative location so it matches what the DB stores.
		e.Location = relativizeLocation(lib, "/"+e.Location)
		walked = append(walked, e)
	}
	if walkErr := <-errc; walkErr != nil && !errors.Is(walkErr, context.Canceled) {
		slog.Warn("library scan: walk error", "library_id", lib.ID, "err", walkErr)
	}

	// Phase 3: diff.
	cs := scan.Diff(walked, dbFiles)

	// Phase 4: act on each category.
	fileCount := len(walked)
	discovered := 0

	// Unchanged: clear missing flag if file reappeared.
	for _, f := range cs.Unchanged {
		if f.MissingSince != nil {
			if err := deps.Files.ClearMissing(ctx, f.ID); err != nil {
				slog.Warn("library scan: clear missing", "id", f.ID, "err", err)
			}
		}
	}

	// Changed: re-hash and update the row in place (or reattach on rename).
	for _, ce := range cs.Changed {
		key := joinKey(root, ce.Walk.Location)
		hash, size, herr := hashing.HashFile(ctx, store, key)
		if herr != nil {
			slog.Warn("library scan: rehash failed", "loc", ce.Walk.Location, "err", herr)
			continue
		}
		reattached, rerr := scan.MaybeReattach(ctx, deps.Files, lib.ID, hash, ce.Walk.Location, ce.DB.ID)
		if rerr != nil {
			slog.Warn("library scan: reattach failed", "loc", ce.Walk.Location, "err", rerr)
		} else if reattached {
			continue
		}
		if err := deps.Files.SetContentHash(ctx, ce.DB.ID, hash, size, ce.Walk.Mtime); err != nil {
			slog.Warn("library scan: update changed row", "id", ce.DB.ID, "err", err)
		}
	}

	// New: enqueue supported files through the bookdrop pipeline.
	for _, w := range cs.New {
		absPath := joinKey(root, w.Location)
		// fileproc.IsSupported expects a slash-prefixed path.
		if !fileproc.IsSupported("/" + absPath) {
			continue
		}
		format := fileproc.FormatForExt(filepath.Ext(w.Location))
		// Bookdrop still wants the absolute path with leading slash.
		fullPath := "/" + absPath
		item, created, err := deps.BookDrop.Enqueue(ctx, fullPath, format, w.Size)
		if err != nil {
			slog.Warn("library scan: enqueue", "path", fullPath, "err", err)
			continue
		}
		if !created {
			continue
		}
		if deps.Queue != nil {
			if err := deps.Queue.EnqueueBookDrop(ctx, item.ID); err != nil {
				slog.Warn("library scan: enqueue queue job", "id", item.ID, "err", err)
			}
		}
		discovered++
	}

	// Missing: soft-flag files for the 24h purge sweeper.
	for _, f := range cs.Missing {
		if f.MissingSince != nil {
			continue // already marked
		}
		if err := deps.Files.MarkMissing(ctx, f.ID, time.Now()); err != nil {
			slog.Warn("library scan: mark missing", "id", f.ID, "err", err)
		}
	}

	if err := deps.Lib.TouchScan(ctx, lib.ID, fileCount, discovered); err != nil {
		slog.Warn("library scan: touch", "id", lib.ID, "err", err)
	}
	slog.Info("library scan done",
		"library", lib.ID, "root", root,
		"files", fileCount, "discovered", discovered,
		"changed", len(cs.Changed), "missing", len(cs.Missing),
	)
	return nil
}

// legacyScan is the pre-Plan-C implementation kept as a fallback for
// code paths where deps.Files or deps.Storage is nil (e.g. the SQLite
// queue test that passes nil for both). It walks storage directly and
// de-dupes by BookExistsByPath.
func legacyScan(ctx context.Context, lib model.Library, deps LibraryScanDeps) error {
	root := lib.Path
	if lib.Root != nil && *lib.Root != "" {
		root = *lib.Root
	}

	var fileCount, discovered int
	if deps.Storage == nil {
		slog.Warn("library scan: storage is nil, skipping", "library_id", lib.ID)
		_ = deps.Lib.TouchScan(ctx, lib.ID, 0, 0)
		return nil
	}
	it, err := deps.Storage.List(ctx, root)
	if err != nil {
		slog.Warn("library scan: list failed", "path", root, "err", err)
		_ = deps.Lib.TouchScan(ctx, lib.ID, 0, 0)
		return nil
	}
	defer func() { _ = it.Close() }()

	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			slog.Warn("library scan: iteration error", "path", root, "err", err)
			break
		}
		// LocalFS keys are slash-paths under the backend root; the
		// backend is rooted at "/" today (Plan A) so obj.Key starts
		// with the absolute library path. Plan B reroots backends
		// per-library and obj.Key becomes library-relative.
		p := "/" + obj.Key
		if !fileproc.IsSupported(p) {
			continue
		}
		fileCount++

		relLoc := relativizeLocation(lib, p)

		if deps.Files != nil {
			exists, err := deps.Files.ExistsByLocation(ctx, lib.ID, relLoc)
			if err != nil {
				slog.Warn("library scan: files exists check", "lib", lib.ID, "loc", relLoc, "err", err)
				// fall through to legacy check
			} else if exists {
				continue
			}
		}

		already, err := deps.Lib.BookExistsByPath(ctx, p)
		if err != nil {
			slog.Warn("library scan: book exists check", "path", p, "err", err)
			continue
		}
		if already {
			continue
		}

		format := fileproc.FormatForExt(filepath.Ext(p))

		item, created, err := deps.BookDrop.Enqueue(ctx, p, format, obj.Size)
		if err != nil {
			slog.Warn("library scan: enqueue", "path", p, "err", err)
			continue
		}
		if !created {
			continue
		}
		if deps.Queue != nil {
			if err := deps.Queue.EnqueueBookDrop(ctx, item.ID); err != nil {
				slog.Warn("library scan: enqueue queue job", "id", item.ID, "err", err)
			}
		}
		discovered++
	}

	if err := deps.Lib.TouchScan(ctx, lib.ID, fileCount, discovered); err != nil {
		slog.Warn("library scan: touch", "id", lib.ID, "err", err)
	}
	slog.Info("library scan done", "library", lib.ID, "path", root, "files", fileCount, "discovered", discovered)
	return nil
}

// LibraryScanWorker is the River adapter for LibraryScan.
type LibraryScanWorker struct {
	river.WorkerDefaults[LibraryScanArgs]
	Deps LibraryScanDeps
}

func (w *LibraryScanWorker) Work(ctx context.Context, job *river.Job[LibraryScanArgs]) error {
	return LibraryScan(ctx, job.Args, w.Deps)
}

// relativizeLocation strips the library root from abs, returning the
// path the files table stores. If abs doesn't fall under the library
// root (a corner case Plan B's backfill stores verbatim), return abs.
func relativizeLocation(lib model.Library, abs string) string {
	root := ""
	if lib.Root != nil {
		root = *lib.Root
	}
	if root == "" {
		root = lib.Path
	}
	if root == "" {
		return abs
	}
	prefix := root
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if strings.HasPrefix(abs, prefix) {
		return abs[len(prefix):]
	}
	return abs
}

// orZero dereferences a *string, returning "" when nil.
func orZero(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
