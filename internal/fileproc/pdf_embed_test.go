package fileproc

import (
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
