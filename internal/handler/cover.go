package handler

import (
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/repo"
)

// BookCover streams the approved book's cover image. 404s when either the
// book has no cover or the file is missing on disk — the SPA falls back to
// the generated typographic tile in that case.
func (h *Handler) BookCover(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	book, err := h.lib.GetBook(c.Request.Context(), userID, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			c.Status(http.StatusNotFound)
			return
		}
		writeServerError(c, "cover lookup", err)
		return
	}
	if !book.HasCover || h.covers == nil {
		c.Status(http.StatusNotFound)
		return
	}
	f, err := h.covers.OpenBook(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.Status(http.StatusNotFound)
			return
		}
		writeServerError(c, "cover open", err)
		return
	}
	defer func() { _ = f.Close() }()

	mime := book.CoverMime
	if mime == "" {
		mime = "application/octet-stream"
	}
	c.Header("Content-Type", mime)
	c.Header("Cache-Control", "private, max-age=86400")
	if _, err := io.Copy(c.Writer, f); err != nil {
		// Headers are already on the wire; log + move on.
		writeServerError(c, "cover stream", err)
	}
}
