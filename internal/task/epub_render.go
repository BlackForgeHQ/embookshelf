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
// on the tracking row. Failure semantics mirror the markdown worker:
// every failure path writes the row before returning, permanent
// failures wrap ErrDoNotRetry, and a pending markdown rendition is a
// plain error so River's retry becomes the wait.
func EpubRender(ctx context.Context, a jobs.EpubRenderArgs, deps EpubRenderDeps) error {
	book, err := deps.Books.GetByID(ctx, "", a.BookID)
	if err != nil {
		return fmt.Errorf("load book %s: %w (%w)", a.BookID, err, jobs.ErrDoNotRetry)
	}

	if !model.Convertible(book.Format) {
		msg := fmt.Sprintf("format %s cannot become a generated EPUB — the chain starts from %v", book.Format, model.ConvertibleFormats())
		_ = deps.Renditions.MarkFailed(ctx, a.BookID, msg)
		return fmt.Errorf("%s: %w", msg, jobs.ErrDoNotRetry)
	}

	cfg, err := deps.Config(ctx)
	if err != nil {
		_ = deps.Renditions.MarkFailed(ctx, a.BookID, "read converter settings: "+err.Error())
		return fmt.Errorf("read converter settings: %w", err)
	}
	if !cfg.Enabled || cfg.BaseURL == "" {
		_ = deps.Renditions.MarkFailed(ctx, a.BookID, "converter extension is not configured")
		return ErrConverterNotConfigured
	}
	if deps.Markdown == nil {
		_ = deps.Renditions.MarkFailed(ctx, a.BookID, "markdown feed is not wired")
		return fmt.Errorf("markdown feed is not wired: %w", jobs.ErrDoNotRetry)
	}

	if err := deps.Renditions.MarkRunning(ctx, a.BookID); err != nil {
		return fmt.Errorf("mark running: %w", err)
	}

	markdown, err := deps.Markdown.Text(ctx, book, epubTextCap)
	if err != nil {
		var failed *service.RenditionFailedError
		switch {
		case errors.As(err, &failed):
			// The chained stage's message, verbatim (ADR-0034 §5).
			_ = deps.Renditions.MarkFailed(ctx, a.BookID, failed.Msg)
			return fmt.Errorf("markdown rendition failed: %w (%w)", err, jobs.ErrDoNotRetry)
		case errors.Is(err, service.ErrRenditionPending):
			// Loud but transient: the poll sees "waiting for the
			// markdown rendition", and the retry is the wait.
			_ = deps.Renditions.MarkFailed(ctx, a.BookID, "waiting for the markdown rendition")
			return err
		default:
			_ = deps.Renditions.MarkFailed(ctx, a.BookID, "read markdown rendition: "+err.Error())
			return err
		}
	}

	result, err := deps.Render(ctx, cfg.BaseURL, service.EpubRenderRequest{
		Markdown: markdown,
		Title:    book.Title,
		Author:   book.Author,
		Language: "en",
	})
	if err != nil {
		_ = deps.Renditions.MarkFailed(ctx, a.BookID, err.Error())
		var rejected *service.ConvertRejectedError
		if errors.As(err, &rejected) {
			return fmt.Errorf("%w (%w)", err, jobs.ErrDoNotRetry)
		}
		return err
	}
	defer func() { _ = os.Remove(result.Path) }()

	// Hash before placing — placement consumes the staged file, and the
	// files row's content_hash is the identity scan's rename safety net
	// keys on (same order as audiobook finalize).
	epubHash, err := hashFile(ctx, result.Path)
	if err != nil {
		_ = deps.Renditions.MarkFailed(ctx, a.BookID, "hash epub: "+err.Error())
		return fmt.Errorf("hash epub for %s: %w", a.BookID, err)
	}
	sourceHash := deps.SourceHash(ctx, book)

	placed, err := deps.Place(ctx, book, result.Path)
	if err != nil {
		_ = deps.Renditions.MarkFailed(ctx, a.BookID, "place epub: "+err.Error())
		return fmt.Errorf("place epub for %s: %w", a.BookID, err)
	}

	fileID, err := upsertEpubFile(ctx, deps.Files, book, placed, epubHash)
	if err != nil {
		_ = deps.Renditions.MarkFailed(ctx, a.BookID, "record files row: "+err.Error())
		return fmt.Errorf("record files row for %s: %w", a.BookID, err)
	}

	if err := deps.Renditions.MarkReady(ctx, a.BookID, fileID, sourceHash, result.Version); err != nil {
		return fmt.Errorf("mark ready: %w", err)
	}
	return nil
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
