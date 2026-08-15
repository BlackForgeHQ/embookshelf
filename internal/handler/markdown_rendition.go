// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/layout"
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
	h.renditionStatus(c, renditionStatusSpec{
		available:      h.renditions != nil,
		unavailableMsg: "markdown renditions are unavailable",
		readOp:         "read markdown rendition",
		load: func(ctx context.Context) (any, error) {
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
		none: markdownRenditionDTO{State: "none"},
	})
}

// sourceStale answers a rendition badge's staleness — the shared
// service.Staleness composition (#340). The install-with-no-library-
// store degrade (h.primaryHash nil ⇒ never stale) is the module's one
// nil policy now, not a branch each caller re-derives.
func (h *Handler) sourceStale(
	ctx context.Context, book model.Book, state model.RenditionState, recorded []byte,
) bool {
	return service.NewStaleness(h.primaryHash).Stale(ctx, book, state, recorded)
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
// Deliberately its own chain rather than an adapter of renditionServe
// (#316). What it shares with the two ?rendition= arms is the download
// header, and that is what `attachment` is; the rest disagrees with them
// at every step — 503 where they answer 404 because this route *is* the
// feature, a read failure told apart from an empty row, its own open,
// and an attachment every time rather than on ?download=. Expressed as a
// spec those disagreements were four fields and a delivery override
// nothing else used, and this body was longer for it.
func (h *Handler) BookMarkdownDownload(c *gin.Context, s bookScope) {
	ctx := c.Request.Context()
	if h.renditions == nil || h.libStore == nil {
		writeError(c, http.StatusServiceUnavailable, "markdown renditions are unavailable")
		return
	}
	rendition, err := h.renditions.GetByBookID(ctx, s.Book.ID)
	if errors.Is(err, repo.ErrNotFound) ||
		(err == nil && (rendition.State != model.RenditionReady || rendition.Location == "")) {
		writeError(c, http.StatusNotFound, "this book has no markdown rendition")
		return
	}
	if err != nil {
		writeServerError(c, "read markdown rendition", err)
		return
	}

	handle, err := h.libStore.For(ctx, s.Book.LibraryID)
	if err != nil {
		writeServerError(c, "resolve library", err)
		return
	}
	src, err := handle.Open(ctx, rendition.Location)
	if err != nil {
		writeServerError(c, "open markdown rendition", err)
		return
	}
	defer func() { _ = src.Close() }()

	attachment(c, layout.SanitizeTitle(s.Book.Title)+".md")
	c.DataFromReader(http.StatusOK, src.Size(), "text/markdown; charset=utf-8",
		io.NewSectionReader(src, 0, src.Size()), nil)
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
