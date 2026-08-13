// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
)

// Byte-for-byte pins of the three OPDS documents this server emits.
//
// The substring assertions in opds_test.go pin the paging contract; these
// pin the wire format itself — namespaces, attribute order, indentation,
// the rel vocabulary, the entry field order. They exist because feed
// assembly moved from the handler into opds.Catalog (#319), and a
// refactor of an interchange format is only safe if the bytes are pinned
// before it starts. The goldens below were captured from the pre-move
// handler code; a diff here is a client-visible change, never a
// formatting preference.

// pinnedFeedUpdated replaces the feed-level <updated> — the one value in
// these documents that comes from the clock rather than the inputs. It is
// always the first <updated> in the document: Feed emits id, title,
// subtitle, updated before any entry.
func pinnedFeedUpdated(t *testing.T, doc string) string {
	t.Helper()
	loc := regexp.MustCompile(`<updated>[^<]*</updated>`).FindStringIndex(doc)
	if loc == nil {
		t.Fatal("feed has no <updated> — Atom requires one")
	}
	return doc[:loc[0]] + "<updated>PINNED</updated>" + doc[loc[1]:]
}

func opdsWireBook(id, title string) model.Book {
	return model.Book{
		ID:          id,
		Title:       title,
		Author:      "Ursula K. Le Guin",
		Publisher:   "Harper & Row",
		Description: "A <hainish> tale.",
		ISBN:        "978-0-441-47812-5",
		Year:        1969,
		Tags:        []string{"scifi", "classic"},
		Format:      "EPUB",
		Path:        "/lib/" + id + ".epub",
		HasCover:    true,
		CreatedAt:   time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC),
	}
}

func TestOPDSNavigationFeedWireFormat(t *testing.T) {
	libs := []model.Library{
		{Name: "Sci-Fi", Slug: "scifi", CreatedAt: time.Date(2023, 1, 2, 3, 4, 5, 0, time.UTC)},
		{Name: "History", Slug: "history", CreatedAt: time.Date(2023, 6, 7, 8, 9, 10, 0, time.UTC)},
	}

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/opds/", nil)

	catalog := opdsCatalog(c)
	catalog.Now = func() time.Time { return time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC) }

	doc, err := catalog.Navigation(libs)
	if err != nil {
		t.Fatalf("Navigation: %v", err)
	}

	if got := string(doc.Body); got != wantNavigationXML {
		t.Errorf("navigation feed changed on the wire.\n got:\n%s\nwant:\n%s", got, wantNavigationXML)
	}
}

