// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
)

// comicInfoFixtureXML is the ComicInfo.xml the CBR and CB7 fixtures both
// carry, so "the same metadata comes out of all three containers" is a
// comparison of like with like.
const comicInfoFixtureXML = `<?xml version="1.0"?>
<ComicInfo>
  <Series>Saga</Series>
  <Number>1</Number>
  <Writer>Brian K. Vaughan</Writer>
  <Summary>Test summary.</Summary>
  <LanguageISO>en</LanguageISO>
</ComicInfo>`

func assertComicInfoFixture(t *testing.T, meta Metadata) {
	t.Helper()
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

// The requirement #310 is really made of: a comic packed as ZIP, RAR or
// 7z is the same comic, so the three processors must produce the same
// Metadata down to the cover bytes. Asserted against each other rather
// than against three separately-written expectations, because it is the
// agreement that is the requirement — a rule that changed in comic.go
// would move all three answers together and this would still hold, while
// a rule that grew a second copy in one container would not.
func TestComicContainersAgree(t *testing.T) {
	entries := map[string][]byte{
		"ComicInfo.xml": []byte(comicInfoFixtureXML),
		"notes.txt":     []byte("skip"),
		"page10.png":    []byte("ten"),
		"page2.png":     []byte("page-two"),
	}

	cbz := cbzSource(t, entries)
	defer func() { _ = cbz.Close() }()
	cbr := cbrSource(t, comicFixtureEntries()...)
	defer func() { _ = cbr.Close() }()
	cb7 := memSourceFromFile(t, cb7Fixture)
	defer func() { _ = cb7.Close() }()

	ctx := context.Background()
	fromCBZ, err := CBZProcessor{}.Extract(ctx, cbz)
	if err != nil {
		t.Fatalf("cbz: %v", err)
	}
	fromCBR, err := CBRProcessor{}.Extract(ctx, cbr)
	if err != nil {
		t.Fatalf("cbr: %v", err)
	}
	fromCB7, err := CB7Processor{}.Extract(ctx, cb7)
	if err != nil {
		t.Fatalf("cb7: %v", err)
	}

	if string(fromCBZ.CoverBytes) != "page-two" {
		t.Fatalf("cbz cover = %q — the fixtures disagree before the comparison starts", fromCBZ.CoverBytes)
	}
	if !reflect.DeepEqual(fromCBZ, fromCBR) {
		t.Errorf("cbr metadata differs from cbz:\n cbz = %+v\n cbr = %+v", fromCBZ, fromCBR)
	}
	if !reflect.DeepEqual(fromCBZ, fromCB7) {
		t.Errorf("cb7 metadata differs from cbz:\n cbz = %+v\n cb7 = %+v", fromCBZ, fromCB7)
	}
}

// fakeComicArchive stands in for a container so the pass above it can be
// tested on its own — the two failure shapes a real archive can produce
// (one unreadable entry, and a whole archive that will not read) without
// having to craft bytes that produce them.
type fakeComicArchive struct {
	names []string
	files map[string][]byte
	err   error
}

func (f *fakeComicArchive) entries() []string { return f.names }

func (f *fakeComicArchive) read(want map[string]int64) (map[string][]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := map[string][]byte{}
	for name := range want {
		if b, ok := f.files[name]; ok {
			out[name] = b
		}
	}
	return out, nil
}

// A cover that will not read leaves the book cover-less rather than
// failing the import — EPUB's, FB2's and MOBI's contract, held here too.
func TestExtractComic_UnreadableCoverDegrades(t *testing.T) {
	a := &fakeComicArchive{
		names: []string{"01.png", "ComicInfo.xml"},
		files: map[string][]byte{"ComicInfo.xml": []byte(comicInfoFixtureXML)},
	}
	meta, err := extractComic("cbz", a)
	if err != nil {
		t.Fatalf("extractComic: %v", err)
	}
	if meta.HasCover || meta.CoverBytes != nil {
		t.Error("expected no cover when the entry could not be read")
	}
	assertComicInfoFixture(t, meta)
}

// An archive-wide failure is not degraded past — the item fails.
func TestExtractComic_ArchiveErrorFailsTheExtraction(t *testing.T) {
	a := &fakeComicArchive{names: []string{"01.png"}, err: errEncryptedArchive}
	if _, err := extractComic("cb7", a); !errors.Is(err, errEncryptedArchive) {
		t.Fatalf("err = %v, want errEncryptedArchive", err)
	}
}

func TestExtractComic_NoImages(t *testing.T) {
	a := &fakeComicArchive{names: []string{"readme.txt", "ComicInfo.xml"}}
	_, err := extractComic("cb7", a)
	if err == nil {
		t.Fatal("expected an error for an image-less archive")
	}
	if got := err.Error(); got != "cb7 contains no images" {
		t.Errorf("err = %q, want the container named", got)
	}
}

// The bound that stands between a decompression bomb and this process.
// endlessReader is what a bomb looks like from the read side: a few KB of
// archive that will hand back bytes forever.
func TestReadCappedEntry_RefusesABomb(t *testing.T) {
	r := &endlessReader{}
	b, err := readCappedEntry(r, "01.png", 1<<16)
	if err == nil {
		t.Fatalf("expected a cap error, read %d bytes", len(b))
	}
	if b != nil {
		t.Errorf("returned %d bytes alongside the error", len(b))
	}
	// One byte past the cap is what tells "over" from "exactly at", and
	// nothing past that may be pulled: the point of the cap is that the
	// bomb is never buffered.
	if r.served > (1<<16)+1 {
		t.Errorf("read %d bytes for a %d byte cap", r.served, 1<<16)
	}
}

// A file landing exactly on the cap is not over it.
func TestReadCappedEntry_ExactlyAtTheCap(t *testing.T) {
	r := io.LimitReader(&endlessReader{}, 32)
	b, err := readCappedEntry(r, "01.png", 32)
	if err != nil {
		t.Fatalf("readCappedEntry: %v", err)
	}
	if len(b) != 32 {
		t.Errorf("read %d bytes, want 32", len(b))
	}
}

type endlessReader struct{ served int64 }

func (e *endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'A'
	}
	e.served += int64(len(p))
	return len(p), nil
}
