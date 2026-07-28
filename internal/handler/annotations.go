// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// annotationDTO mirrors model.Annotation on the wire. The client derives
// "kind" from which string fields are populated — see the migration.
type annotationDTO struct {
	ID           string `json:"id"`
	BookID       string `json:"bookId"`
	Locator      string `json:"locator,omitempty"`
	SelectedText string `json:"selectedText,omitempty"`
	Note         string `json:"note,omitempty"`
	Color        string `json:"color"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

func toAnnotationDTO(a model.Annotation) annotationDTO {
	return annotationDTO{
		ID:           a.ID,
		BookID:       a.BookID,
		Locator:      a.Locator,
		SelectedText: a.SelectedText,
		Note:         a.Note,
		Color:        a.Color,
		CreatedAt:    a.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:    a.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

type createAnnotationReq struct {
	Locator      string `json:"locator"`
	SelectedText string `json:"selectedText"`
	Note         string `json:"note"`
	Color        string `json:"color,omitempty"`
}

type patchAnnotationReq struct {
	SelectedText *string `json:"selectedText,omitempty"`
	Note         *string `json:"note,omitempty"`
	Color        *string `json:"color,omitempty"`
}

// AnnotationsForBook lists every annotation the current user has on a
// specific book. Used by the reader's notes panel and the book detail
// page's Notes tab.
// The book-scoped seam has already resolved the id, which is what keeps a
// caller who merely knows a book id from reading annotations on a book
// that is not there — the existence check used to be an idiom this body
// carried itself.
func (h *Handler) AnnotationsForBook(c *gin.Context, s bookScope) {
	userID, bookID := s.UserID, s.Book.ID
	list, err := h.annotations.ListForBook(c.Request.Context(), userID, bookID)
	if err != nil {
		writeServerError(c, "annotations list", err)
		return
	}
	out := make([]annotationDTO, 0, len(list))
	for _, a := range list {
		out = append(out, toAnnotationDTO(a))
	}
	c.JSON(http.StatusOK, gin.H{"annotations": out})
}

// AnnotationsRecent returns a flat list across every book for the Notebook
// view. Default cap is 200 rows; `?limit=N` narrows further (clamped
// server-side). Each row carries its bookId so the client can hydrate
// the title / author from its cached books list.
func (h *Handler) AnnotationsRecent(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	list, err := h.annotations.ListRecent(c.Request.Context(), userID, limit)
	if err != nil {
		writeServerError(c, "annotations recent", err)
		return
	}
	out := make([]annotationDTO, 0, len(list))
	for _, a := range list {
		out = append(out, toAnnotationDTO(a))
	}
	c.JSON(http.StatusOK, gin.H{"annotations": out})
}

// AnnotationCreate inserts a new annotation for the current user. The
// book scope comes from the path param, not the body, so a client can't
// create annotations on a book they aren't allowed to see.
func (h *Handler) AnnotationCreate(c *gin.Context, s bookScope) {
	userID, bookID := s.UserID, s.Book.ID

	var body createAnnotationReq
	if !bindJSON(c, &body) {
		return
	}
	a, err := h.annotations.Create(c.Request.Context(), model.Annotation{
		UserID:       userID,
		BookID:       bookID,
		Locator:      body.Locator,
		SelectedText: body.SelectedText,
		Note:         body.Note,
		Color:        body.Color,
	})
	if err != nil {
		if errors.Is(err, service.ErrEmptyAnnotation) {
			writeError(c, http.StatusBadRequest, err.Error())
			return
		}
		writeServerError(c, "annotation create", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"annotation": toAnnotationDTO(a)})
}

// AnnotationPatch lets the user edit the note text, selected-text copy,
// or color. Everything else is immutable — moving a highlight is a
// delete + re-create.
func (h *Handler) AnnotationPatch(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")

	var body patchAnnotationReq
	if !bindJSON(c, &body) {
		return
	}
	a, err := h.annotations.Update(c.Request.Context(), userID, id, body.Note, body.SelectedText, body.Color)
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrNotFound):
			writeError(c, http.StatusNotFound, "annotation not found")
		case errors.Is(err, service.ErrEmptyAnnotation):
			writeError(c, http.StatusBadRequest, err.Error())
		default:
			writeServerError(c, "annotation patch", err)
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"annotation": toAnnotationDTO(a)})
}

// AnnotationDelete removes one annotation. 204; `repo.ErrNotFound` → 404.
func (h *Handler) AnnotationDelete(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	if err := h.annotations.Delete(c.Request.Context(), userID, id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "annotation not found")
			return
		}
		writeServerError(c, "annotation delete", err)
		return
	}
	c.Status(http.StatusNoContent)
}