func TestOPDSAcquisitionFeedWireFormat(t *testing.T) {
	store := &pagingBookStore{
		books: []model.Book{
			opdsWireBook("aaaaaaaa-0001-4001-8001-000000000001", "The Left Hand of Darkness"),
			// A book with nothing optional set: no author, no cover, no
			// year, no tags — the omitempty half of the entry mapping.
			{ID: "aaaaaaaa-0001-4001-8001-000000000002", Title: "Untitled", Format: "PDF"},
		},
		total: 120,
	}
	h := &Handler{books: store}

	rec := opdsRequest(t, h.OPDSAll, "/opds/all?page=2", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	if got := pinnedFeedUpdated(t, rec.Body.String()); got != wantAcquisitionXML {
		t.Errorf("acquisition feed changed on the wire.\n got:\n%s\nwant:\n%s", got, wantAcquisitionXML)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/atom+xml;profile=opds-catalog;kind=acquisition" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestOPDSSearchFeedPageLinksKeepTheQueryString(t *testing.T) {
	store := &pagingBookStore{books: opdsBooks(50), total: 120}
	h := &Handler{books: store}

	rec := opdsRequest(t, h.OPDSSearch, "/opds/search?q=le+guin&page=2", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	body := rec.Body.String()
	for _, want := range []string{
		`<link rel="self" href="http://example.com/opds/search?q=le+guin&amp;page=2"`,
		`<link rel="previous" href="http://example.com/opds/search?q=le+guin&amp;page=1"`,
		`<link rel="next" href="http://example.com/opds/search?q=le+guin&amp;page=3"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("search feed is missing %s\nbody:\n%s", want, body)
		}
	}
}

func TestOPDSSearchDescriptionWireFormat(t *testing.T) {
	h := &Handler{}

	rec := opdsRequest(t, h.OPDSSearchDescription, "/opds/search.xml", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	if got := rec.Body.String(); got != wantOpenSearchXML {
		t.Errorf("OpenSearch description changed on the wire.\n got:\n%s\nwant:\n%s", got, wantOpenSearchXML)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/opensearchdescription+xml" {
		t.Errorf("Content-Type = %q", ct)
	}
}

const wantNavigationXML = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>urn:embookshelf:opds:root</id>
  <title>embookshelf</title>
  <updated>2024-01-02T03:04:05Z</updated>
  <author>
    <name>embookshelf</name>
  </author>
  <link rel="self" href="http://example.com/opds/" type="application/atom+xml;profile=opds-catalog;kind=navigation"></link>
  <link rel="start" href="http://example.com/opds/" type="application/atom+xml;profile=opds-catalog;kind=navigation"></link>
  <link rel="search" href="http://example.com/opds/search.xml" type="application/opensearchdescription+xml"></link>
  <link rel="search" href="http://example.com/opds/search?q={searchTerms}" type="application/atom+xml;profile=opds-catalog;kind=acquisition" title="Search"></link>
  <entry>
    <id>urn:embookshelf:opds:all</id>
    <title>All books</title>
    <updated>2024-01-02T03:04:05Z</updated>
    <content type="text">Every book in the library.</content>
    <link rel="subsection" href="http://example.com/opds/all" type="application/atom+xml;profile=opds-catalog;kind=acquisition"></link>
  </entry>
  <entry>
    <id>urn:embookshelf:opds:recent</id>
    <title>Recently added</title>
    <updated>2024-01-02T03:04:05Z</updated>
    <content type="text">Newest imports first.</content>
    <link rel="subsection" href="http://example.com/opds/recent" type="application/atom+xml;profile=opds-catalog;kind=acquisition"></link>
  </entry>
  <entry>
    <id>urn:embookshelf:opds:library:scifi</id>
    <title>Sci-Fi</title>
    <updated>2023-01-02T03:04:05Z</updated>
    <content type="text">Library: Sci-Fi</content>
    <link rel="subsection" href="http://example.com/opds/library/scifi" type="application/atom+xml;profile=opds-catalog;kind=acquisition"></link>
  </entry>
  <entry>
    <id>urn:embookshelf:opds:library:history</id>
    <title>History</title>
    <updated>2023-06-07T08:09:10Z</updated>
    <content type="text">Library: History</content>
    <link rel="subsection" href="http://example.com/opds/library/history" type="application/atom+xml;profile=opds-catalog;kind=acquisition"></link>
  </entry>
</feed>`

const wantAcquisitionXML = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:opds="http://opds-spec.org/2010/catalog" xmlns:dc="http://purl.org/dc/elements/1.1/">
  <id>urn:embookshelf:opds:all</id>
  <title>All books</title>
  <updated>PINNED</updated>
  <link rel="self" href="http://example.com/opds/all?page=2" type="application/atom+xml;profile=opds-catalog;kind=acquisition"></link>
  <link rel="start" href="http://example.com/opds/" type="application/atom+xml;profile=opds-catalog;kind=navigation"></link>
  <link rel="up" href="http://example.com/opds/" type="application/atom+xml;profile=opds-catalog;kind=navigation"></link>
  <link rel="previous" href="http://example.com/opds/all?page=1" type="application/atom+xml;profile=opds-catalog;kind=acquisition"></link>
  <link rel="next" href="http://example.com/opds/all?page=3" type="application/atom+xml;profile=opds-catalog;kind=acquisition"></link>
  <entry>
    <id>urn:embookshelf:book:aaaaaaaa-0001-4001-8001-000000000001</id>
    <title>The Left Hand of Darkness</title>
    <updated>2024-03-04T05:06:07Z</updated>
    <published>2024-03-04T05:06:07Z</published>
    <author>
      <name>Ursula K. Le Guin</name>
    </author>
    <summary type="text">A &lt;hainish&gt; tale.</summary>
    <dc:publisher>Harper &amp; Row</dc:publisher>
    <dc:issued>1969</dc:issued>
    <dc:identifier>urn:isbn:9780441478125</dc:identifier>
    <category term="scifi" label="scifi"></category>
    <category term="classic" label="classic"></category>
    <link rel="http://opds-spec.org/image/thumbnail" href="http://example.com/opds/cover/aaaaaaaa-0001-4001-8001-000000000001" type="image/jpeg"></link>
    <link rel="http://opds-spec.org/image" href="http://example.com/opds/cover/aaaaaaaa-0001-4001-8001-000000000001" type="image/jpeg"></link>
    <link rel="http://opds-spec.org/acquisition" href="http://example.com/opds/book/aaaaaaaa-0001-4001-8001-000000000001/download" type="application/epub+zip"></link>
  </entry>
  <entry>
    <id>urn:embookshelf:book:aaaaaaaa-0001-4001-8001-000000000002</id>
    <title>Untitled</title>
    <updated>0001-01-01T00:00:00Z</updated>
    <published>0001-01-01T00:00:00Z</published>
    <link rel="http://opds-spec.org/acquisition" href="http://example.com/opds/book/aaaaaaaa-0001-4001-8001-000000000002/download" type="application/pdf"></link>
  </entry>
</feed>`

const wantOpenSearchXML = `<?xml version="1.0" encoding="UTF-8"?>
<OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/">
  <ShortName>embookshelf</ShortName>
  <Description>Search the embookshelf catalog</Description>
  <Url type="application/atom+xml;profile=opds-catalog;kind=acquisition" template="http://example.com/opds/search?q={searchTerms}"></Url>
  <InputEncoding>UTF-8</InputEncoding>
</OpenSearchDescription>`
