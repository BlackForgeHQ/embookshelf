// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"fmt"
	"io"

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

	// The shared finishing tail (#341): staged-file lifetime, rejection
	// verdict and hash-before-Record ordering live there, not here.
	fin := &renditionFinish{book: book, sourceHash: deps.SourceHash, record: deps.Record}
	defer fin.cleanup()

	return renditionRun(ctx, deps.Renditions, a.BookID,
		fin.convert(func(ctx context.Context) (service.ConvertResult, error) {
			body, _, closer, err := deps.Open(ctx, book)
			if err != nil {
				return service.ConvertResult{}, fmt.Errorf("open book file: %w", err)
			}
			defer func() { _ = closer.Close() }()
			return deps.Convert(ctx, cfg.BaseURL, body)
		}),
		fin.finish("place markdown", func(ctx context.Context, rec service.DerivedRecord, sourceHash []byte, version string) error {
			return deps.Renditions.MarkReady(ctx, a.BookID, rec.Location, rec.Size, sourceHash, version)
		}),
	)
}
