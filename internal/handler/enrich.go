package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/middleware"
	"github.com/blackforge/embookshelf/internal/provider"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/view/partial"
)

// EnrichSearch runs a metadata search across providers and renders the
// results fragment into the book-edit page.
//
// Query source: explicit ?title and ?author params (passed from the current
// form values) override the book's stored values, so typing in the edit form
// refines subsequent searches.
func (h *Handler) EnrichSearch(c *gin.Context) {
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
		slog.Error("enrich lookup", "id", id, "err", err)
		c.String(http.StatusInternalServerError, "failed")
		return
	}

	q := provider.Query{
		Title:  strings.TrimSpace(firstNonEmpty(c.Query("title"), book.Title)),
		Author: strings.TrimSpace(firstNonEmpty(c.Query("author"), book.Author)),
		ISBN:   strings.TrimSpace(firstNonEmpty(c.Query("isbn"), book.ISBN)),
	}

	matches, err := h.enrich.Search(c.Request.Context(), q)
	if err != nil {
		slog.Error("enrich search", "id", id, "err", err)
		c.String(http.StatusBadGateway, "search failed")
		return
	}
	render(c, partial.EnrichResults(id, matches))
}

// EnrichApplyCover fetches the selected match's cover image, stores it in
// the coverstore, and returns the refreshed book-detail panel (so HTMX can
// swap the cover). The URL must match the provider allow-list or we refuse.
func (h *Handler) EnrichApplyCover(c *gin.Context) {
	userID := requireUser(c)
	if userID == "" {
		return
	}
	id := c.Param("id")

	rawURL := strings.TrimSpace(c.PostForm("url"))
	if rawURL == "" {
		c.String(http.StatusBadRequest, "url required")
		return
	}

	if _, err := h.enrich.ImportCoverFromURL(c.Request.Context(), id, rawURL); err != nil {
		if errors.Is(err, service.ErrBadCoverURL) {
			c.String(http.StatusBadRequest, "cover url not from an allowed provider")
			return
		}
		slog.Error("import cover", "id", id, "err", err)
		c.String(http.StatusBadGateway, "cover fetch failed")
		return
	}

	book, err := h.lib.GetBook(c.Request.Context(), userID, id)
	if err != nil {
		slog.Error("reload book", "id", id, "err", err)
		c.String(http.StatusInternalServerError, "failed")
		return
	}
	if middleware.IsHTMX(c.Request) && !middleware.IsHTMXBoosted(c.Request) {
		shelfSlugs, _ := h.shelf.SlugsForBook(c.Request.Context(), userID, id)
		userShelves, _ := h.shelf.List(c.Request.Context(), userID)
		render(c, partial.BookDetailPanel(book, shelfSlugs, userShelves))
		return
	}
	c.Redirect(http.StatusSeeOther, "/app/book/"+id)
}

func firstNonEmpty(xs ...string) string {
	for _, s := range xs {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
