// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"context"
	"encoding/binary"
	"math/rand"
	"strings"
	"testing"
)

// MOBI/AZW3 fixtures are built here rather than checked in as binaries:
// the whole point of these tests is what happens when a field lies about
// a length or an offset, and a builder that can be told to lie is the
// only way to write those cases. The valid path is the same builder with
// nothing overridden, so a real file and a malformed one differ by
// exactly the field under test.

const (
	fxPalmHeaderLen  = 78
	fxRecordEntryLen = 8
	fxPalmDocLen     = 16
	fxMOBIHeaderLen  = 232 // a typical MOBI6 header length

	// Field offsets inside record 0, absolute (the MOBI header starts at
	// 16, so these are 16 + the documented MOBI-header-relative offset).
	fxOffHeaderLen   = 20
	fxOffEncoding    = 28
	fxOffFileVersion = 36
	fxOffFullNameOff = 16 + 68
	fxOffFullNameLen = 16 + 72
	fxOffFirstImage  = 16 + 92
	fxOffEXTHFlags   = 16 + 112
)

var be = binary.BigEndian

// exthEntry is one EXTH record. lenOverride writes a length field other
// than the true 8+len(data) — the malformed-EXTH lever.
type exthEntry struct {
	typ         uint32
	data        []byte
	lenOverride uint32
}

func exthStr(typ uint32, s string) exthEntry { return exthEntry{typ: typ, data: []byte(s)} }
func exthRaw(typ uint32, b []byte) exthEntry { return exthEntry{typ: typ, data: b} }
func exthU32(typ uint32, v uint32) exthEntry {
	b := make([]byte, 4)
	be.PutUint32(b, v)
	return exthEntry{typ: typ, data: b}
}

// rec0Opts describes record 0. The zero value builds a valid, minimal
// UTF-8 MOBI6 record; every override exists for one malformed case.
type rec0Opts struct {
	fullName    string
	encoding    uint32 // 0 → 65001 (UTF-8)
	fileVersion uint32
	firstImage  uint32 // 0 → the 0xFFFFFFFF "none" sentinel
	exth        []exthEntry
	noEXTH      bool

	noMOBIMagic         bool
	headerLenOverride   uint32
	exthLenOverride     uint32
	fullNameOffOverride uint32
	fullNameLenOverride uint32
}

func buildEXTH(entries []exthEntry, lenOverride uint32) []byte {
	var body []byte
	for _, e := range entries {
		l := uint32(8 + len(e.data))
		if e.lenOverride != 0 {
			l = e.lenOverride
		}
		rec := make([]byte, 8)
		be.PutUint32(rec[0:], e.typ)
		be.PutUint32(rec[4:], l)
		body = append(body, rec...)
		body = append(body, e.data...)
	}
	out := make([]byte, 12)
	copy(out, "EXTH")
	be.PutUint32(out[4:], uint32(12+len(body)))
	be.PutUint32(out[8:], uint32(len(entries)))
	if lenOverride != 0 {
		be.PutUint32(out[4:], lenOverride)
	}
	out = append(out, body...)
	// Real writers pad the block to a 4-byte boundary.
	for len(out)%4 != 0 {
		out = append(out, 0)
	}
	return out
}

