package provider

import "testing"

func TestToISBN10(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Pragmatic Programmer 20th Anniversary Edition.
		{"978-0-13-595705-9", "0135957052"},
		// Already ISBN-10 (with hyphens) round-trips after stripping.
		{"0-201-61622-X", "020161622X"},
		// Plain digits.
		{"9780132350884", "0132350882"},
		// 979 prefix has no ISBN-10 mapping.
		{"979-10-90636-07-1", ""},
		// Garbage in → empty out, never panic.
		{"", ""},
		{"not an isbn", ""},
		{"123", ""},
	}
	for _, tc := range cases {
		got := toISBN10(tc.in)
		if got != tc.want {
			t.Errorf("toISBN10(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
