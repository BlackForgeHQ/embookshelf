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
	"path/filepath"
	"strings"

	"github.com/riverqueue/river"

	"github.com/blackforge/embookshelf/internal/extractor"
	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
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
	// Resolver maps a backend_id to Storage. Bookdrop ingest uses the
	// default backend (empty backend_id) because the bookdrop item does
	// not yet carry a library_id at ingest time. Limitation: sidecars
	// for libraries on non-default backends are not read during ingest.
	// Plan F2 addresses this when bookdrop carries library_id.
	Resolver storage.Resolver
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

	// Pre-flight format check. Dispatch is also re-run inside
	// ingest.Extract; we run it up-front so an unsupported format
	// short-circuits to "failed" (non-retry) before we open the file.
	if _, _, err := fileproc.Dispatch(item.Path); err != nil {
		if errors.Is(err, fileproc.ErrUnsupportedFormat) {
			_ = deps.Svc.Fail(ctx, itemID, err)
			return nil
		}
		return err
	}

	// Resolve the default storage backend for Source-based extraction and
	// sidecar reads. Bookdrop ingest doesn't know the library_id at this
	// point, so always use the default backend (empty backend_id).
	var store storage.Storage
	if deps.Resolver != nil {
		if resolved, resolveErr := deps.Resolver.Resolve(""); resolveErr == nil {
			store = resolved
		} else {
			slog.Warn("bookdrop sidecar resolve failed", "item_id", itemID, "err", resolveErr)
		}
	}
	if store == nil {
		_ = deps.Svc.Fail(ctx, itemID, errors.New("no storage backend available"))
		return nil
	}

	key := strings.TrimPrefix(item.Path, "/")
	src, openErr := store.Open(ctx, key)
	if openErr != nil {
		slog.Warn("bookdrop: open source", "path", item.Path, "err", openErr)
		_ = deps.Svc.Fail(ctx, itemID, openErr)
		return nil
	}
	defer func() { _ = src.Close() }()

	res, extractErr := extractor.Extract(ctx, store, src, item.Format, key)
	if extractErr != nil {
		slog.Warn("bookdrop extract failed", "item_id", itemID, "path", item.Path, "err", extractErr)
		_ = deps.Svc.Fail(ctx, itemID, extractErr)
		return nil
	}

	// Filename fallback: extractors that can't surface a Title (e.g. PDFs
	// without /Info Title, or sparse OPF metadata) would otherwise leave
	// the BookDrop list with a blank row. Use the basename without the
	// extension so the user has something selectable to triage.
	if strings.TrimSpace(res.Title) == "" {
		base := filepath.Base(item.Path)
		res.Title = strings.TrimSuffix(base, filepath.Ext(base))
	}

	if err := deps.Svc.RecordMetadata(
		ctx, itemID,
		res.Title, res.Author, res.Description, res.Language, res.ISBN,
		res.CoverBytes, res.CoverMime,
	); err != nil {
		return err
	}

	// Audio formats: persist duration / narrator on the bookdrop row so
	// Approve doesn't need a re-extract pass post-Place. Non-audio
	// extraction leaves DurationSeconds nil, so we skip the UPDATE.
	if isAudioFormat(item.Format) {
		if err := deps.Svc.SetAudio(ctx, itemID, res.DurationSeconds, res.Narrator, nil); err != nil {
			slog.Warn("bookdrop set audio failed", "item_id", itemID, "err", err)
			// Non-fatal: text metadata is already recorded. Approve
			// will simply leave the audio fields empty.
		}
	}
	return nil
}

// isAudioFormat reports whether a books.format / bookdrop_items.format
// value names an audio file the AudioProcessor extracts metadata from.
func isAudioFormat(f string) bool {
	switch f {
	case "MP3", "M4B":
		return true
	}
	return false
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
