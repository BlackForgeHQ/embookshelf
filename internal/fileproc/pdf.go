package fileproc

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PDFProcessor extracts minimal metadata from a PDF — title and author when
// the Info dictionary is present, otherwise a filename-derived title. No
// cover extraction: pdfjs-dist renders page one client-side, which the UI
// uses as the cover. This is intentionally a shallow parser — enough to
// feed BookDrop and the book row; not a full PDF library.
type PDFProcessor struct{}

// pdfInfoFieldRe matches `/Name (value)` entries in the Info dict. Handles
// escaped parens inside the string. Misses:
//   - hex-literal strings (/Title <FEFF...>)
//   - UTF-16BE BOM-prefixed strings (rare in seed/fixture files)
//   - PDFs whose Info dict lives past the first ~1 MB or trailer tail
//
// For those, we fall back to the filename.
var pdfInfoFieldRe = regexp.MustCompile(`/([A-Za-z][A-Za-z0-9]*)\s*\(((?:\\.|[^)])*)\)`)

func (PDFProcessor) Extract(ctx context.Context, filePath string) (Metadata, error) {
	_ = ctx

	f, err := os.Open(filePath)
	if err != nil {
		return Metadata{}, fmt.Errorf("open pdf: %w", err)
	}
	defer f.Close()

	// Sniff the magic so a non-PDF file with a .pdf extension surfaces as
	// a processor error instead of silently producing empty metadata.
	hdr := make([]byte, 5)
	if n, _ := f.Read(hdr); n < 5 || !bytes.HasPrefix(hdr, []byte("%PDF-")) {
		return Metadata{}, fmt.Errorf("not a PDF file")
	}

	// The Info dict typically lives either near the top (linearized PDFs)
	// or in the trailer at the end. Sample both windows so small fixtures
	// and most real-world files land in one of them.
	st, err := f.Stat()
	if err != nil {
		return Metadata{}, err
	}
	const window = 1 << 20 // 1 MB
	if _, err := f.Seek(0, 0); err != nil {
		return Metadata{}, err
	}
	head := make([]byte, window)
	hn, _ := f.Read(head)
	head = head[:hn]

	var tail []byte
	if st.Size() > int64(hn) {
		const tailSize = 8 << 10 // 8 KB
		pos := st.Size() - tailSize
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
	m.Author = pdfInfoField(scan, "Author")
	if subject := pdfInfoField(scan, "Subject"); subject != "" {
		m.Description = subject
	}
	if m.Title == "" {
		base := filepath.Base(filePath)
		m.Title = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return m, nil
}

// pdfInfoField pulls a named literal string from the PDF Info dict. Empty
// when the key isn't present in string form.
func pdfInfoField(data []byte, key string) string {
	for _, match := range pdfInfoFieldRe.FindAllSubmatch(data, -1) {
		if string(match[1]) != key {
			continue
		}
		return unescapePDFLiteral(string(match[2]))
	}
	return ""
}

// unescapePDFLiteral handles the escapes PDF string literals use. Covers
// \\ \( \) and newline escapes — the common ones. Other sequences fall
// through unchanged.
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
	return strings.TrimSpace(b.String())
}
