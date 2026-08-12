// SPDX-License-Identifier: AGPL-3.0-or-later

package model_test

import (
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
)

// TestStale — the one staleness predicate every tier's badge reads
// (#297). The empty-hash arms are the documented policy: a comparison
// that cannot be made answers "not known to be stale", never a scare
// label.
func TestStale(t *testing.T) {
	cases := map[string]struct {
		current, recorded []byte
		want              bool
	}{
		"same bytes are fresh":         {current: []byte{1, 2}, recorded: []byte{1, 2}, want: false},
		"different bytes are stale":    {current: []byte{1, 2}, recorded: []byte{3, 4}, want: true},
		"no current hash reads fresh":  {current: nil, recorded: []byte{1}, want: false},
		"no recorded hash reads fresh": {current: []byte{1}, recorded: nil, want: false},
		"neither hash reads fresh":     {current: nil, recorded: nil, want: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := model.Stale(tc.current, tc.recorded); got != tc.want {
				t.Errorf("Stale(%x, %x) = %v, want %v", tc.current, tc.recorded, got, tc.want)
			}
		})
	}
}

// TestRenditionStateCanBeStale is the gate every rendition-badge caller
// used to restate for itself — the handler gated on ready, the feed
// relied on its own upstream switch. Declared once here, beside Stale,
// and quantified over every state so a fifth rendition state added
// without an opinion here fails loud rather than silently reading
// fresh or, worse, stale (#322).
func TestRenditionStateCanBeStale(t *testing.T) {
	for _, state := range model.AllRenditionStates() {
		want := state == model.RenditionReady
		if got := state.CanBeStale(); got != want {
			t.Errorf("%q.CanBeStale() = %v, want %v — only a ready row was ever compared against anything",
				state, got, want)
		}
	}
}
