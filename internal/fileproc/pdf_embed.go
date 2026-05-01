package fileproc

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/blackforge/embookshelf/internal/storage"
)

// encodePDFString returns a PDF hex-string literal containing the
// UTF-16BE encoding of s with a leading BOM (0xFEFF). PDF readers
// honor the BOM and decode the rest as UTF-16BE; this is the only
// reliable way to ship arbitrary Unicode through a /Info string.
//
// Output shape: "<FEFF...>" — angle brackets included.
func encodePDFString(s string) string {
	var b strings.Builder
	b.WriteByte('<')
	b.WriteString("FEFF")
	for _, cu := range utf16.Encode([]rune(s)) {
		fmt.Fprintf(&b, "%04X", cu)
	}
	b.WriteByte('>')
	return b.String()
}

// findStartxref returns the byte offset recorded after the last
// "startxref" keyword in the PDF — the location of the most recent
// xref table or stream. New incremental revisions chain back here
// via /Prev in their trailer.
func findStartxref(data []byte) (int64, error) {
	tailStart := len(data) - 1024
	if tailStart < 0 {
		tailStart = 0
	}
	tail := data[tailStart:]
	idx := bytes.LastIndex(tail, []byte("startxref"))
	if idx < 0 {
		return 0, fmt.Errorf("pdf: startxref not found in last 1024 bytes")
	}
	rest := tail[idx+len("startxref"):]
	rest = bytes.TrimLeft(rest, " \t\r\n")
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, fmt.Errorf("pdf: startxref not followed by a number")
	}
	off, err := strconv.ParseInt(string(rest[:end]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("pdf: parse startxref offset: %w", err)
	}
	if !bytes.Contains(tail, []byte("%%EOF")) {
		return 0, fmt.Errorf("pdf: %%EOF marker not found")
	}
	return off, nil
}

// infoRefRe matches "/Info N G R" inside a trailer dict.
var infoRefRe = regexp.MustCompile(`/Info\s+(\d+)\s+(\d+)\s+R`)

// findInfoRef returns the existing /Info object reference, if any.
// Scans the trailer dict; absent on a fresh PDF that never had
// metadata.
func findInfoRef(data []byte) (objNum, gen int, ok bool) {
	m := infoRefRe.FindSubmatch(data)
	if m == nil {
		return 0, 0, false
	}
	num, _ := strconv.Atoi(string(m[1]))
	g, _ := strconv.Atoi(string(m[2]))
	return num, g, true
}

// sizeRe matches "/Size N" inside a trailer dict.
var sizeRe = regexp.MustCompile(`/Size\s+(\d+)`)

// nextObjectNumber returns the smallest free object number for an
// incremental update. /Size in the trailer is the count of in-use
// objects (0..Size-1); the next free one is therefore Size.
func nextObjectNumber(data []byte) int {
	m := sizeRe.FindSubmatch(data)
	if m == nil {
		return 1
	}
	n, _ := strconv.Atoi(string(m[1]))
	if n <= 0 {
		return 1
	}
	return n
}

// buildInfoBody renders the /Info dict body (between "<<" and ">>")
// for a new /Info object. Only fields PDF /Info natively carries
// land here; everything else spills to the sidecar.
//
// Critical: /CreationDate is never written. PDF readers and most
// authoring tools treat it as the file-creation timestamp;
// rewriting it confuses every downstream consumer. Published-date
// always goes to the sidecar for PDFs.
func buildInfoBody(in EmbedInput) string {
	var b strings.Builder
	b.WriteString("<<")

	emit := func(name, value string) {
		if value == "" {
			return
		}
		fmt.Fprintf(&b, " /%s %s", name, encodePDFString(value))
	}

	emit("Title", in.Title)
	emit("Author", in.Author)
	emit("Subject", in.Description)
	emit("Keywords", joinKeywords(in.Tags, in.Genres))

	b.WriteString(" >>")
	return b.String()
}

// joinKeywords builds the "tag:foo, tag:bar, genre:baz" string per
// the inline-prefix scheme. Empty slices return "" so emit drops
// the field.
func joinKeywords(tags, genres []string) string {
	parts := make([]string, 0, len(tags)+len(genres))
	for _, t := range tags {
		if t == "" {
			continue
		}
		parts = append(parts, "tag:"+t)
	}
	for _, g := range genres {
		if g == "" {
			continue
		}
		parts = append(parts, "genre:"+g)
	}
	return strings.Join(parts, ", ")
}

// buildIncrementalUpdate appends a new revision to data: a fresh
// /Info object containing in's fields, a one-entry xref subsection
// pointing at the new object, and a trailer whose /Prev chains back
// to the original startxref. The original bytes are preserved
// byte-identically; readers walk forward to the latest revision.
//
// Returns the full new PDF bytes (original + appendix). The caller
// writes them back via storage.Put atomically.
func buildIncrementalUpdate(data []byte, in EmbedInput) ([]byte, error) {
	prevXref, err := findStartxref(data)
	if err != nil {
		return nil, err
	}
	objNum := nextObjectNumber(data)
	body := buildInfoBody(in)

	var ap bytes.Buffer
	if len(data) > 0 && data[len(data)-1] != '\n' {
		ap.WriteByte('\n')
	}
	objStart := int64(len(data)) + int64(ap.Len())
	fmt.Fprintf(&ap, "%d 0 obj\n%s\nendobj\n", objNum, body)
	xrefStart := int64(len(data)) + int64(ap.Len())
	fmt.Fprintf(&ap, "xref\n%d 1\n%010d %05d n \n", objNum, objStart, 0)
	fmt.Fprintf(&ap, "trailer\n<< /Size %d /Prev %d /Info %d 0 R /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		objNum+1, prevXref, objNum, xrefStart)

	out := make([]byte, 0, len(data)+ap.Len())
	out = append(out, data...)
	out = append(out, ap.Bytes()...)
	return out, nil
}

// PDFEmbedder writes /Info metadata into a PDF via incremental
// update. Existing object table and trailer stay byte-identical;
// a new revision is appended at end-of-file pointing at a fresh
// /Info object. /CreationDate is never written.
type PDFEmbedder struct{}

// Embed implements Embedder. Returns the new PDF bytes (original +
// incremental-update appendix). Caller writes back via storage.Put.
func (PDFEmbedder) Embed(ctx context.Context, src storage.Source, in EmbedInput) ([]byte, error) {
	_ = ctx

	data := make([]byte, src.Size())
	if _, err := src.ReadAt(data, 0); err != nil && err.Error() != "EOF" {
		return nil, fmt.Errorf("read pdf: %w", err)
	}
	return buildIncrementalUpdate(data, in)
}
