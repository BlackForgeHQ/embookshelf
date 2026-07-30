// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// appSettingsStore is the handler tier's view of the app_settings rows,
// declared here for the same reason bookStore is declared in
// bookscope.go: an interface the settings endpoints can be driven
// against, rather than *repo.AppSettingsRepo, which drags a live
// Postgres into every test and so meant none were written. The rules
// inside these endpoints — the tri-state secret, the keep-versus-clear
// contract for every stored credential, which repo refusal is a 400 and
// which is a 500 — were unreachable without a database, and every one of
// them went untested.
//
// Narrow on purpose: exactly the methods the settings endpoints call and
// nothing else. The rows are typed accessors rather than a generic
// get/set because the row owns its own normalize-and-validate (see
// repo.Setting), and a handler that reached past that would be able to
// persist a config the CLI would refuse.
//
// Writes the OIDC panel makes are absent by design — those go through
// service.OIDCSettingsService as one decision (#195), so this store only
// reads OIDC rows.
type appSettingsStore interface {
	GetBool(ctx context.Context, name string) (bool, error)
	SetBool(ctx context.Context, name string, v bool) error

	GetEmail(ctx context.Context) (repo.EmailConfig, error)
	SetEmail(ctx context.Context, cfg repo.EmailConfig) error

	GetReadingGuide(ctx context.Context) (repo.ReadingGuideConfig, error)
	SetReadingGuide(ctx context.Context, cfg repo.ReadingGuideConfig) error

	GetAudiobook(ctx context.Context) (repo.AudiobookConfig, error)
	SetAudiobook(ctx context.Context, cfg repo.AudiobookConfig) error

	GetForwardAuth(ctx context.Context) (repo.ForwardAuthConfig, error)
	SetForwardAuth(ctx context.Context, cfg repo.ForwardAuthConfig) error

	GetOIDCAutoProvision(ctx context.Context) (repo.OIDCAutoProvisionDetails, error)
	GetGoogle(ctx context.Context) (repo.OAuthPresetConfig, error)
	GetGitHub(ctx context.Context) (repo.OAuthPresetConfig, error)
	GetGenericOIDC(ctx context.Context) (repo.GenericOIDCConfig, error)
}

// The production store is the repo itself; this is where that stops
// compiling if the two drift.
var _ appSettingsStore = (*repo.AppSettingsRepo)(nil)

// resolveSecret reconciles the three-state secret input every settings
// panel sends, so the SMTP password and the OIDC client secrets follow
// one rule:
//   - new value provided       → use it
//   - empty + "set" still true → keep existing (admin just didn't retype it)
//   - empty + "set" false      → explicit clear
func resolveSecret(incoming string, setFlag bool, existing string) string {
	if incoming != "" {
		return incoming
	}
	if setFlag {
		return existing
	}
	return ""
}

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
	Name string `json:"name" binding:"required"`
	Kind string `json:"kind"`
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
	if name == "" {
		writeError(c, http.StatusBadRequest, "name is required")
		return
	}

	lib, err := h.lib.Create(c.Request.Context(), name, kind)
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrLibraryNameTaken):
			writeError(c, http.StatusConflict, "a library with that name already exists")
		case errors.Is(err, repo.ErrLibraryPathTaken):
			writeError(c, http.StatusConflict, "that filesystem path is already bound to another library")
		case errors.Is(err, service.ErrS3NotConfigured):
			writeError(c, http.StatusBadRequest, err.Error())
		case errors.Is(err, service.ErrDataPathNotConfigured):
			writeError(c, http.StatusBadRequest, err.Error())
		default:
			writeServerError(c, "settings library create", err)
		}
		return
	}

	// Optional async initial scan. River queue owns the actual work; a
	// missing queue is a deployment smell but shouldn't block creation —
	// the library exists either way, so this site logs rather than taking
	// requireQueue's degrade, which would fail a create that succeeded.
	if body.Scan && h.queue != nil {
		if err := h.queue.Enqueue(c.Request.Context(), jobs.LibraryScanArgs{LibraryID: lib.ID}); err != nil {
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
	q, ok := h.requireQueue(c)
	if !ok {
		return
	}
	if err := q.Enqueue(c.Request.Context(), jobs.LibraryScanArgs{LibraryID: id}); err != nil {
		writeServerError(c, "settings enqueue scan", err)
		return
	}
	c.Status(http.StatusAccepted)
}
