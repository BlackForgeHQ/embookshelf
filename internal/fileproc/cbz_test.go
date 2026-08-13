// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
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

// cbzSourceInOrder is cbzSource with the entries written in the order
// given rather than in Go's map order — which the cross-container parity
// test needs, since the ComicInfo and cover.* rules take the first match
// in archive order.
func cbzSourceInOrder(t *testing.T, entries []comicEntry) *memSource {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.data); err != nil {
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

// The page reader tells "these bytes are not a ZIP" from "this ZIP will
// not open", because the handler turns the first into a 415 saying pages
// come from .cbz only and the second into a 500. Since #310 both are
// reachable from a book stamped CBZ: the comic aliases ingest now, so a
// RAR on the shelf opens the comic reader, and a damaged .cbz must not be
// told it is a RAR.
func TestCBZPagesClassifiesNonZIPFromDamagedZIP(t *testing.T) {
	good := incompressibleCBZ(t, []string{"01.png"}, 64)

	cases := []struct {
		name     string
		raw      []byte
		wantKind bool // true: reads as ErrComicNotZIP
	}{
		{name: "rar bytes", raw: append([]byte("Rar!\x1a\x07\x01\x00"), bytes.Repeat([]byte{0}, 64)...), wantKind: true},
		{name: "7z bytes", raw: append([]byte("7z\xbc\xaf\x27\x1c"), bytes.Repeat([]byte{0}, 64)...), wantKind: true},
		{name: "empty", raw: nil, wantKind: true},
		{name: "truncated zip", raw: good[:len(good)/2], wantKind: false},
		// The other magic a ZIP can start with — an archive with no
		// entries — truncated so the end-of-central-directory record it
		// heads is incomplete. Still a ZIP by its signature, so it takes
		// the damaged-archive route, which is the second half of
		// hasZipMagic that the cases above leave untouched.
		{name: "truncated empty-archive record", raw: append([]byte("PK\x05\x06"), bytes.Repeat([]byte{0}, 8)...), wantKind: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := memSourceFromBytes(c.raw)
			defer func() { _ = src.Close() }()

			_, err := CBZPages(src)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := errors.Is(err, ErrComicNotZIP); got != c.wantKind {
				t.Errorf("errors.Is(err, ErrComicNotZIP) = %v, want %v (err = %v)", got, c.wantKind, err)
			}

			_, err = CBZPage(src, 0, io.Discard)
			if err == nil {
				t.Fatal("expected an error from the page read too")
			}
			if got := errors.Is(err, ErrComicNotZIP); got != c.wantKind {
				t.Errorf("page: errors.Is(err, ErrComicNotZIP) = %v, want %v (err = %v)", got, c.wantKind, err)
			}
		})
	}
}

// An object store is the one place a CBZ could live that the page reader
// could not reach: CBZPages and CBZPage took a filesystem path, so comic
// pagination on an S3-backed library had no call that could even be
// written — the same shape as the library scan that skipped every
// object-store library while reporting success.
//
// The counting store answers the second question at the same time. A page
// read is random access into a zip: the reader wants the central
// directory at the tail and one entry's bytes, and pulling the whole
// archive for each page is a different thing that happens to return the
// same image. The assertion below is what separates them.
func TestCBZPagesAndPageOverAnObjectStore(t *testing.T) {
	store := &countingObjectStore{objects: map[string][]byte{}}
	const key = "Brian K. Vaughan/Saga 1/saga-01.cbz"
	store.objects[key] = incompressibleCBZ(t, []string{"03.png", "01.png", "02.png"}, 128<<10)
	archiveSize := int64(len(store.objects[key]))

	src, err := store.Open(context.Background(), key)
	if err != nil {
		t.Fatalf("open object: %v", err)
	}
	defer func() { _ = src.Close() }()

	pages, err := CBZPages(src)
	if err != nil {
		t.Fatalf("CBZPages: %v", err)
	}
	if len(pages) != 3 {
		t.Fatalf("len(pages) = %d, want 3", len(pages))
	}
	if pages[0] != "01.png" || pages[1] != "02.png" || pages[2] != "03.png" {
		t.Errorf("page order wrong: %v", pages)
	}

	var buf bytes.Buffer
	mime, err := CBZPage(src, 1, &buf)
	if err != nil {
		t.Fatalf("CBZPage(1): %v", err)
	}
	if mime != "image/png" {
		t.Errorf("mime = %q, want image/png", mime)
	}
	if got := buf.Bytes(); !bytes.Equal(got, pageFiller("02.png", 128<<10)) {
		t.Errorf("page 1 body is not the bytes of 02.png (%d bytes read)", len(got))
	}

	if _, err := CBZPage(src, 99, io.Discard); err == nil {
		t.Error("expected an error for an out-of-range page")
	}

	// One page out of three, plus two directory reads — a third of the
	// archive and change. A reader that downloads the object to serve a
	// page would be at or above the whole archive here, and one that
	// downloads it per call would be at three times it.
	if store.read > archiveSize/2 {
		t.Errorf("listing + one page pulled %d bytes of a %d-byte archive — "+
			"the reader is downloading the whole object per page",
			store.read, archiveSize)
	}
}

