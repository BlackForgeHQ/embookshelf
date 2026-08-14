// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"golang.org/x/text/encoding/htmlindex"

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
	// Unused: the whole document is read and parsed in one bounded pass
	// (fb2MaxBytes caps it below) — there is no long-running or
	// cancelable step for ctx to interrupt.
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
	dec := xml.NewDecoder(bytes.NewReader(raw))
	// FB2 is a Russian-origin format, and windows-1251 is its dominant
	// wild encoding — encoding/xml only decodes UTF-8 and US-ASCII on its
	// own, so anything else declared in the XML prolog needs a
	// CharsetReader or Decode fails with "encoding ... declared but
	// Decoder.CharsetReader is nil".
	dec.CharsetReader = fb2CharsetReader
	if err := dec.Decode(&doc); err != nil {
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

// fb2CharsetReader resolves a non-UTF-8, non-ASCII encoding declared in an
// FB2's XML prolog (windows-1251 chief among them — FB2 is a Russian-origin
// format and windows-1251 is its dominant wild encoding, but koi8-r and
// others do turn up) and wraps input in a decoder that transcodes it to
// UTF-8 as the XML decoder reads. htmlindex covers the WHATWG label set
// (case-insensitive, with the usual aliases like "cp1251" and "win-1251"),
// which is broader than hand-listing the handful of encodings FB2 producers
// actually use. An unrecognized or garbage declared encoding is reported as
// a clean error here rather than panicking or silently misdecoding.
func fb2CharsetReader(charset string, input io.Reader) (io.Reader, error) {
	enc, err := htmlindex.Get(charset)
	if err != nil {
		return nil, fmt.Errorf("fb2: unsupported declared encoding %q: %w", charset, err)
	}
	return enc.NewDecoder().Reader(input), nil
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
// content. ok is false — never an error — for a missing id, invalid
// base64, or content that doesn't sniff as a recognized image, so a bad
// cover reference degrades to no cover instead of failing metadata that's
// otherwise good.
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
		// b.ContentType is an attribute the file's author wrote, not
		// something derived from the image data — trusting it verbatim
		// and serving it back as this cover's Content-Type is a stored
		// XSS primitive (content-type="text/html" plus an HTML payload).
		// Sniff the decoded bytes instead and use that; a declared type
		// that doesn't match a recognized image format degrades to no
		// cover rather than being served at all.
		mime = SniffImageMime(decoded)
		if mime == "" {
			return nil, "", false
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
