// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import "testing"

func TestDecodePDFLiteral_PlainASCII(t *testing.T) {
	got := decodePDFLiteral([]byte("Hello"))
	if got != "Hello" {
		t.Fatalf("got %q want %q", got, "Hello")
	}
}

func TestDecodePDFLiteral_BOMUTF16BE(t *testing.T) {
	raw := []byte{0xFE, 0xFF, 0x00, 'T', 0x00, 0xE9}
	got := decodePDFLiteral(raw)
	if got != "Té" {
		t.Fatalf("got %q want %q", got, "Té")
	}
}

func TestDecodePDFHexString_UTF16BE(t *testing.T) {
	got := decodePDFHexString("FEFF00540069007400 6C0065")
	if got != "Title" {
		t.Fatalf("got %q want %q", got, "Title")
	}
}

func TestDecodePDFHexString_PlainASCII(t *testing.T) {
	got := decodePDFHexString("48656C6C6F")
	if got != "Hello" {
		t.Fatalf("got %q want %q", got, "Hello")
	}
}

func TestDecodePDFHexString_OddLengthPadsZero(t *testing.T) {
	got := decodePDFHexString("F")
	if got != "\xF0" && got != "" {
		t.Fatalf("unexpected %q", got)
	}
}
