package handler

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/middleware"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/view/page"
	"github.com/blackforge/embookshelf/internal/view/partial"
)

func (h *Handler) Library(c *gin.Context) {
	userID := requireUser(c)
	if userID == "" {
		return
	}

	libs, err := h.lib.List(c.Request.Context())
	if err != nil {
		slog.Error("list libraries", "err", err)
		c.String(http.StatusInternalServerError, "failed to load libraries")
		return
	}

	activeSlug := c.Query("lib")
	if activeSlug == "" && len(libs) > 0 {
		activeSlug = libs[0].Slug
	}

	params := model.SearchParams{
		Query:  c.Query("q"),
		Format: c.QueryArray("format"),
		Sort:   c.Query("sort"),
	}

	books, err := h.lib.Search(c.Request.Context(), userID, activeSlug, params)
	if err != nil {
		slog.Error("search library", "slug", activeSlug, "err", err)
		c.String(http.StatusInternalServerError, "failed to load books")
		return
	}

	if middleware.IsHTMX(c.Request) && !middleware.IsHTMXBoosted(c.Request) {
		render(c, partial.LibraryGrid(books))
		return
	}

	shelves, err := h.shelf.List(c.Request.Context(), userID)
	if err != nil {
		slog.Error("list shelves", "err", err)
		shelves = nil
	}
	render(c, page.Library(libs, shelves, books, activeSlug, params, h.cfg.DiskType))
}
