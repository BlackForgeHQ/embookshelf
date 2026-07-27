// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
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
	zr, err := zip.NewReader(src, src.Size())
	if err != nil {
		return "", false, fmt.Errorf("open epub: %w", err)
	}
	opfPath, err := rootfilePath(zr)
	if err != nil {
		return "", false, err
	}
	raw, err := readZipFile(zr, opfPath)
	if err != nil {
		return "", false, fmt.Errorf("read opf: %w", err)
	}
	var doc opfSpineDoc
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return "", false, fmt.Errorf("parse opf: %w", err)
	}

	byID := make(map[string]opfItem, len(doc.Manifest.Items))
	for _, it := range doc.Manifest.Items {
		byID[it.ID] = it
	}
	// Hrefs are relative to the OPF's own directory, not the archive root.
	// Most EPUBs nest their OPF, so resolving against the root instead
	// makes every chapter unreadable.
	base := path.Dir(opfPath)

	var b strings.Builder
	truncated := false
	for _, ref := range doc.Spine.ItemRefs {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		item, ok := byID[ref.IDRef]
		if !ok || !textualMediaTypes[item.MediaType] {
			continue
		}
		payload, err := readZipFile(zr, path.Join(base, item.Href))
		if err != nil {
			// A spine entry pointing at a missing file is a broken book,
			// not a broken reader — take what the rest of it gives.
			continue
		}
		if appendDocumentText(&b, payload, limit) {
			truncated = true
			break
		}
	}

	text := strings.TrimSpace(b.String())
	if text == "" {
		return "", false, ErrNoReadableText
	}
	return text, truncated, nil
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
			if int64(b.Len())+int64(len(s))+1 > limit {
				// Fill what remains so the cap binds exactly, then stop.
				if room := limit - int64(b.Len()); room > 0 {
					b.WriteString(s[:room])
				}
				return true
			}
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(s)
		}
	}
}
