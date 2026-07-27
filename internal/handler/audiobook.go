// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/layout"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/sse"
	"github.com/blackforge/embookshelf/internal/tts"
)

// audiobookDTO is the per-book status the detail page polls.
//
// Carries the segment counts rather than a percentage: there is no
// job-status API, and progress is done-over-total on rows that survive a
// reload and a restart, which is the property a run measured in tens of
// minutes actually needs (ADR-0028 §7).
type audiobookDTO struct {
	BookID         string  `json:"bookId"`
	State          string  `json:"state"`
	Engine         string  `json:"engine"`
	Voice          string  `json:"voice"`
	Model          string  `json:"model,omitempty"`
	Error          string  `json:"error,omitempty"`
	SegmentsTotal  int     `json:"segmentsTotal"`
	SegmentsDone   int     `json:"segmentsDone"`
	SegmentsFailed int     `json:"segmentsFailed"`
	DurationS      float64 `json:"durationSeconds"`
	// Stale reports that the EPUB has changed since this narration was
	// made. Surfaced rather than acted on — throwing away hours of audio
	// because someone re-uploaded a better copy would be worse.
	Stale bool `json:"stale"`
}

// audiobookEstimateDTO is the pre-flight guardrail on a real-money action.
type audiobookEstimateDTO struct {
	Chars        int     `json:"chars"`
	Segments     int     `json:"segments"`
	AudioSeconds int     `json:"audioSeconds"`
	CostUSD      float64 `json:"costUsd"`
	Engine       string  `json:"engine"`
	Voice        string  `json:"voice"`
}

// audiobookGenerateRequest is the generate dialog's payload. Both fields
// are optional overrides of the instance default — the dialog exists
// because a different narrator for a different novel is most of the
// product (ADR-0026 §6).
type audiobookGenerateRequest struct {
	Voice string `json:"voice"`
	Model string `json:"model"`
}

// BookAudiobookGet returns the narration status for one book. 404 when
// none has ever been started, which the client reads as "offer Generate".
func (h *Handler) BookAudiobookGet(c *gin.Context) {
	if h.audiobooks == nil {
		writeError(c, http.StatusNotFound, "this book has no generated narration")
		return
	}
	// Taken once, before any other work. requireUserID writes its own 401
	// and returns "", so deriving the id part-way through — as this used
	// to, inline in the book lookup below — put a response on the wire
	// that the handler then followed with a 200.
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	ctx := c.Request.Context()
	id := c.Param("id")

	// The book is resolved first: this is a book-scoped route, and every
	// answer below is about that book. The error used to go to the blank
	// identifier, which handed the DTO a zero-value Book — so a book we
	// could not load was reported as never stale rather than as missing.
	book, err := h.lib.GetBook(ctx, userID, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "book not found")
			return
		}
		writeServerError(c, "audiobook book lookup", err)
		return
	}

	run, err := h.audiobookRepo.GetByBookID(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "this book has no generated narration")
			return
		}
		writeServerError(c, "audiobook get", err)
		return
	}
	cov, err := h.audiobookRepo.Coverage(ctx, id)
	if err != nil {
		writeServerError(c, "audiobook coverage", err)
		return
	}
	c.JSON(http.StatusOK, h.audiobookDTO(c, book, run, cov))
}

// BookAudiobookEstimate reports what a run would cost without starting
// one. Admin-only like generation itself: the number is the guardrail,
// and it is only useful to whoever can act on it.
func (h *Handler) BookAudiobookEstimate(c *gin.Context) {
	book, opts, ok := h.audiobookPreflight(c)
	if !ok {
		return
	}

	est, err := h.audiobooks.Estimate(c.Request.Context(), book, opts)
	if err != nil {
		h.writeAudiobookError(c, err)
		return
	}
	c.JSON(http.StatusOK, audiobookEstimateDTO{
		Chars:        est.Chars,
		Segments:     est.Segments,
		AudioSeconds: est.AudioSeconds,
		CostUSD:      est.CostUSD,
		Engine:       est.Engine,
		Voice:        est.Voice,
	})
}

