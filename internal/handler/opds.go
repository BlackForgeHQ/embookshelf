package handler

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/opds"
)

const opdsPageSize = 50

// OPDSRoot serves the navigation feed at /opds/.
func (h *Handler) OPDSRoot(c *gin.Context) {
	libs, err := h.lib.List(c.Request.Context())
	if err != nil {
		slog.Error("opds root: list libs", "err", err)
		c.String(http.StatusInternalServerError, "failed")
		return
	}

	base := opdsBase(c)
	now := opds.NowAtom()

	feed := opds.Feed{
		Xmlns:   opds.NamespaceAtom,
		ID:      opds.InstanceID + ":opds:root",
		Title:   "embookshelf",
		Updated: now,
		Author:  &opds.Author{Name: "embookshelf"},
		Links: []opds.Link{
			{Rel: opds.RelSelf, Href: base + "/opds/", Type: opds.MimeNavigation},
			{Rel: opds.RelStart, Href: base + "/opds/", Type: opds.MimeNavigation},
			{Rel: opds.RelSearch, Href: base + "/opds/search.xml", Type: opds.MimeOpenSearch},
			{Rel: opds.RelSearch, Href: base + "/opds/search?q={searchTerms}", Type: opds.MimeAcquisition, Title: "Search"},
		},
		Entries: []opds.Entry{
			{
				ID:      opds.InstanceID + ":opds:all",
				Title:   "All books",
				Updated: now,
				Content: &opds.TextField{Type: "text", Value: "Every book in the library."},
				Links:   []opds.Link{{Rel: opds.RelSubsection, Href: base + "/opds/all", Type: opds.MimeAcquisition}},
			},
			{
				ID:      opds.InstanceID + ":opds:recent",
				Title:   "Recently added",
				Updated: now,
				Content: &opds.TextField{Type: "text", Value: "Newest imports first."},
				Links:   []opds.Link{{Rel: opds.RelSubsection, Href: base + "/opds/recent", Type: opds.MimeAcquisition}},
			},
		},
	}
	for _, lib := range libs {
		feed.Entries = append(feed.Entries, opds.Entry{
			ID:      opds.InstanceID + ":opds:library:" + lib.Slug,
			Title:   lib.Name,
			Updated: opds.AtomTime(lib.CreatedAt),
			Content: &opds.TextField{Type: "text", Value: "Library: " + lib.Name},
			Links:   []opds.Link{{Rel: opds.RelSubsection, Href: base + "/opds/library/" + lib.Slug, Type: opds.MimeAcquisition}},
		})
	}

	writeFeed(c, feed, opds.MimeNavigation)
}

// OPDSAll serves the complete library as a paged acquisition feed.
func (h *Handler) OPDSAll(c *gin.Context) {
	userID := opdsUserID(c)
	if userID == "" {
		return
	}

	libs, err := h.lib.List(c.Request.Context())
	if err != nil {
		c.String(http.StatusInternalServerError, "failed")
		return
	}

	var books []model.Book
	for _, lib := range libs {
		part, err := h.lib.Search(c.Request.Context(), userID, lib.Slug, model.SearchParams{Sort: "recent"})
		if err != nil {
			slog.Error("opds all: search", "lib", lib.Slug, "err", err)
			continue
		}
		books = append(books, part...)
	}

	h.writeAcquisition(c, opdsAcquisitionContext{
		ID:       opds.InstanceID + ":opds:all",
		Title:    "All books",
		SelfPath: "/opds/all",
	}, books)
}

// OPDSLibrary serves one library's books as a paged acquisition feed.
func (h *Handler) OPDSLibrary(c *gin.Context) {
	userID := opdsUserID(c)
	if userID == "" {
		return
	}
	slug := c.Param("slug")
	books, err := h.lib.Search(c.Request.Context(), userID, slug, model.SearchParams{})
	if err != nil {
		slog.Error("opds library", "slug", slug, "err", err)
		c.String(http.StatusInternalServerError, "failed")
		return
	}
	h.writeAcquisition(c, opdsAcquisitionContext{
		ID:       opds.InstanceID + ":opds:library:" + slug,
		Title:    "Library · " + slug,
		SelfPath: "/opds/library/" + url.PathEscape(slug),
	}, books)
}

