// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
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
	ResumeAudio   string   `json:"resumeAudio,omitempty"`
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
	// unlocked books keep the payload small. Keys are model.LockField
	// values, derived from model.LockSpecs.
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
		ResumeAudio:     b.ResumeAudio,
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
// everything is unlocked so the DTO stays lean on fresh books. Derived
// from model.LockSpecs, so a new lock reaches the client without an edit
// here — the omission that used to leave a flag invisible.
func serializeLocks(l model.BookLocks) map[string]bool {
	locked := l.Locked()
	if len(locked) == 0 {
		return nil
	}
	out := make(map[string]bool, len(locked))
	for _, f := range locked {
		out[string(f)] = true
	}
	return out
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
		books, err := h.shelf.Books(c.Request.Context(), userID, shelfSlug, strings.TrimSpace(c.Query("sort")))
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

	books, err := h.books.Search(c.Request.Context(), userID, library, params)
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
func (h *Handler) BookPatch(c *gin.Context, s bookScope) {
	userID, id := s.UserID, s.Book.ID
	current := s.Book

	var patch bookPatch
	if !bindJSON(c, &patch) {
		return
	}
	patch.toDomain().Apply(&current)

	outcome, err := h.lib.UpdateBookMetadata(c.Request.Context(), current)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "book not found")
			return
		}
		writeServerError(c, "book update", err)
		return
	}

	h.writeBookDetail(c, userID, id, outcome, "book metadata write degraded")
}

// BookProgressUpdate stores the current user's reading progress + resume
// token for a book. Returns 204 (no body) — callers can refetch if they
// need the updated float.
func (h *Handler) BookProgressUpdate(c *gin.Context, s bookScope) {
	userID, id := s.UserID, s.Book.ID

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
//
// A `public:<slug>` URL form is accepted: the owner-admin's picker
// shows their shared shelf using the canonical public-prefixed slug
// (ADR-0017); we strip and resolve to the same row via (user_id,
// slug). Non-owners attempting the public path 404 from the repo
// lookup — the picker filters them client-side as well.
func (h *Handler) BookAddShelf(c *gin.Context, s bookScope) {
	slug, _ := service.SplitPublicSlug(c.Param("slug"))
	if err := h.shelf.AddBook(c.Request.Context(), s.UserID, slug, s.Book.ID); err != nil {
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

// BookDelete hard-deletes a book. Admin-gated at the router; books are a
// shared instance resource (no per-user ownership), so letting any reader
// nuke a book everyone else can see is the wrong default.
//
// The handler's whole job here is the HTTP contract: map the one fatal
// error to a status. Authorization and resolving the id to a row — so an
// unknown book is a 404 rather than a silent 204 — are the book-scoped
// seam's, which is why the body starts from a book. The delete sequence — snapshot
// the storage keys, drop the row, then remove the bytes, the cover art
// and any legacy on-disk file — belongs to LibraryService.DeleteBook,
// which is where its ordering invariant can be tested.
//
// A degraded cleanup does not change the status code. The row is gone,
// so 204 is the truth as far as the client is concerned, and 204 carries
// no body to put warnings in; what is left behind is bytes nothing
// references, which is an operator's problem and goes to the log.
func (h *Handler) BookDelete(c *gin.Context, s bookScope) {
	id := s.Book.ID

	outcome, err := h.lib.DeleteBook(c.Request.Context(), s.Book)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "book not found")
			return
		}
		writeServerError(c, "book delete", err)
		return
	}
	if warnings := outcome.Warnings(); len(warnings) > 0 {
		slog.Warn("book delete degraded", "book", id, "warnings", warnings)
	}

	c.Status(http.StatusNoContent)
}

// BookRemoveShelf takes a book off a shelf. No-op when the book isn't on
// the shelf; distinguishing "not there" from "shelf doesn't exist" isn't
// worth the extra query.
func (h *Handler) BookRemoveShelf(c *gin.Context, s bookScope) {
	slug, _ := service.SplitPublicSlug(c.Param("slug"))
	if err := h.shelf.RemoveBook(c.Request.Context(), s.UserID, slug, s.Book.ID); err != nil {
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
// toDomain converts the wire patch into the domain patch. The editing rules
// — trimming, clamps, tag cleanup, the PublishDate/Year coupling, tri-state
// public reviews — live on model.BookPatch.Apply so they hold for every
// caller, not only this endpoint.
func (p bookPatch) toDomain() model.BookPatch {
	return model.BookPatch{
		Title:              p.Title,
		Subtitle:           p.Subtitle,
		Author:             p.Author,
		Format:             p.Format,
		Year:               p.Year,
		PublishDate:        p.PublishDate,
		Language:           p.Language,
		Rating:             p.Rating,
		Palette:            p.Palette,
		Description:        p.Description,
		ISBN:               p.ISBN,
		ISBN10:             p.ISBN10,
		Publisher:          p.Publisher,
		Series:             p.Series,
		SeriesNum:          p.SeriesNum,
		SeriesTotal:        p.SeriesTotal,
		Genres:             p.Genres,
		Moods:              p.Moods,
		Tags:               p.Tags,
		AgeRating:          p.AgeRating,
		ContentRating:      p.ContentRating,
		Pages:              p.Pages,
		PublicReviews:      p.PublicReviews,
		PublicReviewsClear: p.PublicReviewsClear,
	}
}

// BookDetail returns a single book enriched with the user's shelf
// membership slugs.
func (h *Handler) BookDetail(c *gin.Context, s bookScope) {
	h.writeBookDetail(c, s.UserID, s.Book.ID, service.Outcome{}, "")
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
func (h *Handler) BookFile(c *gin.Context, s bookScope) {
	book := s.Book
	// A book can be consumed as text or as narration — two renditions of
	// one book (ADR-0025 §3). books.format names the primary one, so the
	// caller has to say when it wants the other; without this selector
	// the generated MP3 is unreachable and Listen has no source.
	if c.Query("rendition") == renditionAudio {
		h.serveNarrationRendition(c, book)
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

// extForFormat maps our Format tag to the conventional file extension.
// "" for unsupported formats so the download still works (no extension)
// rather than failing. The table is model.FormatSpecs (#194).
func extForFormat(format string) string {
	return model.ExtForFormat(format)
}
