// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// markdownRenditionStore is the slice of BookMarkdownRenditionRepo the
// routes need — an interface so the handler tier is exercisable without
// Postgres, same reasoning as appSettingsStore.
type markdownRenditionStore interface {
	Start(ctx context.Context, bookID string) error
	GetByBookID(ctx context.Context, bookID string) (model.MarkdownRendition, error)
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

// BookMarkdownGet answers the rendition's state for the book page.
func (h *Handler) BookMarkdownGet(c *gin.Context, s bookScope) {
	ctx := c.Request.Context()
	if h.renditions == nil {
		writeError(c, http.StatusServiceUnavailable, "markdown renditions are unavailable")
		return
	}
	rendition, err := h.renditions.GetByBookID(ctx, s.Book.ID)
	if errors.Is(err, repo.ErrNotFound) {
		c.JSON(http.StatusOK, markdownRenditionDTO{State: "none"})
		return
	}
	if err != nil {
		writeServerError(c, "read markdown rendition", err)
		return
	}

	dto := markdownRenditionDTO{
		State:            string(rendition.State),
		Error:            rendition.Error,
		Location:         rendition.Location,
		SizeBytes:        rendition.SizeBytes,
		ConverterVersion: rendition.ConverterVersion,
		UpdatedAt:        &rendition.UpdatedAt,
	}
	dto.Stale = h.renditionStale(c, s.Book, rendition)
	c.JSON(http.StatusOK, dto)
}

// renditionStale compares the row's source hash with the book's current
// primary file. Answerable only when both hashes exist and the library
// resolves; anything else reads as not-stale rather than a scare label.
func (h *Handler) renditionStale(c *gin.Context, book model.Book, r model.MarkdownRendition) bool {
	if r.State != model.MarkdownRenditionReady || len(r.SourceContentHash) == 0 || h.libStore == nil {
		return false
	}
	handle, err := h.libStore.For(c.Request.Context(), book.LibraryID)
	if err != nil {
		return false
	}
	current := handle.PrimaryContentHash(c.Request.Context(), book)
	if len(current) == 0 {
		return false
	}
	return !bytes.Equal(current, r.SourceContentHash)
}

// BookMarkdownGenerate enqueues a conversion. Admin-gated at the route:
// the sidecar spends CPU somebody pays for.
func (h *Handler) BookMarkdownGenerate(c *gin.Context, s bookScope) {
	ctx := c.Request.Context()
	if h.renditions == nil {
		writeError(c, http.StatusServiceUnavailable, "markdown renditions are unavailable")
		return
	}

	if !model.Convertible(s.Book.Format) {
		writeErrorCode(c, http.StatusUnsupportedMediaType, CodeFormatNotConvertible,
			"only "+model.ConvertibleFormatList()+" books can be converted — "+
				s.Book.Format+" is served natively or not supported")
		return
	}

	cfg, err := h.appSettings.GetConverter(ctx)
	if err != nil {
		writeServerError(c, "read converter settings", err)
		return
	}
	if !cfg.Enabled || cfg.BaseURL == "" {
		writeErrorCode(c, http.StatusServiceUnavailable, CodeConverterDisabled,
			"converter extension is not configured")
		return
	}

	q, ok := h.requireQueue(c)
	if !ok {
		return
	}
	// The row goes pending before the enqueue so the status endpoint has
	// an answer the moment the button is pressed; a failed enqueue is
	// recorded on it rather than leaving a phantom pending.
	if err := h.renditions.Start(ctx, s.Book.ID); err != nil {
		writeServerError(c, "start markdown rendition", err)
		return
	}
	if err := q.Enqueue(ctx, jobs.MarkdownRenditionArgs{BookID: s.Book.ID}); err != nil {
		writeServerError(c, "queue markdown conversion", err)
		return
	}
	c.Status(http.StatusAccepted)
}
