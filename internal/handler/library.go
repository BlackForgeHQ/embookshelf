package handler

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// libraryDTO is the wire shape for a library row.
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
//
// The extended metadata surface (subtitle, language, publish date, genres,
// moods, series total, ISBN-10, age/content rating, pages, public
// reviews) is optional on the wire — fields stay out of the JSON blob
// when empty so clients that don't care about them don't see a pile of
// blank strings.
type bookDTO struct {
	ID            string   `json:"id"`
	LibraryID     string   `json:"libraryId"`
	Title         string   `json:"title"`
	Subtitle      string   `json:"subtitle,omitempty"`
	Author        string   `json:"author"`
	Format        string   `json:"format"`
	Year          int      `json:"year"`
	PublishDate   string   `json:"publishDate,omitempty"` // YYYY-MM-DD, empty when unset
	Language      string   `json:"language,omitempty"`
	Progress      float64  `json:"progress"`
	ResumeCFI     string   `json:"resumeCfi,omitempty"`
	Rating        int      `json:"rating"`
	Palette       string   `json:"palette"`
	Description   string   `json:"description,omitempty"`
	ISBN          string   `json:"isbn,omitempty"`
	ISBN10        string   `json:"isbn10,omitempty"`
	Publisher     string   `json:"publisher,omitempty"`
	Series        string   `json:"series,omitempty"`
	SeriesNum     int      `json:"seriesNum,omitempty"`
	SeriesTotal   int      `json:"seriesTotal,omitempty"`
	Genres        []string `json:"genres"`
	Moods         []string `json:"moods"`
	Tags          []string `json:"tags"`
	AgeRating     string   `json:"ageRating,omitempty"`
	ContentRating string   `json:"contentRating,omitempty"`
	Pages         int      `json:"pages,omitempty"`
	PublicReviews *bool    `json:"publicReviews,omitempty"`
	HasCover      bool     `json:"hasCover"`
	CoverMime     string   `json:"coverMime,omitempty"`
	// CoverVersion is a short cache-buster derived from the cover bytes
	// hash. Empty when the book has no cover or the hash hasn't been
	// backfilled yet. Clients append it to the cover URL as ?v=… so a
	// new upload invalidates the browser cache without dropping the
	// long max-age on the underlying response.
	CoverVersion string `json:"coverVersion,omitempty"`
	AddedAt      string `json:"addedAt"`
	// DurationSeconds is populated only for audio formats (MP3, M4B);
	// nil otherwise (omitted from the JSON via *int + omitempty).
	DurationSeconds *int         `json:"durationSeconds,omitempty"`
	Narrator        string       `json:"narrator,omitempty"`
	Chapters        []chapterDTO `json:"chapters,omitempty"`
	// Locks is a sparse map — only fields currently locked appear, so
	// unlocked books keep the payload small. Keys match model.LockFields.
	Locks map[string]bool `json:"locks,omitempty"`
}

// chapterDTO mirrors model.Chapter on the wire — keeps the JSON shape
// camel-case for the TypeScript client.
type chapterDTO struct {
	Title  string  `json:"title"`
	StartS float64 `json:"startS"`
	EndS   float64 `json:"endS"`
}

func toBookDTO(b model.Book) bookDTO {
	tags := b.Tags
	if tags == nil {
		tags = []string{}
	}
	genres := b.Genres
	if genres == nil {
		genres = []string{}
	}
	moods := b.Moods
	if moods == nil {
		moods = []string{}
	}
	publishDate := ""
	if b.PublishDate != nil {
		publishDate = b.PublishDate.UTC().Format("2006-01-02")
	}
	return bookDTO{
		ID:              b.ID,
		LibraryID:       b.LibraryID,
		Title:           b.Title,
		Subtitle:        b.Subtitle,
		Author:          b.Author,
		Format:          b.Format,
		Year:            b.Year,
		PublishDate:     publishDate,
		Language:        b.Language,
		Progress:        float64(b.Progress) / 100.0,
		ResumeCFI:       b.ResumeCFI,
		Rating:          b.Rating,
		Palette:         firstNonEmpty(b.CoverPalette, "navy"),
		Description:     b.Description,
		ISBN:            b.ISBN,
		ISBN10:          b.ISBN10,
		Publisher:       b.Publisher,
		Series:          b.Series,
		SeriesNum:       b.SeriesIndex,
		SeriesTotal:     b.SeriesTotal,
		Genres:          genres,
		Moods:           moods,
		Tags:            tags,
		AgeRating:       b.AgeRating,
		ContentRating:   b.ContentRating,
		Pages:           b.Pages,
		PublicReviews:   b.PublicReviews,
		HasCover:        b.HasCover,
		CoverMime:       b.CoverMime,
		CoverVersion:    coverVersion(b.CoverHash),
		AddedAt:         b.CreatedAt.UTC().Format(time.RFC3339),
		DurationSeconds: b.DurationSeconds,
		Narrator:        b.Narrator,
		Chapters:        chaptersToDTO(b.Chapters),
		Locks:           serializeLocks(b.Locks),
	}
}

