// SPDX-License-Identifier: AGPL-3.0-or-later

package opds

import (
	"strconv"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
)

// DefaultPageSize is how many books one acquisition page carries. It is
// the catalog's number: the repository window a caller reads for a page
// and the arithmetic that decides whether that page has a successor are
// the same fact, and they used to be two — a page-size constant in the
// handler feeding both a query and a hand-written next-link predicate.
const DefaultPageSize = 50

// Document is a rendered feed: the bytes plus the content type OPDS
// clients expect to receive them under. Callers write both; neither the
// Atom profile strings nor the namespaces leave this package.
type Document struct {
	ContentType string
	Body        []byte
}

// FeedRef names one acquisition feed. Key is the stable suffix of the
// feed's urn: id ("all", "recent", "library:scifi", "search:dune");
// SelfPath is where the feed lives on this server, query string and all.
type FeedRef struct {
	Key      string
	Title    string
	SelfPath string
}

// Catalog renders the three documents an OPDS 1.2 client asks for. Base
// is the absolute origin every href hangs off — clients resolve nothing
// relative, so the caller answers "what is my public origin?" once and
// the feeds never ask again.
//
// PageSize may be left zero for DefaultPageSize. Now is the clock behind
// <updated>; nil means time.Now, and a test sets it to make a feed's
// bytes deterministic.
type Catalog struct {
	Base     string
	PageSize int
	Now      func() time.Time
}

// NewCatalog builds a catalog serving absolute URLs under base.
func NewCatalog(base string) Catalog {
	return Catalog{Base: base, PageSize: DefaultPageSize}
}

// Window is the repository read one page needs: the limit and offset
// that produce the same slice this catalog will then page links for.
func (c Catalog) Window(page int) (limit, offset int) {
	if page < 1 {
		page = 1
	}
	size := c.pageSize()
	return size, (page - 1) * size
}

// Navigation renders the root feed: the standing shelves every install
// has, then one entry per library.
func (c Catalog) Navigation(libraries []model.Library) (Document, error) {
	now := c.updated()

	f := feed{
		Xmlns:   namespaceAtom,
		ID:      instanceID + ":opds:root",
		Title:   "embookshelf",
		Updated: now,
		Author:  &author{Name: "embookshelf"},
		Links: []link{
			{Rel: relSelf, Href: c.Base + "/opds/", Type: mimeNavigation},
			{Rel: relStart, Href: c.Base + "/opds/", Type: mimeNavigation},
			{Rel: relSearch, Href: c.Base + "/opds/search.xml", Type: mimeOpenSearch},
			{Rel: relSearch, Href: c.Base + "/opds/search?q={searchTerms}", Type: mimeAcquisition, Title: "Search"},
		},
		Entries: []entry{
			{
				ID:      instanceID + ":opds:all",
				Title:   "All books",
				Updated: now,
				Content: &textField{Type: "text", Value: "Every book in the library."},
				Links:   []link{{Rel: relSubsection, Href: c.Base + "/opds/all", Type: mimeAcquisition}},
			},
			{
				ID:      instanceID + ":opds:recent",
				Title:   "Recently added",
				Updated: now,
				Content: &textField{Type: "text", Value: "Newest imports first."},
				Links:   []link{{Rel: relSubsection, Href: c.Base + "/opds/recent", Type: mimeAcquisition}},
			},
		},
	}
	for _, lib := range libraries {
		f.Entries = append(f.Entries, entry{
			ID:      instanceID + ":opds:library:" + lib.Slug,
			Title:   lib.Name,
			Updated: atomTime(lib.CreatedAt),
			Content: &textField{Type: "text", Value: "Library: " + lib.Name},
			Links:   []link{{Rel: relSubsection, Href: c.Base + "/opds/library/" + lib.Slug, Type: mimeAcquisition}},
		})
	}

	return render(f, mimeNavigation)
}

// Acquisition renders one page of one book feed. books is the window the
// caller read and total the full match count: rel=next exists exactly
// when the total reaches past the end of this window, which is why the
// caller never has to hold the whole feed in memory to know.
func (c Catalog) Acquisition(ref FeedRef, books []model.Book, total, page int) (Document, error) {
	if page < 1 {
		page = 1
	}
	selfHREF := c.Base + ref.SelfPath
	if page > 1 {
		selfHREF = withPage(selfHREF, page)
	}

	f := feed{
		Xmlns:     namespaceAtom,
		XmlnsOpds: namespaceOPDS,
		XmlnsDC:   namespaceDC,
		ID:        instanceID + ":opds:" + ref.Key,
		Title:     ref.Title,
		Updated:   c.updated(),
		Links: []link{
			{Rel: relSelf, Href: selfHREF, Type: mimeAcquisition},
			{Rel: relStart, Href: c.Base + "/opds/", Type: mimeNavigation},
			{Rel: relUp, Href: c.Base + "/opds/", Type: mimeNavigation},
		},
	}
	if page > 1 {
		f.Links = append(f.Links, link{
			Rel: relPrevious, Href: withPage(c.Base+ref.SelfPath, page-1), Type: mimeAcquisition,
		})
	}
	if (page-1)*c.pageSize()+len(books) < total {
		f.Links = append(f.Links, link{
			Rel: relNext, Href: withPage(c.Base+ref.SelfPath, page+1), Type: mimeAcquisition,
		})
	}
	for _, b := range books {
		f.Entries = append(f.Entries, bookEntry(b, c.bookLinks(b)))
	}

	return render(f, mimeAcquisition)
}

// OpenSearch renders the description document that lets a client turn
// its own search box into a request against this catalog.
func (c Catalog) OpenSearch() (Document, error) {
	return render(openSearchDescription{
		Xmlns:         namespaceOpenSearch,
		ShortName:     "embookshelf",
		Description:   "Search the embookshelf catalog",
		InputEncoding: "UTF-8",
		URL: osURL{
			Type:     mimeAcquisition,
			Template: c.Base + "/opds/search?q={searchTerms}",
		},
	}, mimeOpenSearch)
}

// bookLinks resolves the absolute URLs one book's entry points at. A
// cover is linked twice — full size and thumbnail — because OPDS clients
// look for different rels and this server serves one image for both.
func (c Catalog) bookLinks(b model.Book) bookLinks {
	l := bookLinks{
		Download:     c.Base + "/opds/book/" + b.ID + "/download",
		DownloadMime: model.MIMEForFormat(b.Format),
	}
	if b.HasCover {
		l.Cover = c.Base + "/opds/cover/" + b.ID
		l.Thumbnail = c.Base + "/opds/cover/" + b.ID
	}
	return l
}

func (c Catalog) pageSize() int {
	if c.PageSize <= 0 {
		return DefaultPageSize
	}
	return c.PageSize
}

func (c Catalog) updated() string {
	if c.Now == nil {
		return atomTime(time.Now())
	}
	return atomTime(c.Now())
}

// withPage adds ?page=N to a feed URL, extending a query string that is
// already there rather than starting a second one — the search feed
// carries its terms in exactly that position.
func withPage(u string, page int) string {
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + "page=" + strconv.Itoa(page)
}
