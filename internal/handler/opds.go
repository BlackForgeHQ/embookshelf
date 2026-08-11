// SPDX-License-Identifier: AGPL-3.0-or-later

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

// OPDSAll serves the complete catalog as a paged acquisition feed.
func (h *Handler) OPDSAll(c *gin.Context) {
	h.serveCatalogFeed(c, opdsAcquisitionContext{
		ID:       opds.InstanceID + ":opds:all",
		Title:    "All books",
		SelfPath: "/opds/all",
	}, "", model.SearchParams{Sort: "recent"})
}

// OPDSLibrary serves one library's books as a paged acquisition feed.
func (h *Handler) OPDSLibrary(c *gin.Context) {
	slug := c.Param("slug")
	h.serveCatalogFeed(c, opdsAcquisitionContext{
		ID:       opds.InstanceID + ":opds:library:" + slug,
		Title:    "Library · " + slug,
		SelfPath: "/opds/library/" + url.PathEscape(slug),
	}, slug, model.SearchParams{})
}

// OPDSRecent shows newly-added books first, across all libraries.
func (h *Handler) OPDSRecent(c *gin.Context) {
	h.serveCatalogFeed(c, opdsAcquisitionContext{
		ID:       opds.InstanceID + ":opds:recent",
		Title:    "Recently added",
		SelfPath: "/opds/recent",
	}, "", model.SearchParams{Sort: "recent"})
}

// OPDSSearch serves the free-text search acquisition feed. An empty
// query renders an empty feed without asking the catalog.
func (h *Handler) OPDSSearch(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	feedCtx := opdsAcquisitionContext{
		ID:       opds.InstanceID + ":opds:search:" + q,
		Title:    "Search: " + q,
		SelfPath: "/opds/search?q=" + url.QueryEscape(q),
	}
	if q == "" {
		if opdsUserID(c) == "" {
			return
		}
		h.writeAcquisition(c, feedCtx, nil, 0, 1)
		return
	}
	h.serveCatalogFeed(c, feedCtx, "", model.SearchParams{Query: q})
}

// serveCatalogFeed asks the catalog for one page of one feed and renders
// it. Aggregation across libraries, the downloadable filter and the
// total all belong to the catalog; a failed read is a 500, never a
// silently shorter feed.
func (h *Handler) serveCatalogFeed(c *gin.Context, feedCtx opdsAcquisitionContext, slug string, p model.SearchParams) {
	userID := opdsUserID(c)
	if userID == "" {
		return
	}
	page := clampInt(parseIntOr(c.Query("page"), 1), 1, 1_000_000)
	p.Limit = opdsPageSize
	p.Offset = (page - 1) * opdsPageSize
	p.Downloadable = true

	books, total, err := h.books.Search(c.Request.Context(), userID, slug, p)
	if err != nil {
		slog.Error("opds feed: search", "feed", feedCtx.SelfPath, "err", err)
		c.String(http.StatusInternalServerError, "failed")
		return
	}
	h.writeAcquisition(c, feedCtx, books, total, page)
}

// OPDSSearchDescription serves the OpenSearch description XML.
func (h *Handler) OPDSSearchDescription(c *gin.Context) {
	base := opdsBase(c)
	desc := opds.OpenSearchDescription{
		Xmlns:         "http://a9.com/-/spec/opensearch/1.1/",
		ShortName:     "embookshelf",
		Description:   "Search the embookshelf catalog",
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
func (h *Handler) OPDSDownload(c *gin.Context, s bookScope) {
	id, book := s.Book.ID, s.Book
	if book.Path == "" {
		c.String(http.StatusNotFound, "no file on disk for this book")
		return
	}
	mime := mimeForFormat(book.Format)
	if mime == "" {
		mime = "application/octet-stream"
	}
	if err := h.serveBookFile(c, book, mime); err != nil {
		slog.Warn("opds serve", "id", id, "err", err)
		c.String(http.StatusForbidden, err.Error())
		return
	}
}

// OPDSCover streams the approved book's cover over the OPDS-authed surface.
// Mirrors BookCover but uses the Basic Auth context rather than the session.
// Like BookCover, it asks coverstore for the book's bytes and lets the
// module decide which namespace they are in.
func (h *Handler) OPDSCover(c *gin.Context, s bookScope) {
	id, book := s.Book.ID, s.Book
	if !book.HasCover || h.covers == nil {
		c.Status(http.StatusNotFound)
		return
	}
	mime := book.CoverMime
	if mime == "" {
		mime = "application/octet-stream"
	}

	rc, err := h.covers.Open(book)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.Status(http.StatusNotFound)
			return
		}
		slog.Warn("opds cover open", "book_id", id, "err", err)
		c.Status(http.StatusInternalServerError)
		return
	}
	defer func() { _ = rc.Close() }()
	c.Header("Content-Type", mime)
	c.Header("Cache-Control", "private, max-age=86400")
	if _, err := io.Copy(c.Writer, rc); err != nil {
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

// writeAcquisition serializes one page of an OPDS acquisition feed.
// books is the page window the catalog returned and total its full match
// count; rel=next / rel=previous links are derived from those, not from
// slicing a full list in memory.
func (h *Handler) writeAcquisition(c *gin.Context, ctx opdsAcquisitionContext, books []model.Book, total, page int) {
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
	if page > 1 {
		feed.Links = append(feed.Links, opds.Link{
			Rel: opds.RelPrevious, Href: appendPage(base+ctx.SelfPath, page-1), Type: opds.MimeAcquisition,
		})
	}
	if (page-1)*opdsPageSize+len(books) < total {
		feed.Links = append(feed.Links, opds.Link{
			Rel: opds.RelNext, Href: appendPage(base+ctx.SelfPath, page+1), Type: opds.MimeAcquisition,
		})
	}
	for _, b := range books {
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
// want absolute URLs in the feed. An e-reader resolves those against the
// proxy it can reach, not the internal Host, so this is the same origin
// question the OIDC surfaces ask.
func opdsBase(c *gin.Context) string {
	return requestOrigin(c)
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
