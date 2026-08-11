// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// epubRenditionStore is the slice of BookEpubRenditionRepo the worker
// writes through.
type epubRenditionStore interface {
	MarkRunning(ctx context.Context, bookID string) error
	MarkReady(ctx context.Context, bookID, fileID string, sourceHash []byte, version string) error
	MarkFailed(ctx context.Context, bookID, msg string) error
}

// epubFiles is the narrow files-repo slice the finalize step needs —
// the narrationFiles trio, for the same regeneration reason: the
// location is stable, so a second render must update the row that
// exists rather than violate UNIQUE(library_id, location).
type epubFiles = narrationFiles

// epubTextCap bounds how much markdown feeds the render. Deliberately
// enormous — the EPUB wants the whole book, unlike the guide's cost
// dial — while still refusing a pathological rendition.
const epubTextCap int64 = 256 * 1024 * 1024

// EpubRenderDeps groups the seams the worker needs. Markdown is the
// chained stage: the same feed the guide consumes, so a missing or
// stale rendition is requested and waited for identically (ADR-0034 §5).
type EpubRenderDeps struct {
	Config     func(context.Context) (repo.ConverterConfig, error)
	Renditions epubRenditionStore
	Books      bookReader
	Markdown   *service.MarkdownFeed
	// SourceHash is the book's primary file (the PDF) — what staleness
	// is answered against, not the markdown.
	SourceHash func(context.Context, model.Book) []byte
	Render     func(ctx context.Context, baseURL string, req service.EpubRenderRequest) (service.ConvertResult, error)
	Place      func(context.Context, model.Book, string) (service.PlaceResult, error)
	Files      epubFiles
}

// EpubRender renders one book's generated EPUB and records the outcome
// on the tracking row. A sequence of renditionRun steps, sharing the
// markdown worker's loud-failure choreography: the wrapper writes the
// row before any failure returns, permanent verdicts wrap
// ErrDoNotRetry, and a pending markdown rendition is a plain error so
// River's retry becomes the wait.
func EpubRender(ctx context.Context, a jobs.EpubRenderArgs, deps EpubRenderDeps) error {
	var (
		book     model.Book
		cfg      repo.ConverterConfig
		markdown string
		result   service.ConvertResult
	)
	defer func() {
		if result.Path != "" {
			_ = os.Remove(result.Path)
		}
	}()

	return renditionRun(ctx, deps.Renditions, a.BookID,
		func(ctx context.Context) (string, bool, error) {
			var err error
			book, err = deps.Books.GetByID(ctx, "", a.BookID)
			if err != nil {
				return "", true, fmt.Errorf("load book %s: %w", a.BookID, err)
			}
			return "", false, nil
		},
		func(context.Context) (string, bool, error) {
			if model.Convertible(book.Format) {
				return "", false, nil
			}
			msg := fmt.Sprintf("format %s cannot become a generated EPUB — the chain starts from %v", book.Format, model.ConvertibleFormats())
			return msg, true, errors.New(msg)
		},
		func(ctx context.Context) (string, bool, error) {
			var err error
			cfg, err = deps.Config(ctx)
			if err != nil {
				return "read converter settings: " + err.Error(), false, fmt.Errorf("read converter settings: %w", err)
			}
			if !cfg.Configured() {
				return repo.MsgConverterNotConfigured, true, ErrConverterNotConfigured
			}
			if deps.Markdown == nil {
				return "markdown feed is not wired", true, errors.New("markdown feed is not wired")
			}
			return "", false, nil
		},
		func(ctx context.Context) (string, bool, error) {
			if err := deps.Renditions.MarkRunning(ctx, a.BookID); err != nil {
				return "", false, fmt.Errorf("mark running: %w", err)
			}
			return "", false, nil
		},
		// The chained stage (ADR-0034 §5): the same feed the guide
		// consumes, so a missing or stale rendition is requested and
		// waited for identically.
		func(ctx context.Context) (string, bool, error) {
			var err error
			markdown, err = deps.Markdown.Text(ctx, book, epubTextCap)
			if err == nil {
				return "", false, nil
			}
			var failed *service.RenditionFailedError
			switch {
			case errors.As(err, &failed):
				// The chained stage's message, verbatim (ADR-0034 §5).
				return failed.Msg, true, fmt.Errorf("markdown rendition failed: %w", err)
			case errors.Is(err, service.ErrRenditionPending):
				// Loud but transient: the poll sees "waiting for the
				// markdown rendition", and the retry is the wait.
				return "waiting for the markdown rendition", false, err
			default:
				return "read markdown rendition: " + err.Error(), false, err
			}
		},
		func(ctx context.Context) (string, bool, error) {
			var err error
			result, err = deps.Render(ctx, cfg.BaseURL, service.EpubRenderRequest{
				Markdown: markdown,
				Title:    book.Title,
				Author:   book.Author,
				Language: "en",
			})
			if err != nil {
				var rejected *service.ConvertRejectedError
				return err.Error(), errors.As(err, &rejected), err
			}
			return "", false, nil
		},
		func(ctx context.Context) (string, bool, error) {
			// Hash before placing — placement consumes the staged file, and
			// the files row's content_hash is the identity scan's rename
			// safety net keys on (same order as audiobook finalize).
			epubHash, err := hashFile(ctx, result.Path)
			if err != nil {
				return "hash epub: " + err.Error(), false, fmt.Errorf("hash epub for %s: %w", a.BookID, err)
			}
			sourceHash := deps.SourceHash(ctx, book)

			placed, err := deps.Place(ctx, book, result.Path)
			if err != nil {
				return "place epub: " + err.Error(), false, fmt.Errorf("place epub for %s: %w", a.BookID, err)
			}

			fileID, err := upsertEpubFile(ctx, deps.Files, book, placed, epubHash)
			if err != nil {
				return "record files row: " + err.Error(), false, fmt.Errorf("record files row for %s: %w", a.BookID, err)
			}

			if err := deps.Renditions.MarkReady(ctx, a.BookID, fileID, sourceHash, result.Version); err != nil {
				return "", false, fmt.Errorf("mark ready: %w", err)
			}
			return "", false, nil
		},
	)
}

// upsertEpubFile lands the generated EPUB in files without violating
// UNIQUE(library_id, location) on regeneration — update the existing
// row's hash if the location is already recorded, insert otherwise.
func upsertEpubFile(ctx context.Context, files epubFiles, book model.Book, placed service.PlaceResult, hash []byte) (string, error) {
	if existing, err := files.GetByLocation(ctx, book.LibraryID, placed.Location); err == nil {
		if err := files.SetContentHash(ctx, existing.ID, hash, placed.Size, placed.Mtime); err != nil {
			return "", err
		}
		return existing.ID, nil
	}
	inserted, err := files.Insert(ctx, model.File{
		LibraryID:   book.LibraryID,
		BookID:      book.ID,
		Location:    placed.Location,
		Size:        placed.Size,
		Mtime:       placed.Mtime,
		ContentHash: hash,
		Format:      "EPUB",
	})
	if err != nil {
		return "", err
	}
	return inserted.ID, nil
}
