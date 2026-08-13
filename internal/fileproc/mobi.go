// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	"github.com/blackforge/embookshelf/internal/storage"
)

// MOBIProcessor extracts metadata and the cover from Amazon's two
// PalmDB-container formats: MOBI (Mobipocket, "KF7") and AZW3 (KF8).
// Metadata and cover only — there is no text extraction here, so neither
// format joins the Narratable, Convertible or Kindle-eligible sets
// (model.FormatSpecs); this is what BookDrop needs to show a book on the
// shelf instead of refusing the file (#311).
//
// The layout, since nothing else in this package speaks it:
//
//   - The file is a PalmDB database: a 78-byte header, then an index of
//     8-byte entries giving each record's absolute offset. Records are
//     delimited by the next record's offset — a record carries no length
//     of its own.
//   - Record 0 is all header: a 16-byte PalmDOC header, then the MOBI
//     header (identified by "MOBI" magic and self-describing its length),
//     then an optional EXTH block of typed key/value records, then the
//     book's full name. AZW3 shares all of it; the MOBI header's file
//     version is what tells the two apart (8 for KF8).
//   - Title is EXTH 503 (updated title) falling back to the full-name
//     field the MOBI header points at, author is EXTH 100, and the cover
//     is the record at first-image-index + EXTH 201.
//
// Every one of those offsets and lengths is a number read out of the file
// itself, which is to say attacker-controlled. Two rules keep that safe.
// Reads are bounded structurally: this never buffers the file, only the
// two records it needs (record 0 and the cover), each within a cap and
// each within bounds derived from the real file size. And the container
// is strict where the content is lenient — a PalmDB index, MOBI header or
// EXTH block that does not describe itself consistently fails the item
// with a clear error, while a full-name or cover pointer that lands
// outside the file degrades to a missing field, since the rest of the
// metadata is still good. That is EPUB's and FB2's cover contract, held
// one level further out.
type MOBIProcessor struct{}

const (
	// palmDBHeaderLen is the fixed PalmDB header; the record index
	// follows it, palmDBEntryLen per record.
	palmDBHeaderLen = 78
	palmDBEntryLen  = 8
	// palmBookMobi is the type+creator pair at offset 60, shared by MOBI
	// and AZW3. A PalmDOC ("TEXtREAd") or any other Palm database is not
	// a book this processor can read.
	palmBookMobi = "BOOKMOBI"

	// palmDocHeaderLen is the PalmDOC header at the head of record 0; the
	// MOBI header begins immediately after it.
	palmDocHeaderLen = 16
	// mobiHeaderMinLen is the shortest self-consistent MOBI header:
	// magic, length, type, encoding, unique id and file version. Older
	// Mobipocket files really are this short, and every field past it is
	// read only after checking the declared length reaches it.
	mobiHeaderMinLen = 24

	// MOBI-header-relative offsets of the fields this processor reads.
	mobiOffTextEncoding = 12
	mobiOffFileVersion  = 20
	mobiOffFullNameOff  = 68
	mobiOffFullNameLen  = 72
	mobiOffFirstImage   = 92
	mobiOffEXTHFlags    = 112

	// exthPresentFlag is the bit in the MOBI header's EXTH flags that
	// says an EXTH block follows the header.
	exthPresentFlag = 0x40
	// exthHeaderLen is EXTH's own fixed header: magic, length, count.
	exthHeaderLen = 12
	// exthRecordHeaderLen is the type+length pair each record carries;
	// the length includes these 8 bytes.
	exthRecordHeaderLen = 8

	// EXTH record types this processor reads. 121 is consulted only as a
	// fallback source for the first-image record index, which KF8 files
	// carry there when the MOBI header's own field is left unset.
	exthAuthor       = 100
	exthDescription  = 103
	exthISBN         = 104
	exthFirstImage   = 121
	exthCoverOffset  = 201
	exthThumbOffset  = 202
	exthUpdatedTitle = 503
	exthLanguage     = 524

	// mobiNoIndex is the sentinel MOBI headers use for "this index is not
	// set", alongside a plain zero.
	mobiNoIndex = 0xFFFFFFFF

	// mobiUTF8 and mobiCP1252 are the only two text encodings the format
	// uses for header strings.
	mobiUTF8   = 65001
	mobiCP1252 = 1252
)

