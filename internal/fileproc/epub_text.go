// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"math"
	"path"
	"strings"

	"github.com/blackforge/embookshelf/internal/storage"
)

// ErrNoReadableText is returned when an EPUB contains no document the
// extractor can read. Callers distinguish it from a malformed archive:
// this is a book that simply has nothing to summarise, and the reading
// guide falls back to a metadata-only source (ADR-0024 §2).
var ErrNoReadableText = errors.New("epub has no readable text")

// opfSpine is the reading order. Deliberately not part of opfPackage in
// epub.go — that type is the metadata read path, and adding a field there
// would make every metadata extraction parse the spine too.
type opfSpineDoc struct {
	Manifest opfMani `xml:"manifest"`
	Spine    struct {
		ItemRefs []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
	// Guide is the EPUB 2 declaration of which documents are front
	// matter. Unused by the reading-guide text path, which wants every
	// word; narration uses it to avoid reading the copyright page aloud.
	Guide struct {
		References []struct {
			Type string `xml:"type,attr"`
			Href string `xml:"href,attr"`
		} `xml:"reference"`
	} `xml:"guide"`
}

// textualMediaTypes are the spine document types worth reading. A spine
// should only reference these, but manifests carry stylesheets and images
// and a malformed spine can point at one.
var textualMediaTypes = map[string]bool{
	"application/xhtml+xml": true,
	"text/html":             true,
	"application/xml":       true,
	"text/xml":              true,
}

// ExtractEPUBText returns the book's prose in spine order, with markup,
// scripts and stylesheets removed.
//
// limit caps the returned text; the second result reports whether it bound.
// The cap matters because the caller pays per token and an EPUB can be
// hundreds of megabytes — a truncated read still produces a usable guide,
// but the caller needs to know the model saw only part of the book.
//
// Returns ErrNoReadableText when the spine yields nothing.
func ExtractEPUBText(ctx context.Context, src storage.Source, limit int64) (string, bool, error) {
	text, truncated, _, err := walkEPUBText(ctx, src, limit)
	return text, truncated, err
}

// OutlineEntry is one chapter of a book's table of contents, located in
// the book's extracted text.
type OutlineEntry struct {
	Title string
	// Offset is where the chapter's text starts in ExtractEPUBText's
	// output, in bytes — the same coordinates paging uses (ADR-0039 §6).
	Offset int64
}

// ExtractEPUBOutline returns the book's chapters in reading order, each
// with its start offset in ExtractEPUBText's output.
//
// Titles come from the table of contents (tocTitles). Granularity is the
// spine document, like narration's chapters: a TOC row pointing inside a
// document names the document. Documents the TOC does not name are left
// out — books routinely split one chapter across several files — unless
// the TOC names none at all, when every document is listed as "Section N"
// so a book without a TOC still has a map.
//
// Offsets come from the same walk that produces the text, so they cannot
// disagree with it. Returns ErrNoReadableText when the spine yields
// nothing.
func ExtractEPUBOutline(ctx context.Context, src storage.Source) ([]OutlineEntry, error) {
	_, _, docs, err := walkEPUBText(ctx, src, math.MaxInt64)
	if err != nil {
		return nil, err
	}
	var titled, all []OutlineEntry
	for i, d := range docs {
		all = append(all, OutlineEntry{Title: fmt.Sprintf("Section %d", i+1), Offset: d.start})
		if d.title != "" {
			titled = append(titled, OutlineEntry{Title: d.title, Offset: d.start})
		}
	}
	if len(titled) > 0 {
		return titled, nil
	}
	return all, nil
}

// textDoc is one spine document that contributed text: its TOC title,
// if any, and where its text starts in the walk's output.
type textDoc struct {
	title string
	start int64
}

// walkEPUBText is ExtractEPUBText's body, also reporting where each
// contributing document starts — the one walk both the text and the
// outline come from.
func walkEPUBText(ctx context.Context, src storage.Source, limit int64) (string, bool, []textDoc, error) {
	zr, err := zip.NewReader(src, src.Size())
	if err != nil {
		return "", false, nil, fmt.Errorf("open epub: %w", err)
	}
	opfPath, err := rootfilePath(zr)
	if err != nil {
		return "", false, nil, err
	}
	raw, err := readZipFile(zr, opfPath)
	if err != nil {
		return "", false, nil, fmt.Errorf("read opf: %w", err)
	}
	var doc opfSpineDoc
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return "", false, nil, fmt.Errorf("parse opf: %w", err)
	}

	byID := make(map[string]opfItem, len(doc.Manifest.Items))
	for _, it := range doc.Manifest.Items {
		byID[it.ID] = it
	}
	// Hrefs are relative to the OPF's own directory, not the archive root.
	// Most EPUBs nest their OPF, so resolving against the root instead
	// makes every chapter unreadable.
	base := path.Dir(opfPath)
	titles := tocTitles(zr, base, doc.Manifest.Items)

	var (
		b         strings.Builder
		docs      []textDoc
		truncated bool
	)
	for _, ref := range doc.Spine.ItemRefs {
		if err := ctx.Err(); err != nil {
			return "", false, nil, err
		}
		item, ok := byID[ref.IDRef]
		if !ok || !textualMediaTypes[item.MediaType] {
			continue
		}
		docPath := path.Join(base, item.Href)
		payload, err := readZipFile(zr, docPath)
		if err != nil {
			// A spine entry pointing at a missing file is a broken book,
			// not a broken reader — take what the rest of it gives.
			continue
		}
		before := int64(b.Len())
		bound := appendDocumentText(&b, payload, limit)
		if int64(b.Len()) > before {
			// The separator, when there is one, belongs before the
			// document: its text starts after it.
			start := before
			if before > 0 {
				start++
			}
			docs = append(docs, textDoc{title: titles[docPath], start: start})
		}
		if bound {
			truncated = true
			break
		}
	}

	text := strings.TrimSpace(b.String())
	if text == "" {
		return "", false, nil, ErrNoReadableText
	}
	return text, truncated, docs, nil
}

// appendDocumentText writes one document's character data into b, skipping
// script and style bodies. Reports whether the limit bound.
func appendDocumentText(b *strings.Builder, payload []byte, limit int64) bool {
	dec := xml.NewDecoder(strings.NewReader(string(payload)))
	// EPUB documents reference HTML entity sets the Go decoder does not
	// know; without this a single &nbsp; aborts the whole chapter.
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity

	skipDepth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			// Includes io.EOF. A malformed tail still leaves usable prose
			// in b, which beats discarding the chapter.
			return false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch strings.ToLower(t.Name.Local) {
			case "script", "style", "head":
				skipDepth++
			default:
				if skipDepth > 0 {
					skipDepth++
				}
			}
		case xml.EndElement:
			if skipDepth > 0 {
				skipDepth--
			}
		case xml.CharData:
			if skipDepth > 0 {
				continue
			}
			s := strings.TrimSpace(string(t))
			if s == "" {
				continue
			}
			// The separator is written on both paths, so the text a
			// smaller limit yields is always a prefix of what a larger
			// one yields — paging by offset (ADR-0039 §6) depends on it.
			// The fill path used to drop it, gluing two documents
			// together at the cut and shifting every later offset.
			if b.Len() > 0 {
				if int64(b.Len())+1 > limit {
					return true
				}
				b.WriteByte(' ')
			}
			if int64(b.Len())+int64(len(s)) > limit {
				// Fill what remains so the cap binds exactly, then stop.
				if room := limit - int64(b.Len()); room > 0 {
					b.WriteString(s[:room])
				}
				return true
			}
			b.WriteString(s)
		}
	}
}
