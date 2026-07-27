// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
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
	book, err := h.books.GetByID(c.Request.Context(), userID, id)
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

	result, err := h.enrich.Search(c.Request.Context(), q)
	if err != nil {
		writeServerError(c, "enrich search", err)
		return
	}

	out := make([]enrichMatchDTO, 0, len(result.Matches))
	for _, m := range result.Matches {
		out = append(out, toEnrichMatchDTO(m))
	}
	providers := make([]string, 0, len(result.QueriedProviders))
	for _, p := range result.QueriedProviders {
		providers = append(providers, string(p))
	}
	c.JSON(http.StatusOK, gin.H{
		"query":     gin.H{"title": q.Title, "author": q.Author, "isbn": q.ISBN},
		"matches":   out,
		"providers": providers,
	})
}

// EnrichStream runs the same fan-out as EnrichSearch but streams each
// match as an SSE frame so the UI can render results progressively
// instead of waiting on the slowest provider. Query params mirror
// EnrichSearch; a single `done` event closes the stream.
//
// Frames:
//
//	event: match    data: <enrichMatchDTO JSON>
//	event: provider-error  data: {"provider":"...","error":"..."}
//	event: done     data: {"providers":["..."]}
//
// Client disconnect cancels the request context, which cancels every
// in-flight provider HTTP call via net/http's context integration.
func (h *Handler) EnrichStream(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	book, err := h.books.GetByID(c.Request.Context(), userID, id)
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

	// SSE headers must land before the first write; once Gin buffers
	// anything else the browser won't upgrade the response.
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Flush()

	stream := h.enrich.SearchStream(c.Request.Context(), q)
	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-stream:
			if !ok {
				return
			}
			if ev.Done {
				queried := make([]string, 0, len(ev.Queried))
				for _, p := range ev.Queried {
					queried = append(queried, string(p))
				}
				payload, _ := json.Marshal(gin.H{"providers": queried})
				_, _ = fmt.Fprintf(c.Writer, "event: done\ndata: %s\n\n", payload)
				c.Writer.Flush()
				return
			}
			if ev.Err != nil {
				payload, _ := json.Marshal(gin.H{
					"provider": string(ev.Provider),
					"error":    ev.Err.Error(),
				})
				_, _ = fmt.Fprintf(c.Writer, "event: provider-error\ndata: %s\n\n", payload)
				c.Writer.Flush()
				continue
			}
			if ev.Match == nil {
				continue
			}
			dto := toEnrichMatchDTO(*ev.Match)
			payload, err := json.Marshal(dto)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(c.Writer, "event: match\ndata: %s\n\n", payload)
			c.Writer.Flush()
		}
	}
}

// applyMetadataReq is the body for PUT /books/:id/metadata — apply a
// single user-selected match onto the book, respecting per-field locks.
// `source` + `sourceId` identify the candidate (server trusts the
// client for the payload; the alternative of re-fetching adds latency
// and upstream quota cost for no security gain since the user could
// already PATCH the same fields manually).
type applyMetadataReq struct {
	Source          string   `json:"source"`
	SourceID        string   `json:"sourceId"`
	Title           string   `json:"title"`
	Authors         []string `json:"authors"`
	Description     string   `json:"description,omitempty"`
	Publisher       string   `json:"publisher,omitempty"`
	Year            int      `json:"year,omitempty"`
	ISBN            string   `json:"isbn,omitempty"`
	Series          string   `json:"series,omitempty"`
	Categories      []string `json:"categories,omitempty"`
	Language        string   `json:"language,omitempty"`
	CoverURL        string   `json:"coverUrl,omitempty"`
	MergeCategories bool     `json:"mergeCategories,omitempty"`
	ApplyCover      bool     `json:"applyCover,omitempty"`
}

// EnrichApplyMatch persists a user-selected provider match onto the
// book, respecting per-field locks and optionally unioning categories
// or pulling the cover.
func (h *Handler) EnrichApplyMatch(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	book, err := h.books.GetByID(c.Request.Context(), userID, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "book not found")
			return
		}
		writeServerError(c, "enrich apply get book", err)
		return
	}

	var body applyMetadataReq
	if !bindJSON(c, &body) {
		return
	}

	match := provider.Match{
		Source:      provider.Source(body.Source),
		SourceID:    body.SourceID,
		Title:       body.Title,
		Authors:     body.Authors,
		Description: body.Description,
		Publisher:   body.Publisher,
		Year:        body.Year,
		ISBN:        body.ISBN,
		Series:      body.Series,
		Categories:  body.Categories,
		Language:    body.Language,
		CoverURL:    body.CoverURL,
	}

	updated, err := h.enrich.ApplyMatch(c.Request.Context(), book, match, service.ApplyOptions{
		MergeCategories: body.MergeCategories,
		ApplyCover:      body.ApplyCover,
	}, service.TriggerApplyEnrichment)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "book not found")
			return
		}
		writeServerError(c, "enrich apply", err)
		return
	}

	// Reload so the response carries fresh cover flags + any side
	// effects of the cover-from-url import that ran after UpdateMetadata.
	fresh, err := h.books.GetByID(c.Request.Context(), userID, updated.ID)
	if err != nil {
		writeServerError(c, "enrich apply reload", err)
		return
	}
	shelves, err := h.shelf.SlugsForBook(c.Request.Context(), userID, updated.ID)
	if err != nil {
		writeServerError(c, "enrich apply shelves", err)
		return
	}
	if shelves == nil {
		shelves = []string{}
	}
	c.JSON(http.StatusOK, gin.H{
		"book": bookDetailDTO{
			bookDTO: toBookDTO(fresh),
			Shelves: shelves,
		},
	})
}

