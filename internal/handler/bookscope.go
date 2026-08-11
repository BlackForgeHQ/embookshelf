// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// bookStore is the handler tier's read view of the catalog.
//
// GetByID is what the book-scoped seam below is built on; Search rides
// along because the list surfaces read the same store and there is no
// second catalog to point them at. Declared as an interface rather than
// taking *repo.BookRepo so a book-scoped handler body is reachable in a
// test with a fake — which the five-line preamble made impossible, since
// every body began by talking to a real database.
type bookStore interface {
	GetByID(ctx context.Context, userID, id string) (model.Book, error)
	// Search returns one window of the catalog plus the total match
	// count — paging and cross-library aggregation are the catalog's
	// answer, not the caller's arithmetic (#241).
	Search(ctx context.Context, userID, librarySlug string, p model.SearchParams) ([]model.Book, int, error)
}

// bookScope is what the book-scoped seam resolves before a handler body
// runs: who is asking, and the book they asked about.
type bookScope struct {
	UserID string
	Book   model.Book
}

// bookHandler is a handler body that starts from a resolved book.
//
// The book arrives as an argument rather than out of the gin context on
// purpose: a body cannot run without one, so the seam fails closed by
// construction instead of by convention. Wiring a book-scoped route
// without the seam does not compile.
type bookHandler func(*gin.Context, bookScope)

// bookScoped is the book-scoped seam: the one place that takes the
// session user, resolves the :id route parameter against the catalog,
// answers 404 for a book that is not there and 500 for a lookup that
// failed, and hands the body a loaded book.
//
// It exists because that preamble was part of the interface every book
// endpoint had to learn and restate — roughly two dozen call sites across
// eight files, with the not-found sentinel branched on in most of them.
// Restating it is what let audiobook status drop the error on the floor
// and report on a zero-value Book, and what let the reading-guide routes
// skip the existence check altogether.
//
// Routes that only need the book to exist take the scope and ignore the
// Book field; the check is no longer an idiom a handler can forget.
func (h *Handler) bookScoped(fn bookHandler) gin.HandlerFunc {
	return h.scopeBook(requireUserID, writeBookLookupError, fn)
}

// opdsBookScoped is the same seam over the OPDS surface, which
// authenticates with HTTP Basic and answers in plain text rather than the
// Error envelope. Same resolve, same branch, different vocabulary — the
// two response shapes are a real difference between the surfaces, not a
// duplicated rule.
func (h *Handler) opdsBookScoped(fn bookHandler) gin.HandlerFunc {
	return h.scopeBook(opdsUserID, writeOPDSBookLookupError, fn)
}

// scopeBook is the resolve itself. identify returns "" once it has
// written its own 401; fail renders a lookup error in the caller's
// vocabulary. Neither can fall through to fn.
func (h *Handler) scopeBook(
	identify func(*gin.Context) string,
	fail func(*gin.Context, error),
	fn bookHandler,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := identify(c)
		if userID == "" {
			return
		}
		book, err := h.books.GetByID(c.Request.Context(), userID, c.Param("id"))
		if err != nil {
			fail(c, err)
			return
		}
		fn(c, bookScope{UserID: userID, Book: book})
	}
}

// writeBookLookupError renders a failed resolve as the Error envelope.
func writeBookLookupError(c *gin.Context, err error) {
	if errors.Is(err, repo.ErrNotFound) {
		writeError(c, http.StatusNotFound, "book not found")
		return
	}
	writeServerError(c, "book lookup", err)
}

// writeOPDSBookLookupError renders a failed resolve for an OPDS reader.
// Plain text, because that surface has never spoken the Error envelope
// and a catalog client only reads the status anyway.
func writeOPDSBookLookupError(c *gin.Context, err error) {
	if errors.Is(err, repo.ErrNotFound) {
		c.String(http.StatusNotFound, "book not found")
		return
	}
	c.String(http.StatusInternalServerError, "internal error")
}
