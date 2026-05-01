package fileproc

import (
	"bytes"
	"context"
	"fmt"
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

// makeMinimalPDF returns a minimal valid PDF containing one page
// and an /Info dict with a single /CreationDate field. Tests use it
// to assert (a) /Info edits land via incremental update, and (b)
// /CreationDate survives the edit.
func makeMinimalPDF(t *testing.T) []byte {
	t.Helper()
	body := []byte("%PDF-1.4\n%âãÏÓ\n" +
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n" +
		"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n" +
		"3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Contents 0 0 R >>\nendobj\n" +
		"4 0 obj\n<< /CreationDate (D:20240101120000Z) >>\nendobj\n")
	xrefStart := len(body)
	xref := []byte(fmt.Sprintf(
		"xref\n0 5\n"+
			"0000000000 65535 f \n"+
			"%010d 00000 n \n"+
			"%010d 00000 n \n"+
			"%010d 00000 n \n"+
			"%010d 00000 n \n",
		bytes.Index(body, []byte("1 0 obj")),
		bytes.Index(body, []byte("2 0 obj")),
		bytes.Index(body, []byte("3 0 obj")),
		bytes.Index(body, []byte("4 0 obj")),
	))
	trailer := []byte(fmt.Sprintf(
		"trailer\n<< /Size 5 /Root 1 0 R /Info 4 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		xrefStart,
	))
	out := append(body, xref...)
	out = append(out, trailer...)
	return out
}

func TestMinimalPDF_Parses(t *testing.T) {
	data := makeMinimalPDF(t)
	src := newBytesSource(data)
	defer func() { _ = src.Close() }()
	if _, err := (PDFProcessor{}).Extract(context.Background(), src); err != nil {
		t.Errorf("Extract: %v", err)
	}
}

func TestPDFEmbedder_Embed_RoundTrip(t *testing.T) {
	original := makeMinimalPDF(t)
	src := newBytesSource(original)
	defer func() { _ = src.Close() }()

	in := EmbedInput{
		Title:       "Curated PDF",
		Author:      "Curated Author",
		Description: "A PDF.",
		Tags:        []string{"tech"},
		Genres:      []string{"reference"},
	}
	out, err := PDFEmbedder{}.Embed(context.Background(), src, in)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	// Round-trip via reader is best-effort because PDFProcessor's regex
	// only handles literal strings, not hex strings. Assert structural
	// markers in the output bytes.
	if !bytes.Contains(out, []byte("/Title <FEFF")) {
		t.Error("output missing hex-encoded /Title")
	}
	if !bytes.Contains(out, []byte("/Author <FEFF")) {
		t.Error("output missing hex-encoded /Author")
	}
	if !bytes.Contains(out, []byte("/Keywords <FEFF")) {
		t.Error("output missing hex-encoded /Keywords")
	}

	src2 := newBytesSource(out)
	defer func() { _ = src2.Close() }()
	if _, err := (PDFProcessor{}).Extract(context.Background(), src2); err != nil {
		t.Fatalf("Extract after Embed: %v", err)
	}
}

func TestPDFEmbedder_Embed_PreservesCreationDate(t *testing.T) {
	original := makeMinimalPDF(t)
	src := newBytesSource(original)
	defer func() { _ = src.Close() }()

	out, err := PDFEmbedder{}.Embed(context.Background(), src, EmbedInput{Title: "T"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if !bytes.HasPrefix(out, original) {
		t.Error("Embed must not modify original bytes (incremental update only)")
	}
	if !bytes.Contains(out[:len(original)], []byte("/CreationDate (D:20240101120000Z)")) {
		t.Error("/CreationDate must survive in the original prefix")
	}
}

func TestDispatchEmbedder_PDF(t *testing.T) {
	emb, err := DispatchEmbedder("PDF")
	if err != nil {
		t.Fatalf("DispatchEmbedder(PDF): %v", err)
	}
	if _, ok := emb.(PDFEmbedder); !ok {
		t.Errorf("got %T, want PDFEmbedder", emb)
	}
}
