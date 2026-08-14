// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"strings"
	"testing"
)

// --- the fixture writer --------------------------------------------------
//
// RAR archives in these tests are built here rather than checked in as
// binaries. There is no pure-Go RAR *writer* (rardecode reads only) and
// no RAR creator that is not RARLAB's proprietary one, so the choice was
// between a committed blob nobody can regenerate and ~60 lines that spell
// the container out. The format is small enough to spell out:
//
//	signature   "Rar!\x1a\x07\x01\x00"
//	block       crc32le(sizevint || body) || sizevint || body
//	  body      type vint, flags vint, [extra size vint], [data size vint],
//	            type-specific data, extra area
//	main (1)    archive flags vint
//	file (2)    file flags vint, unpacked size vint, attributes vint,
//	            [crc32le], compression info vint, host OS vint,
//	            name length vint, name; file data follows the block
//	end (5)     end flags vint
//
// Compression info 0 means "version 0, method 0" — stored, which is what
// WinRAR does with already-compressed page images anyway, and what lets
// this write archives without implementing RAR's compressor.
//
// The reader is the real third-party one, so an archive these tests
// accept is an archive rardecode accepts: the writer cannot agree with
// itself about a malformed file.

// rarEntry is one file to pack.
//
// encrypted writes the file-encryption extra record, which is how a
// password-protected archive announces itself; the salt and IV are
// dummies because no key is ever derived without a password to derive it
// from. solid marks the file as continuing the previous file's
// dictionary, the flag that decides whether the read walk may skip
// entries it does not want.
type rarEntry struct {
	name      string
	data      []byte
	encrypted bool
	solid     bool
}

func rarUvarint(v uint64) []byte {
	var b []byte
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// rarBlock frames a block body with its length and header CRC.
func rarBlock(body []byte) []byte {
	sized := append(rarUvarint(uint64(len(body))), body...)
	out := make([]byte, 4)
	binary.LittleEndian.PutUint32(out, crc32.ChecksumIEEE(sized))
	return append(out, sized...)
}

func rarArchive(entries ...rarEntry) []byte {
	var out bytes.Buffer
	out.Write([]byte{'R', 'a', 'r', '!', 0x1a, 0x07, 0x01, 0x00})

	main := rarUvarint(1)                 // block type: main archive
	main = append(main, rarUvarint(0)...) // block flags
	out.Write(rarBlock(append(main, 0)))  // archive flags: none
	for _, e := range entries {
		var extra []byte
		if e.encrypted {
			rec := rarUvarint(1)                                 // record type: encryption
			rec = append(rec, rarUvarint(0)...)                  // version
			rec = append(rec, rarUvarint(0)...)                  // flags: no password check
			rec = append(rec, 15)                                // KDF count
			rec = append(rec, bytes.Repeat([]byte{0xAB}, 16)...) // salt
			rec = append(rec, bytes.Repeat([]byte{0xCD}, 16)...) // IV
			extra = append(rarUvarint(uint64(len(rec))), rec...)
		}

		sum := make([]byte, 4)
		binary.LittleEndian.PutUint32(sum, crc32.ChecksumIEEE(e.data))

		// Compression info: version 0, method 0 (stored), bit 6 solid.
		// Stored data needs no dictionary, so a "solid" archive here is
		// one the reader reports as solid and can still decode — enough
		// to drive the branch that decides how to walk one.
		comp := uint64(0)
		if e.solid {
			comp |= 0x40
		}

		data := rarUvarint(0x0004)                              // file flags: has CRC32
		data = append(data, rarUvarint(uint64(len(e.data)))...) // unpacked size
		data = append(data, rarUvarint(0)...)                   // attributes
		data = append(data, sum...)                             // data CRC32
		data = append(data, rarUvarint(comp)...)                // compression info
		data = append(data, rarUvarint(1)...)                   // host OS: unix
		data = append(data, rarUvarint(uint64(len(e.name)))...) // name length
		data = append(data, e.name...)

		flags := uint64(0x0002) // block carries data
		if len(extra) > 0 {
			flags |= 0x0001 // block carries an extra area
		}
		body := rarUvarint(2)
		body = append(body, rarUvarint(flags)...)
		if len(extra) > 0 {
			body = append(body, rarUvarint(uint64(len(extra)))...)
		}
		body = append(body, rarUvarint(uint64(len(e.data)))...)
		body = append(body, data...)
		body = append(body, extra...)

		out.Write(rarBlock(body))
		out.Write(e.data)
	}

	end := rarUvarint(5)
	end = append(end, rarUvarint(0)...)
	out.Write(rarBlock(append(end, 0)))
	return out.Bytes()
}

func cbrSource(t *testing.T, entries ...rarEntry) *memSource {
	t.Helper()
	b := rarArchive(entries...)
	return &memSource{Reader: bytes.NewReader(b), size: int64(len(b))}
}

// rarEntriesFrom packs the container-neutral fixture entries as plain
// stored RAR files — no encryption, no solid flag, which is what every
// cross-container comparison wants.
func rarEntriesFrom(entries []comicEntry) []rarEntry {
	out := make([]rarEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, rarEntry{name: e.name, data: e.data})
	}
	return out
}

