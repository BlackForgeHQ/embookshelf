// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"testing"
	"time"
)

func TestNewID(t *testing.T) {
	id := NewID()
	if len(id) != 36 {
		t.Fatalf("got %q (len=%d), want 36-char UUID", id, len(id))
	}
	if id == NewID() {
		t.Fatal("two NewID() calls returned the same value")
	}
}

func TestScanTime(t *testing.T) {
	want := time.Date(2024, 3, 1, 12, 30, 0, 0, time.UTC)

	var got time.Time
	if err := ScanTime(want, &got); err != nil {
		t.Fatalf("ScanTime: %v", err)
	}
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	// NULL into a non-nullable destination is an error.
	if err := ScanTime(nil, &got); err == nil {
		t.Fatal("ScanTime(nil): want error, got nil")
	}

	// Anything other than a time.Time is a driver/codec bug.
	if err := ScanTime("2024-03-01T12:30:00Z", &got); err == nil {
		t.Fatal("ScanTime(string): want error, got nil")
	}

	if err := ScanTime(want, nil); err == nil {
		t.Fatal("ScanTime(nil dst): want error, got nil")
	}
}

func TestScanNullTime(t *testing.T) {
	want := time.Date(2024, 3, 1, 12, 30, 0, 0, time.UTC)

	var got *time.Time
	if err := ScanNullTime(want, &got); err != nil {
		t.Fatalf("ScanNullTime: %v", err)
	}
	if got == nil || !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	if err := ScanNullTime(nil, &got); err != nil {
		t.Fatalf("ScanNullTime(nil): %v", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestScanStringSlice(t *testing.T) {
	// The pgx codec can hand us a ready-made []string. Just copy it.
	var dst []string
	if err := ScanStringSlice([]string{"a", "b"}, &dst); err != nil {
		t.Fatalf("ScanStringSlice: %v", err)
	}
	if len(dst) != 2 || dst[0] != "a" {
		t.Fatalf("got %v, want [a b]", dst)
	}

	// pgx stdlib delivers TEXT[] as a literal string when the scan
	// destination is `any`. Confirm we parse the literal form.
	cases := []struct {
		in   string
		want []string
	}{
		{"{}", []string{}},
		{"{sci-fi,drama}", []string{"sci-fi", "drama"}},
		{`{"a,b",c}`, []string{"a,b", "c"}},
		{`{"with \"quote\"","back\\slash"}`, []string{`with "quote"`, `back\slash`}},
		{"{NULL,a}", []string{"", "a"}},
		{`{"NULL"}`, []string{"NULL"}},
	}
	for _, tc := range cases {
		var got []string
		if err := ScanStringSlice(tc.in, &got); err != nil {
			t.Fatalf("literal %q: %v", tc.in, err)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("literal %q: got %v, want %v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("literal %q: got %v, want %v", tc.in, got, tc.want)
			}
		}
	}

	// The []byte form of the same literal parses identically.
	var byteDst []string
	if err := ScanStringSlice([]byte("{x,y}"), &byteDst); err != nil {
		t.Fatalf("[]byte literal: %v", err)
	}
	if len(byteDst) != 2 || byteDst[1] != "y" {
		t.Fatalf("[]byte literal: got %v, want [x y]", byteDst)
	}

	// NULL src: nil slice, no error.
	var nilDst []string
	if err := ScanStringSlice(nil, &nilDst); err != nil {
		t.Fatalf("nil src: %v", err)
	}
	if len(nilDst) != 0 {
		t.Fatalf("nil src: got %v, want []", nilDst)
	}

	// Malformed literal is an error, not a silent empty slice.
	var badDst []string
	if err := ScanStringSlice("not-an-array", &badDst); err == nil {
		t.Fatal("malformed literal: want error, got nil")
	}
}
