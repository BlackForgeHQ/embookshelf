// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"path"
	"strings"

	"github.com/blackforge/embookshelf/internal/storage"
)

// DefaultSegmentChars caps one unit of synthesis at roughly 45 minutes of
// audio. The cap is what keeps a chapter job inside River's rescue window
// and keeps the cost of a retry to cents rather than dollars (ADR-0028 §3).
const DefaultSegmentChars = 40_000

// Segment is one unit of synthesis: the text of one job, plus where it
// belongs in the finished audiobook.
//
// ChapterIndex is not Seq. A long chapter is split across several
// segments that share a title and a chapter index, because the reader's
// chapter drawer should show the chapter the author wrote rather than the
// pieces the engine's request cap forced on us.
type Segment struct {
	Seq          int
	ChapterIndex int
	ChapterTitle string
	Text         string
}

// SegmentOptions tunes the split. The zero value is the intended
// configuration; fields exist so a test can force a small cap.
type SegmentOptions struct {
	// MaxChars bounds one segment. Zero means DefaultSegmentChars.
	MaxChars int
}

func (o SegmentOptions) maxChars() int {
	if o.MaxChars > 0 {
		return o.MaxChars
	}
	return DefaultSegmentChars
}

// ExtractEPUBSegments splits a book into the units a narration job works
// on: one per spine document, titled from the table of contents, further
// split when a document exceeds the character cap.
//
// Deliberately separate from ExtractEPUBText, which flattens the whole
// book into one string for the reading-guide prompt. That flattening is
// exactly what narration cannot use — the spine boundaries it discards
// are the chapter marks (ADR-0028 §4).
//
// Returns ErrNoReadableText when nothing in the spine yields prose.
func ExtractEPUBSegments(ctx context.Context, src storage.Source, opts SegmentOptions) ([]Segment, error) {
	zr, err := zip.NewReader(src, src.Size())
	if err != nil {
		return nil, fmt.Errorf("open epub: %w", err)
	}
	opfPath, err := rootfilePath(zr)
	if err != nil {
		return nil, err
	}
	raw, err := readZipFile(zr, opfPath)
	if err != nil {
		return nil, fmt.Errorf("read opf: %w", err)
	}
	var doc opfSpineDoc
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse opf: %w", err)
	}

	byID := make(map[string]opfItem, len(doc.Manifest.Items))
	for _, it := range doc.Manifest.Items {
		byID[it.ID] = it
	}
	base := path.Dir(opfPath)
	titles := tocTitles(zr, base, doc.Manifest.Items)
	skip := frontMatterPaths(base, doc)

	var segs []Segment
	chapterIndex := 0
	for _, ref := range doc.Spine.ItemRefs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item, ok := byID[ref.IDRef]
		if !ok || !textualMediaTypes[item.MediaType] {
			continue
		}
		docPath := path.Join(base, item.Href)
		if skip[docPath] {
			continue
		}
		payload, rerr := readZipFile(zr, docPath)
		if rerr != nil {
			// A spine entry pointing at a missing file is a broken book,
			// not a broken reader — narrate the rest of it.
			continue
		}

		var b strings.Builder
		appendDocumentText(&b, payload, int64(1)<<62)
		text := strings.TrimSpace(b.String())
		if text == "" {
			continue
		}

		title := titles[docPath]
		if title == "" {
			// Plenty of books leave documents out of the table of contents.
			// The drawer still needs a stable label, numbered by position
			// among the documents that made it in rather than by spine
			// index, so skipped front matter does not leave a gap.
			title = fmt.Sprintf("Section %d", chapterIndex+1)
		}

		for _, chunk := range SplitForSynthesis(text, opts.maxChars()) {
			segs = append(segs, Segment{
				Seq:          len(segs),
				ChapterIndex: chapterIndex,
				ChapterTitle: title,
				Text:         chunk,
			})
		}
		chapterIndex++
	}

	if len(segs) == 0 {
		return nil, ErrNoReadableText
	}
	return segs, nil
}