// countingObjectStore is a storage.Storage that reports itself an object
// store and records every byte its Sources hand back, so a test can hold
// the reader to range reads rather than a download.
type countingObjectStore struct {
	storage.Storage
	objects map[string][]byte
	read    int64
}

func (s *countingObjectStore) Capabilities() storage.Capability {
	return storage.CapObjectStore | storage.CapRange
}

func (s *countingObjectStore) Open(_ context.Context, key string) (storage.Source, error) {
	b, ok := s.objects[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return &countingSource{Reader: bytes.NewReader(b), size: int64(len(b)), store: s}, nil
}

type countingSource struct {
	*bytes.Reader
	size  int64
	store *countingObjectStore
}

func (c *countingSource) Size() int64  { return c.size }
func (c *countingSource) Close() error { return nil }

func (c *countingSource) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.Reader.ReadAt(p, off)
	c.store.read += int64(n)
	return n, err
}

// pageFiller is deterministic per-name filler that does not compress, so
// the archive built from it is as large as its pages and a full download
// is measurably different from a range read.
func pageFiller(name string, size int) []byte {
	out := make([]byte, size)
	// xorshift seeded from the name: no dependency on the global rand,
	// and every page differs from every other.
	var state uint32 = 2166136261
	for i := 0; i < len(name); i++ {
		state = (state ^ uint32(name[i])) * 16777619
	}
	for i := range out {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		out[i] = byte(state)
	}
	return out
}

// incompressibleCBZ builds a CBZ whose entries are stored uncompressed,
// each `size` bytes of filler.
func incompressibleCBZ(t *testing.T, names []string, size int) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range names {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(pageFiller(name, size)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The local library, through the same reader and the backend a local
// install actually boots. Unchanged behaviour is the point: one page
// listing, one page's bytes, one out-of-range error.
func TestCBZPagesAndPage(t *testing.T) {
	dir := t.TempDir()
	writeCBZ(t, dir, map[string][]byte{
		"03.png": []byte("three"),
		"01.png": []byte("one"),
		"02.png": []byte("two"),
	})
	fs, err := local.New(dir)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	src, err := fs.Open(context.Background(), "test.cbz")
	if err != nil {
		t.Fatalf("open cbz: %v", err)
	}
	defer func() { _ = src.Close() }()

	pages, err := CBZPages(src)
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
	mime, err := CBZPage(src, 1, &buf)
	if err != nil {
		t.Fatalf("CBZPage(1): %v", err)
	}
	if mime != "image/png" {
		t.Errorf("mime = %q", mime)
	}
	if buf.String() != "two" {
		t.Errorf("page 1 body = %q, want 'two'", buf.String())
	}

	if _, err := CBZPage(src, 99, io.Discard); err == nil {
		t.Error("expected error for out-of-range page")
	}
}
