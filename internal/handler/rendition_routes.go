// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/layout"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
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
	// unavailableCode is the catalogued code riding beside unavailableMsg,
	// for the one artifact whose unavailable answer is a UI-switchable
	// code rather than a plain sentence — the reading guide's
	// CodeGuidesDisabled (#320). Empty leaves the answer as writeError
	// always wrote it for markdown and the EPUB: a message with no code.
	unavailableCode string
	// notConvertibleMsg is the 415 refusal for a book outside the
	// Convertible set. Unused when formatGate is set.
	notConvertibleMsg string
	// formatGate overrides the built-in Convertible → requireConverter
	// step for the one artifact whose own gate does not agree with it:
	// the reading guide is offered for every format, Convertible or not
	// — a metadata-only guide asks nothing of the converter — so its
	// preflight (guidePreflightConvertible) replaces the pair rather than
	// running beside it. nil keeps the built-in pair, which markdown and
	// the EPUB both still own outright. A non-nil gate has already
	// written the response before returning false.
	formatGate func(c *gin.Context, s bookScope) bool
	// requestOp labels the server-error log line.
	requestOp string
	// request asks for the rendition — service.RenditionRequests.One,
	// which owns the Start-before-Enqueue ordering and records a refused
	// enqueue on the row (#317). The reading guide has no tracking row to
	// start; its request is a direct Enqueue (#320).
	request func(ctx context.Context, bookID string) error
}

// renditionGenerate is the shared gate chain behind every generate
// button: nil-store → Convertible → requireConverter → requireQueue →
// request+202. The order is load-bearing — the instant refusals answer
// the click now rather than a job failing in thirty seconds. The
// Start-before-Enqueue ordering and the phantom-pending compensation
// live in the request module, not here (#317). The middle step is a
// per-artifact formatGate override for the one artifact whose format
// rule disagrees with the built-in pair (#320). Held by
// TestRenditionGenerateGateChain over all three artifacts.
func (h *Handler) renditionGenerate(c *gin.Context, s bookScope, spec renditionRouteSpec) {
	ctx := c.Request.Context()
	if !spec.available {
		if spec.unavailableCode != "" {
			writeErrorCode(c, http.StatusServiceUnavailable, spec.unavailableCode, spec.unavailableMsg)
		} else {
			writeError(c, http.StatusServiceUnavailable, spec.unavailableMsg)
		}
		return
	}
	if spec.formatGate != nil {
		if !spec.formatGate(c, s) {
			return
		}
	} else {
		if !model.Convertible(s.Book.Format) {
			writeErrorCode(c, http.StatusUnsupportedMediaType, CodeFormatNotConvertible, spec.notConvertibleMsg)
			return
		}
		if _, ok := h.requireConverter(c); !ok {
			return
		}
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

// renditionServeSpec is one artifact's serve configuration — the third
// leg of the rendition route seam, beside generate and status.
//
// Scoped to the ?rendition= arms of the file route: the generated EPUB
// and the narration, which are the same route asked for different bytes
// of the same book. Both answer 404 with one sentence for every state no
// download is offered for — the seam must not claim a book has an
// artifact this install cannot serve — and both hand the bytes to the
// library's own delivery decision.
//
// The markdown rendition is deliberately not here. Its route is a
// download route of its own: it answers 503 where these answer 404, it
// tells a failed read from an empty one, it opens its bytes itself
// rather than through the delivery decision, and it always attaches.
// Fitting it took four fields and a function-valued escape hatch that no
// other artifact used, and its own body grew for the privilege — a
// config struct pretending to be a seam. It keeps `attachment`, which is
// the part that really was shared (#316).
type renditionServeSpec struct {
	// noneMsg is the artifact's one not-found sentence: the answer to a
	// seam that is not wired, to a book that never had one, to a run
	// that has not finished, and to a pointer that leads nowhere.
	noneMsg string
	// resolveOp labels the server-error log line for the LibraryStore
	// resolve, the one step here that is a fault rather than an answer.
	resolveOp string
	// ready loads the artifact's tracking row, applies the
	// ready-plus-artifact-pointer gate and answers the id of the files
	// row holding the bytes. ok=false is every state no download is
	// offered for, including this artifact's store not being wired —
	// which is the same answer for the same reason.
	//
	// It returns the pointer rather than parking it in a variable the
	// locate step reads later: the two steps are one dependency, and the
	// compiler should be the thing that says so.
	ready func(ctx context.Context) (fileID string, ok bool)
	// locate resolves where those bytes live inside the library. Both
	// artifacts are renditionFileLocation today; it stays a field
	// because "where this artifact's bytes are" is the one question a
	// third one could answer differently.
	locate func(ctx context.Context, handle *service.LibraryHandle, bookID, fileID string) string
	// ext is the extension the download name carries; the name itself is
	// the book's sanitised title, the same for both artifacts.
	ext string
	// mime is the Content-Type the bytes are served with.
	mime string
}

// renditionServe is the shared chain behind both rendition arms of the
// file route: no library store → tracking row and its ready-plus-pointer
// gate → LibraryStore resolve → locate the bytes → the download header →
// the library's delivery decision. The order is load-bearing: a book
// with no such artifact is answered from its own tracking row rather
// than from a missing file. Held by TestRenditionServeGateChain over
// both artifacts.
func (h *Handler) renditionServe(c *gin.Context, book model.Book, spec renditionServeSpec) {
	ctx := c.Request.Context()
	// The chain resolves every artifact through the library store, so it
	// guards its own dependency rather than asking each adapter to
	// remember to. An install without one has no bytes to offer.
	if h.libStore == nil {
		writeError(c, http.StatusNotFound, spec.noneMsg)
		return
	}
	fileID, ok := spec.ready(ctx)
	if !ok {
		writeError(c, http.StatusNotFound, spec.noneMsg)
		return
	}
	handle, err := h.libStore.For(ctx, book.LibraryID)
	if err != nil {
		writeServerError(c, spec.resolveOp, err)
		return
	}
	location := spec.locate(ctx, handle, book.ID, fileID)
	if location == "" {
		writeError(c, http.StatusNotFound, spec.noneMsg)
		return
	}

	attachIfRequested(c, layout.SanitizeTitle(book.Title)+spec.ext)

	// The library's own delivery decision, not a second reading of where
	// its bytes live. Whether these bytes are redirected to, streamed
	// through the app or read off disk is one question per library, and
	// serveBookFile already asks it for the primary file.
	src, err := handle.FileSource(ctx, location)
	if err != nil {
		writeError(c, http.StatusNotFound, spec.noneMsg)
		return
	}
	if err := h.serveBookSource(c, src, spec.mime); err != nil {
		writeError(c, http.StatusForbidden, err.Error())
	}
}

// renditionFileLocation is the locate step for both artifacts, whose
// tracking row points at a files row: the narration and the generated
// EPUB. "" when the library cannot resolve it — a pointer at a row that
// is gone, which the chain answers as not-found.
//
// Serving needs the same lookup deleting does; what differs is that the
// delete has an ordering to respect, which is why that one lives with
// the delete in AudiobookService rather than here (#191).
func renditionFileLocation(
	ctx context.Context, handle *service.LibraryHandle, bookID, fileID string,
) string {
	if handle == nil || fileID == "" {
		return ""
	}
	f, ok := handle.BookFile(ctx, bookID, fileID)
	if !ok {
		return ""
	}
	return f.Location
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
