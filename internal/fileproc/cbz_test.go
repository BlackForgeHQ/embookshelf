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
// enough that SniffImageMime is happy and the cover bytes round-trip.
var fakePNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89,
}

// writeCBZ writes a CBZ archive to a temp file and returns its path.
// Used by tests that need the archive on disk rather than in memory.
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
// Used by ComicProcessor.Extract tests (no filesystem I/O).
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
	meta, err := ComicProcessor{}.Extract(context.Background(), src)
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
	meta, err := ComicProcessor{}.Extract(context.Background(), src)
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
	meta, err := ComicProcessor{}.Extract(context.Background(), src)
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
	if _, err := (ComicProcessor{}).Extract(context.Background(), src); err == nil {
		t.Fatal("expected error for image-less archive")
	}
}

// openPagesFor drives the paging seam over bytes already in hand. The
// opener hands back a source the set is free to close, so a caller can
// open the same archive twice — which is what a second request does.
func openPagesFor(cache *PageCache, key string, raw []byte) (*ComicPageSet, error) {
	return OpenComicPages(context.Background(), cache, key, func() (storage.Source, error) {
		return memSourceFromBytes(raw), nil
	})
}

// The page reader tells "these bytes are not a comic archive" from "this
// archive will not open", because the handler turns the first into a 415
// naming the containers it can page and the second into a 500. Since
// #310 every one of these is reachable from a book stamped CBZ: the
// comic aliases ingest, so a RAR on the shelf opens the comic reader —
// and since #329 it is *paged*, so a damaged RAR must get a damaged
// archive's answer rather than the one that says to convert the file.
func TestComicPagingTellsAnUnknownContainerFromADamagedOne(t *testing.T) {
	good := incompressibleCBZ(t, []string{"01.png"}, 64)

	cases := []struct {
		name        string
		raw         []byte
		wantUnknown bool // true: reads as ErrComicContainer
	}{
		// Nothing declares itself, so nothing can page it.
		{name: "not an archive at all", raw: bytes.Repeat([]byte("garbage!"), 8), wantUnknown: true},
		{name: "empty", raw: nil, wantUnknown: true},
		// Each container's own magic followed by nothing usable: a
		// damaged archive of a container we page, which keeps the
		// decoder's error rather than being told it is not a comic.
		{name: "damaged rar", raw: append([]byte("Rar!\x1a\x07\x01\x00"), bytes.Repeat([]byte{0}, 64)...), wantUnknown: false},
		{name: "damaged 7z", raw: append([]byte("7z\xbc\xaf\x27\x1c"), bytes.Repeat([]byte{0}, 64)...), wantUnknown: false},
		{name: "truncated zip", raw: good[:len(good)/2], wantUnknown: false},
		// The other magic a ZIP can start with — an archive with no
		// entries — truncated so the end-of-central-directory record it
		// heads is incomplete. Still a ZIP by its signature, so it takes
		// the damaged-archive route.
		{name: "truncated empty-archive record", raw: append([]byte("PK\x05\x06"), bytes.Repeat([]byte{0}, 8)...), wantUnknown: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cache := NewPageCache(t.TempDir(), 1<<20)
			set, err := openPagesFor(cache, "k", c.raw)
			if err == nil {
				_ = set.Close()
				t.Fatal("expected an error")
			}
			if got := errors.Is(err, ErrComicContainer); got != c.wantUnknown {
				t.Errorf("errors.Is(err, ErrComicContainer) = %v, want %v (err = %v)",
					got, c.wantUnknown, err)
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

	// A cache is wired and must stay untouched: ZIP does not join the
	// containers that need extracting, because expanding an archive that
	// already answers a numbered page with a range read would cost more
	// than it saves.
	cacheRoot := t.TempDir()
	cache := NewPageCache(cacheRoot, 1<<20)

	set, err := OpenComicPages(context.Background(), cache, "k", func() (storage.Source, error) {
		return store.Open(context.Background(), key)
	})
	if err != nil {
		t.Fatalf("OpenComicPages: %v", err)
	}
	defer func() { _ = set.Close() }()

	if set.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", set.Len())
	}
	if got := set.names; got[0] != "01.png" || got[1] != "02.png" || got[2] != "03.png" {
		t.Errorf("page order wrong: %v", got)
	}

	rc, mime, err := set.Page(1)
	if err != nil {
		t.Fatalf("Page(1): %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, rc); err != nil {
		t.Fatalf("copy page: %v", err)
	}
	_ = rc.Close()
	if mime != "image/png" {
		t.Errorf("mime = %q, want image/png", mime)
	}
	if got := buf.Bytes(); !bytes.Equal(got, imagePage("02.png", 128<<10)) {
		t.Errorf("page 1 body is not the bytes of 02.png (%d bytes read)", len(got))
	}

	if _, _, err := set.Page(99); err == nil {
		t.Error("expected an error for an out-of-range page")
	}

	if ents, rerr := os.ReadDir(cacheRoot); rerr == nil && len(ents) != 0 {
		t.Errorf("the ZIP arm wrote %d entries into the page cache", len(ents))
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
	return storage.CapObjectStore
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

// imagePage is pageFiller with a real PNG signature in front, so a page
// built from it sniffs as image/png (#331) while staying large and
// incompressible past the first 37 bytes.
func imagePage(name string, size int) []byte {
	return append(append([]byte{}, fakePNG...), pageFiller(name, size)...)
}

// incompressibleCBZ builds a CBZ whose entries are stored uncompressed,
// each a PNG-signed, `size`-byte page of filler.
func incompressibleCBZ(t *testing.T, names []string, size int) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range names {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(imagePage(name, size)); err != nil {
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
		"03.png": comicPage("three"),
		"01.png": comicPage("one"),
		"02.png": comicPage("two"),
	})
	fs, err := local.New(dir)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	set, err := OpenComicPages(context.Background(), nil, "", func() (storage.Source, error) {
		return fs.Open(context.Background(), "test.cbz")
	})
	if err != nil {
		t.Fatalf("OpenComicPages: %v", err)
	}
	defer func() { _ = set.Close() }()

	if set.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", set.Len())
	}
	if got := set.names; got[0] != "01.png" || got[2] != "03.png" {
		t.Errorf("page order wrong: %v", got)
	}

	rc, mime, err := set.Page(1)
	if err != nil {
		t.Fatalf("Page(1): %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, rc); err != nil {
		t.Fatalf("copy page: %v", err)
	}
	_ = rc.Close()
	if mime != "image/png" {
		t.Errorf("mime = %q", mime)
	}
	if want := comicPage("two"); !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("page 1 body = %q, want %q", buf.Bytes(), want)
	}

	if _, _, err := set.Page(99); err == nil {
		t.Error("expected error for out-of-range page")
	}
}

// A page whose entry name says image but whose bytes do not answers
// ErrComicPageNotImage rather than a stream typed from the name (#331).
// The .png name is what let this entry into comicPages' filter in the
// first place; the point of the sniff is that the filter's say-so is
// not enough to decide what a browser is told the bytes are.
func TestCBZPageWithNonImageBytesRefuses(t *testing.T) {
	dir := t.TempDir()
	writeCBZ(t, dir, map[string][]byte{
		"01.png": []byte("this is not an image, whatever its name claims"),
	})
	fs, err := local.New(dir)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	set, err := OpenComicPages(context.Background(), nil, "", func() (storage.Source, error) {
		return fs.Open(context.Background(), "test.cbz")
	})
	if err != nil {
		t.Fatalf("OpenComicPages: %v", err)
	}
	defer func() { _ = set.Close() }()

	rc, _, err := set.Page(0)
	if err == nil {
		_ = rc.Close()
		t.Fatal("expected an error for a page whose bytes are not an image")
	}
	if !errors.Is(err, ErrComicPageNotImage) {
		t.Errorf("err = %v, want errors.Is(err, ErrComicPageNotImage)", err)
	}
}

// A page entry shorter than the sniff window must still be typed
// correctly rather than crashing on a short read: PNG's magic is 8
// bytes and this entry is exactly that, nothing more.
func TestCBZPageShorterThanTheSniffWindow(t *testing.T) {
	dir := t.TempDir()
	writeCBZ(t, dir, map[string][]byte{
		"01.png": fakePNG[:8],
	})
	fs, err := local.New(dir)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	set, err := OpenComicPages(context.Background(), nil, "", func() (storage.Source, error) {
		return fs.Open(context.Background(), "test.cbz")
	})
	if err != nil {
		t.Fatalf("OpenComicPages: %v", err)
	}
	defer func() { _ = set.Close() }()

	rc, mime, err := set.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}
	defer func() { _ = rc.Close() }()
	if mime != "image/png" {
		t.Errorf("mime = %q, want image/png", mime)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read page: %v", err)
	}
	if !bytes.Equal(got, fakePNG[:8]) {
		t.Errorf("body = %x, want %x", got, fakePNG[:8])
	}
}