// coverVersion truncates the sha256 cover hash to a short hex prefix
// suitable for a ?v= cache-buster. 12 hex chars = 48 bits, plenty of
// uniqueness per-book without bloating every URL.
func coverVersion(hash []byte) string {
	if len(hash) == 0 {
		return ""
	}
	enc := hex.EncodeToString(hash)
	if len(enc) > 12 {
		enc = enc[:12]
	}
	return enc
}

func chaptersToDTO(in []model.Chapter) []chapterDTO {
	if len(in) == 0 {
		return nil
	}
	out := make([]chapterDTO, len(in))
	for i, c := range in {
		out[i] = chapterDTO{Title: c.Title, StartS: c.StartS, EndS: c.EndS}
	}
	return out
}

// serializeLocks emits a sparse map of just the set flags. nil when
// everything is unlocked so the DTO stays lean on fresh books. Keys
// match model.LockFields.
func serializeLocks(l model.BookLocks) map[string]bool {
	out := map[string]bool{}
	if l.Title {
		out["title"] = true
	}
	if l.Subtitle {
		out["subtitle"] = true
	}
	if l.Author {
		out["author"] = true
	}
	if l.Description {
		out["description"] = true
	}
	if l.Publisher {
		out["publisher"] = true
	}
	if l.Series {
		out["series"] = true
	}
	if l.ISBN {
		out["isbn"] = true
	}
	if l.ISBN10 {
		out["isbn10"] = true
	}
	if l.Language {
		out["language"] = true
	}
	if l.PublishDate {
		out["publishDate"] = true
	}
	if l.Genres {
		out["genres"] = true
	}
	if l.Moods {
		out["moods"] = true
	}
	if l.Tags {
		out["tags"] = true
	}
	if l.Pages {
		out["pages"] = true
	}
	if l.Cover {
		out["cover"] = true
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
			if errors.Is(err, repo.ErrNotFound) {
				// Unknown shelf slug → treat as empty rather than 500.
				// The dashboard links to well-known slugs (e.g. "reading")
				// that may not exist for a fresh user yet.
				writeBooksPayload(c, nil)
				return
			}
			writeServerError(c, "shelf books", err)
			return
		}
		writeBooksPayload(c, books)
		return
	}

	params := model.SearchParams{
		Query:     strings.TrimSpace(c.Query("q")),
		Sort:      strings.TrimSpace(c.Query("sort")),
		Format:    splitCSV(c.Query("format")),
		Unshelved: c.Query("unshelved") == "1",
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
//
// PublishDate is "YYYY-MM-DD" or empty string to clear. PublicReviews
// is a *bool: omitting keeps the current value, true/false sets it. A
// separate PublicReviewsClear lets callers reset the tri-state to null
// without needing **bool decoding gymnastics.
type bookPatch struct {
	Title              *string   `json:"title,omitempty"`
	Subtitle           *string   `json:"subtitle,omitempty"`
	Author             *string   `json:"author,omitempty"`
	Format             *string   `json:"format,omitempty"`
	Year               *int      `json:"year,omitempty"`
	PublishDate        *string   `json:"publishDate,omitempty"`
	Language           *string   `json:"language,omitempty"`
	Rating             *int      `json:"rating,omitempty"`
	Palette            *string   `json:"palette,omitempty"`
	Description        *string   `json:"description,omitempty"`
	ISBN               *string   `json:"isbn,omitempty"`
	ISBN10             *string   `json:"isbn10,omitempty"`
	Publisher          *string   `json:"publisher,omitempty"`
	Series             *string   `json:"series,omitempty"`
	SeriesNum          *int      `json:"seriesNum,omitempty"`
	SeriesTotal        *int      `json:"seriesTotal,omitempty"`
	Genres             *[]string `json:"genres,omitempty"`
	Moods              *[]string `json:"moods,omitempty"`
	Tags               *[]string `json:"tags,omitempty"`
	AgeRating          *string   `json:"ageRating,omitempty"`
	ContentRating      *string   `json:"contentRating,omitempty"`
	Pages              *int      `json:"pages,omitempty"`
	PublicReviews      *bool     `json:"publicReviews,omitempty"`
	PublicReviewsClear bool      `json:"publicReviewsClear,omitempty"`
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
			bookDTO: toBookDTO(fresh),
			Shelves: shelves,
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

// BookDelete hard-deletes a book and best-effort removes its cover + source
// file from disk. Admin-gated at the router; books are a shared instance
// resource (no per-user ownership), so letting any reader nuke a book
// everyone else can see is the wrong default.
//
// The DB row is authoritative — if that succeeds, we 204 even if the
// filesystem cleanup hiccups (permission, already-gone, etc.). Leaving
// orphan bytes on disk is fixable; leaving the row in while lying that
// we deleted it is not.
//
// Source-file unlink is sandboxed to BOOKDROP_PATH + registered library
// paths, same allowlist BookFile / OPDS download use, so a stray path
// smuggled into the books row can't escape to somewhere unrelated.
func (h *Handler) BookDelete(c *gin.Context) {
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
		writeServerError(c, "book delete lookup", err)
		return
	}

	if err := h.lib.DeleteBook(c.Request.Context(), id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "book not found")
			return
		}
		writeServerError(c, "book delete", err)
		return
	}

	if h.covers != nil {
		if err := h.covers.DeleteBook(id); err != nil {
			slog.Warn("book delete: cover cleanup", "id", id, "err", err)
		}
	}
	if book.Path != "" {
		if err := h.deleteBookFile(c, book.Path); err != nil {
			slog.Warn("book delete: file cleanup", "id", id, "path", book.Path, "err", err)
		}
	}

	c.Status(http.StatusNoContent)
}

