// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
)

// guideStore is the one write a guide job makes.
type guideStore interface {
	Upsert(ctx context.Context, g model.ReadingGuide) error
}

// ReadingGuideDeps groups the seams the worker needs.
//
// Config is read per job rather than captured at boot, so an admin
// changing the model, the language or the cap takes effect on the next
// job instead of at the next restart — the same hot-reload behaviour
// Notifier gives the email subsystem.
type ReadingGuideDeps struct {
	Config    func(context.Context) (repo.ReadingGuideConfig, error)
	Completer func(repo.ReadingGuideConfig) (service.GuideCompleter, error)
	Guides    guideStore
	Books     bookReader
	// Open yields the book's bytes through the library handle, which is
	// what keeps guide generation working on S3-backed libraries.
	Open    func(context.Context, model.Book) (storage.Source, error)
	Publish func(bookID string)
}

// publish emits the guide's completion event. See SegmentDeps.publish
// (audiobook.go) for why a nil publisher is not an error.
func (d ReadingGuideDeps) publish(bookID string) {
	if d.Publish != nil {
		d.Publish(bookID)
	}
}

// bookOpenerFunc adapts the Open seam to the opener interface the
// generator service takes.
type bookOpenerFunc func(context.Context, model.Book) (storage.Source, error)

func (f bookOpenerFunc) Open(ctx context.Context, book model.Book) (storage.Source, error) {
	return f(ctx, book)
}

// ErrReadingGuidesDisabled is returned when the feature is off. River
// treats it as a permanent failure rather than retrying — a disabled
// feature will still be disabled in thirty seconds — because it wraps
// jobs.ErrDoNotRetry, which internal/queue turns into a JobCancel. The
// claim predated the mechanism by some months (#185).
var ErrReadingGuidesDisabled = fmt.Errorf("reading guides are not enabled: %w", jobs.ErrDoNotRetry)

// ReadingGuide generates and stores one book's guide.
func ReadingGuide(ctx context.Context, a jobs.ReadingGuideArgs, deps ReadingGuideDeps) error {
	cfg, err := deps.Config(ctx)
	if err != nil {
		return fmt.Errorf("read guide settings: %w", err)
	}
	if !cfg.Enabled {
		slog.Debug("reading guide skipped: disabled", "book", a.BookID)
		return ErrReadingGuidesDisabled
	}

	completer, err := deps.Completer(cfg)
	if err != nil {
		return fmt.Errorf("configure model: %w", err)
	}

	// Re-read the book: a title or blurb edited since the job was queued
	// should reach the prompt, and a deleted book should not generate.
	book, err := deps.Books.GetByID(ctx, "", a.BookID)
	if err != nil {
		return fmt.Errorf("load book %s: %w", a.BookID, err)
	}

	svc := service.NewReadingGuideService(
		deps.Guides,
		bookOpenerFunc(deps.Open),
		completer,
		service.ReadingGuideOptions{
			Language: cfg.Language,
			TextCap:  cfg.TextCap,
			Model:    cfg.Model,
		},
	)
	if _, err := svc.Generate(ctx, book); err != nil {
		return fmt.Errorf("generate guide for %s: %w", a.BookID, err)
	}

	deps.publish(a.BookID)
	return nil
}