// BookAudiobookGenerate starts a run. 202 — the work is tens of queued
// jobs and the client polls the status endpoint from here.
//
// Regeneration is destructive by design (ADR-0025 §4), so this endpoint
// overwrites an existing narration. The type-to-confirm lives in the UI,
// where the user can see what they are replacing.
func (h *Handler) BookAudiobookGenerate(c *gin.Context) {
	book, opts, ok := h.audiobookPreflight(c)
	if !ok {
		return
	}

	var req audiobookGenerateRequest
	// A body is optional: generating with the instance defaults is the
	// common case and should not require one.
	_ = c.ShouldBindJSON(&req)
	if req.Voice != "" {
		opts.Voice = req.Voice
	}
	if req.Model != "" {
		opts.Model = req.Model
	}

	if err := h.audiobooks.Start(c.Request.Context(), book, opts); err != nil {
		h.writeAudiobookError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"queued": true})
}

// BookAudiobookCancel stops a run in flight. The only stop-loss on a run
// that may cost a hundred dollars, so it is deliberately available for
// as long as the run is not terminal.
func (h *Handler) BookAudiobookCancel(c *gin.Context) {
	if h.audiobooks == nil {
		writeErrorCode(c, http.StatusServiceUnavailable, CodeAudiobooksDisabled,
			"audiobook generation is not configured")
		return
	}
	id := c.Param("id")
	if err := h.audiobooks.Cancel(c.Request.Context(), id); err != nil {
		h.writeAudiobookError(c, err)
		return
	}
	h.publishAudiobookUpdated(id)
	c.Status(http.StatusNoContent)
}

// BookAudiobookRetry re-enqueues the segments that never finished, and
// only those — the completed ones are already paid for.
func (h *Handler) BookAudiobookRetry(c *gin.Context) {
	if h.audiobooks == nil {
		writeErrorCode(c, http.StatusServiceUnavailable, CodeAudiobooksDisabled,
			"audiobook generation is not configured")
		return
	}
	if err := h.audiobooks.Retry(c.Request.Context(), c.Param("id")); err != nil {
		h.writeAudiobookError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"queued": true})
}

// BookAudiobookDelete removes a narration: the run record, the files row,
// and the bytes. The book keeps its EPUB.
func (h *Handler) BookAudiobookDelete(c *gin.Context) {
	if h.audiobooks == nil {
		writeError(c, http.StatusNotFound, "this book has no generated narration")
		return
	}
	// Same reason as BookAudiobookGet: the id was derived inline in the
	// book lookup below, where a 401 from requireUserID would have been
	// followed by this handler's own 204.
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	ctx := c.Request.Context()
	id := c.Param("id")

	run, err := h.audiobookRepo.GetByBookID(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "this book has no generated narration")
			return
		}
		writeServerError(c, "audiobook delete lookup", err)
		return
	}

	// Resolve the key before the row goes, exactly as BookDelete does:
	// deleting the files row is what makes the location unknowable.
	// Deliberately not deferred — a deferred cleanup would also fire on
	// the failure path below, deleting the audio out from under a run
	// that still points at it.
	var (
		handle   *service.LibraryHandle
		location string
	)
	if run.FileID != nil && h.libStore != nil {
		if book, berr := h.lib.GetBook(ctx, userID, id); berr == nil {
			if lh, herr := h.libStore.For(ctx, book.LibraryID); herr == nil {
				handle = lh
				location = narrationLocation(ctx, handle, id, run)
			}
		}
	}

	if err := h.audiobookRepo.Delete(ctx, id); err != nil {
		writeServerError(c, "audiobook delete", err)
		return
	}

	if handle != nil && location != "" {
		if derr := handle.DeleteBookBytes(ctx, id, []string{location}); derr != nil {
			slog.Warn("audiobook delete: byte cleanup", "book", id, "err", derr)
		}
	}
	h.publishAudiobookUpdated(id)
	c.Status(http.StatusNoContent)
}

