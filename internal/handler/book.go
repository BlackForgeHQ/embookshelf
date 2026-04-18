package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/middleware"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/view/page"
	"github.com/blackforge/embookshelf/internal/view/partial"
)

func (h *Handler) BookDetail(c *gin.Context) {
	userID := requireUser(c)
	if userID == "" {
		return
	}

	id := c.Param("id")
	book, err := h.lib.GetBook(c.Request.Context(), userID, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			c.String(http.StatusNotFound, "book not found")
			return
		}
		slog.Error("get book", "id", id, "err", err)
		c.String(http.StatusInternalServerError, "failed to load book")
		return
	}

	libs, _ := h.lib.List(c.Request.Context())
	shelves, _ := h.shelf.List(c.Request.Context(), userID)
	shelfSlugs, _ := h.shelf.SlugsForBook(c.Request.Context(), userID, id)
	render(c, page.BookDetail(libs, shelves, book, shelfSlugs, h.cfg.DiskType))
}

func (h *Handler) BookEdit(c *gin.Context) {
	userID := requireUser(c)
	if userID == "" {
		return
	}

	id := c.Param("id")
	book, err := h.lib.GetBook(c.Request.Context(), userID, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			c.String(http.StatusNotFound, "book not found")
			return
		}
		slog.Error("get book", "id", id, "err", err)
		c.String(http.StatusInternalServerError, "failed to load book")
		return
	}
	libs, _ := h.lib.List(c.Request.Context())
	shelves, _ := h.shelf.List(c.Request.Context(), userID)
	render(c, page.BookEdit(libs, shelves, book, h.cfg.DiskType))
}

func (h *Handler) BookUpdate(c *gin.Context) {
	userID := requireUser(c)
	if userID == "" {
		return
	}

	id := c.Param("id")
	book, err := h.lib.GetBook(c.Request.Context(), userID, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			c.String(http.StatusNotFound, "book not found")
			return
		}
		slog.Error("get book", "id", id, "err", err)
		c.String(http.StatusInternalServerError, "failed to load book")
		return
	}

	book.Title = strings.TrimSpace(c.PostForm("title"))
	book.Author = strings.TrimSpace(c.PostForm("author"))
	book.Format = strings.TrimSpace(c.PostForm("format"))
	book.CoverPalette = strings.TrimSpace(c.PostForm("cover_palette"))
	book.Description = strings.TrimSpace(c.PostForm("description"))
	book.ISBN = strings.TrimSpace(c.PostForm("isbn"))
	book.Publisher = strings.TrimSpace(c.PostForm("publisher"))
	book.Series = strings.TrimSpace(c.PostForm("series"))
	book.Year = parseIntOr(c.PostForm("year"), 0)
	book.Rating = clampInt(parseIntOr(c.PostForm("rating"), 0), 0, 5)
	book.SeriesIndex = parseIntOr(c.PostForm("series_index"), 0)
	book.Tags = splitTags(c.PostForm("tags"))

	if book.Title == "" {
		c.String(http.StatusBadRequest, "title is required")
		return
	}

	if err := h.lib.UpdateBookMetadata(c.Request.Context(), book); err != nil {
		slog.Error("update book", "id", id, "err", err)
		c.String(http.StatusInternalServerError, "failed to save")
		return
	}

	// Reload with the user's progress populated.
	fresh, err := h.lib.GetBook(c.Request.Context(), userID, book.ID)
	if err == nil {
		book = fresh
	}

	if middleware.IsHTMX(c.Request) && !middleware.IsHTMXBoosted(c.Request) {
		c.Writer.Header().Set("HX-Push-Url", "/app/book/"+book.ID)
		shelfSlugs, _ := h.shelf.SlugsForBook(c.Request.Context(), userID, book.ID)
		userShelves, _ := h.shelf.List(c.Request.Context(), userID)
		render(c, partial.BookDetailPanel(book, shelfSlugs, userShelves))
		return
	}
	c.Redirect(http.StatusSeeOther, "/app/book/"+book.ID)
}

// BookProgress handles POST /app/book/:id/progress. Accepts either
// application/x-www-form-urlencoded (from the detail-panel slider) or
// application/json (from the EPUB reader's periodic save). For HTMX clients
// it swaps the updated book-detail panel in place; for JSON it returns 204.
func (h *Handler) BookProgress(c *gin.Context) {
	userID := requireUser(c)
	if userID == "" {
		return
	}
	id := c.Param("id")

	// ShouldBind picks the binder based on Content-Type.
	var req struct {
		Percent *int   `json:"percent" form:"percent"`
		CFI     string `json:"cfi"     form:"cfi"`
		Action  string `json:"action"  form:"action"`
	}
	_ = c.ShouldBind(&req) // ignore bind errors — we validate fields below

	if req.Action == "clear" {
		if err := h.progress.Clear(c.Request.Context(), userID, id); err != nil {
			slog.Error("clear progress", "id", id, "err", err)
			c.String(http.StatusInternalServerError, "failed")
			return
		}
	} else {
		if req.Percent == nil {
			c.String(http.StatusBadRequest, "percent required (0-100)")
			return
		}
		if err := h.progress.Set(c.Request.Context(), userID, id, *req.Percent, req.CFI); err != nil {
			slog.Error("set progress", "id", id, "err", err)
			c.String(http.StatusInternalServerError, "failed")
			return
		}
	}

	// JSON callers (the reader) don't want an HTML fragment back.
	if c.GetHeader("Content-Type") == "application/json" {
		c.Status(http.StatusNoContent)
		return
	}
	h.respondWithBookDetail(c, userID, id)
}