func mobiRecord0(o rec0Opts) []byte {
	rec := make([]byte, fxPalmDocLen+fxMOBIHeaderLen)

	// PalmDOC header: no compression, no encryption, 4 KB text records.
	be.PutUint16(rec[0:], 1)
	be.PutUint16(rec[10:], 4096)

	magic := "MOBI"
	if o.noMOBIMagic {
		magic = "XXXX"
	}
	copy(rec[16:], magic)

	hlen := uint32(fxMOBIHeaderLen)
	if o.headerLenOverride != 0 {
		hlen = o.headerLenOverride
	}
	be.PutUint32(rec[fxOffHeaderLen:], hlen)
	be.PutUint32(rec[24:], 2) // mobi type: BOOK

	enc := o.encoding
	if enc == 0 {
		enc = 65001
	}
	be.PutUint32(rec[fxOffEncoding:], enc)
	be.PutUint32(rec[32:], 42) // unique id
	be.PutUint32(rec[fxOffFileVersion:], o.fileVersion)

	firstImage := o.firstImage
	if firstImage == 0 {
		firstImage = 0xFFFFFFFF
	}
	be.PutUint32(rec[fxOffFirstImage:], firstImage)

	if !o.noEXTH {
		be.PutUint32(rec[fxOffEXTHFlags:], 0x40)
		rec = append(rec, buildEXTH(o.exth, o.exthLenOverride)...)
	}

	nameOff, nameLen := uint32(len(rec)), uint32(len(o.fullName))
	rec = append(rec, []byte(o.fullName)...)
	if o.fullNameOffOverride != 0 {
		nameOff = o.fullNameOffOverride
	}
	if o.fullNameLenOverride != 0 {
		nameLen = o.fullNameLenOverride
	}
	be.PutUint32(rec[fxOffFullNameOff:], nameOff)
	be.PutUint32(rec[fxOffFullNameLen:], nameLen)

	return rec
}

// palmDBFile wraps records in a PalmDB container. A two-byte gap between
// the record index and the first record mirrors what real writers emit —
// and pins that the parser follows the index rather than assuming record
// 0 begins where the index ends.
func palmDBFile(typ, creator string, records [][]byte) []byte {
	n := len(records)
	head := make([]byte, fxPalmHeaderLen+fxRecordEntryLen*n+2)
	copy(head[0:], "fixture")
	copy(head[60:], typ)
	copy(head[64:], creator)
	be.PutUint16(head[76:], uint16(n))

	off := len(head)
	for i, r := range records {
		be.PutUint32(head[fxPalmHeaderLen+i*fxRecordEntryLen:], uint32(off))
		off += len(r)
	}

	out := head
	for _, r := range records {
		out = append(out, r...)
	}
	return out
}

// mobiFile is the common shape: record 0, then whatever records the test
// wants at indices 1..n (text, images).
func mobiFile(o rec0Opts, rest ...[]byte) []byte {
	records := append([][]byte{mobiRecord0(o)}, rest...)
	return palmDBFile("BOOK", "MOBI", records)
}

func extractMOBI(t *testing.T, raw []byte) (Metadata, error) {
	t.Helper()
	src := memSourceFromBytes(raw)
	defer func() { _ = src.Close() }()
	return MOBIProcessor{}.Extract(context.Background(), src)
}

// --- the happy paths -----------------------------------------------------

// The full acceptance criterion: title and author out of EXTH, cover out
// of the record EXTH 201 names.
func TestMOBIExtract_TitleAuthorAndCover(t *testing.T) {
	raw := mobiFile(rec0Opts{
		fullName:   "Full Name Title",
		firstImage: 2,
		exth: []exthEntry{
			exthStr(100, "Frank Herbert"),
			exthStr(103, "A desert planet."),
			exthStr(104, "9780441013593"),
			exthStr(503, "Dune"),
			exthStr(524, "en"),
			exthU32(201, 0),
		},
	},
		[]byte("text record"),
		fakeJPEG,
	)

	meta, err := extractMOBI(t, raw)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.Format != "MOBI" {
		t.Errorf("Format = %q, want MOBI", meta.Format)
	}
	// EXTH 503 (updated title) wins over the full-name field.
	if meta.Title != "Dune" {
		t.Errorf("Title = %q, want Dune", meta.Title)
	}
	if meta.Author != "Frank Herbert" {
		t.Errorf("Author = %q, want %q", meta.Author, "Frank Herbert")
	}
	if meta.Description != "A desert planet." {
		t.Errorf("Description = %q", meta.Description)
	}
	if meta.ISBN != "9780441013593" {
		t.Errorf("ISBN = %q", meta.ISBN)
	}
	if meta.Language != "en" {
		t.Errorf("Language = %q, want en", meta.Language)
	}
	if !meta.HasCover {
		t.Fatal("expected HasCover")
	}
	if string(meta.CoverBytes) != string(fakeJPEG) {
		t.Errorf("CoverBytes = %x, want %x", meta.CoverBytes, fakeJPEG)
	}
	if meta.CoverMime != "image/jpeg" {
		t.Errorf("CoverMime = %q, want image/jpeg", meta.CoverMime)
	}
}

