package handler

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

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
