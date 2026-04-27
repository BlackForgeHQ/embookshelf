package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

type suggestBookDTO struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Author   string `json:"author"`
	Cover    string `json:"cover"`
	HasCover bool   `json:"hasCover"`
}

type suggestShelfDTO struct {
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	Accent string `json:"accent"`
}

type suggestLibraryDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type searchResponse struct {
	Books     []suggestBookDTO    `json:"books"`
	Shelves   []suggestShelfDTO   `json:"shelves"`
	Libraries []suggestLibraryDTO `json:"libraries"`
}

// Search powers the global command palette and library combobox. Returns
// the top matches across books, shelves (per-user), and libraries.
//
//	?q=<text>       required, trimmed; empty → 400
//	?limit=<int>    optional, default 8, capped at 20 by the service
func (h *Handler) Search(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		writeError(c, http.StatusBadRequest, "q is required")
		return
	}
	limit := 0 // service applies the default
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeError(c, http.StatusBadRequest, "limit must be an integer")
			return
		}
		limit = n
	}

	result, err := h.search.Suggest(c.Request.Context(), userID, q, limit)
	if err != nil {
		if errors.Is(err, service.ErrEmptyQuery) {
			writeError(c, http.StatusBadRequest, "q is required")
			return
		}
		writeServerError(c, "search suggest", err)
		return
	}

	c.JSON(http.StatusOK, searchResponse{
		Books:     toSuggestBookDTOs(result.Books),
		Shelves:   toSuggestShelfDTOs(result.Shelves),
		Libraries: toSuggestLibraryDTOs(result.Libraries),
	})
}

func toSuggestBookDTOs(in []repo.SuggestBook) []suggestBookDTO {
	out := make([]suggestBookDTO, 0, len(in))
	for _, b := range in {
		cover := ""
		if b.HasCover {
			cover = "/api/v1/books/" + b.ID + "/cover"
		}
		out = append(out, suggestBookDTO{
			ID:       b.ID,
			Title:    b.Title,
			Author:   b.Author,
			Cover:    cover,
			HasCover: b.HasCover,
		})
	}
	return out
}

func toSuggestShelfDTOs(in []repo.SuggestShelf) []suggestShelfDTO {
	out := make([]suggestShelfDTO, 0, len(in))
	for _, s := range in {
		out = append(out, suggestShelfDTO{Slug: s.Slug, Name: s.Name, Accent: s.Accent})
	}
	return out
}

func toSuggestLibraryDTOs(in []repo.SuggestLibrary) []suggestLibraryDTO {
	out := make([]suggestLibraryDTO, 0, len(in))
	for _, l := range in {
		out = append(out, suggestLibraryDTO{ID: l.ID, Name: l.Name, Slug: l.Slug})
	}
	return out
}