// deleteBookFile unlinks the on-disk book bytes, but only when path is
// rooted under a configured library root — mirrors the serveBookFile
// sandbox so a malformed books.path can't let delete escape the tree.
func (h *Handler) deleteBookFile(c *gin.Context, path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	roots := []string{}
	if h.cfg.BookDropPath != "" {
		if r, err := filepath.Abs(h.cfg.BookDropPath); err == nil {
			roots = append(roots, r)
		}
	}
	if h.lib != nil {
		if libs, err := h.lib.List(c.Request.Context()); err == nil {
			for _, l := range libs {
				if l.Path == "" {
					continue
				}
				if r, err := filepath.Abs(l.Path); err == nil {
					roots = append(roots, r)
				}
			}
		}
	}

	sep := string(filepath.Separator)
	for _, root := range roots {
		if absPath == root || strings.HasPrefix(absPath, root+sep) {
			if err := os.Remove(absPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		}
	}
	return errors.New("path outside allowed roots")
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
	if p.Subtitle != nil {
		b.Subtitle = strings.TrimSpace(*p.Subtitle)
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
	if p.PublishDate != nil {
		raw := strings.TrimSpace(*p.PublishDate)
		if raw == "" {
			b.PublishDate = nil
		} else if t, err := time.Parse("2006-01-02", raw); err == nil {
			b.PublishDate = &t
			// Keep Year in sync when a full date is supplied — avoids a
			// confusing mismatch between the two columns on display.
			b.Year = t.Year()
		}
	}
	if p.Language != nil {
		b.Language = strings.TrimSpace(*p.Language)
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
	if p.ISBN10 != nil {
		b.ISBN10 = strings.TrimSpace(*p.ISBN10)
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
	if p.SeriesTotal != nil {
		if *p.SeriesTotal < 0 {
			b.SeriesTotal = 0
		} else {
			b.SeriesTotal = *p.SeriesTotal
		}
	}
	if p.Genres != nil {
		b.Genres = cleanStringSlice(*p.Genres)
	}
	if p.Moods != nil {
		b.Moods = cleanStringSlice(*p.Moods)
	}
	if p.Tags != nil {
		b.Tags = cleanStringSlice(*p.Tags)
	}
	if p.AgeRating != nil {
		b.AgeRating = strings.TrimSpace(*p.AgeRating)
	}
	if p.ContentRating != nil {
		b.ContentRating = strings.TrimSpace(*p.ContentRating)
	}
	if p.Pages != nil {
		if *p.Pages < 0 {
			b.Pages = 0
		} else {
			b.Pages = *p.Pages
		}
	}
	// Tri-state public_reviews: explicit clear wins over a set, so
	// callers can send both to "reset then ignore". A plain set just
	// overwrites the current value.
	if p.PublicReviewsClear {
		b.PublicReviews = nil
	} else if p.PublicReviews != nil {
		v := *p.PublicReviews
		b.PublicReviews = &v
	}
}

// cleanStringSlice trims + dedupes a string slice for storage. Keeps
// first-occurrence order and drops empties.
func cleanStringSlice(in []string) []string {
	tags := model.DedupTags(in)
	clean := tags[:0]
	for _, t := range tags {
		if v := strings.TrimSpace(t); v != "" {
			clean = append(clean, v)
		}
	}
	return clean
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
			bookDTO: toBookDTO(b),
			Shelves: shelves,
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
//
// ?download=1 flips Content-Disposition to "attachment" with a
// human-readable filename so browsers save-as instead of embedding.
// Without the flag the response stays "inline" for the in-browser
// reader.
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
	if c.Query("download") != "" {
		filename := downloadFilename(book)
		// Standard RFC 6266: bare `filename=` for ASCII fallback +
		// `filename*=UTF-8''…` for anything non-ASCII so browsers
		// honour the suggested name when the title has diacritics.
		c.Header("Content-Disposition",
			fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
				asciiFallback(filename), url.PathEscape(filename)))
	}
	if err := h.serveBookFile(c, book, mime); err != nil {
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

// downloadFilename composes a human-readable filename for the save-as
// dialog. Falls back to the on-disk basename when the metadata is
// missing — we'd rather ship a working name than drop the download.
func downloadFilename(b model.Book) string {
	title := strings.TrimSpace(b.Title)
	if title == "" {
		if b.Path != "" {
			return filepath.Base(b.Path)
		}
		title = "book"
	}
	name := title
	if author := strings.TrimSpace(b.Author); author != "" {
		name = author + " - " + name
	}
	name = sanitizeFilename(name)
	ext := extForFormat(b.Format)
	if ext != "" && !strings.HasSuffix(strings.ToLower(name), ext) {
		name += ext
	}
	return name
}

// sanitizeFilename strips characters the OS won't accept in a filename
// plus path separators. Conservative: preserves Unicode, drops shell/
// filesystem special chars only.
func sanitizeFilename(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			b.WriteRune('_')
		default:
			if r < 0x20 {
				continue
			}
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// asciiFallback degrades a filename to printable ASCII for the bare
// `filename=` leg of Content-Disposition. Non-ASCII runes collapse to
// '_' so old browsers still get a usable name; modern ones pick the
// UTF-8 variant instead.
func asciiFallback(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r > 0x7e || r < 0x20 {
			b.WriteRune('_')
			continue
		}
		if r == '"' || r == '\\' {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// extForFormat maps our Format tag to the conventional file
// extension. "" for unsupported formats so the download still works
// (no extension) rather than failing.
func extForFormat(format string) string {
	switch format {
	case "EPUB":
		return ".epub"
	case "PDF":
		return ".pdf"
	case "CBZ":
		return ".cbz"
	case "CBR":
		return ".cbr"
	case "MOBI":
		return ".mobi"
	case "AZW3":
		return ".azw3"
	case "FB2":
		return ".fb2"
	case "TXT":
		return ".txt"
	}
	return ""
}
