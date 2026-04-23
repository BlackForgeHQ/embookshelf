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
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/pattern"
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

type createLibraryReq struct {
	Name  string   `json:"name"`
	Paths []string `json:"paths"`
	Scan  bool     `json:"scan"`
}

// SettingsLibraryCreate provisions a new library and (optionally) registers
// initial filesystem paths under it. Mirrors the "Library Creator" flow from
// spec/library-creation.spec.md, adapted to embookshelf's simpler model:
// name + paths only (no icon/watch/format policy — not modeled yet).
//
// Layout-wise the response matches SettingsLibraries so the client can drop
// the new card straight into the admin table without a round trip.
func (h *Handler) SettingsLibraryCreate(c *gin.Context) {
	var body createLibraryReq
	if !bindJSON(c, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(c, http.StatusBadRequest, "name is required")
		return
	}
	if len(body.Paths) == 0 {
		writeError(c, http.StatusBadRequest, "at least one path is required")
		return
	}

	lib, err := h.lib.Create(c.Request.Context(), name)
	if err != nil {
		if errors.Is(err, repo.ErrLibraryNameTaken) {
			writeError(c, http.StatusConflict, "a library with that name already exists")
			return
		}
		writeServerError(c, "settings library create", err)
		return
	}

	// Register paths one-by-one. Dup paths in the same request are silently
	// collapsed; a DB-level dup (ErrLibraryPathTaken) can't fire here because
	// the library was just created, but we still handle the sentinel
	// defensively instead of 500-ing.
	paths := make([]model.LibraryPath, 0, len(body.Paths))
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
		p, perr := h.libPath.Create(c.Request.Context(), lib.ID, cleaned)
		if perr != nil {
			if errors.Is(perr, repo.ErrLibraryPathTaken) {
				continue
			}
			writeServerError(c, "settings library create path", perr)
			return
		}
		paths = append(paths, p)
	}

	// Optional async initial scan (spec §3.3 step 4 / §6.1 step 7). River
	// queue owns the actual work; a missing queue is a deployment smell but
	// shouldn't block creation — we just skip enqueueing.
	if body.Scan && h.queue != nil {
		for _, p := range paths {
			if err := h.queue.EnqueueLibraryScan(c.Request.Context(), p.ID); err != nil {
				slog.Warn("enqueue library scan after create failed", "path", p.Path, "err", err)
			}
		}
	}

	pathDTOs := make([]settingsLibraryPathDTO, 0, len(paths))
	for _, p := range paths {
		pathDTOs = append(pathDTOs, toLibraryPathDTO(p))
	}
	c.JSON(http.StatusCreated, gin.H{
		"library": settingsLibraryDTO{
			libraryDTO: toLibraryDTO(lib),
			Paths:      pathDTOs,
		},
	})
}

type scanLibraryReq struct {
	Paths []string `json:"paths"`
}

// SettingsLibraryScan counts processable files under the provided paths
// *without* creating a library. Spec §5.1 — the UI calls this before submit
// so it can toggle large-library buffering when the count is ≥ 500.
//
// Missing/unreadable paths don't fail the response; they're logged and
// skipped. The walk only inspects extensions (fileproc.IsSupported), not
// file contents, so it's cheap enough to run synchronously on the request.
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