// Without EXTH 503 the title comes from the full-name field the MOBI
// header points at — the pointer is into record 0, not into the file.
func TestMOBIExtract_TitleFallsBackToFullName(t *testing.T) {
	raw := mobiFile(rec0Opts{
		fullName: "The Left Hand of Darkness",
		exth:     []exthEntry{exthStr(100, "Ursula K. Le Guin")},
	})

	meta, err := extractMOBI(t, raw)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.Title != "The Left Hand of Darkness" {
		t.Errorf("Title = %q, want the full-name field", meta.Title)
	}
	if meta.Author != "Ursula K. Le Guin" {
		t.Errorf("Author = %q", meta.Author)
	}
	if meta.HasCover {
		t.Error("expected no cover — no EXTH 201")
	}
}

// A file with no EXTH block at all (the flag bit clear) still yields the
// full-name title rather than failing.
func TestMOBIExtract_NoEXTHBlock(t *testing.T) {
	raw := mobiFile(rec0Opts{fullName: "Bare MOBI", noEXTH: true})

	meta, err := extractMOBI(t, raw)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.Title != "Bare MOBI" {
		t.Errorf("Title = %q, want Bare MOBI", meta.Title)
	}
	if meta.Author != "" {
		t.Errorf("Author = %q, want empty", meta.Author)
	}
}

// KF8 shares the container and the EXTH layout; the file-version field is
// what says which of the two formats the bytes are.
func TestMOBIExtract_KF8IsStampedAZW3(t *testing.T) {
	raw := mobiFile(rec0Opts{
		fullName:    "KF8 Book",
		fileVersion: 8,
		exth:        []exthEntry{exthStr(503, "KF8 Book")},
	})

	meta, err := extractMOBI(t, raw)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.Format != "AZW3" {
		t.Errorf("Format = %q, want AZW3 for file version 8", meta.Format)
	}
}

// Non-ASCII metadata in the other encoding MOBI uses. 0x92 is a right
// single quote in CP1252 and invalid UTF-8 — reading it as UTF-8 would
// put bytes Postgres rejects into the title.
func TestMOBIExtract_CP1252Text(t *testing.T) {
	raw := mobiFile(rec0Opts{
		encoding: 1252,
		fullName: "ignored",
		exth: []exthEntry{
			exthRaw(503, []byte{'L', 0x92, 'a', 'm', 'o', 'u', 'r', ' ', 0xE9, 't', 'e', 'r', 'n', 'e', 'l'}),
			exthRaw(100, []byte{'A', 'n', 'd', 'r', 0xE9}),
		},
	})

	meta, err := extractMOBI(t, raw)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.Title != "L’amour éternel" {
		t.Errorf("Title = %q, want the CP1252 decoding", meta.Title)
	}
	if meta.Author != "André" {
		t.Errorf("Author = %q, want %q", meta.Author, "André")
	}
}

// Invalid UTF-8 in a file that claims UTF-8 is scrubbed rather than
// carried into a Postgres text column, which would reject it outright.
func TestMOBIExtract_InvalidUTF8IsScrubbed(t *testing.T) {
	raw := mobiFile(rec0Opts{
		exth: []exthEntry{exthRaw(503, []byte{'D', 'u', 'n', 0xFF, 'e'})},
	})

	meta, err := extractMOBI(t, raw)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.Title != "Dune" {
		t.Errorf("Title = %q, want the invalid byte dropped", meta.Title)
	}
}