// solidRAREntriesFrom is rarEntriesFrom with every file continuing the
// previous one's dictionary — the shape a page reader cannot walk by
// skipping, and therefore the one the reader's page cache exists for
// (#329).
func solidRAREntriesFrom(entries []comicEntry) []rarEntry {
	out := rarEntriesFrom(entries)
	for i := range out {
		out[i].solid = true
	}
	return out
}

// comicFixtures are the RAR archives committed under testdata/ for the
// packages that cannot call this writer: the BookDrop end-to-end test in
// internal/task feeds the plain one through the pipeline, and the comic
// reader's handler tests page the solid one — which is where "a solid
// archive is decoded once, not once per page" has to be true over real
// HTTP rather than only in this package.
var comicFixtures = []struct {
	path  string
	build func() []byte
}{
	{path: cbrFixture, build: func() []byte { return rarArchive(rarEntriesFrom(comicFixtureEntries())...) }},
	{path: cbrSolidFixture, build: func() []byte {
		return rarArchive(solidRAREntriesFrom(comicFixtureEntries())...)
	}},
}

// TestComicFixture_Generate writes them. Run:
//
//	EMBED_FIXTURE_UPDATE=1 go test ./internal/fileproc/ -run TestComicFixture_Generate
//
// Skipped by default — every test in this file builds its archive in
// memory, and the committed copies exist only because a package two
// directories away cannot call this writer.
func TestComicFixture_Generate(t *testing.T) {
	if os.Getenv("EMBED_FIXTURE_UPDATE") == "" {
		t.Skip("set EMBED_FIXTURE_UPDATE=1 to refresh the committed .cbr fixtures")
	}
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, f := range comicFixtures {
		if err := os.WriteFile(f.path, f.build(), 0o644); err != nil {
			t.Fatalf("write %s: %v", f.path, err)
		}
	}
}

const (
	cbrFixture      = "testdata/comic.cbr"
	cbrSolidFixture = "testdata/comic-solid.cbr"
)

// The committed fixtures are the archives this writer produces — if the
// two drift, the tests in other packages are exercising bytes nothing
// here has ever looked at.
func TestCBRFixturesMatchTheWriter(t *testing.T) {
	for _, f := range comicFixtures {
		want := f.build()
		got, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatalf("read %s: %v — regenerate it with EMBED_FIXTURE_UPDATE=1", f.path, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is %d bytes, the writer produces %d — regenerate it with EMBED_FIXTURE_UPDATE=1",
				f.path, len(got), len(want))
		}
	}
}

// --- the processor -------------------------------------------------------

