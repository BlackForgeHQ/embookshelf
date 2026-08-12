// SPDX-License-Identifier: AGPL-3.0-or-later

package opds

import (
	"strings"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
)

// Feed shape is a property of the catalog, not of HTTP. These tests ask
// the questions that used to need gin, httptest and a substring search:
// how many pages does this total have, which rel links does this page
// carry, does a search feed's page link keep the query it was built
// from.

var pinnedClock = func() time.Time { return time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC) }

func testCatalog() Catalog {
	c := NewCatalog("http://example.com")
	c.Now = pinnedClock
	return c
}

func bookList(n int) []model.Book {
	out := make([]model.Book, n)
	for i := range out {
		out[i] = model.Book{ID: "book-" + strings.Repeat("x", i%3), Title: "t", Format: "EPUB"}
	}
	return out
}

func mustAcquisition(t *testing.T, c Catalog, ref FeedRef, bs []model.Book, total, page int) string {
	t.Helper()
	doc, err := c.Acquisition(ref, bs, total, page)
	if err != nil {
		t.Fatalf("Acquisition: %v", err)
	}
	if doc.ContentType != "application/atom+xml;profile=opds-catalog;kind=acquisition" {
		t.Errorf("ContentType = %q", doc.ContentType)
	}
	return string(doc.Body)
}

func TestAcquisitionPageLinks(t *testing.T) {
	allBooks := FeedRef{Key: "all", Title: "All books", SelfPath: "/opds/all"}

	cases := []struct {
		name     string
		pageSize int
		books    int
		total    int
		page     int
		wantNext string // "" means no next link
		wantPrev string
	}{
		{
			name:  "a full page with one book left over has exactly one next link",
			books: 50, total: 51, page: 1,
			wantNext: "http://example.com/opds/all?page=2",
		},
		{
			name:  "a total that fits one page has neither link",
			books: 30, total: 30, page: 1,
		},
		{
			name:  "a middle page links both ways",
			books: 50, total: 120, page: 2,
			wantNext: "http://example.com/opds/all?page=3",
			wantPrev: "http://example.com/opds/all?page=1",
		},
		{
			name:  "a short last page has no next link",
			books: 20, total: 120, page: 3,
			wantPrev: "http://example.com/opds/all?page=2",
		},
		{
			name:  "an exactly-full last page has no next link",
			books: 50, total: 100, page: 2,
			wantPrev: "http://example.com/opds/all?page=1",
		},
		{
			name:  "an empty page past the end still links back",
			books: 0, total: 51, page: 3,
			wantPrev: "http://example.com/opds/all?page=2",
		},
		{
			name: "the page size is the catalog's, not a constant the caller repeats",
			// 10 per page, 25 total, page 2 → 5 left after this page.
			pageSize: 10, books: 10, total: 25, page: 2,
			wantNext: "http://example.com/opds/all?page=3",
			wantPrev: "http://example.com/opds/all?page=1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := testCatalog()
			if tc.pageSize > 0 {
				c.PageSize = tc.pageSize
			}
			body := mustAcquisition(t, c, allBooks, bookList(tc.books), tc.total, tc.page)

			assertLink(t, body, "next", tc.wantNext)
			assertLink(t, body, "previous", tc.wantPrev)
			if got := strings.Count(body, "<entry>"); got != tc.books {
				t.Errorf("feed carries %d entries, want the %d it was handed", got, tc.books)
			}
		})
	}
}

// assertLink asserts the feed has exactly one rel=name link with href
// want, or none at all when want is empty.
func assertLink(t *testing.T, body, name, want string) {
	t.Helper()
	prefix := `<link rel="` + name + `" href="`
	if want == "" {
		if strings.Contains(body, prefix) {
			t.Errorf("feed has a rel=%s link and should not:\n%s", name, body)
		}
		return
	}
	full := prefix + want + `"`
	if n := strings.Count(body, full); n != 1 {
		t.Errorf("feed has %d links matching %s, want exactly 1:\n%s", n, full, body)
	}
}

func TestAcquisitionSelfLinkCarriesThePage(t *testing.T) {
	c := testCatalog()
	ref := FeedRef{Key: "all", Title: "All books", SelfPath: "/opds/all"}

	first := mustAcquisition(t, c, ref, bookList(50), 120, 1)
	assertLink(t, first, "self", "http://example.com/opds/all")

	second := mustAcquisition(t, c, ref, bookList(50), 120, 2)
	assertLink(t, second, "self", "http://example.com/opds/all?page=2")
}

// A search feed's self path already has a query string, so its page
// links must extend it rather than start a second one — the bug this
// derivation is one place to prevent.
func TestSearchFeedPageLinksKeepTheExistingQueryString(t *testing.T) {
	c := testCatalog()
	ref := FeedRef{
		Key:      "search:le guin",
		Title:    "Search: le guin",
		SelfPath: "/opds/search?q=le+guin",
	}

	body := mustAcquisition(t, c, ref, bookList(50), 120, 2)

	assertLink(t, body, "self", "http://example.com/opds/search?q=le+guin&amp;page=2")
	assertLink(t, body, "previous", "http://example.com/opds/search?q=le+guin&amp;page=1")
	assertLink(t, body, "next", "http://example.com/opds/search?q=le+guin&amp;page=3")
	if strings.Contains(body, "?q=le+guin?page=") {
		t.Error("page link started a second query string")
	}
	if !strings.Contains(body, "<id>urn:embookshelf:opds:search:le guin</id>") {
		t.Errorf("feed id is not derived from the ref key:\n%s", body)
	}
}

