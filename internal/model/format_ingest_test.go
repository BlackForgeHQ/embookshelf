// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import "testing"

// TestIngestFormatForExt — the extension→format folding, previously a
// hand-kept switch in fileproc that had already diverged from this
// table (.cbr was admitted, stamped CBZ, then refused by Dispatch).
// One declaration: an alias lives on its format's row.
func TestIngestFormatForExt(t *testing.T) {
	cases := map[string]string{
		".epub": "EPUB",
		".pdf":  "PDF",
		".cbz":  "CBZ",
		// The comic aliases fold into CBZ, preserving the ingest-side
		// behavior fileproc always had; giving CBR its own ingest life is
		// the processor issue's decision (#310), not this table's.
		".cbr": "CBZ",
		".cb7": "CBZ",
		".mp3": "MP3",
		".m4b": "M4B",
		// Apple's m4a is the same container as m4b.
		".m4a":  "M4B",
		".mobi": "MOBI",
		".azw3": "AZW3",
		".fb2":  "FB2",
		// Case and the leading dot are normalised; the value arrives from
		// filenames in the wild.
		".EPUB": "EPUB",
		"pdf":   "PDF",
		// Unknown and legacy-tag-only extensions produce no format: .txt
		// rows exist in old libraries, but no ingest path produces them.
		".txt": "",
		".xyz": "",
		"":     "",
	}
	for ext, want := range cases {
		if got := IngestFormatForExt(ext); got != want {
			t.Errorf("IngestFormatForExt(%q) = %q, want %q", ext, got, want)
		}
	}
}

// TestIngestExtensionsAreUniqueAndOrdered — every admitted extension is
// declared exactly once (an extension on two rows would stamp two
// formats depending on iteration order), and the derived list is stable
// table order so the watcher's set does not shuffle between builds.
func TestIngestExtensionsAreUniqueAndOrdered(t *testing.T) {
	seen := map[string]string{}
	for _, s := range FormatSpecs {
		for _, ext := range s.IngestExts {
			if prev, dup := seen[ext]; dup {
				t.Errorf("extension %q declared by both %s and %s", ext, prev, s.Format)
			}
			seen[ext] = s.Format
		}
	}
	want := []string{
		".epub", ".pdf",
		".cbz", ".cbr", ".cb7",
		".mp3", ".m4b", ".m4a",
		".mobi", ".azw3", ".fb2",
	}
	got := IngestExtensions()
	if len(got) != len(want) {
		t.Fatalf("IngestExtensions() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("IngestExtensions()[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// TestAudioAxis — the ingest-side "is this file audio" fact joins the
// table; MP3 and M4B alone, matching the fileproc predicate this
// derives.
func TestAudioAxis(t *testing.T) {
	for _, s := range FormatSpecs {
		want := s.Format == "MP3" || s.Format == "M4B"
		if s.Audio != want {
			t.Errorf("%s.Audio = %v, want %v", s.Format, s.Audio, want)
		}
	}
}
