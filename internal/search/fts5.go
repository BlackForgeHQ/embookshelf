// SPDX-License-Identifier: AGPL-3.0-or-later

// Package search holds search-related helpers shared by the library
// repo and the OPDS layer. Today the only export is EscapeFTS5Query
// for the SQLite FTS5 path; Postgres uses websearch_to_tsquery and
// doesn't need an equivalent.
package search

import (
	"strings"
	"unicode"
)

// EscapeFTS5Query turns arbitrary user input into a safe FTS5 MATCH
// expression. The result is a space-separated list of `"<token>"*`
// chunks where each token is the lowercased, ASCII-letter/digit/
// hyphen/apostrophe-only fragment of the input word. Tokens are
// joined by FTS5's implicit AND.
//
// The empty string is returned for input that contains no tokens
// (e.g. "" or "  ?"). Callers should treat that as "no search filter"
// and fall back to whatever query they'd run with no search term.
func EscapeFTS5Query(in string) string {
	in = strings.ToLower(in)
	fields := strings.Fields(in)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		var sb strings.Builder
		for _, r := range f {
			switch {
			case unicode.IsLetter(r), unicode.IsDigit(r), r == '\'', r == '-':
				sb.WriteRune(r)
			}
		}
		t := sb.String()
		if t == "" {
			continue
		}
		out = append(out, `"`+t+`"*`)
	}
	return strings.Join(out, " ")
}
