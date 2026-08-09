// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/blackforge/embookshelf/internal/model"
)

// ErrRenditionPending says the Markdown rendition is not ready yet —
// enqueued, converting, or just requested. Transient by design: the
// guide job returns it plain and River's retry becomes the wait.
var ErrRenditionPending = errors.New("markdown rendition is not ready yet")

// RenditionFailedError carries the rendition row's error verbatim —
// "converter extension is not configured", or the sidecar's own message.
// Permanent from the guide's point of view: retrying the guide will not
// fix the conversion; the admin acting on the verbatim message will.
type RenditionFailedError struct {
	Msg string
}

func (e *RenditionFailedError) Error() string { return e.Msg }

// renditionReader is the slice of BookMarkdownRenditionRepo the feed
// reads through.
type renditionReader interface {
	GetByBookID(ctx context.Context, bookID string) (model.MarkdownRendition, error)
}

// MarkdownFeed hands guide generation a Convertible-format book's text
// from its Markdown rendition (ADR-0033): the consumer side of the
// pipeline. It never converts anything itself — it reads what is there,
// and asks for what is not.
type MarkdownFeed struct {
	// Renditions answers what state the book's rendition is in.
	// ErrNotFound is "never requested", not an error.
	Renditions renditionReader
	// IsMissing reports whether an error from Renditions means "no row".
	IsMissing func(error) bool
	// Open yields the rendition's bytes by its storage location.
	Open func(ctx context.Context, book model.Book, location string) (io.ReadCloser, error)
	// CurrentHash is the book's primary file hash, for the staleness
	// comparison. Empty reads as fresh — a book mid-backfill should not
	// loop forever re-converting.
	CurrentHash func(ctx context.Context, book model.Book) []byte
	// Request starts a conversion: upsert the tracking row to pending
	// and enqueue the markdown.render job.
	Request func(ctx context.Context, bookID string) error
}

// Text returns the book's markdown, capped at textCap bytes.
//
// Absent and stale renditions are requested and reported pending rather
// than silently degraded — the loud-failure rule (ADR-0033 §5): a guide
// generated from nothing when text was one conversion away is exactly
// the silent degrade the reading-guide local-library bug taught this
// codebase to refuse.
func (f *MarkdownFeed) Text(ctx context.Context, book model.Book, textCap int64) (string, error) {
	rendition, err := f.Renditions.GetByBookID(ctx, book.ID)
	if err != nil {
		if f.IsMissing != nil && f.IsMissing(err) {
			return "", f.request(ctx, book.ID)
		}
		return "", fmt.Errorf("read rendition row: %w", err)
	}

	switch rendition.State {
	case model.MarkdownRenditionFailed:
		return "", &RenditionFailedError{Msg: rendition.Error}
	case model.MarkdownRenditionPending, model.MarkdownRenditionRunning:
		return "", ErrRenditionPending
	}

	if f.stale(ctx, book, rendition) {
		return "", f.request(ctx, book.ID)
	}

	r, err := f.Open(ctx, book, rendition.Location)
	if err != nil {
		return "", fmt.Errorf("open markdown rendition: %w", err)
	}
	defer func() { _ = r.Close() }()

	text, err := io.ReadAll(io.LimitReader(r, textCap))
	if err != nil {
		return "", fmt.Errorf("read markdown rendition: %w", err)
	}
	return string(text), nil
}

// request asks for a conversion and answers pending; a refused enqueue
// is the caller's error to see, not a silent skip.
func (f *MarkdownFeed) request(ctx context.Context, bookID string) error {
	if f.Request == nil {
		return errors.New("no conversion dispatcher configured")
	}
	if err := f.Request(ctx, bookID); err != nil {
		return fmt.Errorf("request conversion: %w", err)
	}
	return ErrRenditionPending
}

// stale mirrors the audiobook's rule: answerable only when both hashes
// exist, and a mismatch labels rather than deletes — here the label is
// "convert again before feeding a guide from the old copy".
func (f *MarkdownFeed) stale(ctx context.Context, book model.Book, r model.MarkdownRendition) bool {
	if len(r.SourceContentHash) == 0 || f.CurrentHash == nil {
		return false
	}
	current := f.CurrentHash(ctx, book)
	if len(current) == 0 {
		return false
	}
	return !bytes.Equal(current, r.SourceContentHash)
}
