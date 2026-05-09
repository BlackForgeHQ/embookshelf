// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"encoding/hex"
	"strings"
	"unicode/utf16"
)

// decodePDFLiteral handles a PDF string-literal payload that may be a
// plain ASCII / Latin-1 byte run or a UTF-16BE byte run prefixed with
// the BOM bytes 0xFE 0xFF. The caller has already stripped the
// surrounding parens and unescaped \( \) \\ \n \r \t sequences.
func decodePDFLiteral(b []byte) string {
	if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
		return decodeUTF16BE(b[2:])
	}
	return strings.TrimSpace(string(b))
}

// decodePDFHexString decodes the body of a PDF hex string `<…>` (just
// the hex digits, parens already stripped). When the bytes start with
// the UTF-16BE BOM (FEFF), they're decoded as UTF-16BE; otherwise
// returned as raw bytes interpreted as Latin-1-compatible ASCII.
//
// Per PDF spec, an odd number of hex digits is padded with a trailing
// '0' (so `<F>` ≡ `<F0>`).
func decodePDFHexString(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
			return r
		}
		return -1
	}, s)
	if len(s)%2 == 1 {
		s += "0"
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return ""
	}
	if len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF {
		return decodeUTF16BE(raw[2:])
	}
	return strings.TrimSpace(string(raw))
}

// decodeUTF16BE decodes a byte slice as UTF-16 big-endian. Returns the
// trimmed string. Length < 2 returns "".
func decodeUTF16BE(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	n := len(b) / 2
	u16 := make([]uint16, n)
	for i := 0; i < n; i++ {
		u16[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
	}
	return strings.TrimSpace(string(utf16.Decode(u16)))
}
