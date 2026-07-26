// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import "testing"

// IsAudioFormat used to be copied verbatim into three packages; this is
// the single test that now covers the single fact.
func TestIsAudioFormat(t *testing.T) {
	cases := map[string]bool{
		"MP3": true, "M4B": true,
		"EPUB": false, "PDF": false, "CBZ": false, "": false,
		// Case matters: books.format stores the upper-case form.
		"mp3": false,
	}
	for f, want := range cases {
		if got := IsAudioFormat(f); got != want {
			t.Errorf("IsAudioFormat(%q) = %v, want %v", f, got, want)
		}
	}
}
