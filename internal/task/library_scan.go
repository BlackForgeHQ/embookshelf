package task

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/riverqueue/river"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/hashing"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/scan"
	"github.com/blackforge/embookshelf/internal/service"
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
	// LibStore turns a libraryID into a ready-to-use {Library, Storage}
	// view. Required — without it LibraryScan returns early.
	LibStore service.LibraryStore
	// Files is the storage_v2 file repo. Required by the two-phase
	// walk+diff pipeline. nil → scan returns early.
	Files *repo.FileRepo
}

// LibraryScan walks a library's filesystem root and stages every
// unseen supported file into the bookdrop queue. It does not
// extract metadata — that's BookDropIngest's job, fired for each
// enqueued item. Returning an error from this function asks the
// caller to retry the whole scan; per-file errors are logged and
// skipped without aborting.
func LibraryScan(ctx context.Context, args LibraryScanArgs, deps LibraryScanDeps) error {
	if deps.LibStore == nil || deps.Files == nil {
		slog.Warn("library scan: not wired (missing LibStore or Files)",
			"library_id", args.LibraryID)
		return nil
	}
	handle, err := deps.LibStore.For(ctx, args.LibraryID)
	if err != nil {
		return err
	}
	lib := handle.Library
	if handle.Storage == nil {
		slog.Warn("library scan: no storage for library, skipping", "library_id", lib.ID)
		return nil
	}
	store := handle.Storage
	root := lib.Path
	if lib.Root != nil && *lib.Root != "" {
		root = *lib.Root
	}
	if root == "" {
		slog.Warn("library scan: empty root, skipping", "library_id", lib.ID)
		return nil
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
		e.Location = handle.Relativize("/" + e.Location)
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

// LibraryScanWorker is the River adapter for LibraryScan.
type LibraryScanWorker struct {
	river.WorkerDefaults[LibraryScanArgs]
	Deps LibraryScanDeps
}

func (w *LibraryScanWorker) Work(ctx context.Context, job *river.Job[LibraryScanArgs]) error {
	return LibraryScan(ctx, job.Args, w.Deps)
}
