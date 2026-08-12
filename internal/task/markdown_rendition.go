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
	// Record is the shared finalize tail (#307): places the staged
	// markdown into the book's folder. Markdown records no files row —
	// that exception is RecordDerived's to own (ADR-0033 §4), not this
	// worker's to restate.
	Record func(context.Context, model.Book, string) (service.DerivedRecord, error)
}

// MarkdownRendition converts one book's file into markdown and records
// the outcome on the tracking row. The shared prelude (renditionJob,
// #309) runs the gates and marks the row running; what remains here is
// the artifact's own work as renditionRun steps — the wrapper owns
// writing the row before any failure returns (ADR-0033 §5) and mapping
// permanent verdicts onto ErrDoNotRetry.
func MarkdownRendition(ctx context.Context, a jobs.MarkdownRenditionArgs, deps MarkdownRenditionDeps) error {
	var result service.ConvertResult
	defer func() {
		if result.Path != "" {
			_ = os.Remove(result.Path)
		}
	}()

	book, cfg, err := renditionJob{
		Rows:   deps.Renditions,
		Books:  deps.Books,
		Config: deps.Config,
		Refusal: func(format string) string {
			return fmt.Sprintf("format %s is not convertible — the converter accepts %v", format, model.ConvertibleFormats())
		},
	}.Prepare(ctx, a.BookID)
	if err != nil {
		return err
	}

	return renditionRun(ctx, deps.Renditions, a.BookID,
		func(ctx context.Context) (string, bool, error) {
			body, _, closer, err := deps.Open(ctx, book)
			if err != nil {
				return "open book file: " + err.Error(), false, fmt.Errorf("open book %s: %w", a.BookID, err)
			}
			var convertErr error
			result, convertErr = deps.Convert(ctx, cfg.BaseURL, body)
			_ = closer.Close()
			if convertErr != nil {
				var rejected *service.ConvertRejectedError
				// A rejection is the document itself refused — same bytes,
				// same answer — so it is permanent.
				return convertErr.Error(), errors.As(convertErr, &rejected), convertErr
			}
			return "", false, nil
		},
		func(ctx context.Context) (string, bool, error) {
			// The source hash is read before Record consumes the staged
			// file — it is what answers "is this rendition still current".
			sourceHash := deps.SourceHash(ctx, book)

			rec, err := deps.Record(ctx, book, result.Path)
			if err != nil {
				return "place markdown: " + err.Error(), false, fmt.Errorf("place markdown for %s: %w", a.BookID, err)
			}
			if err := deps.Renditions.MarkReady(ctx, a.BookID, rec.Location, rec.Size, sourceHash, result.Version); err != nil {
				return "", false, fmt.Errorf("mark ready: %w", err)
			}
			return "", false, nil
		},
	)
}