// frontMatterTypes are the EPUB 2 guide reference types that name a
// document nobody wants read aloud. Deliberately short: a guide also
// names the dedication, the epigraph and the foreword, and those are
// prose the author wrote.
var frontMatterTypes = map[string]bool{
	"cover":          true,
	"toc":            true,
	"copyright-page": true,
	"title-page":     true,
	"titlepage":      true,
	"colophon":       true,
}

// frontMatterNames are filename stems that mean the same thing in books
// with no guide element. A heuristic, and kept conservative — skipping a
// real chapter is a worse failure than narrating a title page.
var frontMatterNames = map[string]bool{
	"cover":      true,
	"toc":        true,
	"nav":        true,
	"contents":   true,
	"copyright":  true,
	"colophon":   true,
	"titlepage":  true,
	"title-page": true,
	"title_page": true,
}

// frontMatterPaths collects the archive paths narration must skip.
//
// Three signals, most reliable first: the nav property (EPUB 3 says this
// document *is* the table of contents), the guide element (EPUB 2 says
// what each document is for), and the filename. Synthesizing a read-aloud
// table of contents is worse than useless and cheap to detect (ADR-0028
// §4).
func frontMatterPaths(base string, doc opfSpineDoc) map[string]bool {
	skip := map[string]bool{}
	for _, it := range doc.Manifest.Items {
		p := path.Join(base, it.Href)
		if hasProperty(it.Properties, "nav") {
			skip[p] = true
			continue
		}
		stem := strings.ToLower(strings.TrimSuffix(path.Base(it.Href), path.Ext(it.Href)))
		if frontMatterNames[stem] {
			skip[p] = true
		}
	}
	for _, ref := range doc.Guide.References {
		if frontMatterTypes[strings.ToLower(strings.TrimSpace(ref.Type))] {
			skip[path.Join(base, stripFragment(ref.Href))] = true
		}
	}
	return skip
}

// SplitForSynthesis cuts text into pieces of at most maxChars characters.
//
// Splits land on sentence boundaries. A mid-sentence cut is audible at
// every seam, and a book is ~180 of them — the single most noticeable
// artifact of chunked synthesis (ADR-0028 §8).
//
// Works in runes, not bytes, for two reasons that both bite immediately
// on non-Latin text: a byte index can land inside a multi-byte rune and
// hand the engine invalid UTF-8, and every engine's per-request cap is
// quoted in characters, so a byte budget under-fills it by up to 3×.
func SplitForSynthesis(text string, maxChars int) []string {
	runes := []rune(text)
	if len(runes) <= maxChars {
		return []string{text}
	}
	var out []string
	for len(runes) > maxChars {
		cut := sentenceBoundaryBefore(runes, maxChars)
		out = append(out, strings.TrimSpace(string(runes[:cut])))
		runes = []rune(strings.TrimSpace(string(runes[cut:])))
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}

// sentenceEnders are the characters a sentence can end on, including the
// CJK full stop — an engine narrating Japanese needs the same courtesy.
const sentenceEnders = ".!?。！？"

// sentenceBoundaryBefore finds the largest cut point at or before limit
// that ends a sentence. Falls back to a word boundary, then to limit
// itself, so a text with no punctuation at all still terminates.
//
// Indices are rune offsets into runes, never byte offsets.
func sentenceBoundaryBefore(runes []rune, limit int) int {
	if limit >= len(runes) {
		return len(runes)
	}
	// Look back over a window rather than the whole prefix: a segment
	// that ends 30k characters early because the last full stop was far
	// back would waste most of the cap.
	window := limit / 4
	for i := limit; i > limit-window && i > 0; i-- {
		if strings.ContainsRune(sentenceEnders, runes[i-1]) {
			return i
		}
	}
	// CJK prose has no spaces, so this second pass finds nothing there —
	// which is fine, the full stop above is the boundary that matters.
	for i := limit; i > limit-window && i > 0; i-- {
		if runes[i-1] == ' ' || runes[i-1] == '\n' {
			return i
		}
	}
	return limit
}
