// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// renditionStore is the narrow slice of BookMarkdownRenditionRepo the
// worker writes through.
type renditionStore interface {
	MarkRunning(ctx context.Context, bookID string) error
	MarkReady(ctx context.Context, bookID, location string, size int64, sourceHash []byte, version string) error
	MarkFailed(ctx context.Context, bookID, msg string) error
}

// MarkdownRenditionDeps groups the seams the worker needs. Config is
// read per job so an admin pointing the CONVERTER row at a new URL takes
// effect on the next job, not the next restart. The library-touching
// steps are per-op closures, not a LibraryStore — same argument as
// FinalizeDeps.Place: handing over the store would let a later edit
// reach more than this job needs.
type MarkdownRenditionDeps struct {
	Config     func(context.Context) (repo.ConverterConfig, error)
	Renditions renditionStore
	Books      bookReader
	// Open yields the book's bytes as a stream — the converter POST body.
	Open func(context.Context, model.Book) (io.Reader, int64, io.Closer, error)
	// SourceHash is the book's primary file hash, recorded on the row so
	// staleness is answerable later. Empty is recorded as-is: a book
	// whose hash backfill has not run yet still converts.
	SourceHash func(context.Context, model.Book) []byte
	// Convert speaks the sidecar's wire contract and stages the answer
	// in a temp file.
	Convert func(ctx context.Context, baseURL string, body io.Reader) (service.ConvertResult, error)
	// Place moves the staged markdown into the book's folder.
	Place func(context.Context, model.Book, string) (service.PlaceResult, error)
}

// ErrConverterNotConfigured is the loud "extension not configured"
// answer (ADR-0033 §5) — distinct from a conversion that failed. Wraps
// ErrDoNotRetry: a disabled extension will still be disabled in thirty
// seconds.
var ErrConverterNotConfigured = fmt.Errorf(repo.MsgConverterNotConfigured+": %w", jobs.ErrDoNotRetry)

// MarkdownRendition converts one book's file into markdown and records
// the outcome on the tracking row. Every failure path writes the row
// before returning, so the status API always has the loud answer — what
// lands in the row is surfaced verbatim.
func MarkdownRendition(ctx context.Context, a jobs.MarkdownRenditionArgs, deps MarkdownRenditionDeps) error {
	book, err := deps.Books.GetByID(ctx, "", a.BookID)
	if err != nil {
		// A deleted book cascades its rendition row; nothing to record.
		return fmt.Errorf("load book %s: %w (%w)", a.BookID, err, jobs.ErrDoNotRetry)
	}

	if !model.Convertible(book.Format) {
		msg := fmt.Sprintf("format %s is not convertible — the converter accepts %v", book.Format, model.ConvertibleFormats())
		_ = deps.Renditions.MarkFailed(ctx, a.BookID, msg)
		return fmt.Errorf("%s: %w", msg, jobs.ErrDoNotRetry)
	}

	cfg, err := deps.Config(ctx)
	if err != nil {
		_ = deps.Renditions.MarkFailed(ctx, a.BookID, "read converter settings: "+err.Error())
		return fmt.Errorf("read converter settings: %w", err)
	}
	if !cfg.Configured() {
		_ = deps.Renditions.MarkFailed(ctx, a.BookID, repo.MsgConverterNotConfigured)
		return ErrConverterNotConfigured
	}

	if err := deps.Renditions.MarkRunning(ctx, a.BookID); err != nil {
		return fmt.Errorf("mark running: %w", err)
	}

	body, _, closer, err := deps.Open(ctx, book)
	if err != nil {
		_ = deps.Renditions.MarkFailed(ctx, a.BookID, "open book file: "+err.Error())
		return fmt.Errorf("open book %s: %w", a.BookID, err)
	}
	result, convertErr := deps.Convert(ctx, cfg.BaseURL, body)
	_ = closer.Close()
	if convertErr != nil {
		_ = deps.Renditions.MarkFailed(ctx, a.BookID, convertErr.Error())
		var rejected *service.ConvertRejectedError
		if errors.As(convertErr, &rejected) {
			// The document itself is refused — same bytes, same answer.
			return fmt.Errorf("%w (%w)", convertErr, jobs.ErrDoNotRetry)
		}
		return convertErr
	}
	defer func() { _ = os.Remove(result.Path) }()

	// Hash before placing: placement consumes the staged file, and the
	// hash is what answers "is this rendition still current" later.
	sourceHash := deps.SourceHash(ctx, book)

	placed, err := deps.Place(ctx, book, result.Path)
	if err != nil {
		_ = deps.Renditions.MarkFailed(ctx, a.BookID, "place markdown: "+err.Error())
		return fmt.Errorf("place markdown for %s: %w", a.BookID, err)
	}

	if err := deps.Renditions.MarkReady(ctx, a.BookID, placed.Location, placed.Size, sourceHash, result.Version); err != nil {
		return fmt.Errorf("mark ready: %w", err)
	}
	return nil
}
