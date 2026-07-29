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
// ReaderKind names the surface that opens a format.
//
// Distinct from the Rendition the user picks *inside* that surface: an
// EPUB with a narration is ReaderText here, and the text-or-audio choice
// happens after (ADR-0025 §3). books.format stays the primary-format
// cache; this says which reader that primary format implies.
type ReaderKind string

const (
	// ReaderNone is a format with no reader. It still downloads.
	ReaderNone  ReaderKind = ""
	ReaderText  ReaderKind = "text"
	ReaderComic ReaderKind = "comic"
	ReaderAudio ReaderKind = "audio"
)

type FormatSpec struct {
	// Format is the canonical uppercase tag stored in books.format, as
	// produced by fileproc.FormatForExt.
	Format string
	// Ext is the conventional download extension, leading dot included.
	// Present even for formats with no reader — the bytes still download,
	// and a file named "Dune" with no suffix is one no e-reader opens.
	Ext string
	// MIME is the Content-Type to serve the bytes under, empty when there
	// is no reader for them. The file handler turns that emptiness into a
	// 415 rather than inventing a type.
	MIME string
	// Reader is which surface opens the format.
	Reader ReaderKind
	// Narratable is whether the format carries text an engine can read
	// aloud. EPUB alone: there is no PDF text library in go.mod, CBZ is
	// images, MP3/M4B are already audio, and MOBI/AZW3/FB2 have no
	// extractor (ADR-0028 §4).
	//
	// The set has three enforcement points — the UI button, the handler
	// and the worker — because a re-import can change a book's format
	// between enqueue and dispatch. Three gates, one declaration.
	Narratable bool
	// KindleEligible is whether Amazon's Send-to-Kindle service accepts
	// the format (ADR-0021). EPUB and PDF.
	//
	// Deliberately its own field rather than a reuse of Narratable. The
	// two overlap at EPUB today and answer different questions — one is
	// "does Amazon take this", the other "is there text to read aloud" —
	// and a single set standing for both would silently make PDF
	// narratable the day Amazon changed its mind.
	KindleEligible bool
}

// FormatSpecs is every format the library can hold. The client keeps one
// declaration of its own in ui/src/lib/formats.ts, which
// TestNarratableFormatsMatchClient holds equal to this one in both
// directions — the same shape as the SSE catalog and the error-code
// union, and for the same reason: a runtime fetch to learn that EPUB has
// text would be absurd, so the pair is guarded instead.
var FormatSpecs = []FormatSpec{
	{
		Format: "EPUB", Ext: ".epub", MIME: "application/epub+zip", Reader: ReaderText,
		Narratable: true, KindleEligible: true,
	},
	{Format: "PDF", Ext: ".pdf", MIME: "application/pdf", Reader: ReaderText, KindleEligible: true},
	{Format: "CBZ", Ext: ".cbz", MIME: "application/vnd.comicbook+zip", Reader: ReaderComic},
	{Format: "MP3", Ext: ".mp3", MIME: "audio/mpeg", Reader: ReaderAudio},
	// Apple uses audio/mp4; the m4b container is identical to m4a.
	{Format: "M4B", Ext: ".m4b", MIME: "audio/mp4", Reader: ReaderAudio},
	// Ingested and downloadable, but nothing reads them in-app: there is
	// no extractor and no reader, so they carry an extension and no MIME.
	{Format: "MOBI", Ext: ".mobi"},
	{Format: "AZW3", Ext: ".azw3"},
	{Format: "FB2", Ext: ".fb2"},
	// Tags no ingest path produces any more — fileproc.FormatForExt folds
	// .cbr into CBZ and never returns TXT — but which the download path
	// met on rows written by older releases, and named correctly. Listed
	// so it still does.
	{Format: "CBR", Ext: ".cbr"},
	{Format: "TXT", Ext: ".txt"},
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

// ExtForFormat returns the conventional download extension, or "" for a
// format the library does not know — the download still works, unnamed,
// rather than failing.
func ExtForFormat(format string) string {
	s, _ := LookupFormat(format)
	return s.Ext
}

// MIMEForFormat returns the Content-Type to serve a format under, or ""
// when there is no reader for it. Every caller shares the type and keeps
// its own answer to the empty case: the file handler 415s, the Kindle
// attachment falls back to octet-stream, and the reMarkable upload
// refuses outright.
func MIMEForFormat(format string) string {
	s, _ := LookupFormat(format)
	return s.MIME
}

// ReaderForFormat returns the surface that opens a format.
func ReaderForFormat(format string) ReaderKind {
	s, _ := LookupFormat(format)
	return s.Reader
}

// KindleEligible reports whether Send-to-Kindle accepts a book's format.
func KindleEligible(format string) bool {
	s, ok := LookupFormat(format)
	return ok && s.KindleEligible
}

// NarratableFormats lists the formats that can be read aloud, in table
// order.
func NarratableFormats() []string {
	return formatsWhere(func(s FormatSpec) bool { return s.Narratable })
}

// KindleEligibleFormats lists the formats Send-to-Kindle accepts, in
// table order.
func KindleEligibleFormats() []string {
	return formatsWhere(func(s FormatSpec) bool { return s.KindleEligible })
}

// NarratableFormatList renders those formats for a sentence shown to a
// user — "only EPUB books can be narrated". Built from the table because
// that sentence appeared verbatim in five places, and a second narratable
// format would have left every one of them wrong.
func NarratableFormatList() string {
	return formatList(NarratableFormats())
}

// KindleEligibleFormatList does the same for "Send-to-Kindle accepts
// EPUB and PDF only", which the handler and the client's disabled
// tooltip each spelled out.
func KindleEligibleFormatList() string {
	return formatList(KindleEligibleFormats())
}

func formatsWhere(pred func(FormatSpec) bool) []string {
	var out []string
	for _, s := range FormatSpecs {
		if pred(s) {
			out = append(out, s.Format)
		}
	}
	return out
}

// formatList renders format names as a sentence fragment: "EPUB", "EPUB
// and PDF", "EPUB, PDF and CBZ".
func formatList(names []string) string {
	switch len(names) {
	case 0:
		return "no"
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}
