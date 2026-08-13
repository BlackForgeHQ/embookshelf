// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
)

// The sibling of model's format_parity_test.go, for the fileproc tier:
// every dispatcher here derives from model.FormatSpecs, and this file is
// what stops the two packages re-growing hand-kept switches that
// disagree — the state this replaced admitted .cbr at intake, stamped it
// CBZ, then refused it at dispatch with a message calling it unsupported.

// TestSupportedExtsDeriveFromTheTable — the watcher's admit set is the
// table's ingest set, verbatim.
func TestSupportedExtsDeriveFromTheTable(t *testing.T) {
	if !reflect.DeepEqual(SupportedExts, model.IngestExtensions()) {
		t.Fatalf("SupportedExts = %v\nmodel.IngestExtensions() = %v", SupportedExts, model.IngestExtensions())
	}
}

// TestFormatForExtAgreesWithModel — one folding, not two.
func TestFormatForExtAgreesWithModel(t *testing.T) {
	for _, ext := range SupportedExts {
		if got, want := FormatForExt(ext), model.IngestFormatForExt(ext); got != want || got == "" {
			t.Errorf("FormatForExt(%q) = %q, model says %q", ext, got, want)
		}
	}
	// The historical no-dot call shape keeps working.
	if got := FormatForExt("cbr"); got != "CBZ" {
		t.Errorf(`FormatForExt("cbr") = %q, want CBZ`, got)
	}
}

// TestDispatchCoversEveryProcessorExt — an extension with a processor
// dispatches to it and stamps the table's format.
func TestDispatchCoversEveryProcessorExt(t *testing.T) {
	cases := map[string]string{
		".epub": "EPUB", ".pdf": "PDF", ".cbz": "CBZ",
		".mp3": "MP3", ".m4a": "M4B", ".m4b": "M4B",
		".fb2": "FB2",
	}
	for ext, wantFormat := range cases {
		p, format, err := Dispatch("x" + ext)
		if err != nil || p == nil {
			t.Errorf("Dispatch(%q): p=%v err=%v", ext, p, err)
			continue
		}
		if format != wantFormat {
			t.Errorf("Dispatch(%q) format = %q, want %q", ext, format, wantFormat)
		}
	}
}

// TestDispatchNoProcessorIsPerFormatAndPermanent — an admitted
// extension with no processor fails with a message naming the format,
// not the generic unsupported answer, and still reads as
// ErrUnsupportedFormat so the ingest worker's terminal-failure branch
// keeps firing (no retry for a file that will refuse identically in
// thirty seconds).
//
// This set is the interim ADR-0033-style loud state; #310 (.cbr/.cb7),
// #311 (.mobi/.azw3) and #312 (.fb2, done) each shrink it by wiring a
// processor, at which point the entry moves to the covers-every-ext
// test above.
func TestDispatchNoProcessorIsPerFormatAndPermanent(t *testing.T) {
	noProcessor := map[string]string{
		".cbr":  "CBZ",
		".cb7":  "CBZ",
		".mobi": "MOBI",
		".azw3": "AZW3",
	}
	for ext, format := range noProcessor {
		_, _, err := Dispatch("x" + ext)
		if err == nil {
			t.Errorf("Dispatch(%q) succeeded — a processor exists; move %q to the covered table", ext, ext)
			continue
		}
		if !errors.Is(err, ErrUnsupportedFormat) {
			t.Errorf("Dispatch(%q) err = %v, want it to read as ErrUnsupportedFormat for the terminal-failure branch", ext, err)
		}
		if !strings.Contains(err.Error(), "no processor") || !strings.Contains(err.Error(), format) {
			t.Errorf("Dispatch(%q) err = %q, want a per-format no-processor message naming %s", ext, err, format)
		}
	}

	// Exhaustiveness: admitted extensions are exactly covered + no-processor.
	covered := map[string]bool{
		".epub": true, ".pdf": true, ".cbz": true,
		".mp3": true, ".m4a": true, ".m4b": true,
		".fb2": true,
	}
	for _, ext := range SupportedExts {
		if _, ok := noProcessor[ext]; ok == covered[ext] {
			t.Errorf("extension %q is in neither or both of the covered/no-processor sets", ext)
		}
	}
}

// TestDispatchUnknownExtensionStaysGeneric — an extension the table does
// not know keeps the old answer.
func TestDispatchUnknownExtensionStaysGeneric(t *testing.T) {
	_, format, err := Dispatch("x.xyz")
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err = %v, want ErrUnsupportedFormat", err)
	}
	if strings.Contains(err.Error(), "no processor") {
		t.Fatalf("err = %q — the per-format message is for recognised formats only", err)
	}
	if format != "XYZ" {
		t.Fatalf("format tag = %q, want the historical upper-cased extension", format)
	}
}

// TestDispatchFormatAgreesWithDispatch — the slug-keyed twin answers
// with the same processor type as the extension entry point.
func TestDispatchFormatAgreesWithDispatch(t *testing.T) {
	for _, format := range []string{"EPUB", "PDF", "CBZ", "MP3", "M4B", "FB2"} {
		byFormat, err := DispatchFormat(format)
		if err != nil {
			t.Errorf("DispatchFormat(%q): %v", format, err)
			continue
		}
		spec, _ := model.LookupFormat(format)
		byExt, _, err := Dispatch("x" + spec.Ext)
		if err != nil {
			t.Errorf("Dispatch for %q's ext: %v", format, err)
			continue
		}
		if fmt.Sprintf("%T", byFormat) != fmt.Sprintf("%T", byExt) {
			t.Errorf("DispatchFormat(%q) = %T, Dispatch = %T", format, byFormat, byExt)
		}
	}
	if _, err := DispatchFormat("MOBI"); !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("DispatchFormat(MOBI) err = %v, want ErrUnsupportedFormat while no processor exists", err)
	}
}

// TestIsAudioFormatDerivesFromTheTable — the predicate is the table's
// Audio column, exactly, and stays case-sensitive: books.format stores
// the upper-case form, and a lower-case value reaching this predicate
// is a bug to surface, not to absorb.
func TestIsAudioFormatDerivesFromTheTable(t *testing.T) {
	for _, s := range model.FormatSpecs {
		if got := IsAudioFormat(s.Format); got != s.Audio {
			t.Errorf("IsAudioFormat(%q) = %v, table says %v", s.Format, got, s.Audio)
		}
	}
	if IsAudioFormat("mp3") {
		t.Error(`IsAudioFormat("mp3") = true, want the case-sensitive refusal`)
	}
}
