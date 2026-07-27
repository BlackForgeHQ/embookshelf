// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"archive/zip"
	"encoding/xml"
	"path"
	"strings"
)

// ncxMediaType is how an EPUB 2 manifest declares its table of contents.
const ncxMediaType = "application/x-dtbncx+xml"

// tocTitles maps a spine document's archive path to the title the table
// of contents gives it.
//
// Exists because chapter titles live nowhere else. The OPF names files,
// the spine orders them, and neither carries a word a human wrote — so a
// narration without this labels every chapter "Section 4" and the reader's
// chapter drawer becomes useless (ADR-0028 §4).
//
// Both TOC dialects are read, EPUB 3 first: a hybrid book ships both, and
// the nav document is the one its own version considers authoritative.
// Entries are keyed by document, fragment stripped — a TOC pointing at
// ch01.xhtml#part2 still names ch01.xhtml, because boundaries are file
// granular and resolving anchors inside XHTML is a different feature.
// First entry wins, so a chapter split across several TOC rows takes the
// first title rather than the last.
func tocTitles(zr *zip.Reader, base string, items []opfItem) map[string]string {
	out := map[string]string{}
	for _, it := range items {
		var (
			tocPath = path.Join(base, it.Href)
			entries []tocEntry
		)
		switch {
		case hasProperty(it.Properties, "nav"):
			entries = parseNavDocument(zr, tocPath)
		case it.MediaType == ncxMediaType:
			entries = parseNCX(zr, tocPath)
		default:
			continue
		}
		// Hrefs inside a TOC resolve against the TOC's own directory, not
		// the OPF's. They usually coincide; when they do not, resolving
		// against the wrong one silently names every chapter nothing.
		tocDir := path.Dir(tocPath)
		for _, e := range entries {
			key := path.Join(tocDir, stripFragment(e.href))
			if key == "" || e.title == "" {
				continue
			}
			if _, seen := out[key]; !seen {
				out[key] = e.title
			}
		}
	}
	return out
}

// tocEntry is one row of a table of contents, in either dialect.
type tocEntry struct {
	href  string
	title string
}

// hasProperty reports whether a manifest item's space-separated property
// list contains want. Substring matching would make "nav" match
// "cover-nav", which is a real property in the wild.
func hasProperty(properties, want string) bool {
	for _, p := range strings.Fields(properties) {
		if p == want {
			return true
		}
	}
	return false
}

// stripFragment drops the #anchor from a TOC href.
func stripFragment(href string) string {
	if i := strings.IndexByte(href, '#'); i >= 0 {
		return href[:i]
	}
	return href
}

// parseNavDocument reads an EPUB 3 nav document, collecting every anchor
// with its text.
//
// Deliberately not restricted to <nav epub:type="toc">: the namespaced
// attribute is spelled several ways in the wild, and a nav document holds
// almost nothing but its lists. Landmarks and page-lists may contribute
// extra entries, but they point at front matter that narration skips
// anyway, and first-entry-wins keeps the real TOC's titles.
func parseNavDocument(zr *zip.Reader, navPath string) []tocEntry {
	payload, err := readZipFile(zr, navPath)
	if err != nil {
		return nil
	}
	dec := xml.NewDecoder(strings.NewReader(string(payload)))
	// Nav documents carry the same HTML entities the chapters do; without
	// this a single &nbsp; in a chapter title aborts the whole table.
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity

	var (
		out      []tocEntry
		inAnchor bool
		href     string
		text     strings.Builder
	)
	for {
		tok, err := dec.Token()
		if err != nil {
			// Includes io.EOF. A malformed tail still leaves the titles
			// parsed so far, which beats discarding the whole table.
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if strings.EqualFold(t.Name.Local, "a") {
				inAnchor = true
				href = ""
				text.Reset()
				for _, attr := range t.Attr {
					if strings.EqualFold(attr.Name.Local, "href") {
						href = attr.Value
					}
				}
			}
		case xml.EndElement:
			if inAnchor && strings.EqualFold(t.Name.Local, "a") {
				inAnchor = false
				if title := collapseSpace(text.String()); title != "" && href != "" {
					out = append(out, tocEntry{href: href, title: title})
				}
			}
		case xml.CharData:
			if inAnchor {
				text.Write(t)
			}
		}
	}
}

// ncxDoc is the EPUB 2 table of contents. navPoints nest, and the nesting
// is what "> ol > li" is in the nav dialect — flattened here, because a
// sub-chapter still points at a document and the flat map is all a
// narration needs.
type ncxDoc struct {
	NavPoints []ncxNavPoint `xml:"navMap>navPoint"`
}

type ncxNavPoint struct {
	Label struct {
		Text string `xml:"text"`
	} `xml:"navLabel"`
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	Children []ncxNavPoint `xml:"navPoint"`
}

func parseNCX(zr *zip.Reader, ncxPath string) []tocEntry {
	payload, err := readZipFile(zr, ncxPath)
	if err != nil {
		return nil
	}
	var doc ncxDoc
	if err := xml.Unmarshal(payload, &doc); err != nil {
		return nil
	}
	var out []tocEntry
	var walk func([]ncxNavPoint)
	walk = func(points []ncxNavPoint) {
		for _, p := range points {
			if title := collapseSpace(p.Label.Text); title != "" && p.Content.Src != "" {
				out = append(out, tocEntry{href: p.Content.Src, title: title})
			}
			walk(p.Children)
		}
	}
	walk(doc.NavPoints)
	return out
}

// collapseSpace trims and folds internal whitespace runs. TOC markup is
// routinely pretty-printed across lines, and "Chapter\n    One" is not a
// title anyone wants in a chapter drawer.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
