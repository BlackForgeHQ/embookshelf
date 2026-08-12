// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// The one rendition route surface (#303). The markdown and EPUB routes
// run the same six steps in the same order; what differs per artifact is
// configuration — messages, the Start call, the job args — and a status
// projector. A third derived artifact's routes are a renditionRouteSpec
// and a projector function, not a copy of the chain.

// renditionRouteSpec is one artifact's route configuration.
type renditionRouteSpec struct {
	// available reports the artifact's store seam is wired; false is the
	// deliberate degrade every route answers 503 with unavailableMsg.
	available      bool
	unavailableMsg string
	// notConvertibleMsg is the 415 refusal for a book outside the
	// Convertible set.
	notConvertibleMsg string
	// requestOp labels the server-error log line.
	requestOp string
	// request asks for the rendition — service.RenditionRequests.One,
	// which owns the Start-before-Enqueue ordering and records a refused
	// enqueue on the row (#317).
	request func(ctx context.Context, bookID string) error
}

// renditionGenerate is the shared gate chain behind every generate
// button: nil-store → Convertible → requireConverter → requireQueue →
// request+202. The order is load-bearing — the instant refusals answer
// the click now rather than a job failing in thirty seconds. The
// Start-before-Enqueue ordering and the phantom-pending compensation
// live in the request module, not here (#317). Held by
// TestRenditionGenerateGateChain over both artifacts.
func (h *Handler) renditionGenerate(c *gin.Context, s bookScope, spec renditionRouteSpec) {
	ctx := c.Request.Context()
	if !spec.available {
		writeError(c, http.StatusServiceUnavailable, spec.unavailableMsg)
		return
	}
	if !model.Convertible(s.Book.Format) {
		writeErrorCode(c, http.StatusUnsupportedMediaType, CodeFormatNotConvertible, spec.notConvertibleMsg)
		return
	}
	if _, ok := h.requireConverter(c); !ok {
		return
	}
	if _, ok := h.requireQueue(c); !ok {
		return
	}
	if err := spec.request(ctx, s.Book.ID); err != nil {
		writeServerError(c, spec.requestOp, err)
		return
	}
	c.Status(http.StatusAccepted)
}

// renditionStatus is the shared status-handler shape: unavailable →
// 503, no row → the artifact's "none" answer, otherwise the projector's
// DTO. The projector owns everything artifact-specific, including the
// EPUB's ready-but-no-file → none arm.
func (h *Handler) renditionStatus(
	c *gin.Context, available bool, unavailableMsg, readOp string,
	load func(ctx context.Context) (any, error), none any,
) {
	ctx := c.Request.Context()
	if !available {
		writeError(c, http.StatusServiceUnavailable, unavailableMsg)
		return
	}
	dto, err := load(ctx)
	if errors.Is(err, repo.ErrNotFound) {
		c.JSON(http.StatusOK, none)
		return
	}
	if err != nil {
		writeServerError(c, readOp, err)
		return
	}
	c.JSON(http.StatusOK, dto)
}
