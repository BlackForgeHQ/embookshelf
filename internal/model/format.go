// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import "strings"

// FormatSpec is one book format and what the rest of the codebase is
// allowed to ask about it.
//
// A spec table rather than a predicate per question, following
// editableField and lockSpec next door: the questions multiply — what can
// be narrated, what Send-to-Kindle accepts, which reader renders it, what
// MIME type it carries — and answering each one wherever it is asked
// produces a set of facts about EPUB scattered across two languages with
// nothing making them agree. Adding a format should be one row (#192).
type FormatSpec struct {
	// Format is the canonical uppercase tag stored in books.format, as
	// produced by fileproc.FormatForExt.
	Format string
	// Narratable is whether the format carries text an engine can read
	// aloud. EPUB alone: there is no PDF text library in go.mod, CBZ is
	// images, MP3/M4B are already audio, and MOBI/AZW3/FB2 have no
	// extractor (ADR-0028 §4).
	//
	// The set has three enforcement points — the UI button, the handler
	// and the worker — because a re-import can change a book's format
	// between enqueue and dispatch. Three gates, one declaration.
	Narratable bool
}

// FormatSpecs is every format the library can hold. The client keeps one
// declaration of its own in ui/src/lib/formats.ts, which
// TestNarratableFormatsMatchClient holds equal to this one in both
// directions — the same shape as the SSE catalog and the error-code
// union, and for the same reason: a runtime fetch to learn that EPUB has
// text would be absurd, so the pair is guarded instead.
var FormatSpecs = []FormatSpec{
	{Format: "EPUB", Narratable: true},
	{Format: "PDF"},
	{Format: "CBZ"},
	{Format: "MP3"},
	{Format: "M4B"},
	{Format: "MOBI"},
	{Format: "AZW3"},
	{Format: "FB2"},
}

// LookupFormat finds a format's spec. The lookup is case-insensitive and
// tolerates surrounding space: the value arrives from a database column,
// a filename and a URL query in turn, and only the first of those is
// canonical.
func LookupFormat(format string) (FormatSpec, bool) {
	want := strings.ToUpper(strings.TrimSpace(format))
	for _, s := range FormatSpecs {
		if s.Format == want {
			return s, true
		}
	}
	return FormatSpec{}, false
}

// Narratable reports whether a book's format can be read aloud.
func Narratable(format string) bool {
	s, ok := LookupFormat(format)
	return ok && s.Narratable
}

// NarratableFormats lists the formats that can be read aloud, in table
// order.
func NarratableFormats() []string {
	var out []string
	for _, s := range FormatSpecs {
		if s.Narratable {
			out = append(out, s.Format)
		}
	}
	return out
}

// NarratableFormatList renders those formats for a sentence shown to a
// user — "only EPUB books can be narrated". Built from the table because
// that sentence appeared verbatim in five places, and a second narratable
// format would have left every one of them wrong.
func NarratableFormatList() string {
	names := NarratableFormats()
	switch len(names) {
	case 0:
		return "no"
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}
