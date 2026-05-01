package fileproc

import (
	"fmt"
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
