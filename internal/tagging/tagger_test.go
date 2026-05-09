// SPDX-License-Identifier: AGPL-3.0-or-later

package tagging_test

import (
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/tagging"
)

func TestClassify(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name     string
		lastRead time.Time
		want     tagging.Tier
	}{
		{
			name:     "zero last-read is cold",
			lastRead: time.Time{},
			want:     tagging.TierCold,
		},
		{
			name:     "last-read now is hot",
			lastRead: now,
			want:     tagging.TierHot,
		},
		{
			name:     "last-read 89 days ago is hot",
			lastRead: now.Add(-89 * 24 * time.Hour),
			want:     tagging.TierHot,
		},
		{
			name:     "last-read exactly 90 days ago is hot (boundary inclusive)",
			lastRead: now.Add(-90 * 24 * time.Hour),
			want:     tagging.TierHot,
		},
		{
			name:     "last-read 91 days ago is warm",
			lastRead: now.Add(-91 * 24 * time.Hour),
			want:     tagging.TierWarm,
		},
		{
			name:     "last-read 364 days ago is warm",
			lastRead: now.Add(-364 * 24 * time.Hour),
			want:     tagging.TierWarm,
		},
		{
			name:     "last-read exactly 365 days ago is warm (boundary inclusive)",
			lastRead: now.Add(-365 * 24 * time.Hour),
			want:     tagging.TierWarm,
		},
		{
			name:     "last-read 366 days ago is cold",
			lastRead: now.Add(-366 * 24 * time.Hour),
			want:     tagging.TierCold,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tagging.Classify(now, tc.lastRead)
			if got != tc.want {
				t.Errorf("Classify(now, %v) = %q, want %q", tc.lastRead, got, tc.want)
			}
		})
	}
}
