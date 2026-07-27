// SPDX-License-Identifier: AGPL-3.0-or-later

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
// book has no cover or the bytes are missing on disk — the SPA falls back to
// the generated typographic tile in that case.
//
// Which namespace the bytes come from is coverstore's problem, not this
// handler's: Open takes the book and resolves hashed-then-legacy itself,
// so a library part-way through the Covers backfill serves either kind
// without this route knowing a migration exists.
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

	mime := book.CoverMime
	if mime == "" {
		mime = "application/octet-stream"
	}

	rc, err := h.covers.Open(book)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.Status(http.StatusNotFound)
			return
		}
		writeServerError(c, "cover open", err)
		return
	}
	defer func() { _ = rc.Close() }()

	c.Header("Content-Type", mime)
	c.Header("Cache-Control", "private, max-age=86400")
	if _, err := io.Copy(c.Writer, rc); err != nil {
		// Headers are already on the wire; log + move on.
		writeServerError(c, "cover stream", err)
	}
}
