package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// libraryDTO mirrors the TS Library type in frontend/src/data/mock.ts. The
// `path` / `color` fields stay mock-only (FS roots live under
// library_paths, theme color is a design concern not a DB fact).
type libraryDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	BookCount int    `json:"bookCount"`
	CreatedAt string `json:"createdAt"`
}

func toLibraryDTO(l model.Library) libraryDTO {
	return libraryDTO{
		ID:        l.ID,
		Name:      l.Name,
		Slug:      l.Slug,
		BookCount: l.BookCount,
		CreatedAt: l.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// bookDTO mirrors the TS Book type. Progress is emitted as a 0..1 float to
// match the prototype; the DB column is 0..100 int.
type bookDTO struct {
	ID          string   `json:"id"`
	LibraryID   string   `json:"libraryId"`
	Title       string   `json:"title"`
	Author      string   `json:"author"`
	Format      string   `json:"format"`
	Year        int      `json:"year"`
	Progress    float64  `json:"progress"`
	ResumeCFI   string   `json:"resumeCfi,omitempty"`
	Rating      int      `json:"rating"`
	Palette     string   `json:"palette"`
	Description string   `json:"description,omitempty"`
	ISBN        string   `json:"isbn,omitempty"`
	Publisher   string   `json:"publisher,omitempty"`
	Series      string   `json:"series,omitempty"`
	SeriesNum   int      `json:"seriesNum,omitempty"`
	Tags        []string `json:"tags"`
	HasCover    bool     `json:"hasCover"`
	CoverMime   string   `json:"coverMime,omitempty"`
	AddedAt     string   `json:"addedAt"`
}

func toBookDTO(b model.Book) bookDTO {
	tags := b.Tags
	if tags == nil {
		tags = []string{}
	}
	return bookDTO{
		ID:          b.ID,
		LibraryID:   b.LibraryID,
		Title:       b.Title,
		Author:      b.Author,
		Format:      b.Format,
		Year:        b.Year,
		Progress:    float64(b.Progress) / 100.0,
		ResumeCFI:   b.ResumeCFI,
		Rating:      b.Rating,
		Palette:     firstNonEmpty(b.CoverPalette, "navy"),
		Description: b.Description,
		ISBN:        b.ISBN,
		Publisher:   b.Publisher,
		Series:      b.Series,
		SeriesNum:   b.SeriesIndex,
		Tags:        tags,
		HasCover:    b.HasCover,
		CoverMime:   b.CoverMime,
		AddedAt:     b.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// bookDetailDTO adds the user's shelf memberships to the base book shape.
type bookDetailDTO struct {
	bookDTO
	Shelves []string `json:"shelves"`
}

// Libraries returns every library visible to the current user.
// Single-tenant instance today, so it's just the full list with book counts.
func (h *Handler) Libraries(c *gin.Context) {
	libs, err := h.lib.List(c.Request.Context())
	if err != nil {
		writeServerError(c, "libraries list", err)
		return
	}
	out := make([]libraryDTO, 0, len(libs))
	for _, l := range libs {
		out = append(out, toLibraryDTO(l))
	}
	c.JSON(http.StatusOK, gin.H{"libraries": out})
}

// Books lists books in a library. Query params:
//
//	?library=<slug>   restrict to one library (empty = all)
//	?shelf=<slug>     restrict to a user shelf (wins over library when both are set)
//	?q=<text>         full-text search (title/author/description via tsv)
//	?format=EPUB,PDF  comma-separated format filter
//	?sort=            title|author|recent|year|rating (default: title)
//	?limit=           client-side hint; server caps at 500 today
func (h *Handler) Books(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}

	// Shelf filter short-circuits the library search — shelf membership is
	// a per-user concept that lives in its own join table.
	if shelfSlug := strings.TrimSpace(c.Query("shelf")); shelfSlug != "" {
		books, err := h.shelf.Books(c.Request.Context(), userID, shelfSlug)
		if err != nil {
			writeServerError(c, "shelf books", err)
			return
		}
		writeBooksPayload(c, books)
		return
	}

	params := model.SearchParams{
		Query:  strings.TrimSpace(c.Query("q")),
		Sort:   strings.TrimSpace(c.Query("sort")),
		Format: splitCSV(c.Query("format")),
	}
	library := strings.TrimSpace(c.Query("library"))

	books, err := h.lib.Search(c.Request.Context(), userID, library, params)
	if err != nil {
		writeServerError(c, "books search", err)
		return
	}
	writeBooksPayload(c, books)
}

// bookPatch is the PATCH /books/:id body. Every field is optional — pointer
// fields let us distinguish "not sent" from "sent as zero value". Missing
// fields preserve the existing row; present fields overwrite.
type bookPatch struct {
	Title       *string   `json:"title,omitempty"`
	Author      *string   `json:"author,omitempty"`
	Format      *string   `json:"format,omitempty"`
	Year        *int      `json:"year,omitempty"`
	Rating      *int      `json:"rating,omitempty"`
	Palette     *string   `json:"palette,omitempty"`
	Description *string   `json:"description,omitempty"`
	ISBN        *string   `json:"isbn,omitempty"`
	Publisher   *string   `json:"publisher,omitempty"`
	Series      *string   `json:"series,omitempty"`
	SeriesNum   *int      `json:"seriesNum,omitempty"`
	Tags        *[]string `json:"tags,omitempty"`
}

// progressPayload is the POST /books/:id/progress body. Progress is 0..1 to
// match the read-side DTO; the service converts to an int percent internally.
type progressPayload struct {
	Progress  float64 `json:"progress"`
	ResumeCfi string  `json:"resumeCfi,omitempty"`
}

// BookPatch applies partial metadata updates and returns the fresh detail
// DTO so the caller can skip a follow-up GET.
func (h *Handler) BookPatch(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")

	current, err := h.lib.GetBook(c.Request.Context(), userID, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "book not found")
			return
		}
		writeServerError(c, "book load", err)
		return
	}

	var patch bookPatch
	if !bindJSON(c, &patch) {
		return
	}
	applyBookPatch(&current, patch)

	if err := h.lib.UpdateBookMetadata(c.Request.Context(), current); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "book not found")
			return
		}
		writeServerError(c, "book update", err)
		return
	}

	// Re-load so the response carries any repo-computed fields (e.g. any
	// side effects of the UPDATE) and stays in lockstep with a fresh GET.
	fresh, err := h.lib.GetBook(c.Request.Context(), userID, id)
	if err != nil {
		writeServerError(c, "book reload", err)
		return
	}
	shelves, err := h.shelf.SlugsForBook(c.Request.Context(), userID, id)
	if err != nil {
		writeServerError(c, "book shelves", err)
		return
	}
	if shelves == nil {
		shelves = []string{}
	}
	c.JSON(http.StatusOK, gin.H{
		"book": bookDetailDTO{
			bookDTO:  toBookDTO(fresh),
			Shelves:  shelves,
		},
	})
}

