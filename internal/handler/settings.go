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
	"github.com/blackforge/embookshelf/internal/pattern"
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

type updateFileNamingPatternReq struct {
	// Pointer so callers can explicitly send null to clear the pattern.
	// A missing key is rejected (400) — the UI always sends one or the
	// other, so there's no ambiguity to decode.
	FileNamingPattern *string `json:"fileNamingPattern"`
}

// SettingsLibraryUpdateNamingPattern stores or clears the per-library file
// naming pattern. Spec §7.1 — the pattern is what the bookdrop approval
// flow uses to reorganize accepted files on disk. A null/blank value means
// "keep the original filename" (the resolver's fallback).
func (h *Handler) SettingsLibraryUpdateNamingPattern(c *gin.Context) {
	id := c.Param("id")
	var body updateFileNamingPatternReq
	// Hand-parse so we can distinguish "field missing" from "field null".
	if err := c.ShouldBindJSON(&body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid json")
		return
	}
	if err := h.lib.SetFileNamingPattern(c.Request.Context(), id, body.FileNamingPattern); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "library not found")
			return
		}
		writeServerError(c, "settings library pattern update", err)
		return
	}
	lib, err := h.lib.GetByID(c.Request.Context(), id)
	if err != nil {
		writeServerError(c, "settings library pattern refetch", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"library": toLibraryDTO(lib)})
}

type previewPatternReq struct {
	Pattern string `json:"pattern"`
	Sample  *struct {
		Title           string   `json:"title"`
		Subtitle        string   `json:"subtitle"`
		Authors         []string `json:"authors"`
		Year            int      `json:"year"`
		Series          string   `json:"series"`
		SeriesIndex     float64  `json:"seriesIndex"`
		Language        string   `json:"language"`
		Publisher       string   `json:"publisher"`
		ISBN            string   `json:"isbn"`
		CurrentFilename string   `json:"currentFilename"`
		Extension       string   `json:"extension"`
	} `json:"sample"`
}

// SettingsLibraryPreviewPattern resolves a (pattern, sample metadata) pair
// without touching any library row. Lets the settings UI show the shape of
// a pattern before the admin commits to saving it (spec §5 preview). Blank
// samples fall back to a canned "full" book so the page renders something
// useful before the admin types anything.
func (h *Handler) SettingsLibraryPreviewPattern(c *gin.Context) {
	var body previewPatternReq
	if !bindJSON(c, &body) {
		return
	}
	var in pattern.Input
	if body.Sample != nil {
		s := body.Sample
		in = pattern.Input{
			Title:           s.Title,
			Subtitle:        s.Subtitle,
			Authors:         s.Authors,
			Year:            s.Year,
			Series:          s.Series,
			SeriesIndex:     s.SeriesIndex,
			Language:        s.Language,
			Publisher:       s.Publisher,
			ISBN:            s.ISBN,
			CurrentFilename: s.CurrentFilename,
			Extension:       s.Extension,
		}
	} else {
		in = sampleBookInput()
	}
	if in.CurrentFilename == "" {
		in.CurrentFilename = "original-filename." + firstNonEmpty(in.Extension, "epub")
	}
	if in.Extension == "" {
		in.Extension = "epub"
	}
	c.JSON(http.StatusOK, gin.H{"resolved": pattern.Preview(body.Pattern, in)})
}

// sampleBookInput returns a canonical "full metadata" sample used by the
// preview endpoint when the client doesn't provide one.
func sampleBookInput() pattern.Input {
	return pattern.Input{
		Title:           "The Name of the Wind",
		Authors:         []string{"Patrick Rothfuss"},
		Year:            2007,
		Series:          "The Kingkiller Chronicle",
		SeriesIndex:     1,
		Publisher:       "DAW",
		ISBN:            "9780756404079",
		Language:        "en",
		Extension:       "epub",
		CurrentFilename: "the-name-of-the-wind.epub",
	}
}

type updateDefaultNamingPatternReq struct {
	// Pointer so clients can explicitly PUT {"pattern": null} to clear.
	// An empty string also clears; both produce "" in the DB.
	Pattern *string `json:"pattern"`
}

// SettingsDefaultNamingPatternGet returns the instance-wide default
// file-naming pattern. Empty string means "no default — libraries that
// don't override fall back to the original filename on approval".
func (h *Handler) SettingsDefaultNamingPatternGet(c *gin.Context) {
	pattern, err := h.appSettings.GetDefaultNamingPattern(c.Request.Context())
	if err != nil {
		writeServerError(c, "settings default naming pattern get", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"pattern": pattern})
}

// SettingsDefaultNamingPatternUpdate upserts the instance-wide default.
// Validates by round-tripping through the resolver so an unterminated
// block fails on save rather than silently being kept as an unused
// fallback.
func (h *Handler) SettingsDefaultNamingPatternUpdate(c *gin.Context) {
	var body updateDefaultNamingPatternReq
	if err := c.ShouldBindJSON(&body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid json")
		return
	}
	value := ""
	if body.Pattern != nil {
		value = strings.TrimSpace(*body.Pattern)
	}
	// A blank value is always allowed ("no default"). A non-blank value
	// must parse — the resolver is forgiving at runtime but we want the
	// admin to know about a typo now rather than on the next approval.
	if value != "" {
		sample := sampleBookInput()
		if sample.CurrentFilename == "" {
			sample.CurrentFilename = "sample." + firstNonEmpty(sample.Extension, "epub")
		}
		if out := pattern.Preview(value, sample); out == "" {
			writeError(c, http.StatusBadRequest, "pattern did not resolve to a non-empty path")
			return
		}
	}
	if err := h.appSettings.SetDefaultNamingPattern(c.Request.Context(), value); err != nil {
		writeServerError(c, "settings default naming pattern set", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"pattern": value})
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
