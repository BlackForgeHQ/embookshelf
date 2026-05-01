package fileproc

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
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
