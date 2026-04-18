// Package task holds river job args + workers. Each file corresponds to one
// job kind. Jobs are enqueued through the service layer (which knows about
// river) and executed in the background by the client started in main().
package task

import (
	"context"
	"errors"
	"log/slog"

	"github.com/riverqueue/river"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/service"
)

// BookDropIngestArgs is the payload for processing a single bookdrop item.
type BookDropIngestArgs struct {
	ItemID string `json:"item_id"`
}

// Kind is the river job name. Must be stable — changing it orphans in-flight jobs.
func (BookDropIngestArgs) Kind() string { return "bookdrop.ingest" }

// BookDropWorker runs the ingest pipeline against one bookdrop item: load,
// extract metadata, record results. Transient errors fail the river job (so
// it retries); permanent errors transition the item into 'failed' for review.
type BookDropWorker struct {
	river.WorkerDefaults[BookDropIngestArgs]
	Svc *service.BookDropService
}

func (w *BookDropWorker) Work(ctx context.Context, job *river.Job[BookDropIngestArgs]) error {
	itemID := job.Args.ItemID
	item, err := w.Svc.Get(ctx, itemID)
	if err != nil {
		return err // transient DB error — let river retry
	}

	if err := w.Svc.BeginProcessing(ctx, itemID); err != nil {
		return err
	}

	proc, format, err := fileproc.Dispatch(item.Path)
	if err != nil {
		if errors.Is(err, fileproc.ErrUnsupportedFormat) {
			// Unsupported format is a permanent failure — surface to the user instead of retrying.
			_ = w.Svc.Fail(ctx, itemID, err)
			return nil
		}
		return err
	}
	_ = format // already stored on the item at enqueue time

	meta, err := proc.Extract(ctx, item.Path)
	if err != nil {
		slog.Warn("bookdrop extract failed", "item_id", itemID, "path", item.Path, "err", err)
		_ = w.Svc.Fail(ctx, itemID, err)
		return nil
	}

	return w.Svc.RecordMetadata(
		ctx, itemID,
		meta.Title, meta.Author, meta.Description, meta.Language,
		meta.CoverBytes, meta.CoverMime,
	)
}