// audiobookPreflight resolves the book, the settings and the run options
// every generate-shaped endpoint needs, writing the error response itself
// when anything is missing. Returns ok=false once a response is written.
func (h *Handler) audiobookPreflight(c *gin.Context) (model.Book, service.AudiobookOptions, bool) {
	var (
		zeroBook model.Book
		zeroOpts service.AudiobookOptions
	)
	if h.audiobooks == nil || h.appSettings == nil {
		writeErrorCode(c, http.StatusServiceUnavailable, CodeAudiobooksDisabled,
			"audiobook generation is not configured")
		return zeroBook, zeroOpts, false
	}

	cfg, err := h.appSettings.GetAudiobook(c.Request.Context())
	if err != nil {
		writeServerError(c, "audiobook settings", err)
		return zeroBook, zeroOpts, false
	}
	if !cfg.Enabled {
		writeErrorCode(c, http.StatusServiceUnavailable, CodeAudiobooksDisabled,
			"audiobook generation is not enabled")
		return zeroBook, zeroOpts, false
	}
	id, engine, err := cfg.SelectedEngine()
	if err != nil {
		writeErrorCode(c, http.StatusServiceUnavailable, CodeAudiobooksDisabled, err.Error())
		return zeroBook, zeroOpts, false
	}

	book, err := h.lib.GetBook(c.Request.Context(), requireUserID(c), c.Param("id"))
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "book not found")
			return zeroBook, zeroOpts, false
		}
		writeServerError(c, "audiobook book lookup", err)
		return zeroBook, zeroOpts, false
	}
	// The first of the three gates the Narratable format passes through,
	// mirroring Send-to-Kindle's eligible-format checks: UI, handler,
	// worker. A re-import can change a book's format between them.
	if !service.Narratable(book.Format) {
		writeErrorCode(c, http.StatusUnsupportedMediaType, CodeFormatNotNarratable,
			"only EPUB books can be narrated")
		return zeroBook, zeroOpts, false
	}

	return book, service.AudiobookOptions{
		Engine:               string(id),
		Voice:                engine.DefaultVoice,
		Model:                engine.Model,
		SegmentChars:         cfg.SegmentChars,
		PricePerMillionChars: engine.PricePerMillionChars,
		SourceContentHash:    h.primaryContentHash(c, book),
	}, true
}

// primaryContentHash reads the hash of the book's own file so the run can
// record what it was made from. Best effort — a missing hash costs the
// staleness badge, not the narration.
func (h *Handler) primaryContentHash(c *gin.Context, book model.Book) []byte {
	if h.libStore == nil {
		return nil
	}
	handle, err := h.libStore.For(c.Request.Context(), book.LibraryID)
	if err != nil {
		return nil
	}
	return handle.PrimaryContentHash(c.Request.Context(), book)
}

func (h *Handler) audiobookDTO(c *gin.Context, book model.Book, run model.Audiobook, cov model.AudiobookCoverage) audiobookDTO {
	return audiobookDTO{
		BookID:         run.BookID,
		State:          string(run.State),
		Engine:         run.Engine,
		Voice:          run.Voice,
		Model:          run.Model,
		Error:          run.Error,
		SegmentsTotal:  cov.Total,
		SegmentsDone:   cov.Done,
		SegmentsFailed: cov.Failed,
		DurationS:      float64(run.DurationMS) / 1000,
		Stale:          h.narrationIsStale(c, book, run),
	}
}

// narrationIsStale compares what the run was made from against the book's
// current file. A mismatch means the user replaced their EPUB after
// narrating it, and the audio is of the older text.
func (h *Handler) narrationIsStale(c *gin.Context, book model.Book, run model.Audiobook) bool {
	if len(run.SourceContentHash) == 0 {
		return false
	}
	current := h.primaryContentHash(c, book)
	if len(current) == 0 {
		return false
	}
	return !bytes.Equal(current, run.SourceContentHash)
}

