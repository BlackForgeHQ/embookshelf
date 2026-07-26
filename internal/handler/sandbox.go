// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// ErrPathOutsideRoots is returned when a path does not resolve inside
// any configured root. Serving and deleting share the rule, so a
// change to the sandbox cannot apply to one and miss the other.
var ErrPathOutsideRoots = errors.New("path outside allowed roots")

// sandboxPath resolves path and confirms it lands inside one of roots,
// returning the cleaned absolute path. Fails closed: an empty root list
// admits nothing. Comparison is on cleaned absolute paths with a
// separator-terminated prefix, so a traversal escape is rejected and
// `/data/lib` never admits `/data/lib-backup`.
func sandboxPath(path string, roots []string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("bad path")
	}

	sep := string(filepath.Separator)
	for _, root := range roots {
		if root == "" {
			continue
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if abs == absRoot || strings.HasPrefix(abs, absRoot+sep) {
			return abs, nil
		}
	}
	return "", ErrPathOutsideRoots
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

// sandboxedBookPath is the one gate both the serve and delete paths go
// through before touching a file named by a books.path value.
func (h *Handler) sandboxedBookPath(c *gin.Context, path string) (string, error) {
	return sandboxPath(path, h.bookFileRoots(c.Request.Context()))
}
