// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/layout"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// renditionEpub is BookFile's second rendition selector, beside
// renditionAudio: a book consumed as its generated EPUB (ADR-0034).
const renditionEpub = "epub"

// epubRenditionStore is the slice of BookEpubRenditionRepo the routes
// need.
type epubRenditionStore interface {
	Start(ctx context.Context, bookID string) error
	GetByBookID(ctx context.Context, bookID string) (model.EpubRendition, error)
}

// newEpubRenditionStore keeps a missing repo nil across the interface
// conversion — same trap as newMarkdownRenditionStore.
func newEpubRenditionStore(r *repo.BookEpubRenditionRepo) epubRenditionStore {
	if r == nil {
		return nil
	}
	return r
}

// epubRenditionDTO is the status answer, mirroring the markdown DTO.
type epubRenditionDTO struct {
	State            string     `json:"state"`
	Error            string     `json:"error,omitempty"`
	ConverterVersion string     `json:"converterVersion,omitempty"`
	Stale            bool       `json:"stale"`
	UpdatedAt        *time.Time `json:"updatedAt,omitempty"`
}

// BookEpubGet answers the generated EPUB's state for the Versions tab.
func (h *Handler) BookEpubGet(c *gin.Context, s bookScope) {
	ctx := c.Request.Context()
	if h.epubRenditions == nil {
		writeError(c, http.StatusServiceUnavailable, "generated EPUBs are unavailable")
		return
	}
	rendition, err := h.epubRenditions.GetByBookID(ctx, s.Book.ID)
	if errors.Is(err, repo.ErrNotFound) {
		c.JSON(http.StatusOK, epubRenditionDTO{State: "none"})
		return
	}
	if err != nil {
		writeServerError(c, "read epub rendition", err)
		return
	}
	dto := epubRenditionDTO{
		State:            string(rendition.State),
		Error:            rendition.Error,
		ConverterVersion: rendition.ConverterVersion,
		UpdatedAt:        &rendition.UpdatedAt,
	}
	// A ready row whose file has since gone missing (purged after a
	// scan) is offered as regenerable, not downloadable.
	if rendition.State == model.RenditionReady && rendition.FileID == nil {
		dto.State = "none"
	}
	dto.Stale = h.sourceStale(ctx, s.Book, rendition.State, rendition.SourceContentHash)
	c.JSON(http.StatusOK, dto)
}

// BookEpubGenerate enqueues a render. Admin-gated at the route, same
// gates as the markdown button — plus the same instant refusals, so
// the click answers now rather than a job failing in thirty seconds.
func (h *Handler) BookEpubGenerate(c *gin.Context, s bookScope) {
	ctx := c.Request.Context()
	if h.epubRenditions == nil {
		writeError(c, http.StatusServiceUnavailable, "generated EPUBs are unavailable")
		return
	}
	if !model.Convertible(s.Book.Format) {
		writeErrorCode(c, http.StatusUnsupportedMediaType, CodeFormatNotConvertible,
			"only "+model.ConvertibleFormatList()+" books can become a generated EPUB")
		return
	}
	if _, ok := h.requireConverter(c); !ok {
		return
	}
	q, ok := h.requireQueue(c)
	if !ok {
		return
	}
	if err := h.epubRenditions.Start(ctx, s.Book.ID); err != nil {
		writeServerError(c, "start epub rendition", err)
		return
	}
	if err := q.Enqueue(ctx, jobs.EpubRenderArgs{BookID: s.Book.ID}); err != nil {
		writeServerError(c, "queue epub render", err)
		return
	}
	c.Status(http.StatusAccepted)
}

// serveEpubRendition streams a book's generated EPUB — BookFile's
// ?rendition=epub arm, mirroring serveNarrationRendition: resolve the
// row, insist on ready-and-pointing-at-a-file, then let the library's
// own delivery decision serve the bytes.
func (h *Handler) serveEpubRendition(c *gin.Context, book model.Book) {
	if h.libStore == nil || h.epubRenditions == nil {
		writeError(c, http.StatusNotFound, "this book has no generated EPUB")
		return
	}
	ctx := c.Request.Context()

	rendition, err := h.epubRenditions.GetByBookID(ctx, book.ID)
	if err != nil || rendition.State != model.RenditionReady || rendition.FileID == nil {
		writeError(c, http.StatusNotFound, "this book has no generated EPUB")
		return
	}
	handle, err := h.libStore.For(ctx, book.LibraryID)
	if err != nil {
		writeServerError(c, "epub library handle", err)
		return
	}
	f, ok := handle.BookFile(ctx, book.ID, *rendition.FileID)
	if !ok {
		writeError(c, http.StatusNotFound, "this book has no generated EPUB")
		return
	}

	if c.Query("download") != "" {
		filename := layout.SanitizeTitle(book.Title) + ".epub"
		c.Header("Content-Disposition",
			fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
				asciiFallback(filename), url.PathEscape(filename)))
	}

	src, err := handle.FileSource(ctx, f.Location)
	if err != nil {
		writeError(c, http.StatusNotFound, "this book has no generated EPUB")
		return
	}
	if err := h.serveBookSource(c, src, model.MIMEForFormat("EPUB")); err != nil {
		writeError(c, http.StatusForbidden, err.Error())
	}
}
