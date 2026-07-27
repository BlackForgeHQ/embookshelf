// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/service"
)

// sandboxPath is the serve side of the Book file sandbox. The rule
// itself lives in service.SandboxPath, because the delete side moved
// down into book deletion; keeping one implementation is the reason the
// sandbox is a named thing at all.
func sandboxPath(path string, roots []string) (string, error) {
	return service.SandboxPath(path, roots)
}

// bookFileRoots is the allow-list of directories the app may read book
// bytes from or delete them under: the BookDrop staging area plus every
// registered library root. A library with no local path (S3-backed)
// contributes nothing.
func (h *Handler) bookFileRoots(ctx context.Context) []string {
	roots := make([]string, 0, 4)
	if h.cfg.BookDropPath != "" {
		roots = append(roots, h.cfg.BookDropPath)
	}
	if h.lib != nil {
		if libs, err := h.lib.List(ctx); err == nil {
			for _, l := range libs {
				if l.Path != "" {
					roots = append(roots, l.Path)
				}
			}
		}
	}
	return roots
}

// sandboxedBookPath is the gate every handler read of a file named by a
// books.path value goes through.
func (h *Handler) sandboxedBookPath(c *gin.Context, path string) (string, error) {
	return sandboxPath(path, h.bookFileRoots(c.Request.Context()))
}
