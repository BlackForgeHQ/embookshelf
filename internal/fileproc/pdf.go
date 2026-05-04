package fileproc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/blackforge/embookshelf/internal/storage"
)

// PDFProcessor extracts minimal metadata from a PDF — title and author when
// the Info dictionary is present, otherwise a filename-derived title. No
// cover extraction: pdfjs-dist renders page one client-side, which the UI
// uses as the cover. This is intentionally a shallow parser — enough to
// feed BookDrop and the book row; not a full PDF library.
type PDFProcessor struct{}

// pdfInfoLiteralRe matches `/Name (value)` entries in the Info dict.
// Handles escaped parens inside the string.
//
// pdfInfoHexRe matches `/Name <hex>` entries — Acrobat / MS Word write
// non-ASCII titles as hex-encoded UTF-16BE strings.
//
// Together they cover the two PDF string-literal forms; the helpers in
// pdf_strings.go decode the contents (Latin-1 vs UTF-16BE-BOM).
var pdfInfoLiteralRe = regexp.MustCompile(`/([A-Za-z][A-Za-z0-9]*)\s*\(((?:\\.|[^)])*)\)`)
var pdfInfoHexRe = regexp.MustCompile(`/([A-Za-z][A-Za-z0-9]*)\s*<([0-9A-Fa-f\s]*)>`)

var pdfAuthorSep = regexp.MustCompile(`[,&]`)

// splitAndJoinAuthors normalises a DocInfo Author string by splitting on
// `,` or `&`, trimming each chunk, and rejoining with `, `. A single
// name (no separators) round-trips unchanged. Empty input returns "".
func splitAndJoinAuthors(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	parts := pdfAuthorSep.Split(raw, -1)
	out := parts[:0]
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return strings.Join(out, ", ")
}

func (PDFProcessor) Extract(ctx context.Context, src storage.Source) (Metadata, error) {
	_ = ctx

	// Wrap the Source as an io.ReadSeeker via SectionReader so we can
	// use Read and Seek without consuming the full object.
	f := io.NewSectionReader(src, 0, src.Size())

	// Sniff the magic so a non-PDF file with a .pdf extension surfaces as
	// a processor error instead of silently producing empty metadata.
	hdr := make([]byte, 5)
	if n, _ := f.Read(hdr); n < 5 || !bytes.HasPrefix(hdr, []byte("%PDF-")) {
		return Metadata{}, fmt.Errorf("not a PDF file")
	}

	// The Info dict typically lives either near the top (linearized PDFs)
	// or in the trailer at the end. Sample both windows so small fixtures
	// and most real-world files land in one of them.
	size := src.Size()
	const window = 1 << 20 // 1 MB
	if _, err := f.Seek(0, 0); err != nil {
		return Metadata{}, err
	}
	head := make([]byte, window)
	hn, _ := f.Read(head)
	head = head[:hn]

	var tail []byte
	if size > int64(hn) {
		const tailSize = 256 << 10 // 256 KB — catches Calibre-tail XMP packets
		pos := size - tailSize
		if pos < int64(hn) {
			pos = int64(hn)
		}
		if _, err := f.Seek(pos, 0); err == nil {
			t := make([]byte, tailSize)
			if tn, _ := f.Read(t); tn > 0 {
				tail = t[:tn]
			}
		}
	}
	scan := append(head, tail...)

	m := Metadata{Format: "PDF"}
	m.Title = pdfInfoField(scan, "Title")
	m.Author = splitAndJoinAuthors(pdfInfoField(scan, "Author"))
	if subject := pdfInfoField(scan, "Subject"); subject != "" {
		m.Description = subject
	}

	// XMP overrides DocInfo when present. Calibre, Acrobat, MS Word, and
	// other modern writers store rich metadata in an uncompressed XMP
	// packet wrapped in <?xpacket begin ... ?> markers; that data is more
	// trustworthy than DocInfo (which often goes stale after edits).
	if packet, ok := extractXMPPacket(scan); ok {
		if x, err := parseXMP(packet); err == nil {
			if x.Title != "" {
				m.Title = x.Title
			}
			if len(x.Creators) > 0 {
				m.Author = strings.Join(x.Creators, ", ")
			}
			if x.Description != "" {
				m.Description = x.Description
			}
			if x.Language != "" {
				m.Language = x.Language
			}
			if x.ISBN != "" {
				if v := cleanAndValidateISBN(x.ISBN); v != "" {
					m.ISBN = v
				}
			}
		}
	}
	// No filename fallback — callers supply a Source, not a path.
	return m, nil
}

// pdfInfoField pulls a named string from the PDF Info dict. Tries the
// literal form `(...)` first, then the hex form `<...>`. Returns the
// first non-empty hit decoded via the helpers in pdf_strings.go (which
// handle UTF-16BE-BOM payloads). Empty when the key isn't present.
func pdfInfoField(data []byte, key string) string {
	for _, match := range pdfInfoLiteralRe.FindAllSubmatch(data, -1) {
		if string(match[1]) != key {
			continue
		}
		body := unescapePDFLiteral(string(match[2]))
		if v := decodePDFLiteral([]byte(body)); v != "" {
			return v
		}
	}
	for _, match := range pdfInfoHexRe.FindAllSubmatch(data, -1) {
		if string(match[1]) != key {
			continue
		}
		if v := decodePDFHexString(string(match[2])); v != "" {
			return v
		}
	}
	return ""
}

// unescapePDFLiteral handles the escapes PDF string literals use. Covers
// \\ \( \) and newline escapes — the common ones. Other sequences fall
// through unchanged.
//
// Does NOT trim whitespace: the result may begin with a UTF-16BE BOM
// (0xFE 0xFF) that decodePDFLiteral keys on, and trimming would
// corrupt those leading bytes. Trim semantics are deferred to
// decodePDFLiteral once the encoding is known.
func unescapePDFLiteral(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		next := s[i+1]
		switch next {
		case '(', ')', '\\':
			b.WriteByte(next)
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		default:
			b.WriteByte(next)
		}
		i++
	}
	return b.String()
}
