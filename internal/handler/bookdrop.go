package handler

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// bookdropDTO mirrors model.BookDropItem for the SPA. Filename is the
// basename of Path — the UI shows it as the "file" column; Path stays
// available for tooltips/debugging.
type bookdropDTO struct {
	ID           string `json:"id"`
	Filename     string `json:"filename"`
	Path         string `json:"path"`
	FileSize     int64  `json:"fileSize"`
	Format       string `json:"format"`
	State        string `json:"state"`
	Progress     int    `json:"progress"`
	ErrorMsg     string `json:"errorMsg,omitempty"`
	Title        string `json:"title,omitempty"`
	Author       string `json:"author,omitempty"`
	Description  string `json:"description,omitempty"`
	Language     string `json:"language,omitempty"`
	HasCover     bool   `json:"hasCover"`
	CoverMime    string `json:"coverMime,omitempty"`
	BookID       string `json:"bookId,omitempty"`
	DiscoveredAt string `json:"discoveredAt"`
	UpdatedAt    string `json:"updatedAt"`
}

func toBookDropDTO(item model.BookDropItem) bookdropDTO {
	d := bookdropDTO{
		ID:           item.ID,
		Filename:     filepath.Base(item.Path),
		Path:         item.Path,
		FileSize:     item.FileSize,
		Format:       item.Format,
		State:        string(item.State),
		Progress:     item.Progress,
		ErrorMsg:     item.ErrorMsg,
		Title:        item.Title,
		Author:       item.Author,
		Description:  item.Description,
		Language:     item.Language,
		HasCover:     item.HasCover,
		CoverMime:    item.CoverMime,
		DiscoveredAt: item.DiscoveredAt.UTC().Format(time.RFC3339),
		UpdatedAt:    item.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if item.BookID != nil {
		d.BookID = *item.BookID
	}
	return d
}

// BookDropList returns every item currently in the ingest queue, including
// terminal states so the UI can show "imported" / "rejected" history until
// the user explicitly clears them.
func (h *Handler) BookDropList(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	items, err := h.bookdrop.List(c.Request.Context())
	if err != nil {
		writeServerError(c, "bookdrop list", err)
		return
	}
	out := make([]bookdropDTO, 0, len(items))
	for _, it := range items {
		out = append(out, toBookDropDTO(it))
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

// BookDropCover streams the pre-approval cover image pulled out of the
// ingest worker. Unlike the book cover handler, this reads out of the
// bookdrop namespace and 404s cleanly when the extractor didn't find one.
func (h *Handler) BookDropCover(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	item, err := h.bookdrop.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			c.Status(http.StatusNotFound)
			return
		}
		writeServerError(c, "bookdrop cover lookup", err)
		return
	}
	if !item.HasCover || h.covers == nil {
		c.Status(http.StatusNotFound)
		return
	}
	f, err := h.covers.OpenBookDrop(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.Status(http.StatusNotFound)
			return
		}
		writeServerError(c, "bookdrop cover open", err)
		return
	}
	defer f.Close()

	mime := item.CoverMime
	if mime == "" {
		mime = "application/octet-stream"
	}
	c.Header("Content-Type", mime)
	c.Header("Cache-Control", "private, max-age=3600")
	if _, err := io.Copy(c.Writer, f); err != nil {
		writeServerError(c, "bookdrop cover stream", err)
	}
}

type approveBookDropReq struct {
	LibraryID string `json:"libraryId,omitempty"`
}

// BookDropApprove promotes the queue item into a real book and returns the
// resulting book detail DTO. An empty libraryId falls back to the first
// library — mirrors the service-level default.
func (h *Handler) BookDropApprove(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")

	var body approveBookDropReq
	// Body is optional; tolerate empty or missing payloads.
	if c.Request.ContentLength > 0 {
		if !bindJSON(c, &body) {
			return
		}
	}

	book, err := h.bookdrop.Approve(c.Request.Context(), id, body.LibraryID)
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrNotFound):
			writeError(c, http.StatusNotFound, "bookdrop item not found")
		default:
			// Service returns typed errors only for "not found"; other
			// failures are generic. Log + 500 keeps us from leaking
			// guardrail strings.
			writeServerError(c, "bookdrop approve", err)
		}
		return
	}

	// Re-load through the library service so the response carries the
	// per-user progress fields + shelf memberships (empty on a fresh import).
	fresh, err := h.lib.GetBook(c.Request.Context(), userID, book.ID)
	if err != nil {
		writeServerError(c, "bookdrop approve reload", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"book": bookDetailDTO{
			bookDTO:  toBookDTO(fresh),
			Shelves:  []string{},
		},
	})
}

