package handler

import (
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
)

// mimeForFormat returns the response Content-Type for a given book format,
// or "" when the format doesn't have a reader yet.
func mimeForFormat(format string) string {
	switch format {
	case "EPUB":
		return "application/epub+zip"
	case "PDF":
		return "application/pdf"
	case "CBZ":
		return "application/vnd.comicbook+zip"
	case "MP3":
		return "audio/mpeg"
	case "M4B":
		// Apple uses audio/mp4; the m4b container is identical to m4a.
		return "audio/mp4"
	}
	return ""
}

// serveBookFile chooses between a presigned redirect and local serve based
// on the book's backing storage backend. When the backend supports presign
// and EMBOOKSHELF_PRESIGN_FALLBACK is not "stream", a 302 redirect is
// issued. Otherwise the bytes are served via serveLocalBookFile (c.File()).
func (h *Handler) serveBookFile(c *gin.Context, book model.Book, mime string) error {
	src, err := ResolveBookSource(
		c.Request.Context(), book,
		h.files, h.libRepo, h.resolver,
		h.presignTTL, h.cfg.PresignFallback,
	)
	if err != nil {
		return err
	}
	if src.Kind == "presign" {
		c.Redirect(http.StatusFound, src.URL)
		return nil
	}
	return h.serveLocalBookFile(c, src.Path, mime)
}

// serveLocalBookFile validates that path is rooted under either BOOKDROP_PATH
// or one of the registered library_paths, then serves the bytes with the
// given content type via c.File() (honors Range headers natively).
func (h *Handler) serveLocalBookFile(c *gin.Context, path, mime string) error {
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

func parseIntOr(s string, def int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
