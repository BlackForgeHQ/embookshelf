// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// markdownRenditionStore is the slice of BookMarkdownRenditionRepo the
// routes need — an interface so the handler tier is exercisable without
// Postgres, same reasoning as appSettingsStore.
type markdownRenditionStore interface {
	Start(ctx context.Context, bookID string) error
	GetByBookID(ctx context.Context, bookID string) (model.MarkdownRendition, error)
	CountConversionCoverage(ctx context.Context) (repo.ConversionCoverage, error)
}

// newMarkdownRenditionStore keeps a missing repo nil across the
// interface conversion — same trap newAppSettingsStore exists for: a
// nil pointer boxed into an interface is non-nil, and every degrade
// check downstream would panic instead.
func newMarkdownRenditionStore(r *repo.BookMarkdownRenditionRepo) markdownRenditionStore {
	if r == nil {
		return nil
	}
	return r
}

// markdownRenditionDTO is the status answer. State "none" means no
// conversion was ever requested for this book. Error travels verbatim —
// what the worker recorded is what the user reads (ADR-0033 §5).
type markdownRenditionDTO struct {
	State            string `json:"state"`
	Error            string `json:"error,omitempty"`
	Location         string `json:"location,omitempty"`
	SizeBytes        int64  `json:"sizeBytes,omitempty"`
	ConverterVersion string `json:"converterVersion,omitempty"`
	// Stale — the book's current file no longer matches the hash the
	// markdown was converted from. Labelled, never auto-invalidated.
	Stale     bool       `json:"stale"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}

// BookMarkdownGet answers the rendition's state for the book page —
// the shared status shape with markdown's projector.
func (h *Handler) BookMarkdownGet(c *gin.Context, s bookScope) {
	h.renditionStatus(c, h.renditions != nil, "markdown renditions are unavailable", "read markdown rendition",
		func(ctx context.Context) (any, error) {
			rendition, err := h.renditions.GetByBookID(ctx, s.Book.ID)
			if err != nil {
				return nil, err
			}
			return markdownRenditionDTO{
				State:            string(rendition.State),
				Error:            rendition.Error,
				Location:         rendition.Location,
				SizeBytes:        rendition.SizeBytes,
				ConverterVersion: rendition.ConverterVersion,
				UpdatedAt:        &rendition.UpdatedAt,
				Stale:            h.sourceStale(ctx, s.Book, rendition.State, rendition.SourceContentHash),
			}, nil
		},
		markdownRenditionDTO{State: "none"})
}

// sourceStale answers a rendition badge's staleness for both artifact
// shapes: state.CanBeStale gates on ready — the same gate the feed and
// the audiobook preflight's wrappers now share (#322) — and the
// comparison itself is model.Stale over the shared warn-and-degrade
// hash lookup.
//
// h.primaryHash == nil is kept, and is the one nil-hash-func branch
// that still holds: it is nil exactly when libStore is nil, a real
// installs-without-a-library-store state documented on the field
// itself, not a defensive check against something the Primary hash
// constructor already degrades. The feed's and the audiobook's own
// hash funcs carry no such install-time nil case — see their stale
// methods — so this is the one place the branch earns its keep.
func (h *Handler) sourceStale(
	ctx context.Context, book model.Book, state model.RenditionState, recorded []byte,
) bool {
	if !state.CanBeStale() || h.primaryHash == nil {
		return false
	}
	return model.Stale(h.primaryHash(ctx, book), recorded)
}

// newPrimaryHash keeps a missing library store nil across the closure —
// same trap as newMarkdownRenditionStore: sourceStale nil-checks the
// func, so it must actually be nil when there is no store behind it.
func newPrimaryHash(store service.LibraryStore) func(context.Context, model.Book) []byte {
	if store == nil {
		return nil
	}
	return service.NewPrimaryHash(store)
}

// BookMarkdownDownload streams the rendition as an attachment.
//
// Always through the app, never presigned: BookSource exists for
// multi-hundred-megabyte books, and markdown is megabytes of text at
// worst — dragging the rendition into that machinery buys nothing and
// adds a consumer to vocabulary scoped to files rows.
//
// 404 for every non-ready state: the Versions row is only offered when
// the rendition is ready, so a direct URL hit on a pending or failed
// one answers not-found rather than a half-truth.
func (h *Handler) BookMarkdownDownload(c *gin.Context, s bookScope) {
	const markdownMIME = "text/markdown; charset=utf-8"
	var rowLocation string
	h.renditionServe(c, s.Book, renditionServeSpec{
		available: h.renditions != nil && h.libStore != nil,
		// 503 rather than the 404 the ?rendition= arms give: this route
		// is the feature, so an install without it answers that the
		// feature is missing, not that this book is.
		unavailableStatus: http.StatusServiceUnavailable,
		unavailableMsg:    "markdown renditions are unavailable",
		noneMsg:           "this book has no markdown rendition",
		readOp:            "read markdown rendition",
		resolveOp:         "resolve library",
		gate: func(ctx context.Context) error {
			rendition, err := h.renditions.GetByBookID(ctx, s.Book.ID)
			if errors.Is(err, repo.ErrNotFound) ||
				(err == nil && (rendition.State != model.RenditionReady || rendition.Location == "")) {
				return errRenditionNone
			}
			if err != nil {
				return err
			}
			rowLocation = rendition.Location
			return nil
		},
		// The row names the location itself — no files row to resolve,
		// which is what the other two artifacts' locate step is for.
		locate:       func(context.Context, *service.LibraryHandle) string { return rowLocation },
		ext:          ".md",
		attachAlways: true,
		deliver: func(ctx context.Context, handle *service.LibraryHandle, location string, attach func()) {
			src, err := handle.OpenMarkdown(ctx, location)
			if err != nil {
				writeServerError(c, "open markdown rendition", err)
				return
			}
			defer func() { _ = src.Close() }()

			attach()
			c.DataFromReader(http.StatusOK, src.Size(), markdownMIME,
				io.NewSectionReader(src, 0, src.Size()), nil)
		},
	})
}

// BookMarkdownGenerate enqueues a conversion — the shared gate chain
// with markdown's configuration. Admin-gated at the route: the sidecar
// spends CPU somebody pays for.
func (h *Handler) BookMarkdownGenerate(c *gin.Context, s bookScope) {
	h.renditionGenerate(c, s, renditionRouteSpec{
		available:      h.renditions != nil && h.mdRequests != nil,
		unavailableMsg: "markdown renditions are unavailable",
		notConvertibleMsg: "only " + model.ConvertibleFormatList() + " books can be converted — " +
			s.Book.Format + " is served natively or not supported",
		requestOp: "request markdown conversion",
		request:   h.mdRequests.One,
	})
}
