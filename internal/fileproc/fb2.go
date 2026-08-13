// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/blackforge/embookshelf/internal/storage"
)

// FB2Processor extracts metadata and cover image from a FictionBook 2.0
// file — a single XML document, not an archive.
//
// The whole book (text included) is one <FictionBook> element: a
// <description><title-info> block carries book-title/author/annotation/
// genre/lang, and the cover — when present — is a base64 <binary> sibling
// of <body>, referenced by id from <title-info><coverpage><image
// xlink:href="#id"/>. FB2's default namespace and the xlink prefix on
// that attribute are both irrelevant to Go's decoder: struct tags here
// name only local names, and encoding/xml matches those regardless of
// namespace, so the same struct fields read documents that declare no
// namespace at all (common in the wild) and ones that declare both.
type FB2Processor struct{}

// fb2MaxBytes bounds how much of a single .fb2 this processor will
// buffer in memory. FB2 is one XML document — unlike PDF (pdf.go's 1 MB
// head + 256 KB tail windows), there's no partial-read shape that still
// parses, so the whole file has to be read to extract anything. The
// HTTP upload path already caps a single BookDrop upload at 1 GiB
// (handler/bookdrop.go's maxUploadBytes), but the filesystem-watcher
// intake path has no such cap, so without one here a large dropped
// file would be slurped whole with no limit. 64 MiB is comfortably
// above any real FictionBook — even a sizeable collection with an
// embedded cover rarely clears a few MB of text plus a couple more of
// base64 image data — while still bounding worst-case memory for a
// format this processor cannot stream a window of.
const fb2MaxBytes int64 = 64 << 20 // 64 MiB

type fb2Document struct {
	XMLName     xml.Name       `xml:"FictionBook"`
	Description fb2Description `xml:"description"`
	Binaries    []fb2Binary    `xml:"binary"`
}

type fb2Description struct {
	TitleInfo *fb2TitleInfo `xml:"title-info"`
}

type fb2TitleInfo struct {
	Genres     []string       `xml:"genre"`
	Authors    []fb2Author    `xml:"author"`
	BookTitle  string         `xml:"book-title"`
	Annotation *fb2Annotation `xml:"annotation"`
	Lang       string         `xml:"lang"`
	Coverpage  *fb2Coverpage  `xml:"coverpage"`
}

type fb2Author struct {
	FirstName  string `xml:"first-name"`
	MiddleName string `xml:"middle-name"`
	LastName   string `xml:"last-name"`
	Nickname   string `xml:"nickname"`
}

// fb2Annotation captures only direct <p> text. Real-world annotations
// occasionally nest inline markup (<emphasis>, <strong>) inside a <p>;
// this drops that markup's text rather than trying to render it, which
// is the same tradeoff CBZ's ComicInfo Summary and EPUB's Dublin Core
// description make — the reader shell is not consulted for this field.
type fb2Annotation struct {
	P []string `xml:"p"`
}

type fb2Coverpage struct {
	Image fb2Image `xml:"image"`
}

type fb2Image struct {
	Href string `xml:"href,attr"`
}

type fb2Binary struct {
	ID          string `xml:"id,attr"`
	ContentType string `xml:"content-type,attr"`
	Data        string `xml:",chardata"`
}

func (FB2Processor) Extract(ctx context.Context, src storage.Source) (Metadata, error) {
	_ = ctx

	f := io.NewSectionReader(src, 0, src.Size())
	// Read one byte past the cap so an over-cap file is distinguished
	// from one that lands exactly on it, then fail with a clear error
	// instead of silently parsing a truncated document.
	lr := &io.LimitedReader{R: f, N: fb2MaxBytes + 1}
	raw, err := io.ReadAll(lr)
	if err != nil {
		return Metadata{}, fmt.Errorf("read fb2: %w", err)
	}
	if int64(len(raw)) > fb2MaxBytes {
		return Metadata{}, fmt.Errorf("fb2: file exceeds the %d byte processing cap", fb2MaxBytes)
	}

	var doc fb2Document
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return Metadata{}, fmt.Errorf("parse fb2: %w", err)
	}

	ti := doc.Description.TitleInfo
	if ti == nil {
		return Metadata{}, fmt.Errorf("fb2: no title-info in description")
	}

	m := Metadata{Format: "FB2"}
	m.Title = strings.TrimSpace(ti.BookTitle)
	m.Language = strings.TrimSpace(ti.Lang)

	if len(ti.Authors) > 0 {
		m.Author = fb2AuthorName(ti.Authors[0])
	}

	m.Description = fb2AnnotationText(ti)

	// Cover extraction is best-effort, like EPUB's: a coverpage that
	// points at a missing or malformed binary never fails the whole
	// extraction, since the title/author/annotation are still good.
	if href := fb2CoverHref(ti); href != "" {
		if b, mime, ok := fb2ReadCover(doc.Binaries, href); ok {
			m.HasCover = true
			m.CoverBytes = b
			m.CoverMime = mime
		}
	}

	return m, nil
}

// fb2AuthorName assembles "First Middle Last", falling back to the
// nickname when none of the name parts are present. Only the first
// <author> in title-info feeds Metadata.Author — a single string field,
// the same choice EPUB (first <dc:creator>) and CBZ (Writer) make.
func fb2AuthorName(a fb2Author) string {
	parts := make([]string, 0, 3)
	for _, p := range []string{a.FirstName, a.MiddleName, a.LastName} {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, " ")
	}
	return strings.TrimSpace(a.Nickname)
}

// fb2AnnotationText joins the annotation's paragraphs into Metadata's
// Description field. title-info's <genre> elements are parsed (so a
// document that has them doesn't fail) but intentionally not persisted
// anywhere: Metadata/ExtractResult/BookDropItem and the bookdrop_items
// schema have no genre slot on the BookDrop path for any format, and
// folding them into another field would be inventing storage for data
// this ingest path doesn't otherwise carry. A first-class genre/tags
// field is left to a future issue.
func fb2AnnotationText(ti *fb2TitleInfo) string {
	if ti.Annotation == nil {
		return ""
	}
	var paras []string
	for _, p := range ti.Annotation.P {
		if p = strings.TrimSpace(p); p != "" {
			paras = append(paras, p)
		}
	}
	return strings.Join(paras, "\n\n")
}

// fb2CoverHref returns the binary id referenced by <coverpage><image
// xlink:href="#id"/>, with the leading '#' stripped. Empty when there is
// no coverpage.
func fb2CoverHref(ti *fb2TitleInfo) string {
	if ti.Coverpage == nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(ti.Coverpage.Image.Href), "#")
}

// fb2ReadCover finds the <binary> matching id and base64-decodes its
// content. ok is false — never an error — for a missing id or invalid
// base64, so a bad cover reference degrades to no cover instead of
// failing metadata that's otherwise good.
func fb2ReadCover(binaries []fb2Binary, id string) (data []byte, mime string, ok bool) {
	for _, b := range binaries {
		if b.ID != id {
			continue
		}
		// FB2 producers routinely wrap the base64 body at a fixed column,
		// which StdEncoding's decoder rejects outright if fed as-is —
		// strip all whitespace first rather than only the ends.
		decoded, err := base64.StdEncoding.DecodeString(stripWhitespace(b.Data))
		if err != nil {
			return nil, "", false
		}
		mime = strings.TrimSpace(b.ContentType)
		if mime == "" {
			mime = "application/octet-stream"
		}
		return decoded, mime, true
	}
	return nil, "", false
}

func stripWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, s)
}
