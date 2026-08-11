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
