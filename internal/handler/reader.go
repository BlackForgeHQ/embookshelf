package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/view/page"
)

// BookRead renders the full-screen reader shell. The heavy lifting (EPUB
// parsing, pagination, typography) happens client-side in reader.js.
func (h *Handler) BookRead(c *gin.Context) {
	userID := requireUser(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	book, err := h.lib.GetBook(c.Request.Context(), userID, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			c.String(http.StatusNotFound, "book not found")
			return
		}
		slog.Error("get book", "id", id, "err", err)
		c.String(http.StatusInternalServerError, "failed to load book")
		return
	}
	render(c, page.Read(book))
}

// BookFile streams the book's on-disk file to the browser. The path must
// live under BOOKDROP_PATH to prevent traversal outside the configured root.
func (h *Handler) BookFile(c *gin.Context) {
	userID := requireUser(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	book, err := h.lib.GetBook(c.Request.Context(), userID, id)
	if err != nil {
		c.String(http.StatusNotFound, "book not found")
		return
	}
	if book.Path == "" {
		c.String(http.StatusNotFound, "no file on disk for this book")
		return
	}
	mime := mimeForFormat(book.Format)
	if mime == "" {
		c.String(http.StatusUnsupportedMediaType, "reader doesn't support this format yet")
		return
	}
	if err := h.serveBookFile(c, book.Path, mime); err != nil {
		slog.Warn("serve book file rejected", "id", id, "path", book.Path, "err", err)
		c.String(http.StatusForbidden, err.Error())
		return
	}
}

// mimeForFormat returns the response Content-Type for a given book format,
// or "" when the format doesn't have a reader yet.
func mimeForFormat(format string) string {
	switch format {
	case "EPUB":
		return "application/epub+zip"
	case "PDF":
		return "application/pdf"
	}
	return ""
}

// serveBookFile validates that path is rooted under either BOOKDROP_PATH or
// one of the registered library_paths, then serves the bytes with the given
// content type.
func (h *Handler) serveBookFile(c *gin.Context, path, mime string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return errors.New("bad path")
	}

	roots := []string{}
	if h.cfg.BookDropPath != "" {
		if r, err := filepath.Abs(h.cfg.BookDropPath); err == nil {
			roots = append(roots, r)
		}
	}
	if h.libPath != nil {
		if paths, err := h.libPath.List(c.Request.Context()); err == nil {
			for _, p := range paths {
				if r, err := filepath.Abs(p.Path); err == nil {
					roots = append(roots, r)
				}
			}
		}
	}
	if len(roots) == 0 {
		return errors.New("no allowed roots configured")
	}

	sep := string(filepath.Separator)
	allowed := false
	for _, root := range roots {
		if absPath == root || strings.HasPrefix(absPath, root+sep) {
			allowed = true
			break
		}
	}
	if !allowed {
		return errors.New("path outside allowed roots")
	}

	c.Header("Content-Type", mime)
	c.Header("Cache-Control", "private, max-age=3600")
	c.File(absPath)
	return nil
}