// Several EXTH 100 records: the first wins, matching FB2's first-author
// and EPUB's first-dc:creator rule for a single Author string.
func TestMOBIExtract_FirstAuthorWins(t *testing.T) {
	raw := mobiFile(rec0Opts{
		exth: []exthEntry{
			exthStr(503, "T"),
			exthStr(100, "First Author"),
			exthStr(100, "Second Author"),
		},
	})

	meta, err := extractMOBI(t, raw)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.Author != "First Author" {
		t.Errorf("Author = %q, want the first EXTH 100", meta.Author)
	}
}

// KF8 files routinely leave the MOBI header's first-image field at the
// 0xFFFFFFFF "none" sentinel and carry the index in EXTH 121 instead.
func TestMOBIExtract_FirstImageFromEXTH121(t *testing.T) {
	raw := mobiFile(rec0Opts{
		fileVersion: 8,
		exth: []exthEntry{
			exthStr(503, "KF8"),
			exthU32(121, 2),
			exthU32(201, 0),
		},
	},
		[]byte("text record"),
		fakePNG,
	)

	meta, err := extractMOBI(t, raw)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !meta.HasCover {
		t.Fatal("expected the cover found via the EXTH 121 fallback")
	}
	if meta.CoverMime != "image/png" {
		t.Errorf("CoverMime = %q, want image/png", meta.CoverMime)
	}
}

// EXTH 202 (thumbnail) is the fallback when no 201 is present — a cover
// at reduced size beats no cover.
func TestMOBIExtract_ThumbnailFallback(t *testing.T) {
	raw := mobiFile(rec0Opts{
		firstImage: 1,
		exth: []exthEntry{
			exthStr(503, "T"),
			exthU32(202, 1),
		},
	},
		[]byte("not an image"),
		fakeJPEG,
	)

	meta, err := extractMOBI(t, raw)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !meta.HasCover || string(meta.CoverBytes) != string(fakeJPEG) {
		t.Errorf("expected the EXTH 202 thumbnail as the cover, got HasCover=%v", meta.HasCover)
	}
}

// An ISBN that isn't one is dropped rather than stored, the same
// validation the PDF XMP path applies.
func TestMOBIExtract_JunkISBNDropped(t *testing.T) {
	raw := mobiFile(rec0Opts{
		exth: []exthEntry{exthStr(503, "T"), exthStr(104, "not-an-isbn")},
	})

	meta, err := extractMOBI(t, raw)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.ISBN != "" {
		t.Errorf("ISBN = %q, want it dropped", meta.ISBN)
	}
}

// --- best-effort cover degradation ---------------------------------------

// Every one of these is attacker-controlled data pointing somewhere it
// shouldn't. None of them may fail an otherwise-good extraction, and none
// may panic.
func TestMOBIExtract_BadCoverPointerDegradesToNoCover(t *testing.T) {
	cases := []struct {
		name string
		opts rec0Opts
		rest [][]byte
	}{
		{
			name: "cover offset past the last record",
			opts: rec0Opts{firstImage: 1, exth: []exthEntry{exthStr(503, "T"), exthU32(201, 9999)}},
			rest: [][]byte{fakeJPEG},
		},
		{
			name: "first-image index past the last record",
			opts: rec0Opts{firstImage: 500, exth: []exthEntry{exthStr(503, "T"), exthU32(201, 0)}},
			rest: [][]byte{fakeJPEG},
		},
		{
			name: "cover offset overflows int32 arithmetic",
			opts: rec0Opts{firstImage: 1, exth: []exthEntry{exthStr(503, "T"), exthU32(201, 0xFFFFFFFE)}},
			rest: [][]byte{fakeJPEG},
		},
		{
			name: "cover record is not an image",
			opts: rec0Opts{firstImage: 1, exth: []exthEntry{exthStr(503, "T"), exthU32(201, 0)}},
			rest: [][]byte{[]byte("this is text, not an image")},
		},
		{
			name: "cover offset is the none sentinel",
			opts: rec0Opts{firstImage: 1, exth: []exthEntry{exthStr(503, "T"), exthU32(201, 0xFFFFFFFF)}},
			rest: [][]byte{fakeJPEG},
		},
		{
			name: "cover offset record is truncated",
			opts: rec0Opts{firstImage: 1, exth: []exthEntry{exthStr(503, "T"), exthRaw(201, []byte{0x00})}},
			rest: [][]byte{fakeJPEG},
		},
		{
			name: "no first-image index anywhere",
			opts: rec0Opts{exth: []exthEntry{exthStr(503, "T"), exthU32(201, 0)}},
			rest: [][]byte{fakeJPEG},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			meta, err := extractMOBI(t, mobiFile(c.opts, c.rest...))
			if err != nil {
				t.Fatalf("Extract: %v, want best-effort success", err)
			}
			if meta.HasCover {
				t.Error("expected no cover")
			}
			if meta.Title != "T" {
				t.Errorf("Title = %q — metadata survives a bad cover pointer", meta.Title)
			}
		})
	}
}

