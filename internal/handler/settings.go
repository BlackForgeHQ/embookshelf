package handler

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// settingsLibraryDTO exposes the library row plus its scan aggregates
// (last-scan timestamp, counts) in one shape the admin UI renders as
// a single card. Path is inline because a library owns exactly one
// filesystem root since migration 000018.
type settingsLibraryDTO struct {
	libraryDTO
	Path            string  `json:"path"`
	LastScannedAt   *string `json:"lastScannedAt"`
	FileCount       int     `json:"fileCount"`
	DiscoveredCount int     `json:"discoveredCount"`
}

// SettingsLibraries lists every library with its path + scan stats.
// Admin-only — the path/scan surface is instance-wide config, not
// per-user data.
func (h *Handler) SettingsLibraries(c *gin.Context) {
	libs, err := h.lib.List(c.Request.Context())
	if err != nil {
		writeServerError(c, "settings list libraries", err)
		return
	}

	out := make([]settingsLibraryDTO, 0, len(libs))
	for _, l := range libs {
		d := settingsLibraryDTO{
			libraryDTO:      toLibraryDTO(l),
			Path:            l.Path,
			FileCount:       l.FileCount,
			DiscoveredCount: l.DiscoveredCount,
		}
		if l.LastScannedAt != nil {
			ts := l.LastScannedAt.UTC().Format(time.RFC3339)
			d.LastScannedAt = &ts
		}
		out = append(out, d)
	}
	c.JSON(http.StatusOK, gin.H{"libraries": out})
}

// SettingsLibraryDelete tears down a library and every book, library
// path, shelf assignment, annotation, and reading session that depends
// on it. Source files on disk are intentionally left alone — library
// paths point at user-managed roots, so "unregister this library" is
// not the same as "wipe the bytes". Cover-image files are cleaned up
// best-effort because they're owned by this service.
func (h *Handler) SettingsLibraryDelete(c *gin.Context) {
	id := c.Param("id")
	purge := c.Query("purge") == "true"
	bookIDs, err := h.lib.DeleteLibrary(c.Request.Context(), id, purge)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "library not found")
			return
		}
		writeServerError(c, "settings library delete", err)
		return
	}
	if h.covers != nil {
		for _, bookID := range bookIDs {
			if err := h.covers.DeleteBook(bookID); err != nil {
				slog.Warn("library delete: cover cleanup", "bookId", bookID, "err", err)
			}
		}
	}
	c.Status(http.StatusNoContent)
}

type createLibraryReq struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // "local" (default) | "s3"
	Path string `json:"path"` // required for kind=local; ignored for kind=s3
	Scan bool   `json:"scan"`
}

// SettingsLibraryCreate provisions a new library bound to a single
// filesystem path. The path is fixed at creation time; editing the
// path on an existing library is intentionally not exposed (books on
// disk hold absolute paths that would drift, and the naming-pattern
// placement assumes a stable root).
//
// Layout-wise the response matches SettingsLibraries so the client can
// drop the new card straight into the admin table without a round trip.
func (h *Handler) SettingsLibraryCreate(c *gin.Context) {
	var body createLibraryReq
	if !bindJSON(c, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	kind := service.LibraryKind(strings.TrimSpace(body.Kind))
	path := strings.TrimRight(strings.TrimSpace(body.Path), "/")
	if name == "" {
		writeError(c, http.StatusBadRequest, "name is required")
		return
	}
	// path is required for local libraries but optional for s3 libraries.
	if kind != service.LibraryKindS3 && path == "" {
		writeError(c, http.StatusBadRequest, "path is required for local libraries")
		return
	}

	lib, err := h.lib.Create(c.Request.Context(), name, kind, path)
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrLibraryNameTaken):
			writeError(c, http.StatusConflict, "a library with that name already exists")
		case errors.Is(err, repo.ErrLibraryPathTaken):
			writeError(c, http.StatusConflict, "that filesystem path is already bound to another library")
		case errors.Is(err, service.ErrS3NotConfigured):
			writeError(c, http.StatusBadRequest, err.Error())
		default:
			writeServerError(c, "settings library create", err)
		}
		return
	}

	// Optional async initial scan. River queue owns the actual work; a
	// missing queue is a deployment smell but shouldn't block creation.
	if body.Scan && h.queue != nil {
		if err := h.queue.EnqueueLibraryScan(c.Request.Context(), lib.ID); err != nil {
			slog.Warn("enqueue library scan after create failed", "library", lib.ID, "err", err)
		}
	}

	c.JSON(http.StatusCreated, gin.H{
		"library": settingsLibraryDTO{
			libraryDTO:      toLibraryDTO(lib),
			Path:            lib.Path,
			FileCount:       lib.FileCount,
			DiscoveredCount: lib.DiscoveredCount,
		},
	})
}

type scanLibraryReq struct {
	Paths []string `json:"paths"`
}

// SettingsLibraryScan counts processable files under the provided
// paths *without* creating a library. The UI calls this from the
// "New library" dialog so it can warn the admin about very large
// roots before submit.
//
// Missing/unreadable paths don't fail the response; they're logged and
// skipped. The walk only inspects extensions (fileproc.IsSupported),
// not file contents, so it's cheap enough to run synchronously.
func (h *Handler) SettingsLibraryScan(c *gin.Context) {
	var body scanLibraryReq
	if !bindJSON(c, &body) {
		return
	}
	ctx := c.Request.Context()
	var total int64
	seen := make(map[string]struct{}, len(body.Paths))
	for _, raw := range body.Paths {
		cleaned := strings.TrimRight(strings.TrimSpace(raw), "/")
		if cleaned == "" {
			continue
		}
		if _, dup := seen[cleaned]; dup {
			continue
		}
		seen[cleaned] = struct{}{}
		info, err := os.Stat(cleaned)
		if err != nil || !info.IsDir() {
			slog.Warn("prescan skip unreadable path", "path", cleaned, "err", err)
			continue
		}
		_ = filepath.WalkDir(cleaned, func(p string, d fs.DirEntry, werr error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if werr != nil || d.IsDir() {
				return nil
			}
			if fileproc.IsSupported(p) {
				total++
			}
			return nil
		})
	}
	c.JSON(http.StatusOK, gin.H{"count": total})
}

// SettingsLibraryRescan enqueues a library.scan job for a library's
// single filesystem root. Actual work runs in the river worker
// (internal/task); the handler returns immediately with 202 so the UI
// can surface a "scanning" state that flips when file_count updates
// arrive.
func (h *Handler) SettingsLibraryRescan(c *gin.Context) {
	id := c.Param("id")
	if _, err := h.lib.GetByID(c.Request.Context(), id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "library not found")
			return
		}
		writeServerError(c, "settings library lookup", err)
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
