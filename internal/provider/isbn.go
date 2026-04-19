package provider

import "strings"

// normalizeISBN strips hyphens, spaces, and lowercases the trailing X
// check character into uppercase. Returns "" when nothing usable
// remains.
func normalizeISBN(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			out = append(out, c)
		case c == 'x' || c == 'X':
			out = append(out, 'X')
		}
	}
	return string(out)
}

// toISBN10 returns the ISBN-10 form of the given ISBN. Accepts ISBN-10
// directly or the 978-prefixed ISBN-13 variant; anything else returns
// "". Used by the Amazon cover provider, which keys off ISBN-10.
func toISBN10(s string) string {
	n := normalizeISBN(s)
	switch len(n) {
	case 10:
		return n
	case 13:
		// Only 978 ISBN-13s map cleanly back to ISBN-10. The 979
		// prefix has no ISBN-10 equivalent.
		if !strings.HasPrefix(n, "978") {
			return ""
		}
		core := n[3:12]
		check := isbn10Check(core)
		return core + check
	}
	return ""
}

// isbn10Check computes the ISBN-10 check character from the first 9
// digits. Return value is "0".."9" or "X".
func isbn10Check(nine string) string {
	if len(nine) != 9 {
		return ""
	}
	sum := 0
	for i := 0; i < 9; i++ {
		c := nine[i]
		if c < '0' || c > '9' {
			return ""
		}
		sum += int(c-'0') * (10 - i)
	}
	rem := (11 - (sum % 11)) % 11
	if rem == 10 {
		return "X"
	}
	return string(rune('0' + rem))
}
