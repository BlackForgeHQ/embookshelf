// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import "testing"

func TestEscapeFTS5Query(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"single token", "dune", `"dune"*`},
		{"multi token AND", "dune frank", `"dune"* "frank"*`},
		{"strips quotes", `"dune"`, `"dune"*`},
		{"strips parens", "(dune)", `"dune"*`},
		{"strips reserved", "dune* AND OR NEAR", `"dune"* "and"* "or"* "near"*`},
		{"keeps apostrophe", "robot's dawn", `"robot's"* "dawn"*`},
		{"keeps hyphen", "anti-matter", `"anti-matter"*`},
		{"unicode lowercase", "DÜNE", `"düne"*`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EscapeFTS5Query(tc.in)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
