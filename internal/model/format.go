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
	// images, MP3/M4B are already audio, and MOBI/AZW3/FB2 have no *text*
	// extractor (ADR-0028 §4) — their processors read metadata and a
	// cover, which is a different question from having text to read.
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
	// Convertible is whether the converter extension can turn the format
	// into a Markdown rendition (ADR-0033): the intersection of what
	// anydoc converts and what embookshelf can hold but not read
	// natively. PDF alone today. EPUB is deliberately excluded — native
	// extraction serves it with no sidecar (gap-filler routing, ADR-0033
	// §2) — and MOBI/AZW3/FB2 are outside anydoc's set.
	Convertible bool
	// Embed is whether the format has an in-file metadata write target
	// (ADR-0001): EPUB (OPF rezip) and PDF (/Info). Everything else gets
	// the full-mirror sidecar instead. The writer itself lives in
	// fileproc's embedders map; a parity test holds the two together
	// (#335).
	Embed bool
	// IngestExts are the file extensions intake admits and stamps with
	// this Format — the ingest axis fileproc's dispatchers derive from
	// (#308). Distinct from Ext (the one download extension): a format
	// can be reached from several extensions (.m4a and .m4b are the same
	// container; the comic aliases fold into CBZ), and a legacy tag (CBR,
	// TXT) declares none because no ingest path produces it. An extension
	// may appear on exactly one row.
	IngestExts []string
	// Audio is whether a file of this format is an audiobook at ingest:
	// duration and narrator come from tag metadata rather than a text
	// extractor. The ingest-side fact behind fileproc.IsAudioFormat —
	// says nothing about origin (a generated narration's book stays
	// EPUB, see CONTEXT.md, Audio format).
	Audio bool
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
		Narratable: true, KindleEligible: true, Embed: true,
		IngestExts: []string{".epub"},
	},
	{
		Format: "PDF", Ext: ".pdf", MIME: "application/pdf", Reader: ReaderText,
		KindleEligible: true, Convertible: true, Embed: true,
		IngestExts: []string{".pdf"},
	},
	// The comic aliases fold into CBZ at ingest, the folding FormatForExt
	// always did; giving CBR its own ingest life is the RAR-processor
	// issue's decision (#310), not this table's.
	{
		Format: "CBZ", Ext: ".cbz", MIME: "application/vnd.comicbook+zip", Reader: ReaderComic,
		IngestExts: []string{".cbz", ".cbr", ".cb7"},
	},
	{
		Format: "MP3", Ext: ".mp3", MIME: "audio/mpeg", Reader: ReaderAudio,
		IngestExts: []string{".mp3"}, Audio: true,
	},
	// Apple uses audio/mp4; the m4b container is identical to m4a.
	{
		Format: "M4B", Ext: ".m4b", MIME: "audio/mp4", Reader: ReaderAudio,
		IngestExts: []string{".m4b", ".m4a"}, Audio: true,
	},
	// Admitted and downloadable, but nothing reads them in-app: no
	// reader, so an extension and no MIME. Their ingest processors landed
	// in #311 (MOBI/AZW3) and #312 (FB2) — metadata and cover only, which
	// is why they still join none of the Narratable, KindleEligible or
	// Convertible sets above.
	{Format: "MOBI", Ext: ".mobi", IngestExts: []string{".mobi"}},
	{Format: "AZW3", Ext: ".azw3", IngestExts: []string{".azw3"}},
	{Format: "FB2", Ext: ".fb2", IngestExts: []string{".fb2"}},
	// Legacy tags no ingest path produces — .cbr stamps CBZ above, and
	// nothing stamps TXT — but which the download path met on rows
	// written by older releases, and named correctly. No IngestExts:
	// the extension belongs to the row that stamps it.
	{Format: "CBR", Ext: ".cbr"},
	{Format: "TXT", Ext: ".txt"},
}

// IngestFormatForExt folds a file extension onto the format intake
// stamps it with, or "" for one no ingest path admits. Case and the
// leading dot are normalised — the value arrives from filenames in the
// wild.
func IngestFormatForExt(ext string) string {
	want := "." + strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
	for _, s := range FormatSpecs {
		for _, e := range s.IngestExts {
			if e == want {
				return s.Format
			}
		}
	}
	return ""
}

// IngestExtensions lists every admitted extension in table order — the
// watcher's admit set, derived rather than hand-kept beside the table.
func IngestExtensions() []string {
	var out []string
	for _, s := range FormatSpecs {
		out = append(out, s.IngestExts...)
	}
	return out
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

// Convertible reports whether the converter extension can produce a
// Markdown rendition from a book's format (ADR-0033).
func Convertible(format string) bool {
	s, ok := LookupFormat(format)
	return ok && s.Convertible
}

// ConvertibleFormats lists the formats the converter extension accepts,
// in table order.
func ConvertibleFormats() []string {
	return formatsWhere(func(s FormatSpec) bool { return s.Convertible })
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

// ConvertibleFormatList does the same for the converter extension's
// refusal message (ADR-0033).
func ConvertibleFormatList() string {
	return formatList(ConvertibleFormats())
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
