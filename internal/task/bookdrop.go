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
	"strings"

	"github.com/riverqueue/river"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/sidecar"
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

	proc, format, err := fileproc.Dispatch(item.Path)
	if err != nil {
		if errors.Is(err, fileproc.ErrUnsupportedFormat) {
			_ = deps.Svc.Fail(ctx, itemID, err)
			return nil
		}
		return err
	}
	_ = format

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

	// Open the staged file via the resolved storage backend.
	var meta fileproc.Metadata
	if store != nil {
		key := strings.TrimPrefix(item.Path, "/")
		src, openErr := store.Open(ctx, key)
		if openErr != nil {
			slog.Warn("bookdrop: open source", "path", item.Path, "err", openErr)
			_ = deps.Svc.Fail(ctx, itemID, openErr)
			return nil
		}
		defer func() { _ = src.Close() }()
		meta, err = proc.Extract(ctx, src)
	} else {
		_ = deps.Svc.Fail(ctx, itemID, errors.New("no storage backend available"))
		return nil
	}
	if err != nil {
		slog.Warn("bookdrop extract failed", "item_id", itemID, "path", item.Path, "err", err)
		_ = deps.Svc.Fail(ctx, itemID, err)
		return nil
	}

	// Read the sidecar from the same backend (store is non-nil here;
	// we returned early above if it was nil).
	// Convert the absolute path into a storage key (Plan A LocalFS is
	// rooted at "/" so we strip the leading slash). Then take the
	// directory portion as the lookup prefix.
	{
		key := strings.TrimPrefix(item.Path, "/")
		if sc, scErr := sidecar.Read(ctx, store, key); scErr == nil && !sc.IsZero() {
			meta = layerSidecar(meta, sc)
		} else if scErr != nil {
			slog.Warn("bookdrop sidecar read failed", "item_id", itemID, "key", key, "err", scErr)
			// non-fatal — proceed with embedded metadata only
		}
	}

	if err := deps.Svc.RecordMetadata(
		ctx, itemID,
		meta.Title, meta.Author, meta.Description, meta.Language,
		meta.CoverBytes, meta.CoverMime,
	); err != nil {
		return err
	}

	// Audio formats: persist duration / narrator on the bookdrop row so
	// Approve doesn't need a re-extract pass post-Place. Non-audio
	// processors leave these zero, so the call is a no-op shape (NULL
	// duration, empty narrator, nil chapters) — but we skip it anyway
	// to avoid an unnecessary UPDATE.
	if isAudioFormat(item.Format) {
		if err := deps.Svc.SetAudio(ctx, itemID, meta.DurationSeconds, meta.Narrator, nil); err != nil {
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

// layerSidecar overlays non-empty sidecar fields onto metadata returned
// by the embedded extractor. Only the fields the sidecar carries are
// considered; ground-truth-derived fields (cover bytes, duration, format)
// are never overwritten.
func layerSidecar(m fileproc.Metadata, s sidecar.Sidecar) fileproc.Metadata {
	if s.Title != "" {
		m.Title = s.Title
	}
	if s.Author != "" {
		m.Author = s.Author
	}
	if s.Description != "" {
		m.Description = s.Description
	}
	if s.Language != "" {
		m.Language = s.Language
	}
	return m
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
