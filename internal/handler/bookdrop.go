// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
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
	defer func() { _ = f.Close() }()

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

// BookDropFile streams the staged BookDrop file bytes for client-side
// rendering of a pre-approval cover (e.g. PDF page-1 rasterization in
// the BookDrop preview). Mirrors BookDropCover's auth shape but reads
// the original file from the staging directory.
func (h *Handler) BookDropFile(c *gin.Context) {
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
		writeServerError(c, "bookdrop file lookup", err)
		return
	}
	f, err := os.Open(item.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.Status(http.StatusNotFound)
			return
		}
		writeServerError(c, "bookdrop file open", err)
		return
	}
	defer func() { _ = f.Close() }()
	mime := mimeForFormat(item.Format)
	if mime == "" {
		mime = "application/octet-stream"
	}
	c.Header("Content-Type", mime)
	c.Header("Cache-Control", "private, max-age=3600")
	if _, err := io.Copy(c.Writer, f); err != nil {
		writeServerError(c, "bookdrop file stream", err)
	}
}

// BookDropPutCover accepts a raw image (PNG or JPEG) for a BookDrop
// item that doesn't yet have a pre-approval cover. Idempotent on
// absence: first successful PUT wins; subsequent PUTs return 409.
//
// Cap: 5 MB. Magic-sniff: PNG `89 50 4E 47` or JPEG `FF D8 FF`.
// State gate: item must be in 'discovered', 'processing', or 'ready'.
// Refuses 'imported' / 'rejected' / 'failed'.
func (h *Handler) BookDropPutCover(c *gin.Context) {
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
		writeServerError(c, "bookdrop lookup", err)
		return
	}
	if item.HasCover {
		writeError(c, http.StatusConflict, "cover already present")
		return
	}
	switch item.State {
	case model.BookDropDiscovered, model.BookDropProcessing, model.BookDropReady:
		// accept
	default:
		writeError(c, http.StatusConflict, "item state does not accept cover upload")
		return
	}

	const maxBytes = 5 << 20
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(c, http.StatusRequestEntityTooLarge, "image too large")
			return
		}
		writeServerError(c, "bookdrop read cover body", err)
		return
	}
	mime, ok := sniffCoverMime(raw)
	if !ok {
		writeError(c, http.StatusUnsupportedMediaType, "expected PNG or JPEG")
		return
	}

	if err := h.bookdrop.PutPreapprovalCover(c.Request.Context(), id, raw, mime); err != nil {
		writeServerError(c, "bookdrop put cover", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// sniffCoverMime returns ("image/png", true) for a PNG magic header,
// ("image/jpeg", true) for a JPEG SOI, and ("", false) for anything else.
// Used by BookDropPutCover to gate user-supplied bytes.
func sniffCoverMime(b []byte) (string, bool) {
	switch {
	case len(b) >= 4 && b[0] == 0x89 && b[1] == 0x50 && b[2] == 0x4E && b[3] == 0x47:
		return "image/png", true
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg", true
	}
	return "", false
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

	// Auto-enrich is not requested here. Approve owns that decision and
	// the setting behind it, and dispatches the fan-out to the worker
	// pool — so this response no longer waits on a provider round-trip,
	// and callers that never touch HTTP get the same behaviour.

	// Through the same module every other book-detail response goes
	// through, which is what makes the shelf list real. Approve used to
	// hard-code it empty on the assumption that a freshly imported book
	// sits on no shelf — true of the books row, but the response is per
	// user, and a Shared shelf or a smart shelf can already claim it.
	h.writeBookDetail(c, userID, book.ID, service.Outcome{}, "")
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
	if requireUserID(c) == "" {
		return
	}
	if h.cfg.BookDropPath == "" {
		writeError(c, http.StatusServiceUnavailable, service.ErrBookDropDisabled.Error())
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

	// Per-file outcomes rather than one verdict for the batch: a client
	// dragging in a folder gets told exactly which files landed.
	results := make([]bookdropUploadItem, 0, len(form.File["files"]))
	succeeded := 0
	for _, fh := range form.File["files"] {
		entry := bookdropUploadItem{Filename: filepath.Base(fh.Filename)}
		item, err := h.acceptUpload(c.Request.Context(), fh)
		switch {
		case err == nil:
			dto := toBookDropDTO(item)
			entry.Item = &dto
			succeeded++
		case errors.Is(err, service.ErrUnsupportedFormat):
			entry.Error = "unsupported format"
		default:
			slog.Warn("bookdrop upload", "filename", entry.Filename, "err", err)
			entry.Error = "could not save file"
		}
		results = append(results, entry)
	}

	status := http.StatusCreated
	if succeeded == 0 {
		status = http.StatusBadRequest
	}
	c.JSON(status, gin.H{"results": results})
}

// acceptUpload streams one multipart part into the staging directory via the
// service, which owns the wipe lock, the naming, and the worker handoff.
func (h *Handler) acceptUpload(ctx context.Context, fh *multipart.FileHeader) (model.BookDropItem, error) {
	src, err := fh.Open()
	if err != nil {
		return model.BookDropItem{}, err
	}
	defer func() { _ = src.Close() }()
	return h.bookdrop.Accept(ctx, fh.Filename, src)
}

// BookDropClearProcessed drops every bookdrop row in a terminal state
// ('imported' or 'rejected') so the "Recently processed" list clears.
// In-flight rows (discovered / processing / ready / failed) are left
// alone. Returns the count actually deleted for the UI toast.
//
// Mounted under /api/v1/settings/bookdrop/processed (admin-only) — see
// ADR-0014.
func (h *Handler) BookDropClearProcessed(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	n, err := h.bookdrop.ClearProcessed(c.Request.Context())
	if err != nil {
		writeServerError(c, "bookdrop clear processed", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"cleared": n})
}

// BookDropFilesPreview returns a snapshot of what the wipe op would
// delete — count, bytes, and the count of in-flight files it would skip.
// Mounted under /api/v1/settings/bookdrop/files (admin-only).
func (h *Handler) BookDropFilesPreview(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	preview, err := h.bookdrop.PreviewFiles(c.Request.Context())
	if err != nil {
		writeServerError(c, "bookdrop preview files", err)
		return
	}
	c.JSON(http.StatusOK, preview)
}

// BookDropWipeFiles recursively deletes every file under BOOKDROP_PATH
// (skipping files referenced by 'processing' rows) and drops orphan
// rows. Cross-user blast radius — admin-only. See ADR-0014.
func (h *Handler) BookDropWipeFiles(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	res, err := h.bookdrop.WipeFiles(c.Request.Context())
	if err != nil {
		writeServerError(c, "bookdrop wipe files", err)
		return
	}
	c.JSON(http.StatusOK, res)
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
