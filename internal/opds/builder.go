// SPDX-License-Identifier: AGPL-3.0-or-later

package opds

import (
	"bytes"
	"encoding/xml"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
)

const (
	// InstanceID is the URN prefix for ids that need to be globally-unique
	// within this server. Clients only require stability — not globalness —
	// but OPDS feeds customarily use urn: ids.
	InstanceID = "urn:embookshelf"
)

// MarshalFeed serializes a Feed with the XML declaration prepended.
func MarshalFeed(f Feed) ([]byte, error) {
	body, err := xml.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteString(xml.Header)
	out.Write(body)
	return out.Bytes(), nil
}

// MarshalOpenSearch serializes the OpenSearch description.
func MarshalOpenSearch(d OpenSearchDescription) ([]byte, error) {
	body, err := xml.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteString(xml.Header)
	out.Write(body)
	return out.Bytes(), nil
}

// NowAtom returns the current time in RFC3339 — what Atom specs call for.
func NowAtom() string { return time.Now().UTC().Format(time.RFC3339) }

// AtomTime formats a time.Time for an Atom <updated>/<published>.
func AtomTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// BookEntry converts a model.Book into an OPDS acquisition Entry. The caller
// supplies the absolute HREFs for the download + cover (these depend on the
// request's base URL + cover availability).
type BookLinks struct {
	Download     string // href for the file
	DownloadMime string // application/epub+zip etc.
	Cover        string // full-size cover URL, optional
	Thumbnail    string // thumbnail URL, optional
}

func BookEntry(b model.Book, l BookLinks) Entry {
	e := Entry{
		ID:        InstanceID + ":book:" + b.ID,
		Title:     b.Title,
		Updated:   AtomTime(b.CreatedAt),
		Published: AtomTime(b.CreatedAt),
		Publisher: b.Publisher,
	}
	if b.Author != "" {
		e.Authors = []Author{{Name: b.Author}}
	}
	if b.Description != "" {
		e.Summary = &TextField{Type: "text", Value: b.Description}
	}
	if b.ISBN != "" {
		e.Identifier = "urn:isbn:" + strings.ReplaceAll(b.ISBN, "-", "")
	}
	if b.Year > 0 {
		e.Issued = formatYear(b.Year)
	}
	for _, t := range b.Tags {
		e.Categories = append(e.Categories, Category{Term: t, Label: t})
	}

	// Cover links come first so clients find them without scanning.
	if l.Thumbnail != "" {
		e.Links = append(e.Links, Link{Rel: RelThumbnail, Href: l.Thumbnail, Type: "image/jpeg"})
	}
	if l.Cover != "" {
		e.Links = append(e.Links, Link{Rel: RelImage, Href: l.Cover, Type: "image/jpeg"})
	}
	if l.Download != "" {
		e.Links = append(e.Links, Link{Rel: RelAcquisition, Href: l.Download, Type: l.DownloadMime})
	}

	return e
}

func formatYear(y int) string {
	// A bare yyyy is valid dc:issued per Dublin Core conventions.
	if y <= 0 {
		return ""
	}
	return intToString(y)
}

func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
