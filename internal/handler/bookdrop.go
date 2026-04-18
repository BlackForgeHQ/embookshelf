package handler

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/middleware"
	"github.com/blackforge/embookshelf/internal/sse"
	"github.com/blackforge/embookshelf/internal/view/page"
	"github.com/blackforge/embookshelf/internal/view/partial"
)

func (h *Handler) BookDrop(c *gin.Context) {
	userID := requireUser(c)
	if userID == "" {
		return
	}
	items, err := h.bookdrop.List(c.Request.Context())
	if err != nil {
		slog.Error("list bookdrop", "err", err)
		c.String(http.StatusInternalServerError, "failed to load bookdrop queue")
		return
	}

	libs, _ := h.lib.List(c.Request.Context())
	shelves, _ := h.shelf.List(c.Request.Context(), userID)
	render(c, page.BookDrop(libs, shelves, items, h.cfg.BookDropPath, h.cfg.DiskType))
}

func (h *Handler) BookDropRow(c *gin.Context) {
	id := c.Param("id")
	item, err := h.bookdrop.Get(c.Request.Context(), id)
	if err != nil {
		c.String(http.StatusNotFound, "item not found")
		return
	}
	render(c, partial.BookDropRow(item))
}

func (h *Handler) BookDropApprove(c *gin.Context) {
	id := c.Param("id")
	if _, err := h.bookdrop.Approve(c.Request.Context(), id, ""); err != nil {
		slog.Error("bookdrop approve", "id", id, "err", err)
		c.String(http.StatusInternalServerError, "approve failed")
		return
	}
	h.swapRowOrRedirect(c, id)
}

func (h *Handler) BookDropReject(c *gin.Context) {
	id := c.Param("id")
	if err := h.bookdrop.Reject(c.Request.Context(), id); err != nil {
		slog.Error("bookdrop reject", "id", id, "err", err)
		c.String(http.StatusInternalServerError, "reject failed")
		return
	}
	h.swapRowOrRedirect(c, id)
}

func (h *Handler) swapRowOrRedirect(c *gin.Context, id string) {
	if middleware.IsHTMX(c.Request) && !middleware.IsHTMXBoosted(c.Request) {
		item, err := h.bookdrop.Get(c.Request.Context(), id)
		if err != nil {
			c.String(http.StatusInternalServerError, "row fetch failed")
			return
		}
		render(c, partial.BookDropRow(item))
		return
	}
	c.Redirect(http.StatusSeeOther, "/app/bookdrop")
}

func (h *Handler) Events(c *gin.Context) {
	w := c.Writer
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		c.String(http.StatusInternalServerError, "streaming unsupported")
		return
	}

	ch, cancel := h.hub.Subscribe(32)
	defer cancel()

	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()

	ctx := c.Request.Context()
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			writeSSE(w, ev)
			flusher.Flush()
		case <-keepalive.C:
			_, _ = io.WriteString(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func writeSSE(w io.Writer, ev sse.Event) {
	if ev.Name != "" {
		fmt.Fprintf(w, "event: %s\n", ev.Name)
	}
	fmt.Fprintf(w, "data: %s\n\n", ev.Data)
}
