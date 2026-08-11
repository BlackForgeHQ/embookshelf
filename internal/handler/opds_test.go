// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
)

// The OPDS feeds are one catalog call each (#241): the catalog owns
// aggregation, paging and the downloadable filter, and the feed derives
// its next/previous links from the total the catalog reports. These
// tests pin that contract with a substituted store — the per-library
// loop they replaced swallowed errors and paged in memory.

// searchCall records what a feed asked the catalog for.
type searchCall struct {
	userID string
	slug   string
	params model.SearchParams
}

// pagingBookStore returns a fixed window and total, and records every
// Search so a test can prove a feed asked exactly once.
type pagingBookStore struct {
	books []model.Book
	total int
	err   error
	calls []searchCall
}

func (f *pagingBookStore) GetByID(context.Context, string, string) (model.Book, error) {
	return model.Book{}, errors.New("not used")
}

func (f *pagingBookStore) Search(_ context.Context, userID, slug string, p model.SearchParams) ([]model.Book, int, error) {
	f.calls = append(f.calls, searchCall{userID: userID, slug: slug, params: p})
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.books, f.total, nil
}

func opdsBooks(n int) []model.Book {
	books := make([]model.Book, n)
	for i := range books {
		books[i] = model.Book{
			ID:     fmt.Sprintf("aaaaaaaa-0001-4001-8001-%012d", i),
			Title:  fmt.Sprintf("book-%03d", i),
			Format: "EPUB",
			Path:   fmt.Sprintf("/lib/%03d.epub", i),
		}
	}
	return books
}

// opdsRequest drives one OPDS handler as the Basic-Auth'd user.
func opdsRequest(t *testing.T, fn gin.HandlerFunc, target string, params gin.Params) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	c.Params = params
	c.Request = c.Request.WithContext(
		auth.WithUser(c.Request.Context(), &model.User{ID: scopeTestUserID}))
	fn(c)
	return rec
}

func assertOneCall(t *testing.T, store *pagingBookStore) searchCall {
	t.Helper()
	if len(store.calls) != 1 {
		t.Fatalf("catalog asked %d times, want exactly 1 — aggregation is the catalog's job", len(store.calls))
	}
	return store.calls[0]
}

func TestOPDSAllAsksTheCatalogOnceForThePage(t *testing.T) {
	store := &pagingBookStore{books: opdsBooks(50), total: 120}
	h := &Handler{books: store}

	rec := opdsRequest(t, h.OPDSAll, "/opds/all?page=2", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	call := assertOneCall(t, store)
	if call.slug != "" {
		t.Errorf("slug = %q, want \"\" — all libraries is the catalog's empty-slug read", call.slug)
	}
	if call.userID != scopeTestUserID {
		t.Errorf("userID = %q, want the Basic-Auth user", call.userID)
	}
	p := call.params
	if p.Limit != 50 || p.Offset != 50 {
		t.Errorf("window = limit %d offset %d, want 50/50 for page 2", p.Limit, p.Offset)
	}
	if !p.Downloadable {
		t.Error("Downloadable not set — pathless books would page as dead links")
	}

	body := rec.Body.String()
	if !strings.Contains(body, `rel="next"`) || !strings.Contains(body, "page=3") {
		t.Error("missing next link to page 3 — 120 total at 50/page has a page after 2")
	}
	if !strings.Contains(body, `rel="previous"`) || !strings.Contains(body, "page=1") {
		t.Error("missing previous link to page 1")
	}
	if got := strings.Count(body, "<entry>"); got != 50 {
		t.Errorf("feed has %d entries, want the 50 the catalog returned", got)
	}
}

func TestOPDSAllLastPageHasNoNextLink(t *testing.T) {
	store := &pagingBookStore{books: opdsBooks(20), total: 120}
	h := &Handler{books: store}

	rec := opdsRequest(t, h.OPDSAll, "/opds/all?page=3", nil)

	body := rec.Body.String()
	if strings.Contains(body, `rel="next"`) {
		t.Error("next link on the last page — the total says there is nothing after offset 100+20")
	}
	if !strings.Contains(body, `rel="previous"`) {
		t.Error("missing previous link on page 3")
	}
}

func TestOPDSAllFirstPageHasNoPreviousLink(t *testing.T) {
	store := &pagingBookStore{books: opdsBooks(30), total: 30}
	h := &Handler{books: store}

	rec := opdsRequest(t, h.OPDSAll, "/opds/all", nil)

	body := rec.Body.String()
	if strings.Contains(body, `rel="previous"`) {
		t.Error("previous link on page 1")
	}
	if strings.Contains(body, `rel="next"`) {
		t.Error("next link when the total fits one page")
	}
}

func TestOPDSAllCatalogErrorIsA500NotAShorterFeed(t *testing.T) {
	store := &pagingBookStore{err: errors.New("library exploded")}
	h := &Handler{books: store}

	rec := opdsRequest(t, h.OPDSAll, "/opds/all", nil)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 — a failing catalog must not render as an empty feed", rec.Code)
	}
}

func TestOPDSRecentIsOneGloballyRecentRead(t *testing.T) {
	store := &pagingBookStore{books: opdsBooks(10), total: 10}
	h := &Handler{books: store}

	rec := opdsRequest(t, h.OPDSRecent, "/opds/recent", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	call := assertOneCall(t, store)
	if call.slug != "" || call.params.Sort != "recent" {
		t.Errorf("asked (slug %q, sort %q), want (\"\", \"recent\") — one globally recent list, not per-library blocks",
			call.slug, call.params.Sort)
	}
}

func TestOPDSLibraryScopesTheCatalogToTheSlug(t *testing.T) {
	store := &pagingBookStore{books: opdsBooks(3), total: 3}
	h := &Handler{books: store}

	rec := opdsRequest(t, h.OPDSLibrary, "/opds/library/scifi",
		gin.Params{{Key: "slug", Value: "scifi"}})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	call := assertOneCall(t, store)
	if call.slug != "scifi" {
		t.Errorf("slug = %q, want scifi", call.slug)
	}
	if !call.params.Downloadable {
		t.Error("Downloadable not set on the library feed")
	}
}

func TestOPDSSearchPassesTheQueryThrough(t *testing.T) {
	store := &pagingBookStore{books: opdsBooks(2), total: 2}
	h := &Handler{books: store}

	rec := opdsRequest(t, h.OPDSSearch, "/opds/search?q=dune", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	call := assertOneCall(t, store)
	if call.params.Query != "dune" {
		t.Errorf("query = %q, want dune", call.params.Query)
	}
}

func TestOPDSSearchWithoutQueryDoesNotAskTheCatalog(t *testing.T) {
	store := &pagingBookStore{}
	h := &Handler{books: store}

	rec := opdsRequest(t, h.OPDSSearch, "/opds/search?q=", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(store.calls) != 0 {
		t.Errorf("catalog asked %d times for an empty query, want 0", len(store.calls))
	}
}
