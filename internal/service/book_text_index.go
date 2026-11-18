// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
)

// maxWholeBookBytes bounds the reads that need the whole book at once —
// search and the PDF outline. Far past any novel (a 1,000-page book
// extracts to ~3 MB); a book past it is searched up to here and says so.
const maxWholeBookBytes = 32 << 20

// ErrEmptyTextQuery is a search with nothing to look for.
var ErrEmptyTextQuery = errors.New("search query is empty")

// BookOutlineEntry is one heading of a book's table of contents, located
// in the coordinates Page uses.
type BookOutlineEntry struct {
	Title string
	// Level is the heading depth (1 for #, 2 for ##) where the source
	// has one — Markdown renditions. EPUB tables of contents are read
	// flat (fileproc.tocTitles), so their entries are 0.
	Level  int
	Offset int64
}

// Outline returns the book's table of contents with each entry's offset
// into the text Page serves, so a client can jump straight to a chapter.
//
// EPUB: the TOC's titles at spine-document granularity
// (fileproc.ExtractEPUBOutline). Convertible: the Markdown rendition's ATX
// headings. Same errors as Read.
func (t *BookText) Outline(ctx context.Context, book model.Book) ([]BookOutlineEntry, error) {
	switch {
	case strings.EqualFold(book.Format, "EPUB"):
		src, err := t.Books.Open(ctx, book)
		if err != nil {
			return nil, fmt.Errorf("open book: %w", err)
		}
		defer func() { _ = src.Close() }()
		entries, err := fileproc.ExtractEPUBOutline(ctx, src)
		if err != nil {
			return nil, err
		}
		out := make([]BookOutlineEntry, 0, len(entries))
		for _, e := range entries {
			out = append(out, BookOutlineEntry{Title: e.Title, Offset: e.Offset})
		}
		return out, nil

	case t.Markdown != nil && model.Convertible(book.Format):
		text, err := t.Markdown.Text(ctx, book, maxWholeBookBytes)
		if err != nil {
			return nil, err
		}
		return markdownOutline(text), nil
	}
	return nil, ErrNoBookText
}

// markdownOutline collects ATX headings ("## Title"), skipping fenced
// code blocks, where a line starting with # is a comment, not a heading.
// Setext headings (underlined with === or ---) are not read: converters
// emit ATX, and "---" is far more often a rule than an underline.
func markdownOutline(text string) []BookOutlineEntry {
	var (
		out    []BookOutlineEntry
		fence  string
		offset int64
	)
	for line := range strings.SplitAfterSeq(text, "\n") {
		start := offset
		offset += int64(len(line))

		trimmed := strings.TrimRight(line, "\r\n")
		indented := strings.TrimLeft(trimmed, " ")
		if len(trimmed)-len(indented) > 3 {
			continue // indented code
		}
		if f := fenceMarker(indented); f != "" {
			switch {
			case fence == "":
				fence = f
			case strings.HasPrefix(f, fence):
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		level := 0
		for level < len(indented) && indented[level] == '#' {
			level++
		}
		if level == 0 || level > 6 {
			continue
		}
		rest := indented[level:]
		if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
			continue // "#hashtag", not a heading
		}
		title := strings.TrimSpace(rest)
		// An optional closing run of #s, set off by a space, is
		// decoration: "## Title ##" is "Title", "## C#" is "C#".
		if bare := strings.TrimRight(title, "#"); bare != title &&
			(bare == "" || strings.HasSuffix(bare, " ") || strings.HasSuffix(bare, "\t")) {
			title = strings.TrimSpace(bare)
		}
		if title == "" {
			continue
		}
		out = append(out, BookOutlineEntry{Title: title, Level: level, Offset: start})
	}
	return out
}

// fenceMarker reports the fence a line opens or closes (``` or ~~~, three
// or more), or "".
func fenceMarker(line string) string {
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(line) && line[n] == c {
			n++
		}
		if n >= 3 {
			return line[:n]
		}
	}
	return ""
}

// TextMatch is one place a search found its query.
type TextMatch struct {
	// Offset is where the match starts, in Page's coordinates — pass it
	// (or a little before it) to read the passage.
	Offset int64
	// Snippet is the match with some text either side, cut on
	// character boundaries, with … where it was cut.
	Snippet string
}

// TextSearchResult is a search's answer.
type TextSearchResult struct {
	Matches []TextMatch
	// Total is how many matches the book has, up to maxCountedMatches.
	Total int
	// TotalCapped reports that counting stopped at maxCountedMatches.
	TotalCapped bool
	// Partial reports that the book is longer than maxWholeBookBytes and
	// only its beginning was searched.
	Partial bool
}

const (
	maxCountedMatches = 10_000
	snippetContext    = 160
)

// Search finds query in the book's text and returns up to maxMatches
// matches in reading order.
//
// The query's words match in order, case-insensitively, with any run of
// whitespace between them — a phrase search that survives line breaks
// and the extractor's spacing. It matches substrings, unstemmed and
// unranked: "spice" finds "spices" but not "spicy", and every match
// counts the same.
func (t *BookText) Search(ctx context.Context, book model.Book, query string, maxMatches int) (TextSearchResult, error) {
	words := strings.Fields(query)
	if len(words) == 0 {
		return TextSearchResult{}, ErrEmptyTextQuery
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = regexp.QuoteMeta(w)
	}
	re, err := regexp.Compile(`(?i)` + strings.Join(quoted, `\s+`))
	if err != nil {
		return TextSearchResult{}, fmt.Errorf("compile search: %w", err)
	}

	text, more, err := t.Read(ctx, book, maxWholeBookBytes)
	if err != nil {
		return TextSearchResult{}, err
	}
	locs := re.FindAllStringIndex(text, maxCountedMatches)
	res := TextSearchResult{
		Total:       len(locs),
		TotalCapped: len(locs) == maxCountedMatches,
		Partial:     more,
		Matches:     make([]TextMatch, 0, min(len(locs), maxMatches)),
	}
	for _, loc := range locs[:min(len(locs), maxMatches)] {
		res.Matches = append(res.Matches, TextMatch{
			Offset:  int64(loc[0]),
			Snippet: snippet(text, loc[0], loc[1]),
		})
	}
	return res, nil
}

// snippet returns text[from:to] with up to snippetContext bytes either
// side, both edges on character boundaries.
func snippet(text string, from, to int) string {
	start := max(from-snippetContext, 0)
	for start > 0 && !utf8.RuneStart(text[start]) {
		start++
	}
	end := min(to+snippetContext, len(text))
	for end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	s := strings.TrimSpace(text[start:end])
	if start > 0 {
		s = "…" + s
	}
	if end < len(text) {
		s += "…"
	}
	return s
}
