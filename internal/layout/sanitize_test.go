package layout

import (
	"strings"
	"testing"
)

func TestSanitizeSegment(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		fallback string
		want     string
	}{
		{"plain ascii", "Tolkien", FallbackAuthor, "Tolkien"},
		{"plain title", "The Hobbit", FallbackTitle, "The Hobbit"},
		{"empty falls back", "", FallbackTitle, "Untitled"},
		{"whitespace only falls back", "   ", FallbackAuthor, "Unknown Author"},
		{"dots only falls back", "....", FallbackTitle, "Untitled"},

		{"slash replaced", "a/b", FallbackTitle, "a_b"},
		{"backslash replaced", "a\\b", FallbackTitle, "a_b"},
		{"colon replaced", "a:b", FallbackTitle, "a_b"},
		{"all illegal chars", `a/\:*?"<>|b`, FallbackTitle, "a_________b"},

		{"control bytes replaced", "a\x00b\x1fc", FallbackTitle, "a_b_c"},
		{"DEL replaced", "a\x7fb", FallbackTitle, "a_b"},

		{"trim leading dots", "...foo", FallbackTitle, "foo"},
		{"trim trailing dots", "foo...", FallbackTitle, "foo"},
		{"trim leading whitespace", "  foo", FallbackTitle, "foo"},
		{"trim trailing whitespace", "foo   ", FallbackTitle, "foo"},
		{"interior dots preserved", "foo.bar.baz", FallbackTitle, "foo.bar.baz"},
		{"interior whitespace preserved", "Frank Herbert", FallbackAuthor, "Frank Herbert"},

		{"NTFS CON suffixed", "CON", FallbackTitle, "CON_"},
		{"NTFS lowercase con suffixed", "con", FallbackTitle, "con_"},
		{"NTFS COM4 suffixed", "COM4", FallbackTitle, "COM4_"},
		{"non-reserved similar name passes", "CONS", FallbackTitle, "CONS"},
		{"reserved-with-extension passes", "CON.txt", FallbackTitle, "CON.txt"},

		{"multi-author preserved", "A & B", FallbackAuthor, "A & B"},
		{"comma-list preserved", "A, B, C", FallbackAuthor, "A, B, C"},

		{"unicode preserved", "Антон Чехов", FallbackAuthor, "Антон Чехов"},
		{"emoji preserved", "📚 Library", FallbackTitle, "📚 Library"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeSegment(tc.in, tc.fallback)
			if got != tc.want {
				t.Fatalf("SanitizeSegment(%q, %q) = %q, want %q", tc.in, tc.fallback, got, tc.want)
			}
		})
	}
}

func TestSanitizeSegment_LengthCap(t *testing.T) {
	long := strings.Repeat("a", MaxSegmentBytes+50)
	got := SanitizeSegment(long, FallbackTitle)
	if len(got) > MaxSegmentBytes {
		t.Fatalf("length=%d, want ≤ %d", len(got), MaxSegmentBytes)
	}
	if got != strings.Repeat("a", MaxSegmentBytes) {
		t.Fatalf("expected %d 'a' bytes, got %q", MaxSegmentBytes, got)
	}
}

func TestSanitizeSegment_LengthCapAtRuneBoundary(t *testing.T) {
	// 100 × 4-byte runes = 400 bytes. Cap=200 must land on a rune
	// boundary, i.e. the result is a multiple of 4 bytes (50 runes
	// = 200 bytes).
	long := strings.Repeat("📚", 100)
	got := SanitizeSegment(long, FallbackTitle)
	if len(got) > MaxSegmentBytes {
		t.Fatalf("length=%d, want ≤ %d", len(got), MaxSegmentBytes)
	}
	want := strings.Repeat("📚", 50)
	if got != want {
		t.Fatalf("got %q (%d bytes), want %q (%d bytes)", got, len(got), want, len(want))
	}
}

func TestSanitizeSegment_TruncationExposesTrailingDot(t *testing.T) {
	// Build a segment that, when truncated to 200 bytes, ends in a
	// dot. The post-truncation trim must strip it.
	prefix := strings.Repeat("a", MaxSegmentBytes-1)
	in := prefix + "." + "more"
	got := SanitizeSegment(in, FallbackTitle)
	if strings.HasSuffix(got, ".") {
		t.Fatalf("result %q has trailing dot after truncation", got)
	}
	if got != prefix {
		t.Fatalf("got %q, want %q", got, prefix)
	}
}

func TestSanitizeSegment_NFCNormalization(t *testing.T) {
	// "é" in NFD form: e (U+0065) + combining acute (U+0301).
	// "é" in NFC form: U+00E9 (single codepoint, 2 bytes).
	nfd := "Fiancé"
	nfc := "Fiancé"
	got := SanitizeSegment(nfd, FallbackTitle)
	if got != nfc {
		t.Fatalf("NFD input not normalized: got %q, want %q", got, nfc)
	}
}

func TestSanitizeAuthor_Title(t *testing.T) {
	if got := SanitizeAuthor(""); got != FallbackAuthor {
		t.Fatalf("SanitizeAuthor(\"\") = %q, want %q", got, FallbackAuthor)
	}
	if got := SanitizeTitle(""); got != FallbackTitle {
		t.Fatalf("SanitizeTitle(\"\") = %q, want %q", got, FallbackTitle)
	}
	if got := SanitizeAuthor("Tolkien"); got != "Tolkien" {
		t.Fatalf("SanitizeAuthor(\"Tolkien\") = %q", got)
	}
}