func TestAcquisitionEntryMapsTheBook(t *testing.T) {
	c := testCatalog()
	b := model.Book{
		ID:          "b1",
		Title:       "The Dispossessed",
		Author:      "Ursula K. Le Guin",
		Publisher:   "Harper",
		Description: "An ambiguous utopia.",
		ISBN:        "978-0-06-105488-7",
		Year:        1974,
		Tags:        []string{"scifi"},
		Format:      "EPUB",
		HasCover:    true,
		CreatedAt:   time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC),
	}

	body := mustAcquisition(t, c, FeedRef{Key: "all", Title: "All", SelfPath: "/opds/all"}, []model.Book{b}, 1, 1)

	for _, want := range []string{
		"<id>urn:embookshelf:book:b1</id>",
		"<updated>2024-03-04T05:06:07Z</updated>",
		"<dc:issued>1974</dc:issued>",
		"<dc:identifier>urn:isbn:9780061054887</dc:identifier>",
		`<category term="scifi" label="scifi">`,
		`href="http://example.com/opds/book/b1/download" type="application/epub+zip"`,
		`rel="http://opds-spec.org/image/thumbnail" href="http://example.com/opds/cover/b1"`,
		`rel="http://opds-spec.org/image" href="http://example.com/opds/cover/b1"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("entry is missing %s\n%s", want, body)
		}
	}
}

func TestAcquisitionOmitsCoverLinksWhenThereIsNoCover(t *testing.T) {
	c := testCatalog()
	b := model.Book{ID: "b2", Title: "No cover", Format: "PDF"}

	body := mustAcquisition(t, c, FeedRef{Key: "all", Title: "All", SelfPath: "/opds/all"}, []model.Book{b}, 1, 1)

	if strings.Contains(body, "/opds/cover/") {
		t.Errorf("cover link on a book with no cover:\n%s", body)
	}
	if !strings.Contains(body, `type="application/pdf"`) {
		t.Errorf("acquisition link does not carry the format's mime:\n%s", body)
	}
}

func TestWindowIsThePageSizeInOnePlace(t *testing.T) {
	c := testCatalog()

	cases := []struct{ page, limit, offset int }{
		{1, 50, 0},
		{2, 50, 50},
		{3, 50, 100},
	}
	for _, tc := range cases {
		limit, offset := c.Window(tc.page)
		if limit != tc.limit || offset != tc.offset {
			t.Errorf("Window(%d) = (%d, %d), want (%d, %d)", tc.page, limit, offset, tc.limit, tc.offset)
		}
	}

	c.PageSize = 10
	if limit, offset := c.Window(3); limit != 10 || offset != 20 {
		t.Errorf("Window(3) at size 10 = (%d, %d), want (10, 20)", limit, offset)
	}
}

// The navigation feed's bytes, pinned. Identical to the golden the
// handler carried before assembly moved here (#319).
func TestNavigationFeedWireFormat(t *testing.T) {
	c := testCatalog()
	libs := []model.Library{
		{Name: "Sci-Fi", Slug: "scifi", CreatedAt: time.Date(2023, 1, 2, 3, 4, 5, 0, time.UTC)},
		{Name: "History", Slug: "history", CreatedAt: time.Date(2023, 6, 7, 8, 9, 10, 0, time.UTC)},
	}

	doc, err := c.Navigation(libs)
	if err != nil {
		t.Fatalf("Navigation: %v", err)
	}
	if doc.ContentType != "application/atom+xml;profile=opds-catalog;kind=navigation" {
		t.Errorf("ContentType = %q", doc.ContentType)
	}
	if got := string(doc.Body); got != wantNavigationXML {
		t.Errorf("navigation feed changed on the wire.\n got:\n%s\nwant:\n%s", got, wantNavigationXML)
	}
}

func TestNavigationFeedWithoutLibrariesStillOffersTheStandardShelves(t *testing.T) {
	doc, err := testCatalog().Navigation(nil)
	if err != nil {
		t.Fatalf("Navigation: %v", err)
	}
	body := string(doc.Body)
	if n := strings.Count(body, "<entry>"); n != 2 {
		t.Errorf("%d entries with no libraries, want the 2 standing shelves:\n%s", n, body)
	}
	if !strings.Contains(body, "/opds/all") || !strings.Contains(body, "/opds/recent") {
		t.Errorf("navigation lost a standing shelf:\n%s", body)
	}
}

func TestOpenSearchDescriptionWireFormat(t *testing.T) {
	doc, err := testCatalog().OpenSearch()
	if err != nil {
		t.Fatalf("OpenSearch: %v", err)
	}
	if doc.ContentType != "application/opensearchdescription+xml" {
		t.Errorf("ContentType = %q", doc.ContentType)
	}
	if got := string(doc.Body); got != wantOpenSearchXML {
		t.Errorf("OpenSearch description changed on the wire.\n got:\n%s\nwant:\n%s", got, wantOpenSearchXML)
	}
}

func TestZeroPageSizeFallsBackToTheDefault(t *testing.T) {
	c := Catalog{Base: "http://example.com", Now: pinnedClock}
	if limit, _ := c.Window(1); limit != DefaultPageSize {
		t.Errorf("Window limit = %d on a zero-value PageSize, want %d", limit, DefaultPageSize)
	}
}
