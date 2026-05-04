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

type shelfDTO struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Slug      string           `json:"slug"`
	Accent    string           `json:"accent"`
	BookCount int              `json:"bookCount"`
	IsSmart   bool             `json:"isSmart"`
	Rule      *model.ShelfRule `json:"rule,omitempty"`
	CreatedAt string           `json:"createdAt"`
}

func toShelfDTO(s model.Shelf) shelfDTO {
	return shelfDTO{
		ID:        s.ID,
		Name:      s.Name,
		Slug:      s.Slug,
		Accent:    s.Accent,
		BookCount: s.BookCount,
		IsSmart:   s.IsSmart,
		Rule:      s.Rule,
		CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// Shelves returns the current user's shelves with book counts. Smart
// shelves' counts are evaluated live in the service layer.
func (h *Handler) Shelves(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	list, err := h.shelf.List(c.Request.Context(), userID)
	if err != nil {
		writeServerError(c, "shelves list", err)
		return
	}
	out := make([]shelfDTO, 0, len(list))
	for _, s := range list {
		out = append(out, toShelfDTO(s))
	}
	// Live count for the "Unshelved" virtual view. Cheap NOT EXISTS that
	// hits idx_shelf_books_book; piggybacked here to save the sidebar a
	// second roundtrip.
	unshelvedCount, err := h.shelf.CountUnshelved(c.Request.Context(), userID)
	if err != nil {
		writeServerError(c, "shelves unshelved count", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"shelves": out, "unshelvedCount": unshelvedCount})
}

type createShelfReq struct {
	Name   string           `json:"name"`
	Accent string           `json:"accent,omitempty"`
	Rule   *model.ShelfRule `json:"rule,omitempty"`
}

// ShelfCreate creates a regular shelf when `rule` is absent, or a smart
// shelf when it's present. The repo generates the slug from the name
// and handles collisions by appending -N; callers don't provide a slug.
func (h *Handler) ShelfCreate(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	var body createShelfReq
	if !bindJSON(c, &body) {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		writeError(c, http.StatusBadRequest, "name is required")
		return
	}
	shelf, err := h.shelf.Create(c.Request.Context(), userID, body.Name, body.Accent, body.Rule)
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrShelfSlugTaken):
			writeError(c, http.StatusConflict, "shelf slug already in use")
		case errors.Is(err, model.ErrInvalidRule):
			writeError(c, http.StatusBadRequest, err.Error())
		default:
			writeServerError(c, "shelf create", err)
		}
		return
	}
	c.JSON(http.StatusCreated, gin.H{"shelf": toShelfDTO(shelf)})
}

type patchShelfReq struct {
	Name   *string `json:"name,omitempty"`
	Accent *string `json:"accent,omitempty"`
	// Distinguishing "omitted" from "explicit null" requires raw json
	// peeking; for simplicity we treat a present `rule` field as
	// ruleChanged=true. A regular shelf that sends `rule` gets 400 from
	// the repo layer. Clients never need to send null — just omit.
	Rule    *model.ShelfRule `json:"rule,omitempty"`
	RuleSet bool             `json:"ruleSet,omitempty"`
}

// ShelfUpdate edits a shelf's name, accent, and/or rule. Only smart
// shelves can change their rule; the repo rejects the regular→smart
// transition cleanly.
func (h *Handler) ShelfUpdate(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	slug := c.Param("slug")
	var body patchShelfReq
	if !bindJSON(c, &body) {
		return
	}
	updated, err := h.shelf.Update(c.Request.Context(), userID, slug, body.Name, body.Accent, body.Rule, body.RuleSet)
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrNotFound):
			writeError(c, http.StatusNotFound, "shelf not found")
		case errors.Is(err, model.ErrInvalidRule):
			writeError(c, http.StatusBadRequest, err.Error())
		default:
			// Other service-layer errors (name cannot be empty,
			// cannot assign rule to regular shelf, …) bubble up as
			// 400 — their messages are safe to show.
			writeError(c, http.StatusBadRequest, err.Error())
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"shelf": toShelfDTO(updated)})
}

// ShelfDelete removes a user shelf. 404 when the slug doesn't belong to
// the current user (or doesn't exist).
func (h *Handler) ShelfDelete(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	slug := c.Param("slug")
	if err := h.shelf.Delete(c.Request.Context(), userID, slug); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "shelf not found")
			return
		}
		writeServerError(c, "shelf delete", err)
		return
	}
	c.Status(http.StatusNoContent)
}
