// SPDX-License-Identifier: AGPL-3.0-or-later

package textsplit

import (
	"strings"
	"testing"
)

// sentenceBoundaryBefore's fallback chain — sentence ender, then word
// boundary, then limit itself — was never exercised directly: every
// OnSentences fixture has sentences far shorter than the limit/4 lookback
// window, so the first branch always wins and the rest never runs. These
// pin the two branches that fall through it, with fixtures sized from the
// function's own window arithmetic rather than guessed.
//
// limit=40 gives window := limit/4 == 10, so the loops inspect only rune
// indices [30,39] — index 30 through 39 inclusive — scanning from 39 down
// to 30.

// A sentence ender exists, but well before the lookback window (index 5,
// far short of the window's floor at index 30) — proving the window is
// actually enforced, not merely a comment, since a broken bound would
// wrongly return 6 instead of falling through to the word boundary at 35.
func TestSentenceBoundaryBeforeFallsBackToWordBoundaryOutsideWindow(t *testing.T) {
	const limit = 40
	var b strings.Builder
	b.WriteString("Alpha.")                // indices 0-5: a sentence ender well outside the window
	b.WriteString(strings.Repeat("x", 24)) // indices 6-29: filler, no punctuation, no space
	b.WriteString(strings.Repeat("x", 5))  // indices 30-34: still inside the window, no boundary
	b.WriteString(" ")                     // index 35: the only boundary inside the window
	b.WriteString(strings.Repeat("x", 4))  // indices 36-39: inside the window, after the space
	b.WriteString(strings.Repeat("x", 5))  // indices 40-44: past limit, irrelevant to the cut

	runes := []rune(b.String())
	if len(runes) <= limit {
		t.Fatalf("fixture is %d runes, want more than limit=%d so the fallback path runs", len(runes), limit)
	}

	got := sentenceBoundaryBefore(runes, limit)
	const want = 36 // one past the space at index 35
	if got != want {
		t.Fatalf("sentenceBoundaryBefore = %d, want %d (the word boundary at index 35, not the sentence ender at index 5)", got, want)
	}
	if runes[got-1] != ' ' {
		t.Fatalf("rune before cut = %q, want the space", runes[got-1])
	}
}

// No punctuation and no spaces anywhere near the window — CJK-dense prose
// with an unusually long sentence would look like this. Both fallback
// passes find nothing, so the cut must land on limit itself and the text
// still terminates rather than looping or panicking.
func TestSentenceBoundaryBeforeFallsBackToLimitWithNoBoundary(t *testing.T) {
	const limit = 40
	runes := []rune(strings.Repeat("x", limit+10))

	got := sentenceBoundaryBefore(runes, limit)
	if got != limit {
		t.Fatalf("sentenceBoundaryBefore = %d, want %d (limit itself, since the window has no sentence ender or space)", got, limit)
	}
}
