// SPDX-License-Identifier: AGPL-3.0-or-later

// Package textsplit cuts prose into budgeted pieces on sentence
// boundaries.
//
// Two callers at two granularities: the segment planner splits a chapter
// into River jobs, and a TTS adapter splits a segment into engine calls.
// Same algorithm, different budgets — which is why this is neither the
// EPUB extractor's nor the speech package's to own.
package textsplit

import "strings"

// OnSentences cuts text into pieces of at most maxChars characters.
//
// Splits land on sentence boundaries. A mid-sentence cut is audible at
// every seam, and a book is ~180 of them — the single most noticeable
// artifact of chunked synthesis (ADR-0028 §8).
//
// Works in runes, not bytes, for two reasons that both bite immediately
// on non-Latin text: a byte index can land inside a multi-byte rune and
// hand the engine invalid UTF-8, and every engine's per-request cap is
// quoted in characters, so a byte budget under-fills it by up to 3×.
func OnSentences(text string, maxChars int) []string {
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