// maxUploadBytes caps a single BookDrop upload request. Big enough for a
// handful of PDFs + scanned comics in one shot; restricts runaway memory use
// when parsing multipart bodies.
const maxUploadBytes = 1 << 30 // 1 GiB

// bookdropUploadItem is one row of the upload response. Success entries
// carry the bookdrop item; failed ones carry an error string so the UI can
// render per-file feedback.
type bookdropUploadItem struct {
	Filename string       `json:"filename"`
	Item     *bookdropDTO `json:"item,omitempty"`
	Error    string       `json:"error,omitempty"`
}

// BookDropUpload accepts one or more files via multipart/form-data and
// enqueues each into the ingest pipeline. Files land in the configured
// BookDropPath under a unique name so concurrent uploads of "book.epub"
// don't stomp each other. Unsupported formats are rejected per-file; one
// bad file never fails the whole batch.
func (h *Handler) BookDropUpload(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	if h.cfg.BookDropPath == "" {
		writeError(c, http.StatusServiceUnavailable, "bookdrop is disabled (no BOOKDROP_PATH configured)")
		return
	}

	// Cap the whole request body. gin.Context.Request.Body is what
	// ParseMultipartForm reads from, so this both limits memory + disk
	// spill and trips a clear 413 on oversize uploads.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBytes)
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		writeError(c, http.StatusBadRequest, "invalid upload: "+err.Error())
		return
	}
	form := c.Request.MultipartForm
	if form == nil || len(form.File["files"]) == 0 {
		writeError(c, http.StatusBadRequest, "no files provided (expected multipart field \"files\")")
		return
	}

	if err := os.MkdirAll(h.cfg.BookDropPath, 0o755); err != nil {
		writeServerError(c, "bookdrop mkdir", err)
		return
	}

	results := make([]bookdropUploadItem, 0, len(form.File["files"]))
	for _, fh := range form.File["files"] {
		orig := filepath.Base(fh.Filename)
		entry := bookdropUploadItem{Filename: orig}

		if !fileproc.IsSupported(orig) {
			entry.Error = "unsupported format"
			results = append(results, entry)
			continue
		}

		dest, err := saveUniqueUpload(h.cfg.BookDropPath, orig, fh)
		if err != nil {
			slog.Warn("bookdrop upload save", "filename", orig, "err", err)
			entry.Error = "could not save file"
			results = append(results, entry)
			continue
		}

		format := fileproc.FormatForExt(filepath.Ext(dest))
		item, _, err := h.bookdrop.Enqueue(c.Request.Context(), dest, format, fh.Size)
		if err != nil {
			_ = os.Remove(dest)
			slog.Warn("bookdrop upload enqueue", "filename", orig, "err", err)
			entry.Error = "could not enqueue"
			results = append(results, entry)
			continue
		}

		if h.queue != nil {
			if err := h.queue.EnqueueBookDrop(c.Request.Context(), item.ID); err != nil {
				// The DB row exists — surface the failure but leave the
				// row; the next watcher tick re-enqueues.
				slog.Error("bookdrop upload river job", "item_id", item.ID, "err", err)
			}
		}

		dto := toBookDropDTO(item)
		entry.Item = &dto
		results = append(results, entry)
	}

	status := http.StatusCreated
	succeeded := 0
	for _, r := range results {
		if r.Item != nil {
			succeeded++
		}
	}
	if succeeded == 0 {
		status = http.StatusBadRequest
	}
	c.JSON(status, gin.H{"results": results})
}

// saveUniqueUpload copies the uploaded file into dir under a non-colliding
// basename. The uniqueness strategy is `<base>-<unix-nano>.<ext>` — readable
// and monotonic, good enough under the low concurrency this ingest sees.
func saveUniqueUpload(dir, originalName string, fh *multipart.FileHeader) (string, error) {
	// Break up the filename into base + ext, strip leading dots so a
	// ".epub" upload doesn't become a hidden file.
	name := strings.TrimLeft(originalName, ".")
	if name == "" {
		name = "upload"
	}
	ext := strings.ToLower(filepath.Ext(name))
	base := strings.TrimSuffix(name, ext)
	stamp := time.Now().UnixNano()
	candidate := filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, stamp, ext))

	src, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()

	dst, err := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		_ = os.Remove(candidate)
		return "", err
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(candidate)
		return "", err
	}
	return candidate, nil
}

// BookDropReject marks an item as dismissed and cleans up the pre-approval
// cover. Idempotent — rejecting a rejected item is a no-op.
func (h *Handler) BookDropReject(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	if err := h.bookdrop.Reject(c.Request.Context(), id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "bookdrop item not found")
			return
		}
		writeServerError(c, "bookdrop reject", err)
		return
	}
	c.Status(http.StatusNoContent)
}
