package queue

import (
	"math"
	"testing"
	"time"
)

func TestNextBackoff_growthAndCap(t *testing.T) {
	cases := []struct {
		attempt   int
		minWanted time.Duration
		maxWanted time.Duration
	}{
		{1, 1500 * time.Millisecond, 2500 * time.Millisecond}, // 2s ± 25%
		{2, 3 * time.Second, 5 * time.Second},                 // 4s ± 25%
		{3, 6 * time.Second, 10 * time.Second},                // 8s ± 25%
		{8, 4 * time.Minute, 5 * time.Minute},                 // capped at 5m
		{20, 4 * time.Minute, 5 * time.Minute},                // still capped
	}
	for _, tc := range cases {
		got := nextBackoff(tc.attempt)
		if got < tc.minWanted || got > tc.maxWanted {
			t.Errorf("attempt=%d: got %v, want between %v and %v",
				tc.attempt, got, tc.minWanted, tc.maxWanted)
		}
	}
}

func TestNextBackoff_zeroAttempt(t *testing.T) {
	got := nextBackoff(0)
	if got <= 0 {
		t.Fatalf("attempt=0: got %v, want positive", got)
	}
	if got > 5*time.Second {
		t.Fatalf("attempt=0: got %v, want under 5s (lower bound)", got)
	}
}

func TestNextBackoff_neverNegative(t *testing.T) {
	for i := 0; i < 1000; i++ {
		if d := nextBackoff(1); d < 0 {
			t.Fatalf("got negative backoff: %v", d)
		}
	}
	_ = math.MaxInt32
}