// mobiMaxRecord0Bytes bounds record 0, which is header material —
// PalmDOC + MOBI header + EXTH + the full name, kilobytes in every real
// file. A record index claiming megabytes for it describes a file that is
// not a book, and the refusal happens before the allocation rather than
// after it.
//
// mobiMaxCoverBytes bounds the one other record this processor reads. A
// cover pointer is arithmetic over attacker-controlled numbers, so it can
// name any record in the file including one the size of the whole upload;
// past this size the pointer is treated as wrong and the book keeps its
// metadata without a cover.
const (
	mobiMaxRecord0Bytes int64 = 4 << 20 // 4 MiB
	mobiMaxCoverBytes   int64 = 8 << 20 // 8 MiB
)

func (MOBIProcessor) Extract(ctx context.Context, src storage.Source) (Metadata, error) {
	_ = ctx

	db, err := readPalmDB(src)
	if err != nil {
		return Metadata{}, err
	}

	rec0, err := db.record(src, 0, mobiMaxRecord0Bytes)
	if err != nil {
		return Metadata{}, err
	}

	h, err := parseMOBIHeader(rec0)
	if err != nil {
		return Metadata{}, err
	}

	exth, err := parseEXTH(rec0, h.exthStart)
	if err != nil {
		return Metadata{}, err
	}

	m := Metadata{Format: h.format()}
	m.Title = h.text(exth.first(exthUpdatedTitle))
	if m.Title == "" {
		m.Title = h.text(h.fullName())
	}
	// The first EXTH 100 only: Metadata.Author is one string, the same
	// choice EPUB (first dc:creator) and FB2 (first author) make.
	m.Author = h.text(exth.first(exthAuthor))
	// Passed through as written, markup and all — the same contract EPUB's
	// dc:description has, and the reader shell is not consulted for it.
	m.Description = h.text(exth.first(exthDescription))
	m.Language = h.text(exth.first(exthLanguage))
	if isbn := cleanAndValidateISBN(h.text(exth.first(exthISBN))); isbn != "" {
		m.ISBN = isbn
	}

	// Best-effort, like EPUB's and FB2's: a cover pointer into nowhere
	// leaves the book cover-less rather than failing metadata that is
	// otherwise perfectly good.
	if b, mime, ok := mobiCover(src, db, h, exth); ok {
		m.HasCover = true
		m.CoverBytes = b
		m.CoverMime = mime
	}

	return m, nil
}

// --- the PalmDB container ------------------------------------------------

// palmDB is the record index, validated: offsets[i] is where record i
// starts and offsets[i+1] where it ends, with the file size appended so
// the last record is delimited like every other one.
type palmDB struct {
	offsets []int64
}

func readPalmDB(src storage.Source) (*palmDB, error) {
	size := src.Size()
	if size < palmDBHeaderLen {
		return nil, fmt.Errorf("mobi: file is %d bytes, shorter than the %d-byte PalmDB header", size, palmDBHeaderLen)
	}

	head := make([]byte, palmDBHeaderLen)
	if err := readAtFull(src, head, 0); err != nil {
		return nil, fmt.Errorf("mobi: read PalmDB header: %w", err)
	}
	if tc := string(head[60:68]); tc != palmBookMobi {
		return nil, fmt.Errorf("mobi: PalmDB type/creator is %q, not a MOBI/AZW3 book (%q)", tc, palmBookMobi)
	}

	count := int64(binary.BigEndian.Uint16(head[76:78]))
	if count == 0 {
		return nil, fmt.Errorf("mobi: PalmDB header declares no records")
	}
	indexEnd := int64(palmDBHeaderLen) + count*palmDBEntryLen
	if indexEnd > size {
		return nil, fmt.Errorf("mobi: PalmDB declares %d records, whose %d-byte index does not fit in a %d-byte file",
			count, count*palmDBEntryLen, size)
	}

	index := make([]byte, count*palmDBEntryLen)
	if err := readAtFull(src, index, palmDBHeaderLen); err != nil {
		return nil, fmt.Errorf("mobi: read PalmDB record index: %w", err)
	}

	offsets := make([]int64, 0, count+1)
	prev := indexEnd
	for i := int64(0); i < count; i++ {
		off := int64(binary.BigEndian.Uint32(index[i*palmDBEntryLen:]))
		if off > size {
			return nil, fmt.Errorf("mobi: record %d offset %d lies past the end of the %d-byte file", i, off, size)
		}
		if off < prev {
			if i == 0 {
				return nil, fmt.Errorf("mobi: record 0 offset %d overlaps the %d-byte PalmDB header and record index",
					off, indexEnd)
			}
			return nil, fmt.Errorf("mobi: record %d offset %d is before record %d's offset (%d) — the index is not ascending",
				i, off, i-1, prev)
		}
		offsets = append(offsets, off)
		prev = off
	}
	return &palmDB{offsets: append(offsets, size)}, nil
}

