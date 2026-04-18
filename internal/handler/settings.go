package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/view/page"
)

// SettingsHome is the landing for /app/settings — a shell that points at
// the sub-pages. For now only the Libraries section is populated.
func (h *Handler) SettingsHome(c *gin.Context) {
	userID := requireUser(c)
	if userID == "" {
		return
	}
	libs, _ := h.lib.List(c.Request.Context())
	shelves, _ := h.shelf.List(c.Request.Context(), userID)
	render(c, page.Settings(libs, shelves, h.cfg.DiskType))
}

// SettingsLibraries lists every library with its registered filesystem paths
// and recent scan stats. Admin-only because paths are system state, not user
// preferences.
func (h *Handler) SettingsLibraries(c *gin.Context) {
	userID := requireUser(c)
	if userID == "" {
		return
	}
	u := auth.UserFromContext(c.Request.Context())
	if u == nil || u.Role != model.RoleAdmin {
		c.String(http.StatusForbidden, "admin only")
		return
	}

	libs, err := h.lib.List(c.Request.Context())
	if err != nil {
		slog.Error("list libs", "err", err)
		c.String(http.StatusInternalServerError, "failed")
		return
	}
	paths, err := h.libPath.List(c.Request.Context())
	if err != nil {
		slog.Error("list paths", "err", err)
		c.String(http.StatusInternalServerError, "failed")
		return
	}
	shelves, _ := h.shelf.List(c.Request.Context(), userID)
	render(c, page.SettingsLibraries(libs, shelves, paths, h.cfg.DiskType))
}

// LibraryPathCreate registers a new filesystem root for a library. Admin-only.
func (h *Handler) LibraryPathCreate(c *gin.Context) {
	u := auth.UserFromContext(c.Request.Context())
	if u == nil || u.Role != model.RoleAdmin {
		c.String(http.StatusForbidden, "admin only")
		return
	}
	libID := strings.TrimSpace(c.PostForm("library_id"))
	path := strings.TrimSpace(c.PostForm("path"))
	if libID == "" || path == "" {
		c.String(http.StatusBadRequest, "library_id and path required")
		return
	}
	if _, err := h.libPath.Create(c.Request.Context(), libID, path); err != nil {
		if errors.Is(err, repo.ErrLibraryPathTaken) {
			c.Redirect(http.StatusSeeOther, "/app/settings/libraries")
			return
		}
		slog.Error("create library path", "lib", libID, "path", path, "err", err)
		c.String(http.StatusInternalServerError, "failed to add path")
		return
	}
	c.Redirect(http.StatusSeeOther, "/app/settings/libraries")
}

// LibraryPathDelete removes a filesystem root from its library. Admin-only.
func (h *Handler) LibraryPathDelete(c *gin.Context) {
	u := auth.UserFromContext(c.Request.Context())
	if u == nil || u.Role != model.RoleAdmin {
		c.String(http.StatusForbidden, "admin only")
		return
	}
	id := c.Param("id")
	if err := h.libPath.Delete(c.Request.Context(), id); err != nil && !errors.Is(err, repo.ErrNotFound) {
		slog.Error("delete library path", "id", id, "err", err)
		c.String(http.StatusInternalServerError, "failed")
		return
	}
	c.Redirect(http.StatusSeeOther, "/app/settings/libraries")
}

// LibraryPathScan enqueues a river job that walks the path and stages new
// files into the bookdrop queue for review. Returns immediately — progress
// shows up on the settings page via the last_scanned_at / discovered_count
// columns once the worker finishes.
func (h *Handler) LibraryPathScan(c *gin.Context) {
	u := auth.UserFromContext(c.Request.Context())
	if u == nil || u.Role != model.RoleAdmin {
		c.String(http.StatusForbidden, "admin only")
		return
	}
	if h.queue == nil {
		c.String(http.StatusServiceUnavailable, "queue unavailable")
		return
	}
	id := c.Param("id")
	// Verify the path exists so we fail loudly on a bad id rather than
	// enqueuing a doomed job.
	if _, err := h.libPath.Get(c.Request.Context(), id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			c.String(http.StatusNotFound, "path not found")
			return
		}
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.queue.EnqueueLibraryScan(c.Request.Context(), id); err != nil {
		slog.Error("enqueue scan", "id", id, "err", err)
		c.String(http.StatusInternalServerError, "failed to enqueue")
		return
	}
	c.Redirect(http.StatusSeeOther, "/app/settings/libraries")
}
