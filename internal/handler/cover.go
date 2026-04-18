package handler

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/repo"
)

// BookCover streams the approved book's cover image. 404s for books without
// a cover — the template fallback (palette-generated cover) covers that case.
func (h *Handler) BookCover(c *gin.Context) {
	userID := requireUser(c)
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
		slog.Error("cover lookup", "id", id, "err", err)
		c.Status(http.StatusInternalServerError)
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
		slog.Error("cover open", "id", id, "err", err)
		c.Status(http.StatusInternalServerError)
		return
	}
	defer f.Close()

	mime := book.CoverMime
	if mime == "" {
		mime = "application/octet-stream"
	}
	c.Header("Content-Type", mime)
	c.Header("Cache-Control", "private, max-age=86400")
	if _, err := io.Copy(c.Writer, f); err != nil {
		slog.Warn("cover stream", "id", id, "err", err)
	}
}

// BookDropCover streams the pre-approval cover preview from the bookdrop
// namespace. Used by the queue UI to show thumbnails while items await
// review.
func (h *Handler) BookDropCover(c *gin.Context) {
	if requireUser(c) == "" {
		return
	}
	id := c.Param("id")
	item, err := h.bookdrop.Get(c.Request.Context(), id)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	if !item.HasCover || h.covers == nil {
		c.Status(http.StatusNotFound)
		return
	}

	f, err := h.covers.OpenBookDrop(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.Status(http.StatusNotFound)
			return
		}
		slog.Error("bookdrop cover open", "id", id, "err", err)
		c.Status(http.StatusInternalServerError)
		return
	}
	defer f.Close()

	mime := item.CoverMime
	if mime == "" {
		mime = "application/octet-stream"
	}
	c.Header("Content-Type", mime)
	c.Header("Cache-Control", "private, max-age=3600")
	if _, err := io.Copy(c.Writer, f); err != nil {
		slog.Warn("bookdrop cover stream", "id", id, "err", err)
	}
}
