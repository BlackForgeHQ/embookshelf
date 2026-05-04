package handler

import "testing"

// TestSniffCoverMime exercises the magic-byte sniff used by BookDropPutCover
// to gate user-supplied cover bytes.
func TestSniffCoverMime(t *testing.T) {
	cases := []struct {
		name   string
		input  []byte
		want   string
		wantOK bool
	}{
		{"png", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, "image/png", true},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}, "image/jpeg", true},
		{"garbage", []byte{0x00, 0x01, 0x02, 0x03}, "", false},
		{"empty", []byte{}, "", false},
		{"too-short-png", []byte{0x89, 0x50, 0x4E}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := sniffCoverMime(tc.input)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("sniffCoverMime(%v) = (%q, %v), want (%q, %v)", tc.input, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
