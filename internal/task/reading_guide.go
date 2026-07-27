// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/blackforge/embookshelf/internal/llm"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/sse"
)

// ReadingGuideArgs is the payload for generating one book's guide.
// BookID only — the worker re-reads the row, so a metadata edit between
// enqueue and dispatch is reflected rather than baked into the payload.
type ReadingGuideArgs struct {
	BookID string `json:"book_id"`
}

// Kind is the stable job name.
func (ReadingGuideArgs) Kind() string { return "guide.generate" }

// ReadingGuideDeps groups the seams the worker needs.
//
// Settings is read per job rather than captured at boot, so an admin
// changing the model, the language or the cap takes effect on the next
// job instead of at the next restart — the same hot-reload behaviour
// Notifier gives the email subsystem.
type ReadingGuideDeps struct {
	Settings *repo.AppSettingsRepo
	Guides   *repo.BookReadingGuideRepo
	Books    *repo.BookRepo
	LibStore service.LibraryStore
	Hub      *sse.Hub
}

// ErrReadingGuidesDisabled is returned when the feature is off. River
// treats it as a permanent failure rather than retrying: a disabled
// feature will still be disabled in thirty seconds.
var ErrReadingGuidesDisabled = errors.New("reading guides are not enabled")

// ReadingGuide generates and stores one book's guide.
func ReadingGuide(ctx context.Context, a ReadingGuideArgs, deps ReadingGuideDeps) error {
	cfg, err := deps.Settings.GetReadingGuide(ctx)
	if err != nil {
		return fmt.Errorf("read guide settings: %w", err)
	}
	if !cfg.Enabled {
		slog.Debug("reading guide skipped: disabled", "book", a.BookID)
		return ErrReadingGuidesDisabled
	}

	client, err := llm.New(llm.Config{
		BaseURL:         cfg.BaseURL,
		Model:           cfg.Model,
		APIKey:          cfg.APIKey,
		RequestJSONMode: cfg.RequestJSONMode,
	})
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
		service.NewLibraryBookOpener(deps.LibStore),
		client,
		service.ReadingGuideOptions{
			Language: cfg.Language,
			TextCap:  cfg.TextCap,
			Model:    cfg.Model,
		},
	)
	if _, err := svc.Generate(ctx, book); err != nil {
		return fmt.Errorf("generate guide for %s: %w", a.BookID, err)
	}

	if deps.Hub != nil {
		_ = deps.Hub.Publish(sse.ReadingGuideUpdated{BookID: a.BookID})
	}
	return nil
}
