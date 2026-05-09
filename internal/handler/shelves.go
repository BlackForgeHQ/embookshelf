// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// shelfIconRe enforces a kebab-case lucide icon slug shape. The server
// deliberately does not enumerate lucide's ~1500-icon catalog (ADR-0019);
// a slug that doesn't resolve at render time falls back to a default
// glyph in the UI, owner-only blast radius.
var shelfIconRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

type shelfDTO struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Slug      string           `json:"slug"`
	Accent    string           `json:"accent"`
	Icon      string           `json:"icon"`
	BookCount int              `json:"bookCount"`
	IsSmart   bool             `json:"isSmart"`
	IsPublic  bool             `json:"isPublic"`
	OwnerName string           `json:"ownerName,omitempty"`
	Rule      *model.ShelfRule `json:"rule,omitempty"`
	CreatedAt string           `json:"createdAt"`
}

// toShelfDTO converts a domain shelf to the wire shape. Public shelves
// expose their slug under the `public:` namespace so client URLs and
// query keys disambiguate from a private shelf with the same name.
func toShelfDTO(s model.Shelf) shelfDTO {
	slug := s.Slug
	if s.IsPublic {
		slug = service.PublicSlugPrefix + s.Slug
	}
	return shelfDTO{
		ID:        s.ID,
		Name:      s.Name,
		Slug:      slug,
		Accent:    s.Accent,
		Icon:      s.Icon,
		BookCount: s.BookCount,
		IsSmart:   s.IsSmart,
		IsPublic:  s.IsPublic,
		OwnerName: s.OwnerName,
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
	Icon   string           `json:"icon,omitempty"`
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
	body.Icon = strings.TrimSpace(body.Icon)
	if body.Icon != "" && !shelfIconRe.MatchString(body.Icon) {
		writeError(c, http.StatusBadRequest, "invalid icon slug")
		return
	}
	shelf, err := h.shelf.Create(c.Request.Context(), userID, body.Name, body.Accent, body.Icon, body.Rule)
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
	Icon   *string `json:"icon,omitempty"`
	// Distinguishing "omitted" from "explicit null" requires raw json
	// peeking; for simplicity we treat a present `rule` field as
	// ruleChanged=true. A regular shelf that sends `rule` gets 400 from
	// the repo layer. Clients never need to send null — just omit.
	Rule    *model.ShelfRule `json:"rule,omitempty"`
	RuleSet bool             `json:"ruleSet,omitempty"`
}

// ShelfUpdate edits a shelf's name, accent, and/or rule. Only smart
// shelves can change their rule; the repo rejects the regular→smart
// transition cleanly. A `public:<slug>` URL form is accepted (and
// stripped) because owners use the canonical public-prefixed URL for
// the same row both before and after publishing (ADR-0017).
func (h *Handler) ShelfUpdate(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	slug, _ := service.SplitPublicSlug(c.Param("slug"))
	var body patchShelfReq
	if !bindJSON(c, &body) {
		return
	}
	if body.Icon != nil {
		trimmed := strings.TrimSpace(*body.Icon)
		if !shelfIconRe.MatchString(trimmed) {
			writeError(c, http.StatusBadRequest, "invalid icon slug")
			return
		}
		body.Icon = &trimmed
	}
	updated, err := h.shelf.Update(c.Request.Context(), userID, slug, body.Name, body.Accent, body.Icon, body.Rule, body.RuleSet)
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
// the current user (or doesn't exist). A `public:<slug>` form is
// accepted and stripped — owners use the canonical URL.
func (h *Handler) ShelfDelete(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	slug, _ := service.SplitPublicSlug(c.Param("slug"))
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

type publishShelfReq struct {
	Public bool `json:"public"`
}

// ShelfPublish flips a shelf's is_public flag. Admin-only and
// owner-only — non-admins are blocked here, non-owners 404 from the
// repo lookup. A regular shelf publishing flips on; passing
// `public:false` un-publishes (the canonical un-publish path).
//
// Smart shelves are rejected at the repo layer (CHECK constraint on
// Postgres, application-side check on SQLite).
func (h *Handler) ShelfPublish(c *gin.Context) {
	u := auth.UserFromContext(c.Request.Context())
	if u == nil {
		writeError(c, http.StatusUnauthorized, "authentication required")
		return
	}
	if u.Role != model.RoleAdmin {
		writeError(c, http.StatusForbidden, service.ErrShelfPublishForbidden.Error())
		return
	}
	slug, _ := service.SplitPublicSlug(c.Param("slug"))
	var body publishShelfReq
	if !bindJSON(c, &body) {
		return
	}
	updated, err := h.shelf.SetPublic(c.Request.Context(), u.ID, slug, body.Public)
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrNotFound):
			writeError(c, http.StatusNotFound, "shelf not found")
		default:
			// "smart shelves cannot be public" + any future repo
			// validation surfaces as a 400 — message is safe.
			writeError(c, http.StatusBadRequest, err.Error())
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"shelf": toShelfDTO(updated)})
}
