// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/opds"
)

// OPDSRoot serves the navigation feed at /opds/.
func (h *Handler) OPDSRoot(c *gin.Context) {
	libs, err := h.lib.List(c.Request.Context())
	if err != nil {
		slog.Error("opds root: list libs", "err", err)
		c.String(http.StatusInternalServerError, "failed")
		return
	}
	doc, err := opdsCatalog(c).Navigation(libs)
	writeOPDS(c, doc, err)
}

// OPDSAll serves the complete catalog as a paged acquisition feed.
func (h *Handler) OPDSAll(c *gin.Context) {
	h.serveCatalogFeed(c, opds.FeedRef{
		Key:      "all",
		Title:    "All books",
		SelfPath: "/opds/all",
	}, "", model.SearchParams{Sort: "recent"})
}

// OPDSLibrary serves one library's books as a paged acquisition feed.
func (h *Handler) OPDSLibrary(c *gin.Context) {
	slug := c.Param("slug")
	h.serveCatalogFeed(c, opds.FeedRef{
		Key:      "library:" + slug,
		Title:    "Library · " + slug,
		SelfPath: "/opds/library/" + url.PathEscape(slug),
	}, slug, model.SearchParams{})
}

// OPDSRecent shows newly-added books first, across all libraries.
func (h *Handler) OPDSRecent(c *gin.Context) {
	h.serveCatalogFeed(c, opds.FeedRef{
		Key:      "recent",
		Title:    "Recently added",
		SelfPath: "/opds/recent",
	}, "", model.SearchParams{Sort: "recent"})
}

// OPDSSearch serves the free-text search acquisition feed. An empty
// query renders an empty feed without asking the catalog.
func (h *Handler) OPDSSearch(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	feedRef := opds.FeedRef{
		Key:      "search:" + q,
		Title:    "Search: " + q,
		SelfPath: "/opds/search?q=" + url.QueryEscape(q),
	}
	if q == "" {
		if opdsUserID(c) == "" {
			return
		}
		doc, err := opdsCatalog(c).Acquisition(feedRef, nil, 0, 1)
		writeOPDS(c, doc, err)
		return
	}
	h.serveCatalogFeed(c, feedRef, "", model.SearchParams{Query: q})
}

// serveCatalogFeed asks the catalog for one page of one feed and renders
// it. Aggregation across libraries, the downloadable filter and the
// total all belong to the catalog; a failed read is a 500, never a
// silently shorter feed.
func (h *Handler) serveCatalogFeed(c *gin.Context, ref opds.FeedRef, slug string, p model.SearchParams) {
	userID := opdsUserID(c)
	if userID == "" {
		return
	}
	catalog := opdsCatalog(c)
	page := clampInt(parseIntOr(c.Query("page"), 1), 1, 1_000_000)
	p.Limit, p.Offset = catalog.Window(page)
	p.Downloadable = true

	books, total, err := h.books.Search(c.Request.Context(), userID, slug, p)
	if err != nil {
		slog.Error("opds feed: search", "feed", ref.SelfPath, "err", err)
		c.String(http.StatusInternalServerError, "failed")
		return
	}
	doc, err := catalog.Acquisition(ref, books, total, page)
	writeOPDS(c, doc, err)
}

// OPDSSearchDescription serves the OpenSearch description XML.
func (h *Handler) OPDSSearchDescription(c *gin.Context) {
	doc, err := opdsCatalog(c).OpenSearch()
	writeOPDS(c, doc, err)
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
	mime := coverContentType(book.CoverMime)

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

// opdsCatalog binds the feed module to this request's public origin —
// the one thing the feeds cannot work out for themselves.
func opdsCatalog(c *gin.Context) opds.Catalog {
	return opds.NewCatalog(opdsBase(c))
}

// writeOPDS writes a rendered feed, or a 500 if it could not be built.
func writeOPDS(c *gin.Context, doc opds.Document, err error) {
	if err != nil {
		slog.Error("opds: render feed", "err", err)
		c.String(http.StatusInternalServerError, "failed")
		return
	}
	c.Data(http.StatusOK, doc.ContentType, doc.Body)
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