// OPDSRecent shows newly-added books first, across all libraries.
func (h *Handler) OPDSRecent(c *gin.Context) {
	userID := opdsUserID(c)
	if userID == "" {
		return
	}
	libs, err := h.lib.List(c.Request.Context())
	if err != nil {
		c.String(http.StatusInternalServerError, "failed")
		return
	}
	var books []model.Book
	for _, lib := range libs {
		part, err := h.lib.Search(c.Request.Context(), userID, lib.Slug, model.SearchParams{Sort: "recent"})
		if err != nil {
			continue
		}
		books = append(books, part...)
	}
	h.writeAcquisition(c, opdsAcquisitionContext{
		ID:       opds.InstanceID + ":opds:recent",
		Title:    "Recently added",
		SelfPath: "/opds/recent",
	}, books)
}

// OPDSSearch serves the free-text search acquisition feed.
func (h *Handler) OPDSSearch(c *gin.Context) {
	userID := opdsUserID(c)
	if userID == "" {
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	libs, err := h.lib.List(c.Request.Context())
	if err != nil {
		c.String(http.StatusInternalServerError, "failed")
		return
	}

	var books []model.Book
	if q != "" {
		for _, lib := range libs {
			part, err := h.lib.Search(c.Request.Context(), userID, lib.Slug, model.SearchParams{Query: q})
			if err != nil {
				continue
			}
			books = append(books, part...)
		}
	}
	h.writeAcquisition(c, opdsAcquisitionContext{
		ID:       opds.InstanceID + ":opds:search:" + q,
		Title:    "Search: " + q,
		SelfPath: "/opds/search?q=" + url.QueryEscape(q),
	}, books)
}

// OPDSSearchDescription serves the OpenSearch description XML.
func (h *Handler) OPDSSearchDescription(c *gin.Context) {
	base := opdsBase(c)
	desc := opds.OpenSearchDescription{
		Xmlns:       "http://a9.com/-/spec/opensearch/1.1/",
		ShortName:   "embookshelf",
		Description: "Search the embookshelf catalog",
		InputEncoding: "UTF-8",
		URL: opds.OSURL{
			Type:     opds.MimeAcquisition,
			Template: base + "/opds/search?q={searchTerms}",
		},
	}
	body, err := opds.MarshalOpenSearch(desc)
	if err != nil {
		c.String(http.StatusInternalServerError, "failed")
		return
	}
	c.Data(http.StatusOK, opds.MimeOpenSearch, body)
}

// OPDSDownload streams the on-disk book file. Delegates to the same path
// sandbox as the web reader (BOOKDROP_PATH + registered library paths).
func (h *Handler) OPDSDownload(c *gin.Context) {
	userID := opdsUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	book, err := h.lib.GetBook(c.Request.Context(), userID, id)
	if err != nil {
		c.String(http.StatusNotFound, "book not found")
		return
	}
	if book.Path == "" {
		c.String(http.StatusNotFound, "no file on disk for this book")
		return
	}
	mime := mimeForFormat(book.Format)
	if mime == "" {
		mime = "application/octet-stream"
	}
	if err := h.serveBookFile(c, book.Path, mime); err != nil {
		slog.Warn("opds serve", "id", id, "err", err)
		c.String(http.StatusForbidden, err.Error())
		return
	}
}

// OPDSCover streams the approved book's cover over the OPDS-authed surface.
// Mirrors BookCover but uses the Basic Auth context rather than the session.
func (h *Handler) OPDSCover(c *gin.Context) {
	userID := opdsUserID(c)
	if userID == "" {
		return
	}
	id := c.Param("id")
	book, err := h.lib.GetBook(c.Request.Context(), userID, id)
	if err != nil || !book.HasCover || h.covers == nil {
		c.Status(http.StatusNotFound)
		return
	}
	f, err := h.covers.OpenBook(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.Status(http.StatusNotFound)
			return
		}
		c.Status(http.StatusInternalServerError)
		return
	}
	defer f.Close()
	mime := book.CoverMime
	if mime == "" {
		mime = "application/octet-stream"
	}
	c.Header("Content-Type", mime)
	c.Header("Cache-Control", "private, max-age=86400")
	if _, err := io.Copy(c.Writer, f); err != nil {
		slog.Warn("opds cover stream", "id", id, "err", err)
	}
}