// BookProgressUpdate stores the current user's reading progress + resume
// token for a book. Returns 204 (no body) — callers can refetch if they
// need the updated float.
func (h *Handler) BookProgressUpdate(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")

	var payload progressPayload
	if !bindJSON(c, &payload) {
		return
	}
	percent := int(payload.Progress * 100.0)
	if err := h.progress.Set(c.Request.Context(), userID, id, percent, payload.ResumeCfi); err != nil {
		writeServerError(c, "progress update", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// BookAddShelf puts a book on one of the user's shelves. Idempotent —
// the underlying repo ON CONFLICTs so reposting is a no-op.
func (h *Handler) BookAddShelf(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	slug := c.Param("slug")
	if err := h.shelf.AddBook(c.Request.Context(), userID, slug, id); err != nil {
		switch {
		case errors.Is(err, repo.ErrNotFound):
			writeError(c, http.StatusNotFound, "shelf not found")
		case errors.Is(err, repo.ErrSmartShelfImmutable):
			writeError(c, http.StatusConflict, err.Error())
		default:
			writeServerError(c, "shelf add", err)
		}
		return
	}
	c.Status(http.StatusNoContent)
}

// BookRemoveShelf takes a book off a shelf. No-op when the book isn't on
// the shelf; distinguishing "not there" from "shelf doesn't exist" isn't
// worth the extra query.
func (h *Handler) BookRemoveShelf(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	slug := c.Param("slug")
	if err := h.shelf.RemoveBook(c.Request.Context(), userID, slug, id); err != nil {
		switch {
		case errors.Is(err, repo.ErrNotFound):
			writeError(c, http.StatusNotFound, "shelf not found")
		case errors.Is(err, repo.ErrSmartShelfImmutable):
			writeError(c, http.StatusConflict, err.Error())
		default:
			writeServerError(c, "shelf remove", err)
		}
		return
	}
	c.Status(http.StatusNoContent)
}

// applyBookPatch mutates b in place with the non-nil fields of p. Tag slices
// are copied to avoid sharing backing storage with the request.
func applyBookPatch(b *model.Book, p bookPatch) {
	if p.Title != nil {
		b.Title = strings.TrimSpace(*p.Title)
	}
	if p.Author != nil {
		b.Author = strings.TrimSpace(*p.Author)
	}
	if p.Format != nil {
		b.Format = strings.TrimSpace(*p.Format)
	}
	if p.Year != nil {
		b.Year = *p.Year
	}
	if p.Rating != nil {
		r := *p.Rating
		if r < 0 {
			r = 0
		}
		if r > 5 {
			r = 5
		}
		b.Rating = r
	}
	if p.Palette != nil {
		b.CoverPalette = strings.TrimSpace(*p.Palette)
	}
	if p.Description != nil {
		b.Description = *p.Description
	}
	if p.ISBN != nil {
		b.ISBN = strings.TrimSpace(*p.ISBN)
	}
	if p.Publisher != nil {
		b.Publisher = strings.TrimSpace(*p.Publisher)
	}
	if p.Series != nil {
		b.Series = strings.TrimSpace(*p.Series)
	}
	if p.SeriesNum != nil {
		b.SeriesIndex = *p.SeriesNum
	}
	if p.Tags != nil {
		tags := model.DedupTags(*p.Tags)
		clean := tags[:0]
		for _, t := range tags {
			if v := strings.TrimSpace(t); v != "" {
				clean = append(clean, v)
			}
		}
		b.Tags = clean
	}
}

// BookDetail returns a single book enriched with the user's shelf
// membership slugs.
func (h *Handler) BookDetail(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	b, err := h.lib.GetBook(c.Request.Context(), userID, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "book not found")
			return
		}
		writeServerError(c, "book detail", err)
		return
	}
	shelves, err := h.shelf.SlugsForBook(c.Request.Context(), userID, id)
	if err != nil {
		writeServerError(c, "book shelves", err)
		return
	}
	if shelves == nil {
		shelves = []string{}
	}
	c.JSON(http.StatusOK, gin.H{
		"book": bookDetailDTO{
			bookDTO:  toBookDTO(b),
			Shelves:  shelves,
		},
	})
}

// writeBooksPayload projects a repo result into the list envelope the SPA
// expects: `{ books, total }`. Total mirrors the returned slice length
// today (the repo caps at 500) — a cursor + total split is a future slice.
func writeBooksPayload(c *gin.Context, books []model.Book) {
	out := make([]bookDTO, 0, len(books))
	for _, b := range books {
		out = append(out, toBookDTO(b))
	}
	c.JSON(http.StatusOK, gin.H{
		"books": out,
		"total": len(out),
	})
}

// BookFile streams the book's on-disk bytes into the browser so the EPUB /
// PDF reader can open it. Delegates to serveBookFile for the path sandbox
// (BOOKDROP_PATH + registered library_paths, trailing-separator prefix
// match) — the same validation OPDS downloads use.
func (h *Handler) BookFile(c *gin.Context) {
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
		writeServerError(c, "book file lookup", err)
		return
	}
	if book.Path == "" {
		writeError(c, http.StatusNotFound, "no file on disk for this book")
		return
	}
	mime := mimeForFormat(book.Format)
	if mime == "" {
		writeError(c, http.StatusUnsupportedMediaType, "reader does not support this format")
		return
	}
	if err := h.serveBookFile(c, book.Path, mime); err != nil {
		writeError(c, http.StatusForbidden, err.Error())
		return
	}
}

// requireUserID fetches the session user attached by auth.RequireAuth.
// Returns "" and writes 401 when the context is unexpectedly empty — in
// practice the middleware prevents that, but this double-check avoids
// silent data leaks if a route is wired without RequireAuth by mistake.
func requireUserID(c *gin.Context) string {
	u := auth.UserFromContext(c.Request.Context())
	if u == nil {
		writeError(c, http.StatusUnauthorized, "authentication required")
		return ""
	}
	return u.ID
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func splitCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}