// The full-name pointer is content, not container: out of range means no
// title from it, not a failed file.
func TestMOBIExtract_FullNamePointerOutOfRangeDegrades(t *testing.T) {
	cases := map[string]rec0Opts{
		"offset past the record": {fullName: "Real Title", fullNameOffOverride: 0xFFFFFF00},
		"length past the record": {fullName: "Real Title", fullNameLenOverride: 0xFFFFFF00},
	}
	for name, o := range cases {
		t.Run(name, func(t *testing.T) {
			meta, err := extractMOBI(t, mobiFile(o))
			if err != nil {
				t.Fatalf("Extract: %v, want best-effort success", err)
			}
			if meta.Title != "" {
				t.Errorf("Title = %q, want empty for an out-of-range full-name pointer", meta.Title)
			}
		})
	}
}

// --- malformed containers: clear errors, never a panic -------------------

func TestMOBIExtract_MalformedFilesFailClearly(t *testing.T) {
	valid := mobiFile(rec0Opts{fullName: "T", exth: []exthEntry{exthStr(503, "T")}})

	// A record count that lies about the file: the index alone cannot fit.
	countLies := append([]byte(nil), valid...)
	be.PutUint16(countLies[76:], 60000)

	// Record 0's offset points past EOF.
	offsetPastEOF := append([]byte(nil), valid...)
	be.PutUint32(offsetPastEOF[fxPalmHeaderLen:], uint32(len(valid))+1<<20)

	// Two records whose offsets run backwards.
	descending := palmDBFile("BOOK", "MOBI", [][]byte{mobiRecord0(rec0Opts{fullName: "T"}), []byte("second")})
	be.PutUint32(descending[fxPalmHeaderLen:], uint32(len(descending)-1))

	// Record 0 starting inside the header/index it is indexed by.
	overlapping := append([]byte(nil), valid...)
	be.PutUint32(overlapping[fxPalmHeaderLen:], 10)

	// Zero records at all.
	noRecords := append([]byte(nil), valid...)
	be.PutUint16(noRecords[76:], 0)

	cases := []struct {
		name    string
		raw     []byte
		wantMsg string
	}{
		{"empty file", nil, "shorter than"},
		{"truncated PalmDB header", valid[:40], "shorter than"},
		{"not a PalmDB book", func() []byte {
			b := append([]byte(nil), valid...)
			copy(b[60:], "TEXtREAd")
			return b
		}(), "not a MOBI"},
		{"record count lies about the file size", countLies, "index"},
		{"record offset past EOF", offsetPastEOF, "offset"},
		{"record offsets descending", descending, "offset"},
		{"record 0 overlaps the record index", overlapping, "overlaps"},
		{"no records", noRecords, "no records"},
		{"record 0 too short for the headers", palmDBFile("BOOK", "MOBI", [][]byte{[]byte("short")}), "record 0"},
		{"no MOBI magic", mobiFile(rec0Opts{noMOBIMagic: true}), "MOBI header"},
		{"MOBI header length overruns record 0", mobiFile(rec0Opts{headerLenOverride: 0xFFFFFF00}), "MOBI header"},
		{"MOBI header length absurdly small", mobiFile(rec0Opts{headerLenOverride: 8}), "MOBI header"},
		{"EXTH block length overruns record 0", mobiFile(rec0Opts{
			exth: []exthEntry{exthStr(503, "T")}, exthLenOverride: 0xFFFFFF00,
		}), "EXTH"},
		{"EXTH record length overruns the block", mobiFile(rec0Opts{
			exth: []exthEntry{{typ: 503, data: []byte("T"), lenOverride: 0xFFFFFF00}},
		}), "EXTH"},
		{"EXTH record length below the 8-byte minimum", mobiFile(rec0Opts{
			exth: []exthEntry{{typ: 503, data: []byte("T"), lenOverride: 4}},
		}), "EXTH"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := extractMOBI(t, c.raw)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), c.wantMsg) {
				t.Errorf("err = %q, want it to mention %q", err, c.wantMsg)
			}
			if !strings.HasPrefix(err.Error(), "mobi: ") {
				t.Errorf("err = %q, want the mobi: prefix so the failed item names the format", err)
			}
		})
	}
}

