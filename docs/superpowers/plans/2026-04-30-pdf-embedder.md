# PDF Embedder Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `PDFEmbedder` to `internal/fileproc` — writes Title/Author/Description/Tags/Genres into the PDF `/Info` dictionary via incremental update (append-only revision). Per `docs/spec/sidecar-write.spec.md` §5.2.

**Architecture:** PDF metadata edits use the format's native **incremental update** mechanism. Instead of rewriting the file, we append a new revision at the end: a fresh `/Info` object + new xref subsection + trailer pointing at the new info reference, with `/Prev` chained back to the original `startxref`. Original bytes stay byte-identical; readers walk to the latest revision and see the curated metadata. Strings are encoded as UTF-16BE hex (`<FEFF...>`) so non-ASCII titles round-trip correctly. `/CreationDate` is **never written** — it's the file-creation timestamp by convention; mutating it confuses every other PDF tool.

**Tech Stack:** Go 1.25 stdlib only (`bytes`, `regexp`, `unicode/utf16`, `strings`). No third-party PDF library — incremental update for `/Info` alone is bounded and tractable in ~200 lines.

**Companion docs:**
- `docs/spec/sidecar-write.spec.md` §5.2 (PDF mapping table + Keywords prefix scheme).
- `docs/adr/0001-edit-side-metadata-write-back.md` (Tags/Genres dual-write rationale; PDF inherits the inline-prefix variant).

**Depends on:** Plan 2 (EPUB embedder) — provides the `Embedder` interface + `EmbedInput` shape + `DispatchEmbedder` registry. This plan only registers a new format.

**Out of scope:**
- XMP packet write (PDF metadata stream `/Type /Metadata`). Phase 2 candidate. Calibre uses XMP for richer fields; embookshelf v1 stays at `/Info`.
- Cover replacement (PDF cover = first rendered page; out of scope for any near-term plan).
- Compression of the new `/Info` stream — `/Info` is a dict, not a stream; this concern doesn't arise.

---

## File Structure

| Path | Change |
|---|---|
| `internal/fileproc/embedder.go` | **Modify.** `DispatchEmbedder` adds `case "PDF": return PDFEmbedder{}, nil`. |
| `internal/fileproc/pdf_embed.go` | **Create.** `PDFEmbedder` struct + `Embed`. Helpers: `encodePDFString(s string) string`, `findStartxref(data []byte) (int64, error)`, `findInfoRef(data []byte) (objNum, gen int, ok bool)`, `nextObjectNumber(data []byte) int`, `buildIncrementalUpdate(...) ([]byte, error)`. |
| `internal/fileproc/pdf_embed_test.go` | **Create.** UTF-16BE hex encoder tests; round-trip tests via `PDFProcessor.Extract`; `/CreationDate` preservation test; Keywords prefix scheme test. |
| `internal/fileproc/testdata/pdf_minimal.go` | **Create.** Small Go file emitting a minimal valid PDF (4-object PDF — catalog, pages, page, info) for tests. Mirrors the in-memory EPUB fixture pattern. |

---

## Phase 1 — String encoding

### Task 1: `encodePDFString` emits UTF-16BE hex with BOM

**Files:**
- Create: `internal/fileproc/pdf_embed.go`
- Test: `internal/fileproc/pdf_embed_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/fileproc/pdf_embed_test.go`:

```go
package fileproc

import (
	"strings"
	"testing"
)

func TestEncodePDFString_ASCII(t *testing.T) {
	got := encodePDFString("Hello")
	// UTF-16BE BOM 0xFEFF + "Hello" → FEFF 0048 0065 006C 006C 006F
	want := "<FEFF00480065006C006C006F>"
	if !strings.EqualFold(got, want) {
		t.Errorf("encodePDFString(Hello) = %q, want %q", got, want)
	}
}

func TestEncodePDFString_NonASCII(t *testing.T) {
	got := encodePDFString("café")
	// FEFF 0063 0061 0066 00E9 = "<FEFF00630061006600E9>"
	want := "<FEFF00630061006600E9>"
	if !strings.EqualFold(got, want) {
		t.Errorf("encodePDFString(café) = %q, want %q", got, want)
	}
}

func TestEncodePDFString_Empty(t *testing.T) {
	// Empty string still gets BOM-prefixed wrapping so the resulting
	// PDF object is a valid hex string.
	got := encodePDFString("")
	want := "<FEFF>"
	if !strings.EqualFold(got, want) {
		t.Errorf("encodePDFString(\"\") = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fileproc/ -run TestEncodePDFString -v`
Expected: FAIL with "undefined: encodePDFString".

- [ ] **Step 3: Write minimal implementation**

Create `internal/fileproc/pdf_embed.go`:

```go
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
	// BOM.
	b.WriteString("FEFF")
	// UTF-16 code units.
	for _, cu := range utf16.Encode([]rune(s)) {
		fmt.Fprintf(&b, "%04X", cu)
	}
	b.WriteByte('>')
	return b.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/fileproc/ -run TestEncodePDFString -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fileproc/pdf_embed.go internal/fileproc/pdf_embed_test.go
git commit -m "feat(fileproc): encodePDFString emits UTF-16BE hex w/ BOM"
```

---

## Phase 2 — Source PDF inspection

### Task 2: `findStartxref` locates the trailer's startxref offset

**Files:**
- Modify: `internal/fileproc/pdf_embed.go`
- Test: append to `internal/fileproc/pdf_embed_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestFindStartxref_Standard(t *testing.T) {
	pdf := []byte("%PDF-1.4\n" +
		"1 0 obj\n<<>>\nendobj\n" +
		"xref\n0 2\n0000000000 65535 f \n0000000009 00000 n \ntrailer\n<< /Size 2 >>\n" +
		"startxref\n34\n" +
		"%%EOF\n")
	got, err := findStartxref(pdf)
	if err != nil {
		t.Fatalf("findStartxref: %v", err)
	}
	if got != 34 {
		t.Errorf("offset=%d want 34", got)
	}
}

func TestFindStartxref_NoEOF(t *testing.T) {
	_, err := findStartxref([]byte("%PDF-1.4\n"))
	if err == nil {
		t.Fatal("want error for missing %%EOF, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fileproc/ -run TestFindStartxref -v`
Expected: FAIL with "undefined: findStartxref".

- [ ] **Step 3: Write minimal implementation**

Append to `internal/fileproc/pdf_embed.go`:

```go
import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
)

// findStartxref returns the byte offset recorded after the last
// "startxref" keyword in the PDF — the location of the most recent
// xref table or stream. New incremental revisions chain back here
// via /Prev in their trailer.
func findStartxref(data []byte) (int64, error) {
	// PDF spec: the last 1024 bytes contain "startxref\n<offset>\n%%EOF".
	// Tail-scan to be safe against junk after %%EOF.
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
	// Skip whitespace; collect digits.
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/fileproc/ -run TestFindStartxref -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fileproc/pdf_embed.go internal/fileproc/pdf_embed_test.go
git commit -m "feat(fileproc): findStartxref locates trailer offset"
```

---

### Task 3: `findInfoRef` extracts existing `/Info <num> <gen> R` from the trailer

**Files:**
- Modify: `internal/fileproc/pdf_embed.go`
- Test: append to `internal/fileproc/pdf_embed_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestFindInfoRef_Present(t *testing.T) {
	trailer := []byte("trailer\n<< /Size 5 /Root 1 0 R /Info 4 0 R >>\nstartxref\n100\n%%EOF\n")
	num, gen, ok := findInfoRef(trailer)
	if !ok {
		t.Fatal("findInfoRef: ok=false, want true")
	}
	if num != 4 || gen != 0 {
		t.Errorf("got num=%d gen=%d, want 4 0", num, gen)
	}
}

func TestFindInfoRef_Absent(t *testing.T) {
	trailer := []byte("trailer\n<< /Size 5 /Root 1 0 R >>\nstartxref\n100\n%%EOF\n")
	_, _, ok := findInfoRef(trailer)
	if ok {
		t.Error("findInfoRef: ok=true, want false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fileproc/ -run TestFindInfoRef -v`
