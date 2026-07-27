// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import "testing"

// The literals here are what pgx's stdlib driver actually hands database/sql
// for a text[] column: the raw array literal, as a string.
func TestTextArray_Scan(t *testing.T) {
	cases := []struct {
		name string
		src  any
		want []string
		nil_ bool
	}{
		{name: "empty", src: "{}", want: []string{}},
		{name: "plain", src: "{sci-fi,drama}", want: []string{"sci-fi", "drama"}},
		{name: "comma inside element", src: `{"a,b",c}`, want: []string{"a,b", "c"}},
		{name: "quotes and backslashes", src: `{"with \"quote\"","back\\slash"}`, want: []string{`with "quote"`, `back\slash`}},
		{name: "literal NULL element is quoted", src: `{"NULL"}`, want: []string{"NULL"}},
		{name: "bytes form", src: []byte("{x,y}"), want: []string{"x", "y"}},
		{name: "SQL NULL", src: nil, nil_: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := []string{"stale"} // must be overwritten, never appended to
			if err := (TextArray{Dst: &got}).Scan(tc.src); err != nil {
				t.Fatalf("Scan(%v): %v", tc.src, err)
			}
			if tc.nil_ {
				if got != nil {
					t.Fatalf("got %#v, want nil", got)
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %#v, want %#v", got, tc.want)
				}
			}
		})
	}
}

func TestTextArray_ScanErrors(t *testing.T) {
	var dst []string
	if err := (TextArray{Dst: &dst}).Scan("not-an-array"); err == nil {
		t.Fatal("malformed literal: want error, got nil")
	}
	// A NULL element cannot land in a []string, so it must fail loudly
	// rather than silently becoming "".
	if err := (TextArray{Dst: &dst}).Scan("{NULL,a}"); err == nil {
		t.Fatal("NULL element: want error, got nil")
	}
	if err := (TextArray{Dst: nil}).Scan("{a}"); err == nil {
		t.Fatal("nil dst: want error, got nil")
	}
	if err := (TextArray{Dst: &dst}).Scan(42); err == nil {
		t.Fatal("unexpected src type: want error, got nil")
	}
}