// Record 0 is header material — kilobytes in every real file. A record
// index claiming megabytes for it is refused before the allocation, not
// after.
func TestMOBIExtract_OverSizedRecord0Fails(t *testing.T) {
	rec0 := mobiRecord0(rec0Opts{fullName: "T"})
	padded := append(rec0, make([]byte, mobiMaxRecord0Bytes+1-int64(len(rec0)))...)
	raw := palmDBFile("BOOK", "MOBI", [][]byte{padded})

	_, err := extractMOBI(t, raw)
	if err == nil {
		t.Fatal("expected an error for an over-sized record 0")
	}
	if !strings.Contains(err.Error(), "cap") {
		t.Errorf("err = %q, want a message naming the cap", err)
	}
}

// The same guard on the cover side, where it degrades rather than fails:
// a "cover" the size of the whole upload is not one, and must not be read
// into memory to find that out.
func TestMOBIExtract_OverSizedCoverRecordDegrades(t *testing.T) {
	huge := make([]byte, mobiMaxCoverBytes+1)
	copy(huge, fakeJPEG)
	raw := mobiFile(rec0Opts{
		firstImage: 1,
		exth:       []exthEntry{exthStr(503, "T"), exthU32(201, 0)},
	}, huge)

	meta, err := extractMOBI(t, raw)
	if err != nil {
		t.Fatalf("Extract: %v, want best-effort success", err)
	}
	if meta.HasCover {
		t.Error("expected no cover for an over-sized cover record")
	}
	if meta.Title != "T" {
		t.Errorf("Title = %q, want the metadata to survive", meta.Title)
	}
}

// Fuzz-ish sweep: every prefix of a valid file, and a deterministic set of
// single-byte corruptions of it, must return an answer rather than panic
// or wedge. This is the property the whole processor exists to hold —
// these bytes arrive from the internet.
func TestMOBIExtract_TruncationsAndCorruptionsNeverPanic(t *testing.T) {
	full := mobiFile(rec0Opts{
		fullName:   "Dune",
		firstImage: 2,
		exth: []exthEntry{
			exthStr(100, "Frank Herbert"),
			exthStr(503, "Dune"),
			exthU32(201, 0),
		},
	},
		[]byte("text record"),
		fakeJPEG,
	)

	for n := 0; n <= len(full); n++ {
		_, _ = extractMOBI(t, full[:n]) // must not panic
	}

	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		corrupt := append([]byte(nil), full...)
		for f := 0; f < 3; f++ {
			corrupt[rng.Intn(len(corrupt))] = byte(rng.Intn(256))
		}
		_, _ = extractMOBI(t, corrupt) // must not panic
	}
}