// ---------------------------------------------------------------------------
// Helpers

type opdsAcquisitionContext struct {
	ID       string
	Title    string
	SelfPath string
}

// writeAcquisition serializes a paged OPDS acquisition feed. Pagination is
// driven by ?page=N (1-indexed); rel=next / rel=previous links are emitted
// when additional pages exist.
func (h *Handler) writeAcquisition(c *gin.Context, ctx opdsAcquisitionContext, books []model.Book) {
	page := clampInt(parseIntOr(c.Query("page"), 1), 1, 1_000_000)
	total := len(books)

	// Drop books without downloadable paths.
	filtered := books[:0]
	for _, b := range books {
		if b.Path != "" {
			filtered = append(filtered, b)
		}
	}
	books = filtered
	total = len(books)

	start := (page - 1) * opdsPageSize
	if start >= total {
		start = total
	}
	end := start + opdsPageSize
	if end > total {
		end = total
	}
	pageBooks := books[start:end]

	base := opdsBase(c)
	selfHREF := base + ctx.SelfPath
	if page > 1 {
		selfHREF = appendPage(selfHREF, page)
	}

	feed := opds.Feed{
		Xmlns:     opds.NamespaceAtom,
		XmlnsOpds: opds.NamespaceOPDS,
		XmlnsDC:   opds.NamespaceDC,
		ID:        ctx.ID,
		Title:     ctx.Title,
		Updated:   opds.NowAtom(),
		Links: []opds.Link{
			{Rel: opds.RelSelf, Href: selfHREF, Type: opds.MimeAcquisition},
			{Rel: opds.RelStart, Href: base + "/opds/", Type: opds.MimeNavigation},
			{Rel: opds.RelUp, Href: base + "/opds/", Type: opds.MimeNavigation},
		},
	}
	if start > 0 {
		feed.Links = append(feed.Links, opds.Link{
			Rel: opds.RelPrevious, Href: appendPage(base+ctx.SelfPath, page-1), Type: opds.MimeAcquisition,
		})
	}
	if end < total {
		feed.Links = append(feed.Links, opds.Link{
			Rel: opds.RelNext, Href: appendPage(base+ctx.SelfPath, page+1), Type: opds.MimeAcquisition,
		})
	}
	for _, b := range pageBooks {
		links := opds.BookLinks{
			Download:     base + "/opds/book/" + b.ID + "/download",
			DownloadMime: mimeForFormat(b.Format),
		}
		if b.HasCover {
			links.Cover = base + "/opds/cover/" + b.ID
			links.Thumbnail = base + "/opds/cover/" + b.ID
		}
		feed.Entries = append(feed.Entries, opds.BookEntry(b, links))
	}

	writeFeed(c, feed, opds.MimeAcquisition)
}

// writeFeed serializes and writes an OPDS feed with the expected content type.
func writeFeed(c *gin.Context, feed opds.Feed, mime string) {
	body, err := opds.MarshalFeed(feed)
	if err != nil {
		c.String(http.StatusInternalServerError, "failed")
		return
	}
	c.Data(http.StatusOK, mime, body)
}

// opdsBase reconstructs the request origin (scheme + host) — OPDS clients
// want absolute URLs in the feed.
func opdsBase(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host
}

// opdsUserID returns the authenticated user id from the Basic Auth
// middleware; aborts with 401 if missing (defensive — the middleware should
// have already rejected).
func opdsUserID(c *gin.Context) string {
	u := auth.UserFromContext(c.Request.Context())
	if u == nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		return ""
	}
	return u.ID
}

func appendPage(u string, page int) string {
	if strings.Contains(u, "?") {
		return u + "&page=" + strconv.Itoa(page)
	}
	return u + fmt.Sprintf("?page=%d", page)
}
