// SPDX-License-Identifier: AGPL-3.0-or-later

// Package opds builds OPDS 1.2 (Atom/XML) catalog and acquisition feeds.
//
// Catalog is the whole interface: three documents in, bytes and a content
// type out. Everything below — the namespaces, the rel vocabulary, the
// struct tags — is the implementation of a wire format, and a caller that
// had to learn it in order to publish a feed was learning this package's
// homework. Namespaces are declared manually on the root <feed> to
// sidestep Go's encoding/xml namespace handling: the Local tags
// `xml:"dc:language"` are emitted as-is and the xmlns:dc attribute makes
// them valid.
package opds

import "encoding/xml"

// Namespace URIs.
const (
	namespaceAtom       = "http://www.w3.org/2005/Atom"
	namespaceOPDS       = "http://opds-spec.org/2010/catalog"
	namespaceDC         = "http://purl.org/dc/elements/1.1/"
	namespaceOpenSearch = "http://a9.com/-/spec/opensearch/1.1/"
)

// Content types per OPDS 1.2.
const (
	mimeNavigation  = "application/atom+xml;profile=opds-catalog;kind=navigation"
	mimeAcquisition = "application/atom+xml;profile=opds-catalog;kind=acquisition"
	mimeOpenSearch  = "application/opensearchdescription+xml"
)

// Standard link rel values.
const (
	relSelf        = "self"
	relStart       = "start"
	relUp          = "up"
	relSearch      = "search"
	relSubsection  = "subsection"
	relNext        = "next"
	relPrevious    = "previous"
	relAcquisition = "http://opds-spec.org/acquisition"
	relImage       = "http://opds-spec.org/image"
	relThumbnail   = "http://opds-spec.org/image/thumbnail"
)

// feed is the Atom root. XmlnsOpds/XmlnsDC are set on feeds that use those
// namespaces (acquisition feeds), left blank on pure-nav feeds.
type feed struct {
	XMLName   xml.Name `xml:"feed"`
	Xmlns     string   `xml:"xmlns,attr"`
	XmlnsOpds string   `xml:"xmlns:opds,attr,omitempty"`
	XmlnsDC   string   `xml:"xmlns:dc,attr,omitempty"`

	ID       string  `xml:"id"`
	Title    string  `xml:"title"`
	Subtitle string  `xml:"subtitle,omitempty"`
	Updated  string  `xml:"updated"`
	Author   *author `xml:"author,omitempty"`
	Links    []link  `xml:"link"`
	Entries  []entry `xml:"entry"`
}

// author on a feed or entry.
type author struct {
	Name string `xml:"name"`
	URI  string `xml:"uri,omitempty"`
}

// link is an Atom <link>.
type link struct {
	Rel   string `xml:"rel,attr"`
	Href  string `xml:"href,attr"`
	Type  string `xml:"type,attr,omitempty"`
	Title string `xml:"title,attr,omitempty"`
}

// entry is an Atom <entry> — represents one catalog navigation link or one
// book in an acquisition feed.
type entry struct {
	XMLName    xml.Name   `xml:"entry"`
	ID         string     `xml:"id"`
	Title      string     `xml:"title"`
	Updated    string     `xml:"updated"`
	Published  string     `xml:"published,omitempty"`
	Authors    []author   `xml:"author,omitempty"`
	Summary    *textField `xml:"summary,omitempty"`
	Content    *textField `xml:"content,omitempty"`
	Language   string     `xml:"dc:language,omitempty"`
	Publisher  string     `xml:"dc:publisher,omitempty"`
	Issued     string     `xml:"dc:issued,omitempty"`
	Identifier string     `xml:"dc:identifier,omitempty"`
	Categories []category `xml:"category,omitempty"`
	Links      []link     `xml:"link"`
}

// textField is used for <summary> / <content> — type="text" or "html".
type textField struct {
	Type  string `xml:"type,attr,omitempty"`
	Value string `xml:",chardata"`
}

// category marks a genre/tag on an acquisition entry.
type category struct {
	Term  string `xml:"term,attr"`
	Label string `xml:"label,attr,omitempty"`
}

// openSearchDescription powers the search-link discovery that OPDS clients
// use. Served at /opds/search.xml.
type openSearchDescription struct {
	XMLName       xml.Name `xml:"OpenSearchDescription"`
	Xmlns         string   `xml:"xmlns,attr"`
	ShortName     string   `xml:"ShortName"`
	Description   string   `xml:"Description"`
	URL           osURL    `xml:"Url"`
	InputEncoding string   `xml:"InputEncoding,omitempty"`
}

// osURL is the OpenSearch <Url> template entry.
type osURL struct {
	Type     string `xml:"type,attr"`
	Template string `xml:"template,attr"`
}
