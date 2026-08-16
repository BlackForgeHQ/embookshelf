// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
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

// markdownFeedRows is the rendition-row slice the feed and its request
// module share: the state read plus the request half.
type markdownFeedRows interface {
	renditionReader
	markdownRequestRows
}

// NewMarkdownFeed builds the feed over its production collaborators,
// deciding every pairing once (#356): the rows, the ErrNotFound =
// "never requested" binding — a service fact that used to live in the
// queue tier's hand assembly, untested — the BookOps open/hash pair,
// and the request module over the same rows and enqueuer, so the five
// fields cannot be paired wrong at a wiring site. The queue registry
// keeps only its own degrade gate (a nil feed when the converter
// pieces are not wired).
func NewMarkdownFeed(rows markdownFeedRows, ops *BookOps, enq jobs.Enqueuer) *MarkdownFeed {
	return &MarkdownFeed{
		Renditions:  rows,
		IsMissing:   func(err error) bool { return errors.Is(err, repo.ErrNotFound) },
		Open:        ops.OpenMarkdown,
		CurrentHash: ops.PrimaryHash,
		Request:     NewMarkdownRequests(rows, enq).One,
	}
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
	case model.RenditionFailed:
		return "", &RenditionFailedError{Msg: rendition.Error}
	case model.RenditionPending, model.RenditionRunning:
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

// stale is the shared Staleness composition (#340) over the injected
// hash lookup — the same one the handler's badge and the audiobook
// preflight consume, so the three surfaces cannot drift.
func (f *MarkdownFeed) stale(ctx context.Context, book model.Book, r model.MarkdownRendition) bool {
	return NewStaleness(f.CurrentHash).Stale(ctx, book, r.State, r.SourceContentHash)
}
