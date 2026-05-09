// SPDX-License-Identifier: AGPL-3.0-or-later

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

// BookCover streams the approved book's cover image. 404s when either the
// book has no cover or the file is missing on disk — the SPA falls back to
// the generated typographic tile in that case.
//
// Lookup order:
//  1. Hash-keyed path (covers/<hash[0:2]>/<hash>.<ext>) when cover_hash is set.
//  2. Legacy id-keyed path (books/<id>) for books whose cover_hash has not
//     yet been populated by the boot-time backfill.
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

	// Try hash-keyed path first.
	if len(book.CoverHash) > 0 {
		rc, err := h.covers.OpenBookHashed(book.CoverHash, book.CoverMime)
		if err == nil {
			defer func() { _ = rc.Close() }()
			c.Header("Content-Type", mime)
			c.Header("Cache-Control", "private, max-age=86400")
			if _, err := io.Copy(c.Writer, rc); err != nil {
				writeServerError(c, "cover stream", err)
			}
			return
		}
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("cover open hashed", "book_id", id, "err", err)
			writeServerError(c, "cover open", err)
			return
		}
		// ErrNotExist: fall through to legacy path.
	}

	// Legacy fallback: id-keyed path (books/<id>). Used for covers that
	// pre-date the hash-keyed migration and haven't been backfilled yet.
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

	c.Header("Content-Type", mime)
	c.Header("Cache-Control", "private, max-age=86400")
	if _, err := io.Copy(c.Writer, f); err != nil {
		// Headers are already on the wire; log + move on.
		writeServerError(c, "cover stream", err)
	}
}