// BookToggleShelf handles POST /app/book/:id/shelf/:slug with a `present`
// form flag. Used by the add-to-shelf checkboxes on book detail.
func (h *Handler) BookToggleShelf(c *gin.Context) {
	userID := requireUser(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	slug := c.Param("slug")

	// Presence of the checkbox in the form → add; absence → remove. Since
	// checkboxes only submit when checked, we flip behavior based on a
	// toggle-intent form field.
	switch c.PostForm("action") {
	case "add":
		if err := h.shelf.AddBook(c.Request.Context(), userID, slug, id); err != nil {
			slog.Error("add to shelf", "slug", slug, "id", id, "err", err)
			c.String(http.StatusInternalServerError, "failed")
			return
		}
	case "remove":
		if err := h.shelf.RemoveBook(c.Request.Context(), userID, slug, id); err != nil {
			slog.Error("remove from shelf", "slug", slug, "id", id, "err", err)
			c.String(http.StatusInternalServerError, "failed")
			return
		}
	default:
		c.String(http.StatusBadRequest, "action must be add or remove")
		return
	}

	h.respondWithBookDetail(c, userID, id)
}

func (h *Handler) respondWithBookDetail(c *gin.Context, userID, bookID string) {
	book, err := h.lib.GetBook(c.Request.Context(), userID, bookID)
	if err != nil {
		slog.Error("reload book", "id", bookID, "err", err)
		c.String(http.StatusInternalServerError, "failed")
		return
	}
	if middleware.IsHTMX(c.Request) && !middleware.IsHTMXBoosted(c.Request) {
		shelfSlugs, _ := h.shelf.SlugsForBook(c.Request.Context(), userID, bookID)
		userShelves, _ := h.shelf.List(c.Request.Context(), userID)
		render(c, partial.BookDetailPanel(book, shelfSlugs, userShelves))
		return
	}
	c.Redirect(http.StatusSeeOther, "/app/book/"+bookID)
}

func (h *Handler) ShelfView(c *gin.Context) {
	userID := requireUser(c)
	if userID == "" {
		return
	}

	slug := c.Param("slug")
	shelf, err := h.shelf.GetBySlug(c.Request.Context(), userID, slug)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			c.String(http.StatusNotFound, "shelf not found")
			return
		}
		slog.Error("get shelf", "slug", slug, "err", err)
		c.String(http.StatusInternalServerError, "failed to load shelf")
		return
	}

	books, err := h.shelf.Books(c.Request.Context(), userID, slug)
	if err != nil {
		slog.Error("shelf books", "slug", slug, "err", err)
		c.String(http.StatusInternalServerError, "failed to load shelf")
		return
	}

	if middleware.IsHTMX(c.Request) && !middleware.IsHTMXBoosted(c.Request) {
		render(c, partial.LibraryGrid(books))
		return
	}

	libs, _ := h.lib.List(c.Request.Context())
	shelves, _ := h.shelf.List(c.Request.Context(), userID)
	render(c, page.ShelfView(libs, shelves, shelf, books, h.cfg.DiskType))
}

// ShelfCreate handles POST /app/shelves with `name` and optional `accent`.
func (h *Handler) ShelfCreate(c *gin.Context) {
	userID := requireUser(c)
	if userID == "" {
		return
	}
	name := strings.TrimSpace(c.PostForm("name"))
	accent := strings.TrimSpace(c.PostForm("accent"))
	if name == "" {
		c.String(http.StatusBadRequest, "name required")
		return
	}
	_, err := h.shelf.Create(c.Request.Context(), userID, name, accent)
	if err != nil {
		slog.Error("create shelf", "name", name, "err", err)
		c.String(http.StatusInternalServerError, "failed to create shelf")
		return
	}
	c.Redirect(http.StatusSeeOther, "/app/library")
}

// ShelfDelete handles POST /app/shelf/:slug/delete.
func (h *Handler) ShelfDelete(c *gin.Context) {
	userID := requireUser(c)
	if userID == "" {
		return
	}
	slug := c.Param("slug")
	if err := h.shelf.Delete(c.Request.Context(), userID, slug); err != nil && !errors.Is(err, repo.ErrNotFound) {
		slog.Error("delete shelf", "slug", slug, "err", err)
		c.String(http.StatusInternalServerError, "failed to delete")
		return
	}
	c.Redirect(http.StatusSeeOther, "/app/library")
}

func parseIntOr(s string, def int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// splitTags parses the free-text "tags" field from the metadata form.
func splitTags(raw string) []string {
	var out []string
	for _, t := range strings.Split(raw, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	if out == nil {
		out = []string{}
	}
	return model.DedupTags(out)
}
