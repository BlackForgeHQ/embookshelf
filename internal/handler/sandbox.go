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
// bytes from. Collected by service.BookFileRoots, not here: this tier
// carried a character-for-character copy of that loop, and a third
// hand-rolled variant lived in the comic reader until CBZ moved onto the
// storage seam. An allow-list the serve side and the delete side compute
// separately is one edit away from disagreeing, which is the failure the
// sandbox exists to make impossible.
//
// h.lib is a required dependency, but the nil check survives the move:
// the interface would otherwise hold a typed nil pointer and the listing
// would panic instead of degrading to BookDrop alone.
func (h *Handler) bookFileRoots(ctx context.Context) []string {
	var libs service.LibraryLister
	if h.lib != nil {
		libs = h.lib
	}
	return service.BookFileRoots(ctx, h.cfg.BookDropPath, libs)
}

// sandboxedBookPath is the gate every handler read of a file named by a
// books.path value goes through.
func (h *Handler) sandboxedBookPath(c *gin.Context, path string) (string, error) {
	return sandboxPath(path, h.bookFileRoots(c.Request.Context()))
}