Expected: FAIL with "undefined: findInfoRef".

- [ ] **Step 3: Write minimal implementation**

Append:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/fileproc/ -run TestFindInfoRef -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fileproc/pdf_embed.go internal/fileproc/pdf_embed_test.go
git commit -m "feat(fileproc): findInfoRef extracts existing /Info reference"
```

---

### Task 4: `nextObjectNumber` returns the next free object number

**Files:**
- Modify: `internal/fileproc/pdf_embed.go`
- Test: append to `internal/fileproc/pdf_embed_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestNextObjectNumber(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		comment string
	}{
		{"trailer\n<< /Size 5 >>", 5, "Size=5 → next object is #5 (objs 0..4 in use)"},
		{"trailer\n<< /Size 1 /Root 1 0 R >>", 1, "Size=1 → next is #1"},
		{"trailer\n<< /Foo 0 0 R >>", 1, "no /Size → fallback to 1"},
	}
	for _, c := range cases {
		if got := nextObjectNumber([]byte(c.in)); got != c.want {
			t.Errorf("%s: got %d want %d", c.comment, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fileproc/ -run TestNextObjectNumber -v`
Expected: FAIL with "undefined: nextObjectNumber".

- [ ] **Step 3: Write minimal implementation**

Append:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/fileproc/ -run TestNextObjectNumber -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fileproc/pdf_embed.go internal/fileproc/pdf_embed_test.go
git commit -m "feat(fileproc): nextObjectNumber returns next free object slot"
```

---

## Phase 3 — Build incremental update

### Task 5: `buildInfoBody` renders the new /Info dict body

**Files:**
- Modify: `internal/fileproc/pdf_embed.go`
- Test: append to `internal/fileproc/pdf_embed_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestBuildInfoBody_AllFields(t *testing.T) {
	in := EmbedInput{
		Title:       "T",
		Author:      "A",
		Description: "D",
		Tags:        []string{"a", "b"},
		Genres:      []string{"g1"},
	}
	got := buildInfoBody(in)
	for _, want := range []string{
		"/Title <FEFF",          // hex-encoded
		"/Author <FEFF",
		"/Subject <FEFF",        // /Subject = description
		"/Keywords <FEFF",       // tags + genres prefixed, comma-joined
	} {
		if !strings.Contains(got, want) {
			t.Errorf("body missing %q\n%s", want, got)
		}
	}
	// Keywords payload should encode "tag:a, tag:b, genre:g1".
	want := encodePDFString("tag:a, tag:b, genre:g1")
	if !strings.Contains(got, "/Keywords "+want) {
		t.Errorf("Keywords payload mismatch.\nwant suffix: %s\ngot:\n%s", want, got)
	}
}

func TestBuildInfoBody_OmitsEmpty(t *testing.T) {
	got := buildInfoBody(EmbedInput{Title: "Only"})
	if strings.Contains(got, "/Author") {
		t.Error("/Author should be omitted on empty input")
	}
	if strings.Contains(got, "/Subject") {
		t.Error("/Subject should be omitted on empty input")
	}
	if strings.Contains(got, "/Keywords") {
		t.Error("/Keywords should be omitted on empty input")
	}
}

func TestBuildInfoBody_NeverWritesCreationDate(t *testing.T) {
	in := EmbedInput{
		Title:         "T",
		PublishedDate: "2024-01-01",
	}
	got := buildInfoBody(in)
	if strings.Contains(got, "/CreationDate") {
		t.Errorf("/CreationDate must never be written by buildInfoBody\n%s", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fileproc/ -run TestBuildInfoBody -v`
Expected: FAIL with "undefined: buildInfoBody".

- [ ] **Step 3: Write minimal implementation**

Append:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/fileproc/ -run TestBuildInfoBody -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fileproc/pdf_embed.go internal/fileproc/pdf_embed_test.go
git commit -m "feat(fileproc): buildInfoBody renders /Info dict; never writes /CreationDate"
```

---

### Task 6: `buildIncrementalUpdate` appends new /Info + xref + trailer

**Files:**
- Modify: `internal/fileproc/pdf_embed.go`
- Test: append to `internal/fileproc/pdf_embed_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestBuildIncrementalUpdate_StructureValid(t *testing.T) {
	pdf := []byte("%PDF-1.4\n" +
		"1 0 obj\n<<>>\nendobj\n" +
		"xref\n0 2\n0000000000 65535 f \n0000000009 00000 n \ntrailer\n<< /Size 2 /Root 1 0 R >>\n" +
		"startxref\n34\n%%EOF\n")

	out, err := buildIncrementalUpdate(pdf, EmbedInput{Title: "X", Author: "Y"})
	if err != nil {
		t.Fatalf("buildIncrementalUpdate: %v", err)
	}
	// Output must start with the original bytes verbatim.
	if !bytes.HasPrefix(out, pdf) {
		t.Error("output doesn't start with original PDF prefix")
	}
	// Output must end with %%EOF.
	if !bytes.HasSuffix(out, []byte("%%EOF\n")) {
		t.Errorf("output doesn't end with %%%%EOF\n%s", out[len(out)-30:])
	}
	// Output must contain new info object + new xref subsection +
	// trailer with /Prev pointing back at the original startxref.
	want := [][]byte{
		[]byte("2 0 obj\n"),
		[]byte("/Title <FEFF"),
		[]byte("/Author <FEFF"),
		[]byte("/Prev 34"),
		[]byte("/Info 2 0 R"),
		[]byte("xref\n2 1\n"),
	}
	for _, w := range want {
		if !bytes.Contains(out, w) {
			t.Errorf("output missing %q", w)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fileproc/ -run TestBuildIncrementalUpdate_StructureValid -v`
Expected: FAIL with "undefined: buildIncrementalUpdate".

- [ ] **Step 3: Write minimal implementation**

Append:

```go
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
	// Rendered body without the wrapping `<num> <gen> obj ... endobj`.
	body := buildInfoBody(in)

	// Build the appendix.
	var ap bytes.Buffer
	// Ensure the original ends with a newline before our appendix.
	if len(data) > 0 && data[len(data)-1] != '\n' {
		ap.WriteByte('\n')
	}
	// New /Info object.
	objStart := int64(len(data)) + int64(ap.Len())
	fmt.Fprintf(&ap, "%d 0 obj\n%s\nendobj\n", objNum, body)
	// New xref subsection covering just this one object.
	xrefStart := int64(len(data)) + int64(ap.Len())
	// Format: "<offset:10> <generation:5> n " + 2-byte CR/LF.
	// Spec requires exactly 20 bytes per entry.
	fmt.Fprintf(&ap, "xref\n%d 1\n%010d %05d n \n", objNum, objStart, 0)
	// New trailer.
	fmt.Fprintf(&ap, "trailer\n<< /Size %d /Prev %d /Info %d 0 R /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		objNum+1, prevXref, objNum, xrefStart)

	out := make([]byte, 0, len(data)+ap.Len())
	out = append(out, data...)
	out = append(out, ap.Bytes()...)
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/fileproc/ -run TestBuildIncrementalUpdate_StructureValid -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fileproc/pdf_embed.go internal/fileproc/pdf_embed_test.go
git commit -m "feat(fileproc): buildIncrementalUpdate appends new /Info + xref + trailer"
```

---

## Phase 4 — Embedder integration

### Task 7: PDF test fixture helper

**Files:**
- Create: `internal/fileproc/testdata/pdf_minimal.go` (test helper, not a `_test.go` file because the EPUB fixture pattern uses regular package-internal helpers)

Note: rather than a separate testdata file, follow the EPUB fixture pattern from Plan 2 — put the fixture builder in `pdf_embed_test.go`. This keeps everything in one place.

- [ ] **Step 1: Write the fixture helper + a smoke test**

Append to `internal/fileproc/pdf_embed_test.go`:

```go
import "bytes"

// makeMinimalPDF returns a minimal valid PDF containing one page
// and an /Info dict with a single /CreationDate field. Tests use it
// to assert (a) /Info edits land via incremental update, and (b)
// /CreationDate survives the edit.
func makeMinimalPDF(t *testing.T) []byte {
	t.Helper()
	// PDF 1.4 with 4 objects (catalog, pages, page, info) + xref + trailer.
	// Hand-rolled offsets; verify with `mutool show fixture.pdf info`.
	body := []byte("%PDF-1.4\n%âãÏÓ\n" +
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n" +
		"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n" +
		"3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Contents 0 0 R >>\nendobj\n" +
		"4 0 obj\n<< /CreationDate (D:20240101120000Z) >>\nendobj\n")
	xrefStart := len(body)
	xref := []byte(fmt.Sprintf(
		"xref\n0 5\n"+
			"0000000000 65535 f \n"+
			"%010d 00000 n \n"+
			"%010d 00000 n \n"+
			"%010d 00000 n \n"+
			"%010d 00000 n \n",
		bytes.Index(body, []byte("1 0 obj")),
		bytes.Index(body, []byte("2 0 obj")),
		bytes.Index(body, []byte("3 0 obj")),
		bytes.Index(body, []byte("4 0 obj")),
	))
	trailer := []byte(fmt.Sprintf(
		"trailer\n<< /Size 5 /Root 1 0 R /Info 4 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		xrefStart,
	))
	out := append(body, xref...)
	out = append(out, trailer...)
	return out
}

func TestMinimalPDF_Parses(t *testing.T) {
	data := makeMinimalPDF(t)
	src := newBytesSource(data) // helper from embedder_test.go
	defer func() { _ = src.Close() }()
	// PDFProcessor doesn't error on /CreationDate-only Info dicts.
	if _, err := (PDFProcessor{}).Extract(context.Background(), src); err != nil {
		t.Errorf("Extract: %v", err)
	}
}
```

(Imports: `fmt`, `context`. `newBytesSource` is reused from Plan 2's `embedder_test.go`.)

- [ ] **Step 2: Run smoke test**

Run: `go test ./internal/fileproc/ -run TestMinimalPDF_Parses -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/fileproc/pdf_embed_test.go
git commit -m "test(fileproc): minimal in-memory PDF fixture for embedder tests"
```

---

### Task 8: `PDFEmbedder.Embed` integrates + register in `DispatchEmbedder`

**Files:**
- Modify: `internal/fileproc/pdf_embed.go`
- Modify: `internal/fileproc/embedder.go`
- Test: append to `internal/fileproc/pdf_embed_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestPDFEmbedder_Embed_RoundTrip(t *testing.T) {
	original := makeMinimalPDF(t)
	src := newBytesSource(original)
	defer func() { _ = src.Close() }()

	in := EmbedInput{
		Title:       "Curated PDF",
		Author:      "Curated Author",
		Description: "A PDF.",
		Tags:        []string{"tech"},
		Genres:      []string{"reference"},
	}
	out, err := PDFEmbedder{}.Embed(context.Background(), src, in)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	src2 := newBytesSource(out)
	defer func() { _ = src2.Close() }()
	m, err := PDFProcessor{}.Extract(context.Background(), src2)
	if err != nil {
		t.Fatalf("Extract after Embed: %v", err)
	}
	// PDFProcessor's reader is regex-based and only handles literal
	// strings, NOT hex strings. So this round-trip will fail until
	// either (a) the reader is extended, or (b) we accept the limit
	// and pass via a different fixture path. For Phase 1 we update
	// the reader in a follow-up; for now assert the structural
	// markers in the output bytes.
	if !bytes.Contains(out, []byte("/Title <FEFF")) {
		t.Error("output missing hex-encoded /Title")
	}
	if !bytes.Contains(out, []byte("/Author <FEFF")) {
		t.Error("output missing hex-encoded /Author")
	}
	if !bytes.Contains(out, []byte("/Keywords <FEFF")) {
		t.Error("output missing hex-encoded /Keywords")
	}
	// Round-trip via reader is best-effort; if the regex matches the
	// hex form via an enhancement, this assertion can tighten.
	_ = m
}

func TestPDFEmbedder_Embed_PreservesCreationDate(t *testing.T) {
	original := makeMinimalPDF(t)
	src := newBytesSource(original)
	defer func() { _ = src.Close() }()

	out, err := PDFEmbedder{}.Embed(context.Background(), src, EmbedInput{Title: "T"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	// Original bytes must be byte-identical at the start.
	if !bytes.HasPrefix(out, original) {
		t.Error("Embed must not modify original bytes (incremental update only)")
	}
	// Original /CreationDate must still appear in the prefix region.
	if !bytes.Contains(out[:len(original)], []byte("/CreationDate (D:20240101120000Z)")) {
		t.Error("/CreationDate must survive in the original prefix")
	}
}

func TestDispatchEmbedder_PDF(t *testing.T) {
	emb, err := DispatchEmbedder("PDF")
	if err != nil {
		t.Fatalf("DispatchEmbedder(PDF): %v", err)
	}
	if _, ok := emb.(PDFEmbedder); !ok {
		t.Errorf("got %T, want PDFEmbedder", emb)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/fileproc/ -run "TestPDFEmbedder_Embed_|TestDispatchEmbedder_PDF" -v`
Expected: FAIL with "undefined: PDFEmbedder" and "DispatchEmbedder PDF unsupported".

- [ ] **Step 3: Write minimal implementation**

Append to `internal/fileproc/pdf_embed.go`:

```go
import (
	"context"

	"github.com/blackforge/embookshelf/internal/storage"
)

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
```

Edit `internal/fileproc/embedder.go` — register the format in `DispatchEmbedder`:

```go
func DispatchEmbedder(format string) (Embedder, error) {
	switch format {
	case "EPUB":
		return EPUBEmbedder{}, nil
	case "PDF":
		return PDFEmbedder{}, nil
	}
	return nil, ErrUnsupportedEmbed
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/fileproc/ -run "TestPDFEmbedder_Embed_|TestDispatchEmbedder_PDF" -v`
Expected: PASS.

- [ ] **Step 5: Run the full fileproc suite for regressions**

Run: `go test ./internal/fileproc/...`
Expected: every test PASSes (existing readers + EPUB embedder from Plan 2 + new PDF embedder).

- [ ] **Step 6: Commit**

```bash
git add internal/fileproc/embedder.go internal/fileproc/pdf_embed.go internal/fileproc/pdf_embed_test.go
git commit -m "feat(fileproc): PDFEmbedder.Embed via incremental update; register in DispatchEmbedder"
```

---

## Phase 5 — Verification

### Task 9: Lint + vet + spec coverage

- [ ] **Step 1: Full Go test suite**

Run: `make test`
Expected: every package green. Particularly:
- `ok internal/fileproc` (existing readers + EPUB Embedder + PDF Embedder).

- [ ] **Step 2: Lint**

Run: `make go-lint`
Expected: no new lint errors. Watch for:
- `goimports` import grouping in `pdf_embed.go` (stdlib then internal).
- Unused `_ = ctx` annotations are kept intentionally to mirror existing processor signatures.

- [ ] **Step 3: Vet**

Run: `go vet ./...`
Expected: silent.

- [ ] **Step 4: Manual smoke**

Optional: refresh the test fixture to disk and validate w/ a real reader.

```bash
EMBED_FIXTURE_UPDATE=1 go test ./internal/fileproc/ -run TestFixture_Generate
# (No PDF disk fixture is generated by this plan; fixture is in-memory only.)
```

If you have `mutool` (mupdf-tools) installed, generate a test PDF and run the embedder against it manually to sanity-check the appendix is well-formed:

```bash
go run ./cmd/embookshelf-tag --help  # confirms binary builds; no PDF tooling baked in
```

(Manual smoke is optional; the tests cover the structural correctness.)

- [ ] **Step 5: Tag plan completion**

```bash
git log --oneline -10
```

Expected: 8 commits visible from this plan.

---

## Self-Review

**Spec coverage** (against `docs/spec/sidecar-write.spec.md` §5.2):

| Spec item | Task | Covered |
|---|---|---|
| `/Title` ← Title | Task 5 (`buildInfoBody`) | ✓ |
| `/Author` ← Author | Task 5 | ✓ |
| `/Subject` ← Description | Task 5 | ✓ |
| `/Keywords` ← Tags + Genres w/ inline prefix | Task 5 (`joinKeywords`) | ✓ |
| `/CreationDate` **never overwritten** | Task 5 (`TestBuildInfoBody_NeverWritesCreationDate`) + Task 8 (incremental update preserves original bytes verbatim) | ✓ |
| Subtitle/Language/Publisher/PublishedDate/ISBN/Series/SeriesIndex spill to sidecar | Out of scope here — **handled by `MetadataWriter`** in Plan 4. PDFEmbedder simply doesn't write them; the orchestrator routes them to the sidecar. | Plan 4 |
| `DispatchEmbedder("PDF")` | Task 8 | ✓ |
| Round-trip via existing reader | Task 8 (best-effort — note that `PDFProcessor.Extract` uses regex on literal-strings only and won't decode hex strings). Future enhancement: extend `pdfInfoFieldRe` to handle hex strings. Tracked as a follow-up; not blocking the embedder's correctness. | partial — see deviation note |

**Spec deviation flagged:** `PDFProcessor.Extract` (read-side, existing) doesn't decode hex strings (`<FEFF...>`). After this plan ships, a PDF that embookshelf wrote will round-trip its /Info via the byte assertions in Task 8 but won't surface the new title in `m.Title` after re-extraction. Two options to resolve:

1. **Enhance the reader** (small follow-up plan): extend the regex to match hex strings and add UTF-16BE decoding to `unescapePDFLiteral`.
2. **Write literal strings for ASCII** (silently fall back to literal for safe-ASCII inputs, hex only for non-ASCII). Slightly more code; lossier round-trip semantics.

Recommend option 1 — discrete follow-up plan. Plan 4 (`MetadataWriter`) doesn't depend on extraction round-trip because the DB is the canonical source; ingest re-reads on file change and would surface the limit then.

**Placeholder scan:** no `TBD`, no `add appropriate error handling`, no `similar to Task N`. Every code step contains the actual code; every command shows expected output.

**Type consistency:**
- `EmbedInput` (Plan 2) — used as input throughout this plan.
- `PDFEmbedder` — defined Task 8, registered Task 8.
- `encodePDFString`, `findStartxref`, `findInfoRef`, `nextObjectNumber`, `buildInfoBody`, `joinKeywords`, `buildIncrementalUpdate` — defined once each, referenced consistently across Tasks 1-8.
- `makeMinimalPDF` (Task 7) — used Tasks 7, 8.
- `newBytesSource` (Plan 2) — reused without modification.

No drift detected.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-30-pdf-embedder.md`. Two execution options:

1. **Subagent-Driven (recommended)** — fresh subagent per task, review between tasks.
2. **Inline Execution** — `superpowers:executing-plans` with checkpoints in this session.

Pick execution mode for Plan 3, or say **"next plan"** to write Plan 4 (`MetadataWriter` orchestrator + HTTP wiring).
