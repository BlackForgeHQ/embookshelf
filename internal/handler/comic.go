// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/repo"
)

// ComicPagesIndex returns the page count for a CBZ book. The reader uses
// this to size its navigation UI before requesting individual pages.
//
// Response: {"count": 142}
func (h *Handler) ComicPagesIndex(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	book, err := h.lib.GetBook(c.Request.Context(), userID, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "book not found")
			return
		}
		writeServerError(c, "comic pages lookup", err)
		return
	}
	if book.Format != "CBZ" {
		writeError(c, http.StatusUnsupportedMediaType, "not a comic book")
		return
	}
	if book.Path == "" {
		writeError(c, http.StatusNotFound, "no file on disk for this book")
		return
	}
	if err := h.assertPathAllowed(c, book.Path); err != nil {
		writeError(c, http.StatusForbidden, err.Error())
		return
	}
	pages, err := fileproc.CBZPages(book.Path)
	if err != nil {
		writeServerError(c, "list comic pages", err)
		return
	}
	c.Header("Cache-Control", "private, max-age=3600")
	c.JSON(http.StatusOK, gin.H{"count": len(pages)})
}

// ComicPage streams a single page (image bytes) from a CBZ archive.
// Pages are 0-indexed in natural sort order (page2.jpg before page10.jpg).
func (h *Handler) ComicPage(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	nStr := strings.TrimSpace(c.Param("n"))
	n, err := strconv.Atoi(nStr)
	if err != nil || n < 0 {
		writeError(c, http.StatusBadRequest, "invalid page number")
		return
	}
	book, err := h.lib.GetBook(c.Request.Context(), userID, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "book not found")
			return
		}
		writeServerError(c, "comic page lookup", err)
		return
	}
	if book.Format != "CBZ" {
		writeError(c, http.StatusUnsupportedMediaType, "not a comic book")
		return
	}
	if book.Path == "" {
		writeError(c, http.StatusNotFound, "no file on disk for this book")
		return
	}
	if err := h.assertPathAllowed(c, book.Path); err != nil {
		writeError(c, http.StatusForbidden, err.Error())
		return
	}

	// Long cache: page bytes within an archive are immutable for the
	// life of the underlying file. ETag would be more correct but the
	// content rarely changes — a 1-day private cache is plenty.
	c.Header("Cache-Control", "private, max-age=86400, immutable")

	// We can't know the MIME type without opening the archive once.
	// CBZPage handles that and writes directly into the response body.
	mime, err := fileproc.CBZPage(book.Path, n, c.Writer)
	if err != nil {
		// Headers may already be on the wire if the archive opened but
		// the entry was bad mid-stream. We do our best to surface a
		// clean error before any body bytes were written.
		if c.Writer.Written() {
			writeServerError(c, "stream comic page", err)
			return
		}
		writeError(c, http.StatusNotFound, err.Error())
		return
	}
	if !c.Writer.Written() {
		// Belt-and-suspenders: if Copy wrote zero bytes (empty entry),
		// still set the type so the browser knows what it got.
		c.Header("Content-Type", mime)
	}
}

// assertPathAllowed runs the same sandbox check serveBookFile uses, but
// without writing to the response. Returns nil when path is rooted under
// BOOKDROP_PATH or any registered library_path.
func (h *Handler) assertPathAllowed(c *gin.Context, p string) error {
	absPath, err := filepath.Abs(p)
	if err != nil {
		return errors.New("bad path")
	}
	roots := []string{}
	if h.cfg.BookDropPath != "" {
		if r, err := filepath.Abs(h.cfg.BookDropPath); err == nil {
			roots = append(roots, r)
		}
	}
	if h.lib != nil {
		if libs, err := h.lib.List(c.Request.Context()); err == nil {
			for _, l := range libs {
				if l.Path == "" {
					continue
				}
				if r, err := filepath.Abs(l.Path); err == nil {
					roots = append(roots, r)
				}
			}
		}
	}
	if len(roots) == 0 {
		return errors.New("no allowed roots configured")
	}
	sep := string(filepath.Separator)
	for _, root := range roots {
		if absPath == root || strings.HasPrefix(absPath, root+sep) {
			return nil
		}
	}
	return errors.New("path outside allowed roots")
}