func (p *palmDB) count() int { return len(p.offsets) - 1 }

// record reads record i whole, refusing anything past max rather than
// allocating it. The bounds come from the validated index, so the read
// itself can never run off the file.
func (p *palmDB) record(src storage.Source, i int, max int64) ([]byte, error) {
	if i < 0 || i >= p.count() {
		return nil, fmt.Errorf("mobi: record %d is out of range (the file has %d)", i, p.count())
	}
	start, end := p.offsets[i], p.offsets[i+1]
	n := end - start
	if n <= 0 {
		return nil, fmt.Errorf("mobi: record %d is empty", i)
	}
	if n > max {
		return nil, fmt.Errorf("mobi: record %d is %d bytes, over the %d byte cap for it", i, n, max)
	}
	buf := make([]byte, n)
	if err := readAtFull(src, buf, start); err != nil {
		return nil, fmt.Errorf("mobi: read record %d: %w", i, err)
	}
	return buf, nil
}

// readAtFull fills buf from off, treating a short read as an error.
// storage.Source is an io.ReaderAt whose implementations differ on
// whether a read that ends exactly at EOF also reports io.EOF; ReadFull
// over a SectionReader gives one answer for all of them.
func readAtFull(src storage.Source, buf []byte, off int64) error {
	_, err := io.ReadFull(io.NewSectionReader(src, off, int64(len(buf))), buf)
	return err
}

// --- record 0: the PalmDOC + MOBI headers --------------------------------

// mobiHeader is what record 0 says about itself, after validation.
type mobiHeader struct {
	// rec0 is record 0's bytes; hlen is the MOBI header's declared length,
	// already checked to fit inside rec0.
	rec0 []byte
	hlen int

	encoding    uint32
	fileVersion uint32
	// exthStart is where the EXTH block begins inside rec0, or 0 when the
	// header's EXTH flag is clear.
	exthStart int
}

func parseMOBIHeader(rec0 []byte) (mobiHeader, error) {
	if len(rec0) < palmDocHeaderLen+mobiHeaderMinLen {
		return mobiHeader{}, fmt.Errorf("mobi: record 0 is %d bytes, too short for the PalmDOC and MOBI headers", len(rec0))
	}
	if string(rec0[palmDocHeaderLen:palmDocHeaderLen+4]) != "MOBI" {
		return mobiHeader{}, fmt.Errorf("mobi: record 0 carries no MOBI header magic — the file is a PalmDOC, not a MOBI/AZW3 book")
	}

	hlen := int64(binary.BigEndian.Uint32(rec0[palmDocHeaderLen+4:]))
	if hlen < mobiHeaderMinLen {
		return mobiHeader{}, fmt.Errorf("mobi: MOBI header declares a length of %d, below the %d-byte minimum", hlen, mobiHeaderMinLen)
	}
	if hlen > int64(len(rec0)-palmDocHeaderLen) {
		return mobiHeader{}, fmt.Errorf("mobi: MOBI header declares %d bytes but record 0 holds only %d after the PalmDOC header",
			hlen, len(rec0)-palmDocHeaderLen)
	}

	h := mobiHeader{rec0: rec0, hlen: int(hlen)}
	h.encoding, _ = h.u32(mobiOffTextEncoding)
	h.fileVersion, _ = h.u32(mobiOffFileVersion)

	if flags, ok := h.u32(mobiOffEXTHFlags); ok && flags&exthPresentFlag != 0 {
		h.exthStart = palmDocHeaderLen + int(hlen)
	}
	return h, nil
}

// u32 reads a big-endian field at a MOBI-header-relative offset. ok is
// false when the header's declared length (or record 0 itself) does not
// reach it — short headers are legal, so every field past the minimum is
// optional rather than an error.
func (h mobiHeader) u32(rel int) (uint32, bool) {
	if rel < 0 || rel+4 > h.hlen {
		return 0, false
	}
	off := palmDocHeaderLen + rel
	if off+4 > len(h.rec0) {
		return 0, false
	}
	return binary.BigEndian.Uint32(h.rec0[off:]), true
}

