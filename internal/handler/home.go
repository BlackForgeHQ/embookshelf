package handler

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/view/page"
)

func (h *Handler) Home(c *gin.Context) {
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

	total := 0
	for _, lib := range libs {
		books, err := h.lib.Books(c.Request.Context(), userID, lib.Slug)
		if err != nil {
			slog.Error("count books", "lib", lib.Slug, "err", err)
			continue
		}
		total += len(books)
	}

	shelves, err := h.shelf.List(c.Request.Context(), userID)
	if err != nil {
		slog.Error("list shelves", "err", err)
		shelves = nil
	}

	render(c, page.Home(libs, shelves, total, h.cfg.DiskType))
}
