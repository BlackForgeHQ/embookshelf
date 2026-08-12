// SPDX-License-Identifier: AGPL-3.0-or-later

package opds

import (
	"bytes"
	"encoding/xml"
	"strconv"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
)

const (
	// instanceID is the URN prefix for ids that need to be globally-unique
	// within this server. Clients only require stability — not globalness —
	// but OPDS feeds customarily use urn: ids.
	instanceID = "urn:embookshelf"
)

// render serializes one OPDS document with the XML declaration prepended
// and pairs it with the content type it must be served under.
func render(doc any, contentType string) (Document, error) {
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return Document{}, err
	}
	var out bytes.Buffer
	out.WriteString(xml.Header)
	out.Write(body)
	return Document{ContentType: contentType, Body: out.Bytes()}, nil
}

// atomTime formats a time.Time for an Atom <updated>/<published>.
func atomTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// bookLinks are the absolute HREFs one book's entry points at. They
// depend on the request's base URL and on whether a cover exists, so the
// catalog resolves them rather than the entry mapping below.
type bookLinks struct {
	Download     string // href for the file
	DownloadMime string // application/epub+zip etc.
	Cover        string // full-size cover URL, optional
	Thumbnail    string // thumbnail URL, optional
}

// bookEntry converts a model.Book into an OPDS acquisition entry.
func bookEntry(b model.Book, l bookLinks) entry {
	e := entry{
		ID:        instanceID + ":book:" + b.ID,
		Title:     b.Title,
		Updated:   atomTime(b.CreatedAt),
		Published: atomTime(b.CreatedAt),
		Publisher: b.Publisher,
	}
	if b.Author != "" {
		e.Authors = []author{{Name: b.Author}}
	}
	if b.Description != "" {
		e.Summary = &textField{Type: "text", Value: b.Description}
	}
	if b.ISBN != "" {
		e.Identifier = "urn:isbn:" + strings.ReplaceAll(b.ISBN, "-", "")
	}
	if b.Year > 0 {
		// A bare yyyy is valid dc:issued per Dublin Core conventions.
		e.Issued = strconv.Itoa(b.Year)
	}
	for _, t := range b.Tags {
		e.Categories = append(e.Categories, category{Term: t, Label: t})
	}

	// Cover links come first so clients find them without scanning.
	if l.Thumbnail != "" {
		e.Links = append(e.Links, link{Rel: relThumbnail, Href: l.Thumbnail, Type: "image/jpeg"})
	}
	if l.Cover != "" {
		e.Links = append(e.Links, link{Rel: relImage, Href: l.Cover, Type: "image/jpeg"})
	}
	if l.Download != "" {
		e.Links = append(e.Links, link{Rel: relAcquisition, Href: l.Download, Type: l.DownloadMime})
	}

	return e
}
