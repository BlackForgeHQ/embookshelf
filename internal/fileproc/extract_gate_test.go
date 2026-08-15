// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import "testing"

// The audio-field gate ExtractBook applies (#335): duration and
// narrator survive only onto an audio format. It used to live as a
// field-by-field copy into the deleted ExtractResult; the pin is what
// keeps the fold from quietly widening the fields non-audio rows carry.
func TestGateAudioFieldsZeroesNonAudioFormats(t *testing.T) {
	secs := 321
	in := Metadata{Title: "Dune", DurationSeconds: &secs, Narrator: "A Voice"}

	for _, format := range []string{"EPUB", "PDF", "CBZ", "TXT", ""} {
		out := gateAudioFields(in, format)
		if out.DurationSeconds != nil || out.Narrator != "" {
			t.Errorf("gateAudioFields(%q) kept audio fields: %+v", format, out)
		}
		if out.Title != "Dune" {
			t.Errorf("gateAudioFields(%q) touched a non-audio field", format)
		}
	}
	for _, format := range []string{"MP3", "M4B"} {
		out := gateAudioFields(in, format)
		if out.DurationSeconds == nil || *out.DurationSeconds != secs || out.Narrator != "A Voice" {
			t.Errorf("gateAudioFields(%q) dropped audio fields: %+v", format, out)
		}
	}
}