// toggleFieldLocksReq flips the lock flag for one or more metadata
// fields on a single book. `locks` is a sparse map — only mentioned
// fields change. Field keys match model.LockFields.
type toggleFieldLocksReq struct {
	Locks map[string]bool `json:"locks"`
}

// EnrichToggleFieldLocks updates per-field lock flags. Unknown keys are
// rejected so a typo doesn't silently vanish.
func (h *Handler) EnrichToggleFieldLocks(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	book, err := h.books.GetByID(c.Request.Context(), userID, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "book not found")
			return
		}
		writeServerError(c, "lock get book", err)
		return
	}

	var body toggleFieldLocksReq
	if !bindJSON(c, &body) {
		return
	}

	allowed := make(map[string]struct{}, len(model.LockFields))
	for _, f := range model.LockFields {
		allowed[f] = struct{}{}
	}
	for field := range body.Locks {
		if _, ok := allowed[field]; !ok {
			writeError(c, http.StatusBadRequest, "unknown lock field: "+field)
			return
		}
	}

	for field, v := range body.Locks {
		applyLock(&book.Locks, field, v)
	}

	lockOutcome, err := h.lib.UpdateBookMetadata(c.Request.Context(), book)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "book not found")
			return
		}
		writeServerError(c, "lock update", err)
		return
	}

	fresh, err := h.books.GetByID(c.Request.Context(), userID, id)
	if err != nil {
		writeServerError(c, "lock reload", err)
		return
	}
	shelves, err := h.shelf.SlugsForBook(c.Request.Context(), userID, id)
	if err != nil {
		writeServerError(c, "lock shelves", err)
		return
	}
	if shelves == nil {
		shelves = []string{}
	}
	resp := gin.H{
		"book": bookDetailDTO{
			bookDTO: toBookDTO(fresh),
			Shelves: shelves,
		},
	}
	if warnings := lockOutcome.Warnings(); len(warnings) > 0 {
		slog.Warn("lock update write degraded", "book", id, "warnings", warnings)
		resp["warnings"] = warnings
	}
	c.JSON(http.StatusOK, resp)
}

func applyLock(l *model.BookLocks, field string, v bool) {
	switch field {
	case "title":
		l.Title = v
	case "subtitle":
		l.Subtitle = v
	case "author":
		l.Author = v
	case "description":
		l.Description = v
	case "publisher":
		l.Publisher = v
	case "series":
		l.Series = v
	case "isbn":
		l.ISBN = v
	case "isbn10":
		l.ISBN10 = v
	case "language":
		l.Language = v
	case "publishDate":
		l.PublishDate = v
	case "genres":
		l.Genres = v
	case "moods":
		l.Moods = v
	case "tags":
		l.Tags = v
	case "pages":
		l.Pages = v
	case "cover":
		l.Cover = v
	}
}

// isbnLookupReq drives POST /books/metadata/isbn-lookup. Walks enabled
// providers in catalog order and returns the first hit.
type isbnLookupReq struct {
	ISBN string `json:"isbn"`
}

// EnrichISBNLookup runs the ISBN chain and returns the best candidate
// or 404 if no provider has a hit. Used by bulk import flows that want
// a one-shot metadata fetch from a bare ISBN.
func (h *Handler) EnrichISBNLookup(c *gin.Context) {
	if id := requireUserID(c); id == "" {
		return
	}
	var body isbnLookupReq
	if !bindJSON(c, &body) {
		return
	}
	if strings.TrimSpace(body.ISBN) == "" {
		writeError(c, http.StatusBadRequest, "isbn is required")
		return
	}
	match, src, err := h.enrich.LookupByISBN(c.Request.Context(), body.ISBN)
	if err != nil {
		writeServerError(c, "isbn lookup", err)
		return
	}
	if match == nil {
		writeError(c, http.StatusNotFound, "no provider matched this ISBN")
		return
	}
	dto := toEnrichMatchDTO(*match)
	c.JSON(http.StatusOK, gin.H{"provider": string(src), "match": dto})
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
	if _, err := h.books.GetByID(c.Request.Context(), userID, id); err != nil {
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

// EnrichRemoveCover clears the cover for a book: flips has_cover off,
// nulls cover_mime + cover_hash, and best-effort deletes the legacy
// id-keyed file. Hashed cover bytes are kept (content-addressed; may be
// shared with other books). Idempotent — removing again is a no-op.
func (h *Handler) EnrichRemoveCover(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	if _, err := h.books.GetByID(c.Request.Context(), userID, id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "book not found")
			return
		}
		writeServerError(c, "remove cover get book", err)
		return
	}
	if err := h.enrich.ClearCover(c.Request.Context(), id); err != nil {
		writeServerError(c, "remove cover", err)
		return
	}
	c.Status(http.StatusNoContent)
}
