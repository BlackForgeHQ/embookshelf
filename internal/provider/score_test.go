// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import "testing"

func TestScoreMatch_ExactTitle(t *testing.T) {
	q := Query{Title: "The Pragmatic Programmer", Author: "David Thomas"}
	got := scoreMatch(q, "The Pragmatic Programmer", []string{"David Thomas", "Andrew Hunt"})
	if got != 100 {
		t.Errorf("exact title + author match = %d, want 100", got)
	}
}

func TestScoreMatch_ExactTitleNoAuthor(t *testing.T) {
	q := Query{Title: "Dune"}
	got := scoreMatch(q, "Dune", nil)
	if got != 85 {
		t.Errorf("exact title, no author in query = %d, want 85", got)
	}
}

func TestScoreMatch_ISBNAlwaysTop(t *testing.T) {
	q := Query{ISBN: "9780132350884"}
	got := scoreMatch(q, "completely different title", nil)
	if got != 100 {
		t.Errorf("ISBN query = %d, want 100", got)
	}
}

func TestScoreMatch_FuzzyMatch(t *testing.T) {
	q := Query{Title: "Clean Code"}
	got := scoreMatch(q, "Clean Code: A Handbook", nil)
	if got < 50 {
		t.Errorf("contains match = %d, want >= 50", got)
	}
}

func TestScoreMatch_UnicodePrecomposed(t *testing.T) {
	// "u\u0308" (u + combining diaeresis) vs "ü" (precomposed).
	// After NFC normalization both should be identical.
	q := Query{Title: "Gu\u0308nter Grass"}
	got := scoreMatch(q, "G\u00fcnter Grass", nil)
	if got < 85 {
		t.Errorf("NFC-equivalent titles = %d, want >= 85 (exact after normalization)", got)
	}
}

func TestScoreMatch_UnicodeDiacritics(t *testing.T) {
	q := Query{Title: "Les Misérables"}
	got := scoreMatch(q, "Les Misérables", nil)
	if got != 85 {
		t.Errorf("identical Unicode title = %d, want 85", got)
	}
}

func TestScoreMatch_CaseFoldUnicode(t *testing.T) {
	q := Query{Title: "istanbul"}
	got := scoreMatch(q, "Istanbul", nil)
	if got < 85 {
		t.Errorf("case-folded title = %d, want >= 85", got)
	}
}

func TestScoreMatch_TokenOverlap(t *testing.T) {
	q := Query{Title: "Programming in Go", Author: "Mark Summerfield"}
	got := scoreMatch(q, "Go Programming Language", []string{"Alan Donovan"})
	if got < 40 {
		t.Errorf("token overlap = %d, want >= 40", got)
	}
}

func TestScoreMatch_NoMatch(t *testing.T) {
	q := Query{Title: "Cooking with Julia"}
	got := scoreMatch(q, "Advanced Calculus", nil)
	if got > 30 {
		t.Errorf("no match = %d, want <= 30", got)
	}
}

func TestFuzzyRatio(t *testing.T) {
	cases := []struct {
		a, b string
		min  float64
	}{
		{"hello", "hello", 1.0},
		{"hello", "helo", 0.7},
		{"", "", 1.0},
		{"abc", "", 0.0},
	}
	for _, tc := range cases {
		got := fuzzyRatio(tc.a, tc.b)
		if got < tc.min {
			t.Errorf("fuzzyRatio(%q, %q) = %.2f, want >= %.2f", tc.a, tc.b, got, tc.min)
		}
	}
}
