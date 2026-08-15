// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
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

// comicPage is a page body: a real PNG header with a label glued on, so
// an assertion can still say which page won while the bytes remain
// something the pipeline's cover sniff will accept as an image (#330).
// A page whose body was just the label used to reach the database as a
// cover typed from its filename.
func comicPage(label string) []byte {
	return append(append([]byte{}, fakePNG...), label...)
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
		{name: "page10.png", data: comicPage("ten")},
		{name: "page2.png", data: comicPage("page-two")},
	}
}

// comicCoverFixtureEntries is the other order-sensitive shape: several
// pages *and* a top-level cover.png that is neither the first entry nor
// the first page, so a container that lost the preference would answer
// with page1.png and be caught. Matches testdata/comic-cover.cb7.
func comicCoverFixtureEntries() []comicEntry {
	return []comicEntry{
		{name: "ComicInfo.xml", data: []byte(comicInfoFixtureXML)},
		{name: "page10.png", data: comicPage("ten")},
		{name: "page1.png", data: comicPage("one")},
		{name: "cover.png", data: comicPage("the-cover")},
		{name: "page2.png", data: comicPage("two")},
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
		wantCover []byte
	}{
		{
			name:      "cover by natural sort",
			entries:   comicFixtureEntries(),
			cb7:       cb7Fixture,
			wantCover: comicPage("page-two"),
		},
		{
			name:      "top-level cover.png wins",
			entries:   comicCoverFixtureEntries(),
			cb7:       cb7CoverFixture,
			wantCover: comicPage("the-cover"),
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
			fromCBZ, err := ComicProcessor{}.Extract(ctx, cbz)
			if err != nil {
				t.Fatalf("cbz: %v", err)
			}
			fromCBR, err := ComicProcessor{}.Extract(ctx, cbr)
			if err != nil {
				t.Fatalf("cbr: %v", err)
			}
			fromCB7, err := ComicProcessor{}.Extract(ctx, cb7)
			if err != nil {
				t.Fatalf("cb7: %v", err)
			}

			// The ZIP answer is the one the other two are held to, so it is
			// checked against the expectation first — three containers
			// agreeing on the wrong page would otherwise pass.
			if !bytes.Equal(fromCBZ.CoverBytes, c.wantCover) {
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

// The bytes decide the container, never the extension: a .cbz that is
// really a RAR used to fail ingest through the zip parser its name chose
// while paging fine through the byte sniff — two classifications of the
// same file that nothing held in agreement (#344). All three comic
// extensions dispatch to the one processor, and openComic reads the
// magic, so the mismatch case simply works.
func TestComicIngestClassifiesByBytesNotExtension(t *testing.T) {
	// The RAR fixture, arriving under the wrong name.
	p, format, err := Dispatch("mislabeled.cbz")
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if format != "CBZ" {
		t.Fatalf("format = %q, want CBZ — the stamp is the table's row, not the container", format)
	}
	src := cbrSource(t, rarEntriesFrom(comicFixtureEntries())...)
	defer func() { _ = src.Close() }()

	meta, err := p.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v — a RAR under a .cbz name must ingest as the RAR it is", err)
	}
	assertComicInfoFixture(t, meta)
	if !bytes.Equal(meta.CoverBytes, comicPage("page-two")) {
		t.Errorf("cover = %q, want the natural-sort first page", meta.CoverBytes)
	}
}

// Bytes that are none of the three containers answer ErrComicContainer,
// whatever the file was called — the same sentence the paging arm gives,
// because since #344 they are the same classification.
func TestComicIngestRefusesUnknownBytesWithTheContainerError(t *testing.T) {
	src := memSourceFromBytes([]byte("%PDF-1.7 not a comic at all"))
	defer func() { _ = src.Close() }()

	_, err := ComicProcessor{}.Extract(context.Background(), src)
	if !errors.Is(err, ErrComicContainer) {
		t.Fatalf("err = %v, want ErrComicContainer", err)
	}
}

// The entry cap covers all three containers. ZIP was the one without it
// (#344): archive/zip materialises the list either way, but the cap is
// what keeps the passes above from sorting and scanning a list that
// size — and what refuses the archive with a sentence.
func TestZipComicRefusesAnArchiveOverTheEntryCap(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := range comicMaxEntries + 1 {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: strconv.Itoa(i) + ".png", Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte{0}); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	src := memSourceFromBytes(buf.Bytes())
	defer func() { _ = src.Close() }()

	_, err := ComicProcessor{}.Extract(context.Background(), src)
	if err == nil {
		t.Fatal("a ZIP over the entry cap was accepted")
	}
	if !strings.Contains(err.Error(), "entries") {
		t.Errorf("err = %v, want the entry-cap refusal", err)
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
