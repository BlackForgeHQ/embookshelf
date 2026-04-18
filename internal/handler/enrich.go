package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/provider"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// enrichMatchDTO mirrors provider.Match for the SPA. camelCase on the wire
// so the React form can render [t, ...authors] directly from the response.
type enrichMatchDTO struct {
	Source      string   `json:"source"`
	SourceID    string   `json:"sourceId"`
	Title       string   `json:"title"`
	Authors     []string `json:"authors"`
	Description string   `json:"description,omitempty"`
	Publisher   string   `json:"publisher,omitempty"`
	Year        int      `json:"year,omitempty"`
	ISBN        string   `json:"isbn,omitempty"`
	Series      string   `json:"series,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	Language    string   `json:"language,omitempty"`
	CoverURL    string   `json:"coverUrl,omitempty"`
	Confidence  int      `json:"confidence"`
}

func toEnrichMatchDTO(m provider.Match) enrichMatchDTO {
	authors := m.Authors
	if authors == nil {
		authors = []string{}
	}
	cats := m.Categories
	if cats == nil {
		cats = []string{}
	}
	return enrichMatchDTO{
		Source:      string(m.Source),
		SourceID:    m.SourceID,
		Title:       m.Title,
		Authors:     authors,
		Description: m.Description,
		Publisher:   m.Publisher,
		Year:        m.Year,
		ISBN:        m.ISBN,
		Series:      m.Series,
		Categories:  cats,
		Language:    m.Language,
		CoverURL:    m.CoverURL,
		Confidence:  m.Confidence,
	}
}

// EnrichSearch fans a metadata query across every configured provider and
// returns merged, confidence-sorted matches. Query params override the
// book's stored title/author so the user can refine the search from the
// editor form without a PATCH in between.
//
//	?title=<string>
//	?author=<string>
//	?isbn=<string>
func (h *Handler) EnrichSearch(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	book, err := h.lib.GetBook(c.Request.Context(), userID, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "book not found")
			return
		}
		writeServerError(c, "enrich get book", err)
		return
	}

	q := provider.Query{
		Title:  firstNonEmpty(strings.TrimSpace(c.Query("title")), book.Title),
		Author: firstNonEmpty(strings.TrimSpace(c.Query("author")), book.Author),
		ISBN:   firstNonEmpty(strings.TrimSpace(c.Query("isbn")), book.ISBN),
	}

	matches, err := h.enrich.Search(c.Request.Context(), q)
	if err != nil {
		writeServerError(c, "enrich search", err)
		return
	}

	out := make([]enrichMatchDTO, 0, len(matches))
	for _, m := range matches {
		out = append(out, toEnrichMatchDTO(m))
	}
	c.JSON(http.StatusOK, gin.H{
		"query":   gin.H{"title": q.Title, "author": q.Author, "isbn": q.ISBN},
		"matches": out,
	})
}

type coverFromURLReq struct {
	URL string `json:"url"`
}

type coverFromURLResp struct {
	CoverMime string `json:"coverMime"`
}

// EnrichApplyCover downloads a cover from an allow-listed provider URL,
// stores it in the coverstore, and flips has_cover on the book row. The
// service refuses non-HTTPS URLs, non-allow-listed hosts, non-image
// content types, and payloads larger than 10 MB — so this endpoint can't
// be turned into an open proxy.
func (h *Handler) EnrichApplyCover(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	// Ensure the book exists + is visible to the user before touching the
	// cover pipeline. The enrich service itself doesn't enforce ACLs.
	if _, err := h.lib.GetBook(c.Request.Context(), userID, id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "book not found")
			return
		}
		writeServerError(c, "cover-from-url get book", err)
		return
	}

	var body coverFromURLReq
	if !bindJSON(c, &body) {
		return
	}
	if strings.TrimSpace(body.URL) == "" {
		writeError(c, http.StatusBadRequest, "url is required")
		return
	}

	mime, err := h.enrich.ImportCoverFromURL(c.Request.Context(), id, body.URL)
	if err != nil {
		if errors.Is(err, service.ErrBadCoverURL) {
			writeError(c, http.StatusBadRequest, err.Error())
			return
		}
		writeServerError(c, "cover-from-url", err)
		return
	}
	c.JSON(http.StatusOK, coverFromURLResp{CoverMime: mime})
}
