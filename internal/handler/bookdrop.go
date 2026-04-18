package handler

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"

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
