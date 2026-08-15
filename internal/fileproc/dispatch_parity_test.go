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
// dispatches to it and stamps the table's format, and the set below is
// every admitted extension: since #310 wired the comic aliases there is
// no admitted extension left without a processor, and the exhaustiveness
// check is what keeps a newly admitted one from arriving unwired and
// unnoticed.
func TestDispatchCoversEveryProcessorExt(t *testing.T) {
	cases := map[string]string{
		".epub": "EPUB", ".pdf": "PDF",
		".cbz": "CBZ", ".cbr": "CBZ", ".cb7": "CBZ",
		".mp3": "MP3", ".m4a": "M4B", ".m4b": "M4B",
		".fb2": "FB2", ".mobi": "MOBI", ".azw3": "AZW3",
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
	if len(cases) != len(SupportedExts) {
		t.Errorf("%d extensions covered here, %d admitted by the table (%v)",
			len(cases), len(SupportedExts), SupportedExts)
	}
	for _, ext := range SupportedExts {
		if _, ok := cases[ext]; !ok {
			t.Errorf("extension %q is admitted at intake but not covered here — wire a processor for it", ext)
		}
	}
}

// TestDispatchNoProcessorIsPerFormatAndPermanent — a format the table
// knows but nothing extracts fails with a message naming the format, not
// the generic unsupported answer, and still reads as ErrUnsupportedFormat
// so the ingest worker's terminal-failure branch keeps firing (no retry
// for a file that will refuse identically in thirty seconds).
//
// The interim state this guarded on the extension axis is over: #310
// (.cbr/.cb7), #311 (.mobi/.azw3) and #312 (.fb2) wired the last three,
// and the covers-every-ext test above now holds every admitted extension.
// What is left is the slug axis, where the legacy tags the download path
// meets on old rows still have no extractor — TXT is the live case, and
// the refusal's shape has to keep working for the next format admitted
// before its processor lands.
func TestDispatchNoProcessorIsPerFormatAndPermanent(t *testing.T) {
	_, err := DispatchFormat("TXT")
	if err == nil {
		t.Fatal("DispatchFormat(TXT) succeeded — nothing extracts plain text; move it to the covered set")
	}
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("err = %v, want it to read as ErrUnsupportedFormat for the terminal-failure branch", err)
	}
	var npe *NoProcessorError
	if !errors.As(err, &npe) {
		t.Fatalf("err = %T, want a *NoProcessorError", err)
	}
	if npe.Format != "TXT" || npe.Ext != ".txt" {
		t.Errorf("NoProcessorError = %+v, want the format and its extension", npe)
	}
	if !strings.Contains(err.Error(), "no processor") || !strings.Contains(err.Error(), "TXT") {
		t.Errorf("err = %q, want a per-format no-processor message naming TXT", err)
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
//
// CBR is in the set because it is the legacy tag books.format carries on
// rows written before the aliases folded onto CBZ. Its .cbr bytes are a
// RAR archive, and since #310 something opens them — so the slug resolves
// like any other and those old rows extract rather than refuse.
func TestDispatchFormatAgreesWithDispatch(t *testing.T) {
	for _, format := range []string{"EPUB", "PDF", "CBZ", "CBR", "MP3", "M4B", "FB2", "MOBI", "AZW3"} {
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
}

// TestDispatchEmbedderDerivesFromTheTable — the embed axis is the
// table's Embed column exactly, and every declared target has a writer
// wired: a format that gains Embed without an embedders entry fails
// here, not silently as a sidecar-only write it never asked for (#335 —
// this used to be an off-table switch nothing checked).
func TestDispatchEmbedderDerivesFromTheTable(t *testing.T) {
	for _, s := range model.FormatSpecs {
		e, err := DispatchEmbedder(s.Format)
		if s.Embed {
			if err != nil || e == nil {
				t.Errorf("DispatchEmbedder(%q): e=%v err=%v — the table declares an in-file target", s.Format, e, err)
			}
			if _, wired := embedders[s.Format]; !wired {
				t.Errorf("format %q declares Embed but has no embedders entry", s.Format)
			}
		} else if !errors.Is(err, ErrUnsupportedEmbed) {
			t.Errorf("DispatchEmbedder(%q) err = %v, want ErrUnsupportedEmbed", s.Format, err)
		}
	}
	// And the converse: a writer for a format the table does not declare
	// is a wiring the table cannot see.
	for format := range embedders {
		s, ok := model.LookupFormat(format)
		if !ok || !s.Embed {
			t.Errorf("embedders holds %q but the table does not declare Embed for it", format)
		}
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
