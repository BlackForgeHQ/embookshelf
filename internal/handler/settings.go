package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// settingsLibraryPathDTO mirrors model.LibraryPath for admin consumers.
type settingsLibraryPathDTO struct {
	ID              string  `json:"id"`
	LibraryID       string  `json:"libraryId"`
	Path            string  `json:"path"`
	LastScannedAt   *string `json:"lastScannedAt"` // pointer to emit JSON null when missing
	FileCount       int     `json:"fileCount"`
	DiscoveredCount int     `json:"discoveredCount"`
	CreatedAt       string  `json:"createdAt"`
}

func toLibraryPathDTO(p model.LibraryPath) settingsLibraryPathDTO {
	d := settingsLibraryPathDTO{
		ID:              p.ID,
		LibraryID:       p.LibraryID,
		Path:            p.Path,
		FileCount:       p.FileCount,
		DiscoveredCount: p.DiscoveredCount,
		CreatedAt:       p.CreatedAt.UTC().Format(time.RFC3339),
	}
	if p.LastScannedAt != nil {
		ts := p.LastScannedAt.UTC().Format(time.RFC3339)
		d.LastScannedAt = &ts
	}
	return d
}

// settingsLibraryDTO bundles a library with its configured filesystem
// paths — one round trip populates the Libraries admin pane.
type settingsLibraryDTO struct {
	libraryDTO
	Paths []settingsLibraryPathDTO `json:"paths"`
}

// SettingsLibraries lists every library with its registered paths + scan
// stats. Admin-only — the paths/scan surface is instance-wide config, not
// per-user data.
func (h *Handler) SettingsLibraries(c *gin.Context) {
	libs, err := h.lib.List(c.Request.Context())
	if err != nil {
		writeServerError(c, "settings list libraries", err)
		return
	}

	// One query per library is fine at the scale we care about (single-
	// digit libraries). If that ever changes, move this to a single
	// `LEFT JOIN library_paths` query with a GROUP BY.
	out := make([]settingsLibraryDTO, 0, len(libs))
	for _, l := range libs {
		paths, err := h.libPath.ListForLibrary(c.Request.Context(), l.ID)
		if err != nil {
			writeServerError(c, "settings list paths", err)
			return
		}
		pathDTOs := make([]settingsLibraryPathDTO, 0, len(paths))
		for _, p := range paths {
			pathDTOs = append(pathDTOs, toLibraryPathDTO(p))
		}
		out = append(out, settingsLibraryDTO{
			libraryDTO: toLibraryDTO(l),
			Paths:      pathDTOs,
		})
	}
	c.JSON(http.StatusOK, gin.H{"libraries": out})
}

type createPathReq struct {
	LibraryID string `json:"libraryId"`
	Path      string `json:"path"`
}

// SettingsLibraryPathCreate registers a new filesystem root under an
// existing library. Idempotency is enforced by a UNIQUE index on
// (library_id, path) — dup inserts surface as ErrLibraryPathTaken.
func (h *Handler) SettingsLibraryPathCreate(c *gin.Context) {
	var body createPathReq
	if !bindJSON(c, &body) {
		return
	}
	body.LibraryID = strings.TrimSpace(body.LibraryID)
	body.Path = strings.TrimSpace(body.Path)
	if body.LibraryID == "" || body.Path == "" {
		writeError(c, http.StatusBadRequest, "libraryId and path are required")
		return
	}
	path, err := h.libPath.Create(c.Request.Context(), body.LibraryID, body.Path)
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrLibraryPathTaken):
			writeError(c, http.StatusConflict, "that path is already registered for this library")
		case errors.Is(err, repo.ErrNotFound):
			writeError(c, http.StatusNotFound, "library not found")
		default:
			writeServerError(c, "settings path create", err)
		}
		return
	}
	c.JSON(http.StatusCreated, gin.H{"path": toLibraryPathDTO(path)})
}

// SettingsLibraryPathDelete removes a registered filesystem root. Books
// already imported from that root stay in the DB — the repo only removes
// the scan source, not its history.
func (h *Handler) SettingsLibraryPathDelete(c *gin.Context) {
	id := c.Param("id")
	if err := h.libPath.Delete(c.Request.Context(), id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "path not found")
			return
		}
		writeServerError(c, "settings path delete", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// SettingsLibraryPathScan enqueues a library.scan job for the given path.
// Actual work runs in the river worker (internal/task); the handler
// returns immediately with 202 so the UI can surface a "scanning" state
// that flips when file_count updates arrive.
func (h *Handler) SettingsLibraryPathScan(c *gin.Context) {
	id := c.Param("id")
	if _, err := h.libPath.Get(c.Request.Context(), id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "path not found")
			return
		}
		writeServerError(c, "settings path lookup", err)
		return
	}
	if h.queue == nil {
		writeError(c, http.StatusServiceUnavailable, "queue unavailable")
		return
	}
	if err := h.queue.EnqueueLibraryScan(c.Request.Context(), id); err != nil {
		writeServerError(c, "settings enqueue scan", err)
		return
	}
	c.Status(http.StatusAccepted)
}
