// Package task holds job args, business-logic functions, and River
// adapters. The pure functions (BookDropIngest, LibraryScan) are
// dialect-agnostic; River workers and the SQLite queue both call
// them through their respective dispatch paths.
package task

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"os"

	"github.com/riverqueue/river"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/service"
)

// BookDropIngestArgs is the payload for processing a single bookdrop item.
type BookDropIngestArgs struct {
	ItemID string `json:"item_id"`
}

// Kind is the job name used by both River and the SQLite queue.
// Must be stable — changing it orphans in-flight jobs.
func (BookDropIngestArgs) Kind() string { return "bookdrop.ingest" }

// BookDropDeps groups the services BookDropIngest needs.
type BookDropDeps struct {
	Svc *service.BookDropService
}

// BookDropIngest runs the ingest pipeline for one bookdrop item:
// load, extract metadata, record results. Transient errors are
// returned for the caller to retry. Permanent errors transition the
// item into 'failed' for review and return nil so the caller does
// not retry.
func BookDropIngest(ctx context.Context, args BookDropIngestArgs, deps BookDropDeps) error {
	itemID := args.ItemID
	item, err := deps.Svc.Get(ctx, itemID)
	if err != nil {
		return err
	}
	if err := deps.Svc.BeginProcessing(ctx, itemID); err != nil {
		return err
	}

	// Compute the content hash from a dedicated read pass. EPUB/PDF/CBZ
	// need random access for metadata extraction, so we don't share the
	// stream with the processor — one extra full read is cheap relative
	// to the metadata work itself.
	if hash, err := hashFile(ctx, item.Path); err != nil {
		slog.Warn("bookdrop hash failed", "item_id", itemID, "path", item.Path, "err", err)
		// Non-fatal: missing or unreadable file → mark as failed and stop.
		_ = deps.Svc.Fail(ctx, itemID, err)
		return nil
	} else {
		if err := deps.Svc.SetContentHash(ctx, itemID, hash); err != nil {
			slog.Warn("bookdrop set hash failed", "item_id", itemID, "err", err)
			// Persistence failures retry-eligible — return the error.
			return err
		}
	}

	proc, format, err := fileproc.Dispatch(item.Path)
	if err != nil {
		if errors.Is(err, fileproc.ErrUnsupportedFormat) {
			_ = deps.Svc.Fail(ctx, itemID, err)
			return nil
		}
		return err
	}
	_ = format

	meta, err := proc.Extract(ctx, item.Path)
	if err != nil {
		slog.Warn("bookdrop extract failed", "item_id", itemID, "path", item.Path, "err", err)
		_ = deps.Svc.Fail(ctx, itemID, err)
		return nil
	}

	return deps.Svc.RecordMetadata(
		ctx, itemID,
		meta.Title, meta.Author, meta.Description, meta.Language,
		meta.CoverBytes, meta.CoverMime,
	)
}

// hashFile streams item.Path through sha256 and returns the digest.
// Used during bookdrop ingest so the hash is recorded alongside the
// approval-time metadata, well before the file is moved into the
// library tree.
func hashFile(_ context.Context, path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// BookDropWorker is the River adapter for BookDropIngest. River
// constructs the worker once per process; the queue layer wires
// Deps when registering it.
type BookDropWorker struct {
	river.WorkerDefaults[BookDropIngestArgs]
	Deps BookDropDeps
}

func (w *BookDropWorker) Work(ctx context.Context, job *river.Job[BookDropIngestArgs]) error {
	return BookDropIngest(ctx, job.Args, w.Deps)
}
