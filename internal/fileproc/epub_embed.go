package fileproc

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
)

// mutateOPF parses an OPF document, replaces the <metadata> block
// with a fresh one built from in, preserves the rest (manifest,
// spine, guide), and serializes the result.
//
// Strategy: a full XML round-trip via encoding/xml's struct
// marshaling loses arbitrary attributes and namespace prefixes
// (encoding/xml is famously lossy). To stay safe, we slice the
// original bytes around the metadata element by string search,
// build a fresh metadata block as a literal byte string, and stitch
// the three pieces back together. Manifest and spine pass through
// untouched.
func mutateOPF(original []byte, in EmbedInput) ([]byte, error) {
	const openTag = "<metadata"
	openIdx := bytes.Index(original, []byte(openTag))
	if openIdx < 0 {
		return nil, fmt.Errorf("opf: <metadata> not found")
	}
	// Find the close tag — there may be an empty <metadata/> in
	// pathological inputs; we still produce a fresh block.
	closeIdx := bytes.Index(original[openIdx:], []byte("</metadata>"))
	if closeIdx < 0 {
		return nil, fmt.Errorf("opf: </metadata> not found")
	}
	closeIdx += openIdx + len("</metadata>")

	// Locate end of the open <metadata ...> tag so we can preserve
	// its attributes (namespace declarations) verbatim.
	openTagEnd := bytes.IndexByte(original[openIdx:], '>')
	if openTagEnd < 0 {
		return nil, fmt.Errorf("opf: malformed <metadata> open tag")
	}
	openTagEnd += openIdx + 1 // include the '>'

	preserveOpenTag := original[openIdx:openTagEnd]
	after := original[closeIdx:]
	before := original[:openIdx]

	var buf bytes.Buffer
	buf.Write(before)
	buf.Write(preserveOpenTag)
	buf.WriteByte('\n')
	writeMetadataBody(&buf, in)
	buf.WriteString("  </metadata>")
	buf.Write(after)
	return buf.Bytes(), nil
}

// writeMetadataBody renders the metadata children as a UTF-8
// string. Each tag is on its own line, indented two spaces, so the
// output stays human-readable.
func writeMetadataBody(buf *bytes.Buffer, in EmbedInput) {
	emit := func(tag, value string) {
		if value == "" {
			return
		}
		fmt.Fprintf(buf, "    <%s>%s</%s>\n", tag, xmlEscape(value), tag)
	}
	emitAttr := func(tag, attrs, value string) {
		if value == "" {
			return
		}
		fmt.Fprintf(buf, "    <%s %s>%s</%s>\n", tag, attrs, xmlEscape(value), tag)
	}

	emit("dc:title", in.Title)
	if in.Subtitle != "" {
		// EPUB 3: title-type subtitle. Write as a sibling dc:title.
		fmt.Fprintf(buf, "    <dc:title id=\"subtitle\">%s</dc:title>\n", xmlEscape(in.Subtitle))
		buf.WriteString(`    <meta refines="#subtitle" property="title-type">subtitle</meta>` + "\n")
	}
	emit("dc:creator", in.Author)
	emit("dc:description", in.Description)
	emit("dc:language", in.Language)
	emit("dc:publisher", in.Publisher)
	emit("dc:date", in.PublishedDate)
	emitAttr("dc:identifier", `opf:scheme="ISBN"`, in.ISBN)

	if in.Series != "" {
		// EPUB 3 native (the spec).
		fmt.Fprintf(buf, "    <meta property=\"belongs-to-collection\" id=\"series\">%s</meta>\n", xmlEscape(in.Series))
		if in.SeriesIndex > 0 {
			fmt.Fprintf(buf, "    <meta refines=\"#series\" property=\"group-position\">%d</meta>\n", in.SeriesIndex)
		}
		// Calibre compat — uses the OPF 2 <meta name=.../> shape.
		fmt.Fprintf(buf, "    <meta name=\"calibre:series\" content=\"%s\"/>\n", xmlEscape(in.Series))
		if in.SeriesIndex > 0 {
			fmt.Fprintf(buf, "    <meta name=\"calibre:series_index\" content=\"%d\"/>\n", in.SeriesIndex)
		}
	}

	for _, tag := range in.Tags {
		if tag == "" {
			continue
		}
		fmt.Fprintf(buf, "    <meta property=\"embookshelf:tag\">%s</meta>\n", xmlEscape(tag))
		fmt.Fprintf(buf, "    <dc:subject>%s</dc:subject>\n", xmlEscape(tag))
	}
	for _, genre := range in.Genres {
		if genre == "" {
			continue
		}
		fmt.Fprintf(buf, "    <meta property=\"embookshelf:genre\">%s</meta>\n", xmlEscape(genre))
		fmt.Fprintf(buf, "    <dc:subject>%s</dc:subject>\n", xmlEscape(genre))
	}
}

// xmlEscape escapes `<`, `>`, `&`, `"`, `'` for XML text content.
func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
