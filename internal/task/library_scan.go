package task

import (
	"context"
	"io/fs"
	"log/slog"
	"path/filepath"

	"github.com/riverqueue/river"

	"github.com/blackforge/embookshelf/internal/fileproc"
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
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !fileproc.IsSupported(p) {
			return nil
		}
		fileCount++

		already, err := deps.Lib.BookExistsByPath(ctx, p)
		if err != nil {
			slog.Warn("library scan: book exists check", "path", p, "err", err)
			return nil
		}
		if already {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		format := fileproc.FormatForExt(filepath.Ext(p))

		item, created, err := deps.BookDrop.Enqueue(ctx, p, format, info.Size())
		if err != nil {
			slog.Warn("library scan: enqueue", "path", p, "err", err)
			return nil
		}
		if !created {
			return nil
		}
		if deps.Queue != nil {
			if err := deps.Queue.EnqueueBookDrop(ctx, item.ID); err != nil {
				slog.Warn("library scan: enqueue queue job", "id", item.ID, "err", err)
			}
		}
		discovered++
		return nil
	})
	if walkErr != nil {
		slog.Warn("library scan: walk failed", "path", root, "err", walkErr)
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
