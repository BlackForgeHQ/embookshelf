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

// LibraryScanArgs is the payload for walking a single library path.
type LibraryScanArgs struct {
	PathID string `json:"path_id"`
}

func (LibraryScanArgs) Kind() string { return "library.scan" }

// BookDropEnqueuer is the slice of the queue client that the scan worker
// needs. Defined here so the task package doesn't import queue (avoids the
// queue↔task cycle — queue already imports task to register workers).
type BookDropEnqueuer interface {
	EnqueueBookDrop(ctx context.Context, itemID string) error
}

// LibraryScanWorker walks a library_paths row and stages every unseen
// supported file into the bookdrop queue. It doesn't extract metadata
// itself — that's the BookDropWorker's job, which fires for each enqueued
// item.
type LibraryScanWorker struct {
	river.WorkerDefaults[LibraryScanArgs]
	Paths    *service.LibraryPathService
	BookDrop *service.BookDropService
	Lib      *service.LibraryService
	Queue    BookDropEnqueuer
}

func (w *LibraryScanWorker) Work(ctx context.Context, job *river.Job[LibraryScanArgs]) error {
	path, err := w.Paths.Get(ctx, job.Args.PathID)
	if err != nil {
		return err
	}

	var fileCount, discovered int
	walkErr := filepath.WalkDir(path.Path, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip unreadable entries without aborting the whole scan
		}
		if d.IsDir() {
			return nil
		}
		if !fileproc.IsSupported(p) {
			return nil
		}
		fileCount++

		// Skip files already imported into a books row.
		already, err := w.Lib.BookExistsByPath(ctx, p)
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

		item, created, err := w.BookDrop.Enqueue(ctx, p, format, info.Size())
		if err != nil {
			slog.Warn("library scan: enqueue", "path", p, "err", err)
			return nil
		}
		if !created {
			return nil // already in bookdrop_items from a previous scan or the watcher
		}
		if w.Queue != nil {
			if err := w.Queue.EnqueueBookDrop(ctx, item.ID); err != nil {
				slog.Warn("library scan: enqueue river job", "id", item.ID, "err", err)
			}
		}
		discovered++
		return nil
	})
	if walkErr != nil {
		// Log but still record what we got — partial scans are useful.
		slog.Warn("library scan: walk failed", "path", path.Path, "err", walkErr)
	}

	if err := w.Paths.TouchScan(ctx, path.ID, fileCount, discovered); err != nil {
		slog.Warn("library scan: touch", "id", path.ID, "err", err)
	}
	slog.Info("library scan done", "path", path.Path, "files", fileCount, "discovered", discovered)
	return nil
}
