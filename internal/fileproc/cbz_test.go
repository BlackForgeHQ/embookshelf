package fileproc

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestNaturalLess(t *testing.T) {
	cases := []struct {
		a, b string
		less bool
	}{
		{"page2.jpg", "page10.jpg", true},  // numeric run sorts numerically
		{"page10.jpg", "page2.jpg", false}, // reverse
		{"a.jpg", "b.jpg", true},           // pure lex
		{"chapter1/p01.jpg", "chapter2/p01.jpg", true},
		{"01.png", "10.png", true}, // zero-padded sequence sorts numerically
	}
	for _, tc := range cases {
		if got := naturalLess(tc.a, tc.b); got != tc.less {
			t.Errorf("naturalLess(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.less)
		}
	}
}

// fakePNG is the minimal 1x1 PNG signature + IHDR + IDAT + IEND. Just
// enough that mimeFromExt is happy and the cover bytes round-trip.
var fakePNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89,
}

// writeCBZ writes a CBZ archive to a temp file and returns its path.
// Used by tests that need a real path (CBZPages, CBZPage).
func writeCBZ(t *testing.T, dir string, entries map[string][]byte) string {
	t.Helper()
	p := filepath.Join(dir, "test.cbz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// cbzSource builds a CBZ archive in memory and returns a memSource backed by its bytes.
// Used by CBZProcessor.Extract tests (no filesystem I/O).
func cbzSource(t *testing.T, entries map[string][]byte) *memSource {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	return &memSource{Reader: bytes.NewReader(b), size: int64(len(b))}
}

func TestCBZExtract_BasicCover(t *testing.T) {
	src := cbzSource(t, map[string][]byte{
		"page10.png": []byte("ten"),  // out-of-order: tests natural sort
		"page2.png":  fakePNG,        // should win as cover (page 2 < page 10)
		"notes.txt":  []byte("skip"), // non-image, ignored
	})
	defer func() { _ = src.Close() }()
	meta, err := CBZProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.Format != "CBZ" {
		t.Errorf("Format = %q, want CBZ", meta.Format)
	}
	if !meta.HasCover {
		t.Fatal("expected HasCover")
	}
	if !bytes.Equal(meta.CoverBytes, fakePNG) {
		t.Error("CoverBytes != page2.png — natural sort failed")
	}
	if meta.CoverMime != "image/png" {
		t.Errorf("CoverMime = %q, want image/png", meta.CoverMime)
	}
}

func TestCBZExtract_PreferredCover(t *testing.T) {
	src := cbzSource(t, map[string][]byte{
		"01.png":    []byte("first-page"),
		"cover.png": fakePNG, // should win over 01.png
	})
	defer func() { _ = src.Close() }()
	meta, err := CBZProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !bytes.Equal(meta.CoverBytes, fakePNG) {
		t.Error("CoverBytes != cover.png — preferred-cover lookup failed")
	}
}

func TestCBZExtract_ComicInfo(t *testing.T) {
	info := []byte(`<?xml version="1.0"?>
<ComicInfo>
  <Series>Saga</Series>
  <Number>1</Number>
  <Writer>Brian K. Vaughan</Writer>
  <Summary>Test summary.</Summary>
  <LanguageISO>en</LanguageISO>
</ComicInfo>`)
	src := cbzSource(t, map[string][]byte{
		"01.png":        fakePNG,
		"ComicInfo.xml": info,
	})
	defer func() { _ = src.Close() }()
	meta, err := CBZProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.Title != "Saga #1" {
		t.Errorf("Title = %q, want %q", meta.Title, "Saga #1")
	}
	if meta.Author != "Brian K. Vaughan" {
		t.Errorf("Author = %q", meta.Author)
	}
	if meta.Description != "Test summary." {
		t.Errorf("Description = %q", meta.Description)
	}
	if meta.Language != "en" {
		t.Errorf("Language = %q", meta.Language)
	}
}

func TestCBZExtract_NoImagesFails(t *testing.T) {
	src := cbzSource(t, map[string][]byte{
		"readme.txt": []byte("nothing here"),
	})
	defer func() { _ = src.Close() }()
	if _, err := (CBZProcessor{}).Extract(context.Background(), src); err == nil {
		t.Fatal("expected error for image-less archive")
	}
}

func TestCBZPagesAndPage(t *testing.T) {
	dir := t.TempDir()
	cbz := writeCBZ(t, dir, map[string][]byte{
		"03.png": []byte("three"),
		"01.png": []byte("one"),
		"02.png": []byte("two"),
	})
	pages, err := CBZPages(cbz)
	if err != nil {
		t.Fatalf("CBZPages: %v", err)
	}
	if len(pages) != 3 {
		t.Fatalf("len(pages) = %d, want 3", len(pages))
	}
	if pages[0] != "01.png" || pages[2] != "03.png" {
		t.Errorf("page order wrong: %v", pages)
	}

	var buf bytes.Buffer
	mime, err := CBZPage(cbz, 1, &buf)
	if err != nil {
		t.Fatalf("CBZPage(1): %v", err)
	}
	if mime != "image/png" {
		t.Errorf("mime = %q", mime)
	}
	if buf.String() != "two" {
		t.Errorf("page 1 body = %q, want 'two'", buf.String())
	}

	if _, err := CBZPage(cbz, 99, io.Discard); err == nil {
		t.Error("expected error for out-of-range page")
	}
}
