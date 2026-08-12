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

// errRenditionNone is a gate's way of saying "this book has no such
// artifact to serve" — no row, a run still going, a ready row pointing
// at nothing. One sentinel rather than three refusals, so the 404 and
// its sentence are written once per artifact instead of once per state.
var errRenditionNone = errors.New("rendition: nothing to serve")

// renditionDeliver streams the located bytes to the client.
//
// attach is the download header, handed over rather than written by the
// chain because the moment to set it differs: a delivery that can still
// fail (markdown opens the bytes itself) must answer that failure
// without an attachment header on it, while the ones that hand off to
// serveBookSource have to set it before the first byte is written.
type renditionDeliver func(
	ctx context.Context, handle *service.LibraryHandle, location string, attach func(),
)

// renditionServeSpec is one artifact's serve configuration — the third
// leg of the rendition route seam, beside generate and status. What the
// narration, the generated EPUB and the markdown rendition each add to
// the shared chain is this struct and a locate function.
type renditionServeSpec struct {
	// available reports the artifact's stores are wired. False is the
	// deliberate degrade, and the two ways of expressing it differ:
	// markdown's own download route answers 503 because the route exists
	// and the feature does not, while a ?rendition= arm of the file
	// route answers 404 — it must not claim a book has an artifact this
	// install cannot serve.
	available         bool
	unavailableStatus int
	unavailableMsg    string
	// noneMsg is the artifact's one not-found sentence, the answer to
	// every state no download is offered for.
	noneMsg string
	// readOp labels the server-error log line for a tracking-row read
	// that failed for some reason other than "nothing to serve". Only
	// the gates that tell those two apart need it.
	readOp string
	// resolveOp labels it for the LibraryStore resolve.
	resolveOp string
	// gate loads the tracking row and applies the ready-plus-artifact-
	// pointer gate: errRenditionNone for every state the UI must not
	// offer a download for, any other error for a genuine fault.
	gate func(ctx context.Context) error
	// locate resolves where the bytes live inside the library. "" is a
	// row pointing at something the library cannot resolve, which the
	// chain answers as not-found rather than as a broken stream.
	locate func(ctx context.Context, handle *service.LibraryHandle) string
	// ext is the extension the download name carries; the name itself is
	// the book's sanitised title, the same for all three artifacts.
	ext string
	// attachAlways marks a route that is a download route: markdown's
	// answer is always an attachment, while the ?rendition= arms stay
	// inline for the reader until ?download= asks otherwise.
	attachAlways bool
	// mime is the Content-Type the default delivery serves with.
	mime string
	// deliver overrides that default. Nil is the library's own delivery
	// decision — presign, stream or local, the same one the primary file
	// goes through. Markdown overrides it: a rendition that is megabytes
	// of text at worst is always streamed through the app.
	deliver renditionDeliver
}

// renditionServe is the shared chain behind every rendition download:
// unwired seam → tracking row → ready-plus-pointer gate → LibraryStore
// resolve → locate the bytes → the download header → deliver. The order
// is load-bearing: every refusal is taken before the one that costs a
// lookup, and a book with no such artifact is answered from its own
// tracking row rather than from a missing file. Held by
// TestRenditionServeGateChain over all three artifacts.
func (h *Handler) renditionServe(c *gin.Context, book model.Book, spec renditionServeSpec) {
	ctx := c.Request.Context()
	if !spec.available {
		writeError(c, spec.unavailableStatus, spec.unavailableMsg)
		return
	}
	if err := spec.gate(ctx); err != nil {
		if errors.Is(err, errRenditionNone) {
			writeError(c, http.StatusNotFound, spec.noneMsg)
			return
		}
		writeServerError(c, spec.readOp, err)
		return
	}
	handle, err := h.libStore.For(ctx, book.LibraryID)
	if err != nil {
		writeServerError(c, spec.resolveOp, err)
		return
	}
	location := spec.locate(ctx, handle)
	if location == "" {
		writeError(c, http.StatusNotFound, spec.noneMsg)
		return
	}

	attach := func() {
		filename := layout.SanitizeTitle(book.Title) + spec.ext
		if spec.attachAlways {
			attachment(c, filename)
			return
		}
		attachIfRequested(c, filename)
	}
	if spec.deliver != nil {
		spec.deliver(ctx, handle, location, attach)
		return
	}

	// The library's own delivery decision, not a second reading of where
	// its bytes live. Whether these bytes are redirected to, streamed
	// through the app or read off disk is one question per library, and
	// serveBookFile already asks it for the primary file.
	attach()
	src, err := handle.FileSource(ctx, location)
	if err != nil {
		writeError(c, http.StatusNotFound, spec.noneMsg)
		return
	}
	if err := h.serveBookSource(c, src, spec.mime); err != nil {
		writeError(c, http.StatusForbidden, err.Error())
	}
}

// renditionFileLocation is the locate step for the two artifacts whose
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