// The CBZ cover rule, over RAR: first page by natural sort, non-images
// ignored. Deliberately the same fixture shape as
// TestCBZExtract_BasicCover, because "matching CBZ behavior" is the
// requirement and a different fixture would not be evidence of it.
func TestCBRExtract_BasicCover(t *testing.T) {
	src := cbrSource(t,
		rarEntry{name: "page10.png", data: []byte("ten")},
		rarEntry{name: "page2.png", data: fakePNG},
		rarEntry{name: "notes.txt", data: []byte("skip")},
	)
	defer func() { _ = src.Close() }()

	meta, err := CBRProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.Format != "CBZ" {
		t.Errorf("Format = %q, want CBZ — the table folds .cbr onto CBZ", meta.Format)
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

func TestCBRExtract_PreferredCover(t *testing.T) {
	src := cbrSource(t,
		rarEntry{name: "01.png", data: []byte("first-page")},
		rarEntry{name: "cover.png", data: fakePNG},
	)
	defer func() { _ = src.Close() }()

	meta, err := CBRProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !bytes.Equal(meta.CoverBytes, fakePNG) {
		t.Error("CoverBytes != cover.png — preferred-cover lookup failed")
	}
}

func TestCBRExtract_ComicInfo(t *testing.T) {
	src := cbrSource(t,
		rarEntry{name: "01.png", data: fakePNG},
		rarEntry{name: "ComicInfo.xml", data: []byte(comicInfoFixtureXML)},
	)
	defer func() { _ = src.Close() }()

	meta, err := CBRProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	assertComicInfoFixture(t, meta)
	if !meta.HasCover {
		t.Error("expected the cover as well as the metadata")
	}
}

// ComicInfo.xml is read after the cover in archive order here, which is
// the case that decides whether the read pass can serve two entries from
// one walk of a sequential container.
func TestCBRExtract_ComicInfoAfterTheCover(t *testing.T) {
	src := cbrSource(t,
		rarEntry{name: "ComicInfo.xml", data: []byte(comicInfoFixtureXML)},
		rarEntry{name: "01.png", data: fakePNG},
	)
	defer func() { _ = src.Close() }()

	meta, err := CBRProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	assertComicInfoFixture(t, meta)
	if !bytes.Equal(meta.CoverBytes, fakePNG) {
		t.Error("cover not read when ComicInfo comes first in the archive")
	}
}

// A solid archive cannot be walked by skipping — each file continues the
// previous one's dictionary — so the read pass decodes what it steps over
// instead. The cover here is the last entry, behind two it does not want.
func TestCBRExtract_SolidArchive(t *testing.T) {
	src := cbrSource(t,
		rarEntry{name: "ComicInfo.xml", data: []byte(comicInfoFixtureXML), solid: true},
		rarEntry{name: "notes.txt", data: bytes.Repeat([]byte("filler"), 512), solid: true},
		rarEntry{name: "01.png", data: fakePNG, solid: true},
	)
	defer func() { _ = src.Close() }()

	meta, err := CBRProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	assertComicInfoFixture(t, meta)
	if !bytes.Equal(meta.CoverBytes, fakePNG) {
		t.Errorf("CoverBytes = %q, want the last entry's bytes", meta.CoverBytes)
	}
}

// countingComicSource records every byte handed back, so a test can tell
// a walk that seeks past an entry from one that decodes through it.
type countingComicSource struct {
	*bytes.Reader
	size int64
	read int64
}

func (c *countingComicSource) Size() int64  { return c.size }
func (c *countingComicSource) Close() error { return nil }

func (c *countingComicSource) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.Reader.ReadAt(p, off)
	c.read += int64(n)
	return n, err
}

// The solid branch, made discriminating. Deleting the drain in
// rarComic.read leaves TestCBRExtract_SolidArchive passing — a stored
// entry decodes correctly however the reader arrived at it — so what is
// asserted here is the observable difference the drain makes: in a solid
// archive the bytes of an entry nothing wants are pulled through the
// decoder, and in a non-solid one they are seeked past and never read.
//
// Why not a *compressed* solid fixture, which is what would prove the
// drain is load-bearing rather than merely present: RAR compression is
// proprietary and there is no Go encoder for it, so the writer in this
// file emits stored entries only, and no committed archive can be
// regenerated from source either. The necessity argument therefore rests
// on rardecode's own code — newArchiveFileFrom passes reset = !h.Solid to
// decodeReader.init, which keeps the sliding window across files when the
// flag is set, so a file whose predecessors were skipped decodes against
// a window that was never filled. This test pins the behaviour that
// argument asks for.
func TestCBRExtract_SolidWalkDecodesWhatItStepsOver(t *testing.T) {
	const fillerBytes = 256 << 10

	build := func(solid bool) *countingComicSource {
		raw := rarArchive(
			// Not an image and not ComicInfo: nothing wants it, so the only
			// reason to read it is to keep a solid decoder in step.
			rarEntry{name: "filler.bin", data: bytes.Repeat([]byte("x"), fillerBytes), solid: solid},
			rarEntry{name: "01.png", data: fakePNG, solid: solid},
		)
		return &countingComicSource{Reader: bytes.NewReader(raw), size: int64(len(raw))}
	}

	solid := build(true)
	meta, err := CBRProcessor{}.Extract(context.Background(), solid)
	if err != nil {
		t.Fatalf("solid Extract: %v", err)
	}
	if !bytes.Equal(meta.CoverBytes, fakePNG) {
		t.Fatalf("solid cover = %q, want the last entry's bytes", meta.CoverBytes)
	}

	plain := build(false)
	if _, err := (CBRProcessor{}).Extract(context.Background(), plain); err != nil {
		t.Fatalf("non-solid Extract: %v", err)
	}

	if solid.read < fillerBytes {
		t.Errorf("solid walk read %d bytes of a %d-byte filler entry — it skipped what it must decode through",
			solid.read, fillerBytes)
	}
	if plain.read >= fillerBytes {
		t.Errorf("non-solid walk read %d bytes — it should seek past the filler, not decode it", plain.read)
	}
}

// The defect the review found, pinned exactly.
//
// TestCBRExtract_SolidWalkDecodesWhatItStepsOver above covers an entry
// nothing wanted, which the original bug handled correctly — the `continue`
// that skipped the drain lived inside `if wanted`, so it only fired for an
// entry the pass asked for and then could not take: one that runs past its
// per-entry cap. `readCappedEntry` stops one byte past the cap, leaving the
// rest of that entry undecoded, and in a solid archive the files behind it
// decode against a window missing exactly those bytes.
//
// So: a wanted ComicInfo.xml over its 1 MiB cap, followed by the wanted
// cover. The byte count is the assertion, because with stored entries the
// cover still comes out right either way (Next() re-syncs at the packed
// level, which is precisely why this needs counting rather than comparing).
func TestCBRExtract_SolidWalkDrainsAStoppedShortWantedEntry(t *testing.T) {
	const overshoot = 512 << 10
	oversized := bytes.Repeat([]byte(" "), int(comicMaxComicInfoBytes)+overshoot)
	copy(oversized, []byte("<ComicInfo><Series>Saga</Series></ComicInfo>"))

	raw := rarArchive(
		rarEntry{name: "ComicInfo.xml", data: oversized, solid: true},
		rarEntry{name: "01.png", data: fakePNG, solid: true},
	)
	src := &countingComicSource{Reader: bytes.NewReader(raw), size: int64(len(raw))}

	meta, err := CBRProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	// The over-cap entry is dropped, and the entry behind it still arrives.
	if meta.Title != "" {
		t.Errorf("Title = %q — an over-cap ComicInfo was parsed anyway", meta.Title)
	}
	if !bytes.Equal(meta.CoverBytes, fakePNG) {
		t.Errorf("CoverBytes = %q, want the entry behind the over-cap one", meta.CoverBytes)
	}

	// The whole oversized entry has to have been pulled through the
	// decoder, not just the cap's worth: a walk that stops at the cap and
	// seeks the remainder reads about comicMaxComicInfoBytes here.
	if want := int64(len(oversized)); src.read < want {
		t.Errorf("read %d bytes for a %d-byte over-cap entry — the walk stopped short of it and skipped the rest, "+
			"leaving the solid decoder mid-entry", src.read, want)
	}
}

// One unreadable page is one missing field, not a failed import — the
// same degradation a bad entry inside a CBZ gets. The flipped byte here
// is in the cover's data, so its stored checksum no longer matches.
func TestCBRExtract_CorruptPageDegradesToNoCover(t *testing.T) {
	raw := rarArchive(
		rarEntry{name: "01.png", data: fakePNG},
		rarEntry{name: "ComicInfo.xml", data: []byte(comicInfoFixtureXML)},
	)
	raw[40] ^= 0xFF // the first byte of 01.png's stored data

	src := &memSource{Reader: bytes.NewReader(raw), size: int64(len(raw))}
	defer func() { _ = src.Close() }()

	meta, err := CBRProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.HasCover {
		t.Error("expected no cover from an entry whose checksum failed")
	}
	assertComicInfoFixture(t, meta)
}

func TestCBRExtract_NoImagesFails(t *testing.T) {
	src := cbrSource(t, rarEntry{name: "readme.txt", data: []byte("nothing here")})
	defer func() { _ = src.Close() }()

	if _, err := (CBRProcessor{}).Extract(context.Background(), src); err == nil {
		t.Fatal("expected an error for an image-less archive")
	}
}

// A password-protected archive is a different answer from a broken one:
// the file is fine, we simply will not be shown its pages. The BookDrop
// row carries the message, so it has to say which.
func TestCBRExtract_PasswordProtected(t *testing.T) {
	src := cbrSource(t,
		rarEntry{name: "01.png", data: fakePNG, encrypted: true},
		rarEntry{name: "02.png", data: fakePNG, encrypted: true},
	)
	defer func() { _ = src.Close() }()

	_, err := CBRProcessor{}.Extract(context.Background(), src)
	if err == nil {
		t.Fatal("expected an error for a password-protected archive")
	}
	if !errors.Is(err, errEncryptedArchive) {
		t.Errorf("err = %v, want it to read as errEncryptedArchive", err)
	}
	if !strings.Contains(err.Error(), "cbr") {
		t.Errorf("err = %q, want the container named", err)
	}
}

// Corrupt bytes fail the item with a message, never a panic and never a
// half-filled Metadata. Three shapes: not a RAR at all, a truncated
// archive, and a header whose CRC no longer matches its bytes.
func TestCBRExtract_CorruptFails(t *testing.T) {
	good := rarArchive(
		rarEntry{name: "01.png", data: fakePNG},
		rarEntry{name: "02.png", data: fakePNG},
	)

	truncated := append([]byte(nil), good[:len(good)-20]...)

	// Byte 20 is inside the first file block's header (the block starts at
	// 16, after the signature and the main block), so the header CRC no
	// longer matches and the archive cannot be listed at all — as opposed
	// to a flipped *data* byte, which fails one entry and degrades the
	// same way a bad entry in a ZIP does.
	flipped := append([]byte(nil), good...)
	flipped[20] ^= 0xFF

	cases := map[string][]byte{
		"not a rar":      bytes.Repeat([]byte{0x7f}, 512),
		"empty":          nil,
		"truncated":      truncated,
		"bad header crc": flipped,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			src := &memSource{Reader: bytes.NewReader(raw), size: int64(len(raw))}
			defer func() { _ = src.Close() }()
			meta, err := CBRProcessor{}.Extract(context.Background(), src)
			if err == nil {
				t.Fatalf("expected an error, got %+v", meta)
			}
			if meta.HasCover || meta.Title != "" {
				t.Errorf("failed extraction returned metadata: %+v", meta)
			}
		})
	}
}

// An entry that runs past its cap is dropped, not buffered. ComicInfo's
// cap is the small one, so this pins the bound without a fixture the size
// of the cover cap: the oversized XML goes missing and the cover — read
// under its own, larger cap — still arrives.
func TestCBRExtract_OversizedComicInfoIsDropped(t *testing.T) {
	huge := make([]byte, comicMaxComicInfoBytes+1024)
	for i := range huge {
		huge[i] = ' '
	}
	copy(huge, []byte("<ComicInfo><Series>Saga</Series></ComicInfo>"))

	src := cbrSource(t,
		rarEntry{name: "ComicInfo.xml", data: huge},
		rarEntry{name: "01.png", data: fakePNG},
	)
	defer func() { _ = src.Close() }()

	meta, err := CBRProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.Title != "" {
		t.Errorf("Title = %q — an over-cap ComicInfo was parsed anyway", meta.Title)
	}
	if !meta.HasCover {
		t.Error("the cover should still have been read")
	}
}