// narrationLocation resolves the storage key of a run's generated audio.
//
// Matches on file_id rather than on the ".mp3" extension. Provenance is
// the pointer (ADR-0025 §2), and an extension check would misidentify an
// ingested MP3 audiobook sitting beside a narrated EPUB as ours to
// delete.
func narrationLocation(ctx context.Context, handle *service.LibraryHandle, bookID string, run model.Audiobook) string {
	if run.FileID == nil || handle == nil {
		return ""
	}
	f, ok := handle.BookFile(ctx, bookID, *run.FileID)
	if !ok {
		return ""
	}
	return f.Location
}

// publishAudiobookUpdated tells open pages the narration changed. The
// workers publish their own terminal states; these two transitions are
// user-driven and would otherwise be silent until a refresh.
func (h *Handler) publishAudiobookUpdated(bookID string) {
	if h.hub == nil {
		return
	}
	_ = h.hub.Publish(sse.AudiobookUpdated{BookID: bookID})
}

// writeAudiobookError maps the service's errors onto the envelope.
func (h *Handler) writeAudiobookError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrNotNarratable):
		writeErrorCode(c, http.StatusUnsupportedMediaType, CodeFormatNotNarratable,
			"only EPUB books can be narrated")
	case errors.Is(err, tts.ErrPermanent):
		writeError(c, http.StatusBadGateway, err.Error())
	case errors.Is(err, repo.ErrNotFound):
		writeError(c, http.StatusNotFound, "this book has no generated narration")
	default:
		// Cancel-a-finished-run and retry-with-nothing-outstanding both
		// land here: the caller asked for something the current state does
		// not allow, which is a conflict rather than a server fault.
		writeError(c, http.StatusConflict, err.Error())
	}
}

// renditionAudio is the ?rendition= value that selects a book's
// generated narration over its primary file.
const renditionAudio = "audio"

// serveNarrationRendition streams a book's generated audio.
//
// Exists because books.format names the *primary* format and the reader
// dispatches on the rendition the user picked, not on that column
// (ADR-0025 §3). Without an explicit selector the narration has no URL:
// the ordinary file route resolves through primaryFile, which by
// definition returns the EPUB.
func (h *Handler) serveNarrationRendition(c *gin.Context, book model.Book) {
	if h.audiobookRepo == nil || h.libStore == nil {
		writeError(c, http.StatusNotFound, "this book has no generated narration")
		return
	}
	ctx := c.Request.Context()

	run, err := h.audiobookRepo.GetByBookID(ctx, book.ID)
	if err != nil || run.State != model.AudiobookReady || run.FileID == nil {
		writeError(c, http.StatusNotFound, "this book has no generated narration")
		return
	}
	handle, err := h.libStore.For(ctx, book.LibraryID)
	if err != nil {
		writeServerError(c, "narration library handle", err)
		return
	}
	location := narrationLocation(ctx, handle, book.ID, run)
	if location == "" {
		writeError(c, http.StatusNotFound, "this book has no generated narration")
		return
	}

	if c.Query("download") != "" {
		filename := layout.SanitizeTitle(book.Title) + ".mp3"
		c.Header("Content-Disposition",
			fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
				asciiFallback(filename), url.PathEscape(filename)))
	}

	if handle.IsBackendBacked() {
		src := service.BookSource{
			Kind:    service.BookDeliveryStream,
			Storage: handle.Storage,
			Key:     location,
		}
		if err := h.serveStreamedBookFile(c, src, "audio/mpeg"); err != nil {
			writeError(c, http.StatusForbidden, err.Error())
		}
		return
	}

	// Local libraries go through the same sandbox every other filesystem
	// read of a book file passes, so a malformed location cannot escape
	// the library tree.
	if err := h.serveLocalBookFile(c, handle.LocalPath(location), "audio/mpeg"); err != nil {
		writeError(c, http.StatusForbidden, err.Error())
	}
}