// format distinguishes the two formats that share this container. KF8
// files declare file version 8; everything older is Mobipocket.
//
// Only advisory: ExtractBook stamps books.format from the dispatch
// extension, which is what the file is named. It matters for a direct
// Extract caller and for saying, in one place, that this processor
// deliberately answers for both formats.
func (h mobiHeader) format() string {
	if h.fileVersion >= 8 {
		return "AZW3"
	}
	return "MOBI"
}

// fullName returns the book's name field: a pointer *into record 0*, not
// into the file. Out-of-range pointers yield nothing rather than an
// error — the title is content, and EXTH 503 or the sidecar may still
// supply one.
func (h mobiHeader) fullName() []byte {
	off, ok := h.u32(mobiOffFullNameOff)
	if !ok {
		return nil
	}
	n, ok := h.u32(mobiOffFullNameLen)
	if !ok || n == 0 {
		return nil
	}
	start, end := int64(off), int64(off)+int64(n)
	if end > int64(len(h.rec0)) {
		return nil
	}
	return h.rec0[start:end]
}

// text decodes a header string in whichever of MOBI's two encodings the
// header declared, and scrubs whatever remains invalid: these strings end
// up in Postgres text columns, which reject invalid UTF-8 outright.
func (h mobiHeader) text(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	switch h.encoding {
	case mobiCP1252:
		return strings.TrimSpace(decodeCP1252(b))
	case mobiUTF8:
		return strings.TrimSpace(strings.ToValidUTF8(string(b), ""))
	default:
		// Neither of the two encodings the format defines. UTF-8 is the
		// reading that cannot invent characters: the scrub drops whatever
		// does not decode instead of mapping it to some other alphabet.
		return strings.TrimSpace(strings.ToValidUTF8(string(b), ""))
	}
}

// --- the EXTH block ------------------------------------------------------

// exthRecords holds every EXTH record by type, in file order. A type may
// legitimately repeat (several authors, several subjects), so this keeps
// them all and the callers take the first.
type exthRecords map[uint32][][]byte

func (e exthRecords) first(typ uint32) []byte {
	if vs := e[typ]; len(vs) > 0 {
		return vs[0]
	}
	return nil
}

// firstU32 reads a 4-byte EXTH value — the shape the record-index types
// (121, 201, 202) use. ok is false for a missing or truncated record.
func (e exthRecords) firstU32(typ uint32) (uint32, bool) {
	v := e.first(typ)
	if len(v) < 4 {
		return 0, false
	}
	return binary.BigEndian.Uint32(v), true
}

// parseEXTH walks the block at start inside record 0. start == 0 means
// the header's EXTH flag was clear, which is a book with no EXTH, not an
// error. Everything else about the block is structural: a length that
// overruns record 0, a record shorter than its own header, or a record
// that runs past the block's end all mean the file is malformed, and the
// item fails with a message saying so rather than half-parsed metadata.
func parseEXTH(rec0 []byte, start int) (exthRecords, error) {
	if start == 0 {
		return exthRecords{}, nil
	}
	if start < 0 || start+exthHeaderLen > len(rec0) {
		return nil, fmt.Errorf("mobi: the EXTH block starts at %d, past the end of record 0 (%d bytes)", start, len(rec0))
	}
	if string(rec0[start:start+4]) != "EXTH" {
		return nil, fmt.Errorf("mobi: the MOBI header declares an EXTH block but record 0 has no EXTH magic at %d", start)
	}

	blockLen := int64(binary.BigEndian.Uint32(rec0[start+4:]))
	if blockLen < exthHeaderLen {
		return nil, fmt.Errorf("mobi: the EXTH block declares a length of %d, below the %d-byte header", blockLen, exthHeaderLen)
	}
	if int64(start)+blockLen > int64(len(rec0)) {
		return nil, fmt.Errorf("mobi: the EXTH block declares %d bytes at %d, overrunning record 0 (%d bytes)", blockLen, start, len(rec0))
	}
	end := start + int(blockLen)

	count := int64(binary.BigEndian.Uint32(rec0[start+8:]))
	// Each record costs at least its 8-byte header, so a count the block
	// cannot physically hold is a lie about the block.
	if maxRecords := (blockLen - exthHeaderLen) / exthRecordHeaderLen; count > maxRecords {
		return nil, fmt.Errorf("mobi: the EXTH block declares %d records, more than its %d bytes can hold", count, blockLen)
	}

	out := make(exthRecords, count)
	pos := start + exthHeaderLen
	for i := int64(0); i < count; i++ {
		if pos+exthRecordHeaderLen > end {
			return nil, fmt.Errorf("mobi: EXTH record %d starts at %d, past the end of the block (%d)", i, pos, end)
		}
		typ := binary.BigEndian.Uint32(rec0[pos:])
		recLen := int64(binary.BigEndian.Uint32(rec0[pos+4:]))
		if recLen < exthRecordHeaderLen {
			return nil, fmt.Errorf("mobi: EXTH record %d (type %d) declares a length of %d, below the %d-byte record header",
				i, typ, recLen, exthRecordHeaderLen)
		}
		if int64(pos)+recLen > int64(end) {
			return nil, fmt.Errorf("mobi: EXTH record %d (type %d) declares %d bytes at %d, overrunning the block end (%d)",
				i, typ, recLen, pos, end)
		}
		data := rec0[pos+exthRecordHeaderLen : pos+int(recLen)]
		out[typ] = append(out[typ], data)
		pos += int(recLen)
	}
	return out, nil
}

