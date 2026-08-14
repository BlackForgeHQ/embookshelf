// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// BookCover streams the approved book's cover image. 404s when either the
// book has no cover or the bytes are missing on disk — the SPA falls back to
// the generated typographic tile in that case.
//
// Which namespace the bytes come from is coverstore's problem, not this
// handler's: Open takes the book and resolves hashed-then-legacy itself,
// so a library part-way through the Covers backfill serves either kind
// without this route knowing a migration exists.
func (h *Handler) BookCover(c *gin.Context, s bookScope) {
	book := s.Book
	if !book.HasCover || h.covers == nil {
		c.Status(http.StatusNotFound)
		return
	}

	mime := coverContentType(book.CoverMime)

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

// coverContentType is what the three cover routes put on the wire,
// derived from the type stored on the row.
//
// Since #330 that stored type is sniffed from the bytes at ingest, so on
// a library imported after the fix this only ever passes its argument
// through. It exists for the rows that predate it — covers persisted
// with whatever an EPUB manifest or an ID3 frame claimed, and rows
// carried over by `import-sqlite`, neither of which is re-typed by
// anything. A type this route cannot vouch for is served as an opaque
// download instead: the browser saves it rather than rendering it, and a
// cover that was never an image was never going to display anyway.
//
// image/svg+xml is refused with the rest despite being an image type.
// SVG is a document that can carry script, and it is the one image type
// a nosniff header does not defuse.
func coverContentType(stored string) string {
	m := strings.ToLower(strings.TrimSpace(stored))
	if strings.HasPrefix(m, "image/") && !strings.Contains(m, "svg") && !strings.Contains(m, "xml") {
		return m
	}
	return "application/octet-stream"
}
