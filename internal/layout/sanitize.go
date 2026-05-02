// Package layout owns the rules for placing Books on disk under the
// managed library layout `{library_root}/{Author}/{Title}/{filename}`
// (ADR-0003, docs/spec/library-layout.spec.md). The package is
// pure — no I/O — so callers can compose path decisions from the
// placer, metadata writer, and scanner without coupling those modules
// to filesystem operations.
package layout

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// MaxSegmentBytes is the upper bound applied to each path segment after
// sanitization. Picked at 200 to leave headroom under the 255-byte ext4
// and APFS limits while accommodating extension suffixes and collision
// suffixes (` (2)`, ` (3)`, …) added by the placer.
const MaxSegmentBytes = 200

// FallbackAuthor and FallbackTitle are the literal sentinel folder names
// used when the corresponding Book field is empty. They are reserved
// values; users editing a Book to "Unknown Author" or "Untitled"
// produce an indistinguishable folder name, which is acceptable
// (per ADR-0003 §2).
const (
	FallbackAuthor = "Unknown Author"
	FallbackTitle  = "Untitled"
)

// reservedNTFS holds Windows-reserved base names that cannot appear as
// a directory component on NTFS volumes. We do not run on Windows but
// honor the list so that backups synced from a managed library can be
// restored on a Windows host without rewriting paths.
var reservedNTFS = map[string]struct{}{
	"CON": {}, "PRN": {}, "AUX": {}, "NUL": {},
	"COM1": {}, "COM2": {}, "COM3": {}, "COM4": {}, "COM5": {},
	"COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
	"LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {}, "LPT5": {},
	"LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
}

// SanitizeSegment makes s safe to use as a single path segment under
// the managed layout. The returned string never contains any of:
// path separators, NTFS-illegal characters, control bytes, leading or
// trailing dots/whitespace. It is NFC-normalized and capped at
// MaxSegmentBytes UTF-8 bytes.
//
// fallback is returned verbatim when sanitization yields the empty
// string. Pass FallbackAuthor or FallbackTitle for the canonical
// sentinels.
func SanitizeSegment(s, fallback string) string {
	if s == "" {
		return fallback
	}

	s = norm.NFC.String(s)

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' ||
			r == '"' || r == '<' || r == '>' || r == '|':
			b.WriteByte('_')
		case r < 0x20 || r == 0x7f:
			b.WriteByte('_')
		case unicode.IsSpace(r):
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}

	out := strings.TrimFunc(b.String(), func(r rune) bool {
		return r == '.' || unicode.IsSpace(r)
	})

	if out == "" {
		return fallback
	}

	out = truncateUTF8(out, MaxSegmentBytes)

	out = strings.TrimRightFunc(out, func(r rune) bool {
		return r == '.' || unicode.IsSpace(r)
	})

	if out == "" {
		return fallback
	}

	if _, reserved := reservedNTFS[strings.ToUpper(out)]; reserved {
		out += "_"
	}

	return out
}

// SanitizeAuthor wraps SanitizeSegment with FallbackAuthor.
func SanitizeAuthor(author string) string {
	return SanitizeSegment(author, FallbackAuthor)
}

// SanitizeTitle wraps SanitizeSegment with FallbackTitle.
func SanitizeTitle(title string) string {
	return SanitizeSegment(title, FallbackTitle)
}

// truncateUTF8 returns s truncated to at most n bytes, walking back to
// the nearest rune boundary so the result is always valid UTF-8.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
