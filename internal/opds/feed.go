// SPDX-License-Identifier: AGPL-3.0-or-later

// Package opds builds OPDS 1.2 (Atom/XML) catalog and acquisition feeds.
// Namespaces are declared manually on the root <feed> to sidestep Go's
// encoding/xml namespace handling — the Local tags `xml:"dc:language"` are
// emitted as-is and the xmlns:dc attribute makes them valid.
package opds

import "encoding/xml"

// Namespace URIs.
const (
	NamespaceAtom = "http://www.w3.org/2005/Atom"
	NamespaceOPDS = "http://opds-spec.org/2010/catalog"
	NamespaceDC   = "http://purl.org/dc/elements/1.1/"
)

// Content types per OPDS 1.2.
const (
	MimeNavigation  = "application/atom+xml;profile=opds-catalog;kind=navigation"
	MimeAcquisition = "application/atom+xml;profile=opds-catalog;kind=acquisition"
	MimeOpenSearch  = "application/opensearchdescription+xml"
)

// Standard link rel values.
const (
	RelSelf        = "self"
	RelStart       = "start"
	RelUp          = "up"
	RelSearch      = "search"
	RelSubsection  = "subsection"
	RelNext        = "next"
	RelPrevious    = "previous"
	RelAcquisition = "http://opds-spec.org/acquisition"
	RelImage       = "http://opds-spec.org/image"
	RelThumbnail   = "http://opds-spec.org/image/thumbnail"
)

// Feed is the Atom root. XmlnsOpds/XmlnsDC are set on feeds that use those
// namespaces (acquisition feeds), left blank on pure-nav feeds.
type Feed struct {
	XMLName   xml.Name `xml:"feed"`
	Xmlns     string   `xml:"xmlns,attr"`
	XmlnsOpds string   `xml:"xmlns:opds,attr,omitempty"`
	XmlnsDC   string   `xml:"xmlns:dc,attr,omitempty"`

	ID       string  `xml:"id"`
	Title    string  `xml:"title"`
	Subtitle string  `xml:"subtitle,omitempty"`
	Updated  string  `xml:"updated"`
	Author   *Author `xml:"author,omitempty"`
	Links    []Link  `xml:"link"`
	Entries  []Entry `xml:"entry"`
}

// Author on a Feed or Entry.
type Author struct {
	Name string `xml:"name"`
	URI  string `xml:"uri,omitempty"`
}

// Link is an Atom <link>.
type Link struct {
	Rel   string `xml:"rel,attr"`
	Href  string `xml:"href,attr"`
	Type  string `xml:"type,attr,omitempty"`
	Title string `xml:"title,attr,omitempty"`
}

// Entry is an Atom <entry> — represents one catalog navigation link or one
// book in an acquisition feed.
type Entry struct {
	XMLName    xml.Name   `xml:"entry"`
	ID         string     `xml:"id"`
	Title      string     `xml:"title"`
	Updated    string     `xml:"updated"`
	Published  string     `xml:"published,omitempty"`
	Authors    []Author   `xml:"author,omitempty"`
	Summary    *TextField `xml:"summary,omitempty"`
	Content    *TextField `xml:"content,omitempty"`
	Language   string     `xml:"dc:language,omitempty"`
	Publisher  string     `xml:"dc:publisher,omitempty"`
	Issued     string     `xml:"dc:issued,omitempty"`
	Identifier string     `xml:"dc:identifier,omitempty"`
	Categories []Category `xml:"category,omitempty"`
	Links      []Link     `xml:"link"`
}

// TextField is used for <summary> / <content> — type="text" or "html".
type TextField struct {
	Type  string `xml:"type,attr,omitempty"`
	Value string `xml:",chardata"`
}

// Category marks a genre/tag on an acquisition entry.
type Category struct {
	Term  string `xml:"term,attr"`
	Label string `xml:"label,attr,omitempty"`
}

// OpenSearchDescription powers the search-link discovery that OPDS clients
// use. Served at /opds/search.xml.
type OpenSearchDescription struct {
	XMLName       xml.Name `xml:"OpenSearchDescription"`
	Xmlns         string   `xml:"xmlns,attr"`
	ShortName     string   `xml:"ShortName"`
	Description   string   `xml:"Description"`
	URL           OSURL    `xml:"Url"`
	InputEncoding string   `xml:"InputEncoding,omitempty"`
}

// OSURL is the OpenSearch <Url> template entry.
type OSURL struct {
	Type     string `xml:"type,attr"`
	Template string `xml:"template,attr"`
}
