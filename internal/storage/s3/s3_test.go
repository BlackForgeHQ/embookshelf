// SPDX-License-Identifier: AGPL-3.0-or-later

package s3

import "testing"

func TestNormalizeEndpoint(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"  ", ""},
		{"fsn1.your-objectstorage.com", "https://fsn1.your-objectstorage.com"},
		{"https://s3.example.com", "https://s3.example.com"},
		{"http://localhost:9000", "http://localhost:9000"},
		{"localhost:9000", "https://localhost:9000"},
		{"  s3.example.com ", "https://s3.example.com"},
	}
	for _, c := range cases {
		if got := normalizeEndpoint(c.in); got != c.want {
			t.Errorf("normalizeEndpoint(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
