package fileproc

import (
	"bytes"
	"strings"
	"testing"
)

func TestEncodePDFString_ASCII(t *testing.T) {
	got := encodePDFString("Hello")
	want := "<FEFF00480065006C006C006F>"
	if !strings.EqualFold(got, want) {
		t.Errorf("encodePDFString(Hello) = %q, want %q", got, want)
	}
}

func TestEncodePDFString_NonASCII(t *testing.T) {
	got := encodePDFString("café")
	want := "<FEFF00630061006600E9>"
	if !strings.EqualFold(got, want) {
		t.Errorf("encodePDFString(café) = %q, want %q", got, want)
	}
}

func TestEncodePDFString_Empty(t *testing.T) {
	got := encodePDFString("")
	want := "<FEFF>"
	if !strings.EqualFold(got, want) {
		t.Errorf("encodePDFString(\"\") = %q, want %q", got, want)
	}
}

func TestFindStartxref_Standard(t *testing.T) {
	pdf := []byte("%PDF-1.4\n" +
		"1 0 obj\n<<>>\nendobj\n" +
		"xref\n0 2\n0000000000 65535 f \n0000000009 00000 n \ntrailer\n<< /Size 2 >>\n" +
		"startxref\n34\n" +
		"%%EOF\n")
	got, err := findStartxref(pdf)
	if err != nil {
		t.Fatalf("findStartxref: %v", err)
	}
	if got != 34 {
		t.Errorf("offset=%d want 34", got)
	}
}

func TestFindStartxref_NoEOF(t *testing.T) {
	_, err := findStartxref([]byte("%PDF-1.4\n"))
	if err == nil {
		t.Fatal("want error for missing trailer EOF marker, got nil")
	}
}

func TestFindInfoRef_Present(t *testing.T) {
	trailer := []byte("trailer\n<< /Size 5 /Root 1 0 R /Info 4 0 R >>\nstartxref\n100\n%%EOF\n")
	num, gen, ok := findInfoRef(trailer)
	if !ok {
		t.Fatal("findInfoRef: ok=false, want true")
	}
	if num != 4 || gen != 0 {
		t.Errorf("got num=%d gen=%d, want 4 0", num, gen)
	}
}

func TestFindInfoRef_Absent(t *testing.T) {
	trailer := []byte("trailer\n<< /Size 5 /Root 1 0 R >>\nstartxref\n100\n%%EOF\n")
	_, _, ok := findInfoRef(trailer)
	if ok {
		t.Error("findInfoRef: ok=true, want false")
	}
}

func TestNextObjectNumber(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		comment string
	}{
		{"trailer\n<< /Size 5 >>", 5, "Size=5 → next object is #5 (objs 0..4 in use)"},
		{"trailer\n<< /Size 1 /Root 1 0 R >>", 1, "Size=1 → next is #1"},
		{"trailer\n<< /Foo 0 0 R >>", 1, "no /Size → fallback to 1"},
	}
	for _, c := range cases {
		if got := nextObjectNumber([]byte(c.in)); got != c.want {
			t.Errorf("%s: got %d want %d", c.comment, got, c.want)
		}
	}
}

func TestBuildInfoBody_AllFields(t *testing.T) {
	in := EmbedInput{
		Title:       "T",
		Author:      "A",
		Description: "D",
		Tags:        []string{"a", "b"},
		Genres:      []string{"g1"},
	}
	got := buildInfoBody(in)
	for _, want := range []string{
		"/Title <FEFF",
		"/Author <FEFF",
		"/Subject <FEFF",
		"/Keywords <FEFF",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("body missing %q\n%s", want, got)
		}
	}
	want := encodePDFString("tag:a, tag:b, genre:g1")
	if !strings.Contains(got, "/Keywords "+want) {
		t.Errorf("Keywords payload mismatch.\nwant suffix: %s\ngot:\n%s", want, got)
	}
}

func TestBuildInfoBody_OmitsEmpty(t *testing.T) {
	got := buildInfoBody(EmbedInput{Title: "Only"})
	if strings.Contains(got, "/Author") {
		t.Error("/Author should be omitted on empty input")
	}
	if strings.Contains(got, "/Subject") {
		t.Error("/Subject should be omitted on empty input")
	}
	if strings.Contains(got, "/Keywords") {
		t.Error("/Keywords should be omitted on empty input")
	}
}

func TestBuildInfoBody_NeverWritesCreationDate(t *testing.T) {
	in := EmbedInput{
		Title:         "T",
		PublishedDate: "2024-01-01",
	}
	got := buildInfoBody(in)
	if strings.Contains(got, "/CreationDate") {
		t.Errorf("/CreationDate must never be written by buildInfoBody\n%s", got)
	}
}

func TestBuildIncrementalUpdate_StructureValid(t *testing.T) {
	pdf := []byte("%PDF-1.4\n" +
		"1 0 obj\n<<>>\nendobj\n" +
		"xref\n0 2\n0000000000 65535 f \n0000000009 00000 n \ntrailer\n<< /Size 2 /Root 1 0 R >>\n" +
		"startxref\n34\n%%EOF\n")

	out, err := buildIncrementalUpdate(pdf, EmbedInput{Title: "X", Author: "Y"})
	if err != nil {
		t.Fatalf("buildIncrementalUpdate: %v", err)
	}
	if !bytes.HasPrefix(out, pdf) {
		t.Error("output doesn't start with original PDF prefix")
	}
	if !bytes.HasSuffix(out, []byte("%%EOF\n")) {
		t.Errorf("output doesn't end with EOF marker\n%s", out[len(out)-30:])
	}
	want := [][]byte{
		[]byte("2 0 obj\n"),
		[]byte("/Title <FEFF"),
		[]byte("/Author <FEFF"),
		[]byte("/Prev 34"),
		[]byte("/Info 2 0 R"),
		[]byte("xref\n2 1\n"),
	}
	for _, w := range want {
		if !bytes.Contains(out, w) {
			t.Errorf("output missing %q", w)
		}
	}
}
