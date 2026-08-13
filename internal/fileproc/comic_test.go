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

// comicEntry is one archive entry, in a slice so every container fixture
// is built in the *same, stated* order. Archive order is not decoration
// here: the ComicInfo lookup and the top-level cover.* preference both
// take the first match in it, and a fixture built from a Go map would
// shuffle that per run.
type comicEntry struct {
	name string
	data []byte
}

// comicFixtureEntries is the comic every container fixture holds, so "the
// same comic packed three ways produces the same row" is a comparison of
// like with like. Matches what testdata/comic.cb7 and testdata/comic.cbr
// were packed from, entry for entry and in this order.
//
// No cover.* here on purpose: the cover has to come from the natural
// sort, and page10 before page2 is what makes that visible (lexically the
// other way round).
func comicFixtureEntries() []comicEntry {
	return []comicEntry{
		{name: "ComicInfo.xml", data: []byte(comicInfoFixtureXML)},
		{name: "notes.txt", data: []byte("skip")},
		{name: "page10.png", data: []byte("ten")},
		{name: "page2.png", data: []byte("page-two")},
	}
}

// comicCoverFixtureEntries is the other order-sensitive shape: several
// pages *and* a top-level cover.png that is neither the first entry nor
// the first page, so a container that lost the preference would answer
// with page1.png and be caught. Matches testdata/comic-cover.cb7.
func comicCoverFixtureEntries() []comicEntry {
	return []comicEntry{
		{name: "ComicInfo.xml", data: []byte(comicInfoFixtureXML)},
		{name: "page10.png", data: []byte("ten")},
		{name: "page1.png", data: []byte("one")},
		{name: "cover.png", data: []byte("the-cover")},
		{name: "page2.png", data: []byte("two")},
	}
}

// The requirement #310 is really made of: a comic packed as ZIP, RAR or
// 7z is the same comic, so the three processors must produce the same
// Metadata down to the cover bytes. Asserted against each other rather
// than against three separately-written expectations, because it is the
// agreement that is the requirement — a rule that changed in comic.go
// would move all three answers together and this would still hold, while
// a rule that grew a second copy in one container would not.
//
// Both order-sensitive rules are covered: natural sort picking the cover
// out of several pages, and a top-level cover.* beating the first page.
func TestComicContainersAgree(t *testing.T) {
	cases := []struct {
		name      string
		entries   []comicEntry
		cb7       string
		wantCover string
	}{
		{
			name:      "cover by natural sort",
			entries:   comicFixtureEntries(),
			cb7:       cb7Fixture,
			wantCover: "page-two",
		},
		{
			name:      "top-level cover.png wins",
			entries:   comicCoverFixtureEntries(),
			cb7:       cb7CoverFixture,
			wantCover: "the-cover",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cbz := cbzSourceInOrder(t, c.entries)
			defer func() { _ = cbz.Close() }()
			cbr := cbrSource(t, rarEntriesFrom(c.entries)...)
			defer func() { _ = cbr.Close() }()
			cb7 := memSourceFromFile(t, c.cb7)
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

			// The ZIP answer is the one the other two are held to, so it is
			// checked against the expectation first — three containers
			// agreeing on the wrong page would otherwise pass.
			if string(fromCBZ.CoverBytes) != c.wantCover {
				t.Fatalf("cbz cover = %q, want %q", fromCBZ.CoverBytes, c.wantCover)
			}
			assertComicInfoFixture(t, fromCBZ)

			if !reflect.DeepEqual(fromCBZ, fromCBR) {
				t.Errorf("cbr metadata differs from cbz:\n cbz = %+v\n cbr = %+v", fromCBZ, fromCBR)
			}
			if !reflect.DeepEqual(fromCBZ, fromCB7) {
				t.Errorf("cb7 metadata differs from cbz:\n cbz = %+v\n cb7 = %+v", fromCBZ, fromCB7)
			}
		})
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

func (f *fakeComicArchive) read(_ context.Context, want map[string]int64) (map[string][]byte, error) {
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
	meta, err := extractComic(t.Context(), "cbz", a)
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
	if _, err := extractComic(t.Context(), "cb7", a); !errors.Is(err, errEncryptedArchive) {
		t.Fatalf("err = %v, want errEncryptedArchive", err)
	}
}

func TestExtractComic_NoImages(t *testing.T) {
	a := &fakeComicArchive{names: []string{"readme.txt", "ComicInfo.xml"}}
	_, err := extractComic(t.Context(), "cb7", a)
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
