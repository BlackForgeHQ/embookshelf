// SPDX-License-Identifier: AGPL-3.0-or-later

// Package task holds business-logic functions and River adapters. Job
// payloads live in internal/jobs; the pure functions (BookDropIngest,
// LibraryScan) are called by River workers through the registry's
// dispatch path.
package task

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
)

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

// BookDropAutoEnrichDeps groups the seams the auto-enrich worker needs.
// The enable setting is deliberately absent: Approve owns that decision
// (ADR-0012) and a job only exists because Approve already said yes.
type BookDropAutoEnrichDeps struct {
	Books  *repo.BookRepo
	Enrich *service.EnrichmentService
}

// BookDropAutoEnrich runs Auto-enrich for one freshly approved book.
//
// Errors are returned so River retries them: a provider or a database
// that was down when the job first ran is usually up a backoff later,
// which is the durability an inline call in the approve request could
// never offer. A book deleted between approve and dispatch is terminal,
// not a failure — there is nothing left to enrich.
func BookDropAutoEnrich(ctx context.Context, a jobs.BookDropAutoEnrichArgs, deps BookDropAutoEnrichDeps) error {
	if deps.Books == nil || deps.Enrich == nil {
		return errors.New("auto-enrich: worker not configured")
	}
	book, err := deps.Books.GetByID(ctx, "", a.BookID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			slog.Debug("auto-enrich skipped: book gone", "book", a.BookID)
			return nil
		}
		return fmt.Errorf("auto-enrich: load book %s: %w", a.BookID, err)
	}
	applied, err := deps.Enrich.AutoEnrich(ctx, book)
	if err != nil {
		return fmt.Errorf("auto-enrich %s: %w", a.BookID, err)
	}
	slog.Debug("auto-enrich finished", "book", a.BookID, "applied", applied)
	return nil
}

// BookDropIngest runs the ingest pipeline for one bookdrop item:
// load, extract metadata, record results. Transient errors are
// returned for the caller to retry. Permanent errors transition the
// item into 'failed' for review and return nil so the caller does
// not retry.
func BookDropIngest(ctx context.Context, args jobs.BookDropIngestArgs, deps BookDropDeps) error {
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

	res, extractErr := fileproc.ExtractBook(ctx, store, src, item.Format, key)
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
	if fileproc.IsAudioFormat(item.Format) {
		if err := deps.Svc.SetAudio(ctx, itemID, res.DurationSeconds, res.Narrator, nil); err != nil {
			slog.Warn("bookdrop set audio failed", "item_id", itemID, "err", err)
			// Non-fatal: text metadata is already recorded. Approve
			// will simply leave the audio fields empty.
		}
	}
	return nil
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
