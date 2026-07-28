// SPDX-License-Identifier: AGPL-3.0-or-later

package textsplit_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/blackforge/embookshelf/internal/textsplit"
)

// A long chapter becomes several pieces, but each stays under the cap —
// the property ExtractEPUBSegments relies on to keep a job inside
// River's rescue window.
func TestOnSentencesSplitsLongTextKeepingUnderCap(t *testing.T) {
	sentence := "This is a sentence of a reasonable length. "
	long := strings.Repeat(sentence, 60) // ~2.5k chars

	pieces := textsplit.OnSentences(long, 500)
	if len(pieces) < 3 {
		t.Fatalf("got %d pieces, want the text split across several", len(pieces))
	}
	for i, p := range pieces {
		if len(p) > 500 {
			t.Errorf("pieces[%d] is %d chars, over the 500 cap", i, len(p))
		}
	}
}

// A cut mid-sentence is audible at every seam, and a book has ~180 of
// them. Each piece must end where a sentence does.
func TestOnSentencesCutsOnSentenceBoundaries(t *testing.T) {
	long := strings.Repeat("Alpha beta gamma delta. ", 100)

	pieces := textsplit.OnSentences(long, 300)
	if len(pieces) < 2 {
		t.Fatalf("got %d pieces, want several", len(pieces))
	}
	// Every piece but the last ends a sentence.
	for i, p := range pieces[:len(pieces)-1] {
		if !strings.HasSuffix(p, ".") {
			t.Errorf("pieces[%d] ends %q, want a sentence boundary", i, tail(p, 24))
		}
	}
	// And no text is lost across the seams. Joined with a space, because
	// the seam is where one synthesis request ends and the next begins —
	// concatenating bare would glue the last word to the first.
	var joined strings.Builder
	for _, p := range pieces {
		joined.WriteString(p)
		joined.WriteByte(' ')
	}
	if got, want := len(strings.Fields(joined.String())), len(strings.Fields(strings.TrimSpace(long))); got != want {
		t.Errorf("word count across pieces = %d, want %d — the split dropped text", got, want)
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// The cap is in characters, not bytes, and a cut must never land inside a
// multi-byte rune — an engine handed invalid UTF-8 either errors or reads
// a replacement character aloud.
func TestOnSentencesSplitsMultibyteTextSafely(t *testing.T) {
	long := strings.Repeat("彼は砂の中に降りていった。", 80)

	pieces := textsplit.OnSentences(long, 100)
	if len(pieces) < 2 {
		t.Fatalf("got %d pieces, want several", len(pieces))
	}
	for i, p := range pieces {
		if !utf8.ValidString(p) {
			t.Errorf("pieces[%d] is not valid UTF-8 — a rune was cut in half", i)
		}
		if n := utf8.RuneCountInString(p); n > 100 {
			t.Errorf("pieces[%d] is %d characters, over the 100 cap", i, n)
		}
	}
	// The CJK full stop is a sentence boundary too.
	for i, p := range pieces[:len(pieces)-1] {
		if !strings.HasSuffix(p, "。") {
			t.Errorf("pieces[%d] ends %q, want a CJK sentence boundary", i, tail(p, 12))
		}
	}
}
