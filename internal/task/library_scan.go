package task

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"

	"github.com/riverqueue/river"

	"github.com/blackforge/embookshelf/internal/fileproc"
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
	// Storage reads the library's filesystem (or object store) during
	// the walk. Plan A only uses List; future plans use Get/Head for
	// content-hash computation and metadata extraction.
	Storage storage.Storage
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
	if root == "" {
		slog.Warn("library scan: empty path, skipping", "library_id", lib.ID)
		return nil
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
