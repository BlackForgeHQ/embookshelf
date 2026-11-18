// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
)

// ErrNoBookText is the refusal for a format with no text path: comics
// would need OCR, audio transcription, and a Convertible format with no
// converter wired is where it was before ADR-0033. Not attempted rather
// than attempted-and-failed — opening the bytes to rediscover that would
// cost a full download per book on an S3-backed library.
var ErrNoBookText = errors.New("no readable text for this format")

// BookText reads a book's prose: an EPUB from its own spine, a
// Convertible format (PDF) from its Markdown rendition (ADR-0033). The one
// statement of "where does this book's text come from", shared by reading
// guides and the MCP server (ADR-0039 §5) so the two cannot disagree.
//
// It reports failures; it does not decide what they mean. The guide turns
// an unreadable EPUB into a metadata-only guide, while MCP tells the model
// it could not read the book — the same error, two honest answers.
type BookText struct {
	Books bookSourceOpener
	// Markdown may be nil: no converter wired, so a Convertible format
	// answers ErrNoBookText like any other textless format.
	Markdown *MarkdownFeed
}

// Read returns up to limit bytes of the book's text from the start, cut
// on a UTF-8 boundary, and whether more text follows.
//
// Errors:
//   - ErrNoBookText for a format with no text path;
//   - fileproc.ErrNoReadableText for an EPUB whose spine yields nothing;
//   - ErrRenditionPending while a Markdown rendition is converting —
//     including one this call just requested, which is the feed's
//     loud-failure rule (ADR-0033 §5);
//   - *RenditionFailedError carrying the conversion's own message;
//   - anything else is the storage or database failing.
func (t *BookText) Read(ctx context.Context, book model.Book, limit int64) (string, bool, error) {
	if limit <= 0 {
		return "", false, errors.New("book text: limit must be positive")
	}
	// One byte past the limit is how a reader that only caps can still
	// tell "exactly this long" from "longer".
	probe := limit + 1

	switch {
	case strings.EqualFold(book.Format, "EPUB"):
		src, err := t.Books.Open(ctx, book)
		if err != nil {
			return "", false, fmt.Errorf("open book: %w", err)
		}
		defer func() { _ = src.Close() }()
		text, truncated, err := fileproc.ExtractEPUBText(ctx, src, probe)
		if err != nil {
			return "", false, err
		}
		// Length alone cannot say "more": the extractor trims, so a cut
		// that ends on whitespace comes back shorter than the cap. Its
		// own truncation flag is the authority.
		page, cut := capUTF8(text, limit)
		return page, cut || truncated, nil

	case t.Markdown != nil && model.Convertible(book.Format):
		text, err := t.Markdown.Text(ctx, book, probe)
		if err != nil {
			return "", false, err
		}
		page, more := capUTF8(text, limit)
		return page, more, nil
	}
	return "", false, ErrNoBookText
}

// BookTextPage is one window of a book's text.
type BookTextPage struct {
	Text string
	// Offset is where this page starts, in bytes of UTF-8 text. It can be
	// later than asked for: a start that lands inside a character moves
	// forward to the next one.
	Offset int64
	// NextOffset is where the following page starts; nil at the end.
	NextOffset *int64
}

// Page returns up to size bytes of text starting at offset, both edges on
// character boundaries. Text is re-read from the start on every call —
// O(offset), acceptable at book scale (ADR-0039 §6). Same errors as Read.
func (t *BookText) Page(ctx context.Context, book model.Book, offset, size int64) (BookTextPage, error) {
	if offset < 0 || size <= 0 {
		return BookTextPage{}, errors.New("book text: offset must be >= 0 and size > 0")
	}
	// A page must make progress, or a client following next_offset loops
	// forever. A budget smaller than the next character — or one that
	// covers only the whitespace the extractor trims — yields nothing, so
	// the window widens until it holds at least one character. The
	// extractor collapses whitespace to single separators, so a couple of
	// widenings always suffice; the bound is a guard, not a tuning knob.
	var (
		text  string
		more  bool
		start int64
	)
	end := offset + size
	for attempt := 0; ; attempt++ {
		var err error
		text, more, err = t.Read(ctx, book, end)
		if err != nil {
			return BookTextPage{}, err
		}
		start = min(offset, int64(len(text)))
		for start < int64(len(text)) && !utf8.RuneStart(text[start]) {
			start++
		}
		if start < int64(len(text)) || !more {
			break
		}
		if attempt == 4 {
			return BookTextPage{}, fmt.Errorf("book text: no progress past offset %d", offset)
		}
		end += 2 * utf8.UTFMax
	}
	page := BookTextPage{Text: text[start:], Offset: start}
	if more {
		next := int64(len(text))
		page.NextOffset = &next
	}
	return page, nil
}

// capUTF8 trims s to at most limit bytes without splitting a rune, and
// reports whether anything was cut.
func capUTF8(s string, limit int64) (string, bool) {
	if int64(len(s)) <= limit {
		return s, false
	}
	return trimPartialRune(s[:limit]), true
}

// trimPartialRune drops an incomplete UTF-8 sequence from the end of s —
// what a byte-capped cut leaves when it lands mid-character.
func trimPartialRune(s string) string {
	for i := len(s) - 1; i >= 0 && i >= len(s)-utf8.UTFMax; i-- {
		if utf8.RuneStart(s[i]) {
			if utf8.FullRuneInString(s[i:]) {
				return s
			}
			return s[:i]
		}
	}
	return s
}
