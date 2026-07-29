// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"fmt"
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
//
// The 401 and the missing-book 404 are both the book-scoped seam's, taken
// before this body runs. Both used to be this handler's, and both were got
// wrong: the user id was derived part-way through, after a 200 was already
// possible, and the book lookup's error went to the blank identifier, which
// handed the DTO a zero-value Book — so a book we could not load was
// reported as never stale rather than as missing.
func (h *Handler) BookAudiobookGet(c *gin.Context, s bookScope) {
	if h.audiobooks == nil {
		writeError(c, http.StatusNotFound, "this book has no generated narration")
		return
	}
	// Report rather than a repo read: it reconciles the run with its
	// segments before answering, so this poll is also where a run that
	// lost its finalize job gets it back, and it derives staleness where
	// the run's provenance lives (#191).
	rep, err := h.audiobooks.Report(c.Request.Context(), s.Book)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "this book has no generated narration")
			return
		}
		writeServerError(c, "audiobook get", err)
		return
	}
	c.JSON(http.StatusOK, audiobookDTOFrom(rep))
}

// BookAudiobookEstimate reports what a run would cost without starting
// one. Admin-only like generation itself: the number is the guardrail,
// and it is only useful to whoever can act on it.
func (h *Handler) BookAudiobookEstimate(c *gin.Context, s bookScope) {
	if h.audiobooks == nil {
		writeErrorCode(c, http.StatusServiceUnavailable, CodeAudiobooksDisabled,
			service.ErrAudiobooksNotConfigured.Error())
		return
	}

	est, err := h.audiobooks.EstimateRun(c.Request.Context(), s.Book, service.GenerateOverride{})
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
func (h *Handler) BookAudiobookGenerate(c *gin.Context, s bookScope) {
	if h.audiobooks == nil {
		writeErrorCode(c, http.StatusServiceUnavailable, CodeAudiobooksDisabled,
			service.ErrAudiobooksNotConfigured.Error())
		return
	}

	var req audiobookGenerateRequest
	// A body is optional: generating with the instance defaults is the
	// common case and should not require one.
	_ = c.ShouldBindJSON(&req)

	over := service.GenerateOverride{Voice: req.Voice, Model: req.Model}
	if err := h.audiobooks.Generate(c.Request.Context(), s.Book, over); err != nil {
		h.writeAudiobookError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"queued": true})
}

// BookAudiobookCancel stops a run in flight. The only stop-loss on a run
// that may cost a hundred dollars, so it is deliberately available for
// as long as the run is not terminal.
func (h *Handler) BookAudiobookCancel(c *gin.Context, s bookScope) {
	if h.audiobooks == nil {
		writeErrorCode(c, http.StatusServiceUnavailable, CodeAudiobooksDisabled,
			"audiobook generation is not configured")
		return
	}
	id := s.Book.ID
	// No publish here: Cancel makes the transition and the module that
	// makes a transition emits it. This handler used to remember to, and
	// a second caller of Cancel would not have (#210).
	if err := h.audiobooks.Cancel(c.Request.Context(), id); err != nil {
		h.writeAudiobookError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// BookAudiobookRetry re-enqueues the segments that never finished, and
// only those — the completed ones are already paid for.
func (h *Handler) BookAudiobookRetry(c *gin.Context, s bookScope) {
	if h.audiobooks == nil {
		writeErrorCode(c, http.StatusServiceUnavailable, CodeAudiobooksDisabled,
			"audiobook generation is not configured")
		return
	}
	if err := h.audiobooks.Retry(c.Request.Context(), s.Book.ID); err != nil {
		h.writeAudiobookError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"queued": true})
}

// BookAudiobookDelete removes a narration: the run record, the files
// row, the book's chapter list and duration, and the bytes. The book
// keeps its EPUB.
func (h *Handler) BookAudiobookDelete(c *gin.Context, s bookScope) {
	if h.audiobooks == nil {
		writeError(c, http.StatusNotFound, "this book has no generated narration")
		return
	}
	// One call: the ordering invariant — resolve the location while the
	// row that names it still exists, delete the bytes only once the row
	// is gone — belongs with the delete, the way DeleteBook has it (#191).
	if err := h.audiobooks.DeleteNarration(c.Request.Context(), s.Book); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "this book has no generated narration")
			return
		}
		writeServerError(c, "audiobook delete", err)
		return
	}
	h.publishAudiobookUpdated(s.Book.ID)
	c.Status(http.StatusNoContent)
}

func audiobookDTOFrom(rep service.AudiobookReport) audiobookDTO {
	run, cov := rep.Run, rep.Coverage
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
		Stale:          rep.Stale,
	}
}

// narrationLocation resolves the storage key of a run's generated audio.
// Serving it needs the same lookup deleting it does; what differs is that
// the delete has an ordering to respect, which is why that one lives with
// the delete in AudiobookService rather than here (#191).
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

// publishAudiobookUpdated tells open pages the narration changed.
//
// One caller left: deleting a narration removes the row rather than
// moving it, so there is no transition to hang the event off. Every
// state change publishes from AudiobookService.transition instead.
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
			service.ErrNotNarratable.Error())
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

	if handle.IsObjectStore() {
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
