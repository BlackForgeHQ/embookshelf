// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"strings"
	"testing"
)

// EPUB alone: there is no PDF text library in go.mod, CBZ is images,
// audio formats are already audio, and MOBI/AZW3/FB2 have no text
// extractor (ADR-0028 §4) — their ingest processors read metadata and a
// cover only. Stated once here and asserted here, so a format gaining
// narratability is one edit.
func TestNarratableIsEPUBAlone(t *testing.T) {
	t.Parallel()

	narratable := map[string]bool{}
	for _, s := range FormatSpecs {
		if s.Narratable {
			narratable[s.Format] = true
		}
	}

	if len(narratable) != 1 || !narratable["EPUB"] {
		t.Errorf("narratable formats = %v, want EPUB alone", narratable)
	}
}

// Two capabilities, never one set doing double duty: Send-to-Kindle
// takes EPUB and PDF because that is what Amazon's service accepts
// (ADR-0021), and narration takes EPUB alone because that is what
// carries extractable text. They overlap at EPUB today and mean
// different things.
func TestKindleEligibleIsEPUBAndPDF(t *testing.T) {
	t.Parallel()

	eligible := map[string]bool{}
	for _, s := range FormatSpecs {
		if s.KindleEligible {
			eligible[s.Format] = true
		}
	}

	if len(eligible) != 2 || !eligible["EPUB"] || !eligible["PDF"] {
		t.Errorf("Kindle-eligible formats = %v, want EPUB and PDF", eligible)
	}
	if Narratable("PDF") {
		t.Error("PDF is narratable — the two capabilities have been collapsed into one set")
	}
}

// Same case-insensitivity as narratability, for the same reason, and the
// same prose helper: the handler's rejection and the client's disabled
// tooltip both said "Send-to-Kindle accepts EPUB and PDF only" in full.
func TestKindleEligibleIgnoresCaseAndReadsAsProse(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"EPUB", "epub", " PDF "} {
		if !KindleEligible(in) {
			t.Errorf("KindleEligible(%q) = false, want true", in)
		}
	}
	for _, in := range []string{"CBZ", "M4B", ""} {
		if KindleEligible(in) {
			t.Errorf("KindleEligible(%q) = true, want false", in)
		}
	}
	if got := KindleEligibleFormatList(); got != "EPUB and PDF" {
		t.Errorf("KindleEligibleFormatList() = %q, want %q", got, "EPUB and PDF")
	}
}

// The spec table has to cover every format the ingest path can produce,
// or a gate derived from it silently answers "no" for a format nobody
// declared rather than failing where it can be seen.
func TestFormatSpecsCoverEveryIngestedFormat(t *testing.T) {
	t.Parallel()

	// fileproc.FormatForExt's canonical tags. Restated rather than
	// imported: internal/model is a leaf and importing fileproc would
	// invert the dependency. A parity test in fileproc guards the pair.
	for _, format := range []string{"EPUB", "PDF", "CBZ", "MP3", "M4B", "MOBI", "AZW3", "FB2"} {
		if _, ok := LookupFormat(format); !ok {
			t.Errorf("format %q is produced by ingest but has no FormatSpec", format)
		}
	}
}

// Format strings reach this from a database column, a filename and a URL
// query in turn, so the comparison cannot be case-sensitive — the bug
// would be a narration button that vanishes for a book whose row says
// "epub".
func TestNarratableIgnoresCaseAndSurroundingSpace(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"EPUB", "epub", "ePub", "  EPUB  "} {
		if !Narratable(in) {
			t.Errorf("Narratable(%q) = false, want true", in)
		}
	}
	for _, in := range []string{"PDF", "pdf", "", "EPUB3"} {
		if Narratable(in) {
			t.Errorf("Narratable(%q) = true, want false", in)
		}
	}
}

// The user-facing sentence is built from the table rather than written
// out beside it: "only EPUB books can be narrated" appeared verbatim in
// the service, twice in the handler and twice in the client, and a
// second narratable format would have left all five saying otherwise.
func TestNarratableFormatListReadsAsProse(t *testing.T) {
	t.Parallel()

	got := NarratableFormatList()

	if got != "EPUB" {
		t.Errorf("NarratableFormatList() = %q, want %q", got, "EPUB")
	}
	if strings.Contains(got, "[") || strings.Contains(got, "\"") {
		t.Errorf("NarratableFormatList() = %q, want something a sentence can contain", got)
	}
}

// The download extension and Content-Type are one fact per format, and
// they had five copies between them: two tables in the handler, the
// placer's deliberate "mirrors the file handler" copy, the Kindle
// attachment type and the reMarkable upload type (#194).
func TestFormatSpecsCarryExtensionAndMIME(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ format, ext, mime string }{
		{"EPUB", ".epub", "application/epub+zip"},
		{"PDF", ".pdf", "application/pdf"},
		{"CBZ", ".cbz", "application/vnd.comicbook+zip"},
		{"MP3", ".mp3", "audio/mpeg"},
		// Apple uses audio/mp4; the m4b container is identical to m4a.
		{"M4B", ".m4b", "audio/mp4"},
	} {
		if got := ExtForFormat(tc.format); got != tc.ext {
			t.Errorf("ExtForFormat(%s) = %q, want %q", tc.format, got, tc.ext)
		}
		if got := MIMEForFormat(tc.format); got != tc.mime {
			t.Errorf("MIMEForFormat(%s) = %q, want %q", tc.format, got, tc.mime)
		}
	}
}

// A format with no reader still downloads, and still downloads with the
// right name. The empty MIME is what the file handler turns into a 415;
// an extension guessed from nothing would have produced "Dune" with no
// suffix, which no e-reader will open.
func TestFormatsWithNoReaderStillNameTheirFile(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"MOBI", "AZW3", "FB2"} {
		if got := ExtForFormat(format); got == "" {
			t.Errorf("ExtForFormat(%s) = %q, want the conventional extension", format, got)
		}
		if got := MIMEForFormat(format); got != "" {
			t.Errorf("MIMEForFormat(%s) = %q, want empty — there is no reader to serve it to",
				format, got)
		}
	}
}

// An unknown format answers empty rather than guessing, which is what
// lets the file handler return 415 instead of serving bytes under a
// content type it invented.
func TestUnknownFormatHasNoExtensionOrMIME(t *testing.T) {
	t.Parallel()

	if got := ExtForFormat("DJVU"); got != "" {
		t.Errorf("ExtForFormat(DJVU) = %q, want empty", got)
	}
	if got := MIMEForFormat("DJVU"); got != "" {
		t.Errorf("MIMEForFormat(DJVU) = %q, want empty", got)
	}
	if got := ReaderForFormat("DJVU"); got != ReaderNone {
		t.Errorf("ReaderForFormat(DJVU) = %q, want none", got)
	}
}

// Which surface opens a book. Distinct from the Rendition the user then
// picks inside it: an EPUB with a narration is still ReaderText here,
// and the audio/text choice happens after (ADR-0025 §3).
func TestReaderKindPerFormat(t *testing.T) {
	t.Parallel()

	for format, want := range map[string]ReaderKind{
		"EPUB": ReaderText,
		"PDF":  ReaderText,
		"CBZ":  ReaderComic,
		"MP3":  ReaderAudio,
		"M4B":  ReaderAudio,
		"MOBI": ReaderNone,
		"AZW3": ReaderNone,
		"FB2":  ReaderNone,
	} {
		if got := ReaderForFormat(format); got != want {
			t.Errorf("ReaderForFormat(%s) = %q, want %q", format, got, want)
		}
	}
}