// --- the cover -----------------------------------------------------------

// mobiCover resolves the cover record and reads it. Every step can fail
// benignly: the index may be unset, the arithmetic may land outside the
// file, the record may be too big or may not be an image at all. Any of
// those means "this book has no cover we can find", never an error.
//
// A file whose EXTH names no cover at all gets none. The first image
// record is not guessed at: in a MOBI it is as likely to be a publisher
// logo or a chapter ornament as the cover, and a wrong cover is worse
// than the placeholder the UI already draws.
func mobiCover(src storage.Source, db *palmDB, h mobiHeader, exth exthRecords) ([]byte, string, bool) {
	first, ok := mobiFirstImageIndex(h, exth)
	if !ok {
		return nil, "", false
	}
	// 201 is the cover, 202 the thumbnail of it; a smaller cover beats
	// none when a file ships only the latter.
	for _, typ := range []uint32{exthCoverOffset, exthThumbOffset} {
		off, ok := exth.firstU32(typ)
		if !ok || off == mobiNoIndex {
			continue
		}
		// int64 throughout: first + off is two attacker-controlled uint32s,
		// which would wrap in 32-bit arithmetic and index a real record.
		idx := first + int64(off)
		if idx <= 0 || idx >= int64(db.count()) {
			continue
		}
		b, err := db.record(src, int(idx), mobiMaxCoverBytes)
		if err != nil {
			continue
		}
		if mime := mobiImageMime(b); mime != "" {
			return b, mime, true
		}
	}
	return nil, "", false
}

// mobiFirstImageIndex is the record index EXTH 201/202 are relative to.
// The MOBI header's own field is the source; KF8 files that leave it
// unset carry the index in EXTH 121 instead.
func mobiFirstImageIndex(h mobiHeader, exth exthRecords) (int64, bool) {
	if v, ok := h.u32(mobiOffFirstImage); ok && v != 0 && v != mobiNoIndex {
		return int64(v), true
	}
	if v, ok := exth.firstU32(exthFirstImage); ok && v != 0 && v != mobiNoIndex {
		return int64(v), true
	}
	return 0, false
}

// mobiImageMime sniffs a record's magic. Records carry no names or types,
// so this is the only check standing between a cover pointer that is
// wrong (or hostile) and a text record served to the UI as an image.
func mobiImageMime(b []byte) string {
	switch {
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg"
	case len(b) >= 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(b) >= 6 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a"):
		return "image/gif"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	}
	return ""
}

// --- CP1252 --------------------------------------------------------------

// cp1252High maps the 0x80–0x9F range, the only part of Windows-1252 that
// differs from Latin-1 (0xA0–0xFF are their own code points). Spelled out
// here rather than pulled from golang.org/x/text/encoding/charmap: it is
// 32 runes against a dependency edge this package does not otherwise have.
// 0xFFFD stands for the five unassigned positions.
var cp1252High = [32]rune{
	0x20AC, 0xFFFD, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0xFFFD, 0x017D, 0xFFFD,
	0xFFFD, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0xFFFD, 0x017E, 0x0178,
}

func decodeCP1252(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		switch {
		case c < 0x80:
			sb.WriteByte(c)
		case c < 0xA0:
			sb.WriteRune(cp1252High[c-0x80])
		default:
			sb.WriteRune(rune(c))
		}
	}
	return sb.String()
}
