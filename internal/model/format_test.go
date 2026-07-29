// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"strings"
	"testing"
)

// EPUB alone: there is no PDF text library in go.mod, CBZ is images,
// audio formats are already audio, and MOBI/AZW3/FB2 have no extractor
// (ADR-0028 §4). Stated once here and asserted here, so a format gaining
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
