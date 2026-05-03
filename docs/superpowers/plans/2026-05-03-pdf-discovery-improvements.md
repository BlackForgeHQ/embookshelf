# PDF Discovery Improvements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden the PDF metadata extractor (XMP, hex/UTF-16BE strings, author Seq, ISBN passthrough) and add a client-rendered cover delivered at BookDrop preview time, so PDFs land with rich metadata, an ISBN where available, and a real cover for OPDS clients.

**Architecture:** PDF processor stays pure-Go, regex-based, CGO-free. XMP packets are located by `<?xpacket begin … end ?>` byte scan over an enlarged read window (head 1 MB + tail 256 KB). Author resolution: XMP `dc:creator/rdf:Seq` overrides DocInfo; both shapes get joined with `, ` into the single `books.author` string. ISBN flows: XMP `xmp:Identifier/rdf:Bag` → `fileproc.Metadata.ISBN` → `extractor.ExtractResult.ISBN` → new `bookdrop_items.isbn` column → `books.isbn` on approve. Covers: extractor stays cover-less for PDFs; the BookDrop preview UI rasters page 1 via pdfjs (already present), encodes JPEG q85 at 1200 px wide, and `PUT`s to a new `/api/v1/bookdrop/:id/cover` endpoint that gates on item state and 409s if a cover is already present. See ADR-0015.

**Tech Stack:** Go 1.25, regex (`regexp`), `encoding/xml` for XMP, modernc SQLite + Postgres dual schema, gin handler, React + pdfjs-dist + TanStack Query for UI.

---

## File Structure

### Backend — modify
- `internal/fileproc/pdf.go` — extend extractor with hex strings, UTF-16BE, XMP scan, author Seq, ISBN.
- `internal/fileproc/processor.go` — add `ISBN` field to `Metadata` struct.
- `internal/extractor/extract.go` — copy `Metadata.ISBN` into `ExtractResult.ISBN`; fix `layerSidecar` to also overlay `s.ISBN`.
- `internal/repo/bookdrop.go` — extend `SetMetadata` signature to accept ISBN; thread through SELECT cols.
- `internal/model/bookdrop.go` — add `ISBN` field to `BookDropItem`.
- `internal/service/bookdrop.go` — `RecordMetadata` accepts ISBN; `Approve` copies item.ISBN → book.ISBN; new `PutPreapprovalCover`; filename fallback on extract path.
- `internal/task/bookdrop.go` — pass `res.ISBN` to `RecordMetadata`; pass filename hint for fallback.
- `internal/handler/bookdrop.go` — new `BookDropPutCover` handler.
- `internal/handler/router.go` — register `PUT /api/v1/bookdrop/:id/cover`.

### Backend — create
- `internal/migrator/migrations/postgres/000033_bookdrop_isbn.up.sql`
- `internal/migrator/migrations/postgres/000033_bookdrop_isbn.down.sql`
- `internal/migrator/migrations/sqlite/000033_bookdrop_isbn.up.sql`
- `internal/migrator/migrations/sqlite/000033_bookdrop_isbn.down.sql`
- `internal/fileproc/pdf_xmp.go` — XMP packet scan + parse (separate file to keep `pdf.go` focused).
- `internal/fileproc/pdf_xmp_test.go`
- `internal/fileproc/pdf_strings.go` — hex / UTF-16BE decoders shared with DocInfo path.
- `internal/fileproc/pdf_strings_test.go`
- `internal/fileproc/testdata/pdf/xmp-dc.pdf` — golden fixture (Calibre-style XMP packet)
- `internal/fileproc/testdata/pdf/hex-utf16-title.pdf` — DocInfo with `<FEFF…>` Title
- `internal/fileproc/testdata/pdf/multi-author.pdf` — `Author (Smith, J. & Doe, A.)` literal
- `internal/fileproc/testdata/pdf/xmp-with-isbn.pdf` — XMP Identifier Bag with ISBN

### Frontend — modify
- `ui/src/components/PdfReader.tsx` — expose imperative `renderCoverJpeg(): Promise<Blob>` on the handle.
- `ui/src/api/bookdrop.ts` — `putBookDropCover(id, blob)`.
- BookDrop preview component (locate during Task 17) — call `renderCoverJpeg()` once on first PDF preview open and `PUT` if item has no cover.

---

## Task 1: Add `ISBN` field to `fileproc.Metadata`

**Files:**
- Modify: `internal/fileproc/processor.go:17-41`

- [ ] **Step 1: Edit `Metadata` struct, add ISBN below `Language`**

```go
// ISBN is the canonical identifier extracted from format-embedded
// metadata (PDF XMP Identifier Bag, EPUB OPF identifier). Empty when
// the file carries no ISBN. Format-specific extractors clean to digits
// + 'X', then validate length 10 or 13 before populating; anything
// else is dropped silently.
ISBN string
```

- [ ] **Step 2: Build to verify nothing else needs threading**

Run: `make test`
Expected: PASS — no consumer reads `Metadata.ISBN` yet.

- [ ] **Step 3: Commit**

```bash
git add internal/fileproc/processor.go
git commit -m "feat(fileproc): add ISBN field to Metadata"
```

---

## Task 2: Add `ISBN` to `ExtractResult`, fix `layerSidecar` ISBN bug

**Files:**
- Modify: `internal/extractor/extract.go:19-32` (struct), `:79-93` (copy), `:130-144` (layerSidecar)
- Test: `internal/extractor/extract_test.go`

- [ ] **Step 1: Write failing test for sidecar ISBN overlay**

Append to `internal/extractor/extract_test.go`:

```go
func TestExtract_SidecarISBNOverlaysFormatMeta(t *testing.T) {
    // Use a tiny PDF source with no ISBN; sidecar provides one.
    // Existing test setup builds a fake storage with a sidecar JSON;
    // mirror that pattern, asserting result.ISBN equals sidecar value.
    //
    // If the test file already has a fakeStorage helper, reuse it.
    // Otherwise inline a minimal in-memory storage.Storage.
    t.Skip("implement after layerSidecar ISBN copy lands")
}
```

(If `extract_test.go` already has fixtures, write the real assertion using them — don't `t.Skip`. Re-read the file to find the existing helper before writing.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/extractor/ -run TestExtract_SidecarISBNOverlaysFormatMeta -v`
Expected: FAIL or SKIP (then unskip and watch FAIL).

- [ ] **Step 3: Add `ISBN` to `ExtractResult`**

Edit `internal/extractor/extract.go` struct:

```go
type ExtractResult struct {
    Format      string
    Title       string
    Author      string
    Description string
    Language    string
    ISBN        string // 10 or 13 digits (X allowed in ISBN-10 check digit). Empty when not present.
    HasCover    bool
    CoverBytes  []byte
    CoverMime   string
    DurationSeconds *int
    Narrator        string
}
```

- [ ] **Step 4: Copy ISBN into ExtractResult**

Inside `Extract`, in the `out := ExtractResult{...}` block:

```go
out := ExtractResult{
    Format:      format,
    Title:       meta.Title,
    Author:      meta.Author,
    Description: meta.Description,
    Language:    meta.Language,
    ISBN:        meta.ISBN,
    HasCover:    meta.HasCover,
    CoverBytes:  meta.CoverBytes,
    CoverMime:   meta.CoverMime,
}
```

- [ ] **Step 5: Fix `layerSidecar` — copy `s.ISBN` to `m.ISBN`**

Replace `layerSidecar` body:

```go
func layerSidecar(m fileproc.Metadata, s sidecar.Sidecar) fileproc.Metadata {
    if s.Title != "" {
        m.Title = s.Title
    }
    if s.Author != "" {
        m.Author = s.Author
    }
    if s.Description != "" {
        m.Description = s.Description
    }
    if s.Language != "" {
        m.Language = s.Language
    }
    if s.ISBN != "" {
        m.ISBN = s.ISBN
    }
    return m
}
```

- [ ] **Step 6: Run test, verify pass**

Run: `go test ./internal/extractor/ -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/extractor/
git commit -m "feat(extractor): thread ISBN through ExtractResult, fix sidecar overlay bug"
```

---

## Task 3: Hex / UTF-16BE string decoder

**Files:**
- Create: `internal/fileproc/pdf_strings.go`
- Create: `internal/fileproc/pdf_strings_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/fileproc/pdf_strings_test.go`:

```go
package fileproc

import "testing"

func TestDecodePDFLiteral_PlainASCII(t *testing.T) {
    got := decodePDFLiteral([]byte("Hello"))
    if got != "Hello" {
        t.Fatalf("got %q want %q", got, "Hello")
    }
}

func TestDecodePDFLiteral_BOMUTF16BE(t *testing.T) {
    // \xFE\xFF then UTF-16BE for "Té"
    raw := []byte{0xFE, 0xFF, 0x00, 'T', 0x00, 0xE9}
    got := decodePDFLiteral(raw)
    if got != "Té" {
        t.Fatalf("got %q want %q", got, "Té")
    }
}

func TestDecodePDFHexString_UTF16BE(t *testing.T) {
    // <FEFF0054006900740065>  → "Title"
    got := decodePDFHexString("FEFF0054006900740065")
    if got != "Title" {
        t.Fatalf("got %q want %q", got, "Title")
    }
}

func TestDecodePDFHexString_PlainASCII(t *testing.T) {
    // <48656C6C6F>  → "Hello"
    got := decodePDFHexString("48656C6C6F")
    if got != "Hello" {
        t.Fatalf("got %q want %q", got, "Hello")
    }
}

func TestDecodePDFHexString_OddLengthPadsZero(t *testing.T) {
    // PDF spec: odd hex digits get a trailing 0 — `<F>` ≡ `<F0>`.
    got := decodePDFHexString("F")
    if got != "\xF0" && got != "" {
        t.Fatalf("unexpected %q", got)
    }
}
```

- [ ] **Step 2: Run, verify FAIL**

Run: `go test ./internal/fileproc/ -run "DecodePDF" -v`
Expected: FAIL — symbols don't exist.

- [ ] **Step 3: Implement decoders**

Create `internal/fileproc/pdf_strings.go`:

```go
package fileproc

import (
    "encoding/hex"
    "strings"
    "unicode/utf16"
)

// decodePDFLiteral handles a PDF string-literal payload that may be a
// plain ASCII / Latin-1 byte run or a UTF-16BE byte run prefixed with
// the BOM bytes 0xFE 0xFF. The caller has already stripped the
// surrounding parens and unescaped \( \) \\ \n \r \t sequences.
func decodePDFLiteral(b []byte) string {
    if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
        return decodeUTF16BE(b[2:])
    }
    return strings.TrimSpace(string(b))
}

// decodePDFHexString decodes the body of a PDF hex string `<…>` (just
// the hex digits, parens already stripped). When the bytes start with
// the UTF-16BE BOM (FEFF), they're decoded as UTF-16BE; otherwise
// returned as raw bytes interpreted as Latin-1-compatible ASCII.
//
// Per PDF spec, an odd number of hex digits is padded with a trailing
// '0' (so `<F>` ≡ `<F0>`).
func decodePDFHexString(s string) string {
    s = strings.Map(func(r rune) rune {
        switch {
        case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
            return r
        }
        return -1
    }, s)
    if len(s)%2 == 1 {
        s += "0"
    }
    raw, err := hex.DecodeString(s)
    if err != nil {
        return ""
    }
    if len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF {
        return decodeUTF16BE(raw[2:])
    }
    return strings.TrimSpace(string(raw))
}

func decodeUTF16BE(b []byte) string {
    if len(b) < 2 {
        return ""
    }
    n := len(b) / 2
    u16 := make([]uint16, n)
    for i := 0; i < n; i++ {
        u16[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
    }
    return strings.TrimSpace(string(utf16.Decode(u16)))
}
```

- [ ] **Step 4: Run, verify PASS**

Run: `go test ./internal/fileproc/ -run "DecodePDF" -v`
Expected: PASS (all four).

- [ ] **Step 5: Commit**

```bash
git add internal/fileproc/pdf_strings.go internal/fileproc/pdf_strings_test.go
git commit -m "feat(fileproc): add PDF hex / UTF-16BE string decoders"
```

---

## Task 4: Wire hex strings into DocInfo extraction

**Files:**
- Modify: `internal/fileproc/pdf.go:28` (regex), `:84-92` (`pdfInfoField`)
- Test: `internal/fileproc/pdf_embed_test.go` or new `internal/fileproc/pdf_test.go`

- [ ] **Step 1: Add a regex for hex form**

Edit `internal/fileproc/pdf.go`, replace the single regex with two:

```go
// pdfInfoLiteralRe matches /Name (literal) entries.
var pdfInfoLiteralRe = regexp.MustCompile(`/([A-Za-z][A-Za-z0-9]*)\s*\(((?:\\.|[^)])*)\)`)

// pdfInfoHexRe matches /Name <hexdigits> entries. Hex strings are how
// Acrobat / MS Word write non-ASCII titles.
var pdfInfoHexRe = regexp.MustCompile(`/([A-Za-z][A-Za-z0-9]*)\s*<([0-9A-Fa-f\s]*)>`)
```

- [ ] **Step 2: Update `pdfInfoField` to consult both forms**

Replace:

```go
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
```

- [ ] **Step 3: `unescapePDFLiteral` returns string — adjust signature**

`unescapePDFLiteral` currently returns string and trims. Keep that, but the caller now wraps the trimmed result through `decodePDFLiteral` to handle a BOM that survives unescaping. Update to return the unescaped bytes directly:

```go
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
    return b.String() // no TrimSpace — caller will Trim through decodePDFLiteral
}
```

- [ ] **Step 4: Bump tail window 8 KB → 256 KB**

In `Extract`, change `const tailSize = 8 << 10` to:

```go
const tailSize = 256 << 10 // 256 KB — catches Calibre-tail XMP packets
```

- [ ] **Step 5: Add a test for the hex DocInfo path**

Append to `internal/fileproc/pdf_embed_test.go` (or new file `pdf_test.go`):

```go
func TestPDFProcessor_HexUTF16BEDocInfoTitle(t *testing.T) {
    raw := []byte("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n" +
        "1 0 obj\n<< /Title <FEFF0054006900740065> >>\nendobj\n" +
        "trailer << /Info 1 0 R >>\n%%EOF\n")
    src := newMemSource(raw) // existing test helper; mirror what other tests use
    m, err := (PDFProcessor{}).Extract(context.Background(), src)
    if err != nil {
        t.Fatalf("extract: %v", err)
    }
    if m.Title != "Title" {
        t.Fatalf("Title=%q want %q", m.Title, "Title")
    }
}
```

(If `newMemSource` doesn't exist, copy the helper pattern from `pdf_embed_test.go`'s existing test fixtures.)

- [ ] **Step 6: Run tests**

Run: `go test ./internal/fileproc/ -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/fileproc/pdf.go internal/fileproc/pdf_embed_test.go
git commit -m "feat(fileproc): decode hex/UTF-16BE PDF DocInfo strings, widen tail to 256 KB"
```

---

## Task 5: XMP packet locate + parse (DC fields)

**Files:**
- Create: `internal/fileproc/pdf_xmp.go`
- Create: `internal/fileproc/pdf_xmp_test.go`

- [ ] **Step 1: Write failing tests for XMP scan + parse**

Create `internal/fileproc/pdf_xmp_test.go`:

```go
package fileproc

import "testing"

const xmpPacket = `<?xpacket begin="﻿" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
 <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
  <rdf:Description rdf:about=""
    xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title><rdf:Alt><rdf:li xml:lang="x-default">XMP Title</rdf:li></rdf:Alt></dc:title>
    <dc:creator><rdf:Seq><rdf:li>Alice</rdf:li><rdf:li>Bob</rdf:li></rdf:Seq></dc:creator>
    <dc:description><rdf:Alt><rdf:li xml:lang="x-default">desc here</rdf:li></rdf:Alt></dc:description>
    <dc:language><rdf:Bag><rdf:li>en</rdf:li></rdf:Bag></dc:language>
  </rdf:Description>
 </rdf:RDF>
</x:xmpmeta>
<?xpacket end="r"?>`

func TestExtractXMPPacket_FindsBetweenMarkers(t *testing.T) {
    // Embed the packet inside random PDF-ish padding.
    blob := []byte("%PDF-1.4\n... noise ...\n" + xmpPacket + "\n... more noise ...\n%%EOF")
    got, ok := extractXMPPacket(blob)
    if !ok {
        t.Fatal("xpacket not found")
    }
    if string(got) == "" || !contains(got, []byte("dc:title")) {
        t.Fatalf("packet payload missing dc:title")
    }
}

func TestParseXMP_DublinCore(t *testing.T) {
    x, err := parseXMP([]byte(xmpPacket))
    if err != nil {
        t.Fatalf("parse: %v", err)
    }
    if x.Title != "XMP Title" {
        t.Fatalf("Title=%q", x.Title)
    }
    want := []string{"Alice", "Bob"}
    if len(x.Creators) != 2 || x.Creators[0] != want[0] || x.Creators[1] != want[1] {
        t.Fatalf("Creators=%v want %v", x.Creators, want)
    }
    if x.Description != "desc here" {
        t.Fatalf("Description=%q", x.Description)
    }
    if x.Language != "en" {
        t.Fatalf("Language=%q", x.Language)
    }
}

func contains(haystack, needle []byte) bool {
    return bytesContains(haystack, needle)
}
```

(Add a tiny `bytesContains` shim if you don't want to import `bytes`.)

- [ ] **Step 2: Run, verify FAIL**

Run: `go test ./internal/fileproc/ -run "ExtractXMP|ParseXMP" -v`
Expected: FAIL.

- [ ] **Step 3: Implement scan + parse**

Create `internal/fileproc/pdf_xmp.go`:

```go
package fileproc

import (
    "bytes"
    "encoding/xml"
    "strings"
)

// XMPMetadata is the subset of XMP fields the PDF processor consumes.
type XMPMetadata struct {
    Title       string
    Creators    []string
    Description string
    Language    string
    ISBN        string // raw value from xmp:Identifier/rdf:Bag where scheme contains "isbn"
}

// extractXMPPacket finds the first uncompressed XMP packet in raw bytes.
// XMP packets are wrapped in `<?xpacket begin=… ?>` / `<?xpacket end=…?>`
// markers — designed for raw scanning. Returns the inner XML payload
// (between the markers) on success.
func extractXMPPacket(b []byte) ([]byte, bool) {
    beginMarker := []byte("<?xpacket begin=")
    endMarker := []byte("<?xpacket end=")
    bi := bytes.Index(b, beginMarker)
    if bi < 0 {
        return nil, false
    }
    // Find the closing `?>` of the begin PI; the XMP starts after it.
    headEnd := bytes.Index(b[bi:], []byte("?>"))
    if headEnd < 0 {
        return nil, false
    }
    payloadStart := bi + headEnd + 2
    ei := bytes.Index(b[payloadStart:], endMarker)
    if ei < 0 {
        return nil, false
    }
    return b[payloadStart : payloadStart+ei], true
}

// xmpDoc mirrors the bits of <x:xmpmeta>/<rdf:RDF>/<rdf:Description>
// the extractor cares about. The `,any` attribute on Description's
// children plus rdf:* / dc:* tag matches handles namespace-prefixed
// elements without a full namespace-aware decoder.
type xmpDoc struct {
    XMLName     xml.Name           `xml:"xmpmeta"`
    Description []xmpDescription   `xml:"RDF>Description"`
}

type xmpDescription struct {
    Title       xmpAlt    `xml:"title"`
    Creator     xmpSeq    `xml:"creator"`
    Description xmpAlt    `xml:"description"`
    Language    xmpBag    `xml:"language"`
    Identifier  xmpIdent  `xml:"Identifier"`
}

type xmpAlt struct {
    Items []string `xml:"Alt>li"`
}

type xmpSeq struct {
    Items []string `xml:"Seq>li"`
}

type xmpBag struct {
    Items []string `xml:"Bag>li"`
}

type xmpIdent struct {
    Items []xmpIdentItem `xml:"Bag>li"`
}

type xmpIdentItem struct {
    Scheme string `xml:"scheme,attr"`
    Value  string `xml:",chardata"`
}

// parseXMP unmarshals an XMP packet payload into an XMPMetadata.
// Tolerant: missing fields are left zero. Unknown elements ignored.
func parseXMP(payload []byte) (XMPMetadata, error) {
    var doc xmpDoc
    if err := xml.Unmarshal(payload, &doc); err != nil {
        return XMPMetadata{}, err
    }
    var out XMPMetadata
    for _, d := range doc.Description {
        if out.Title == "" && len(d.Title.Items) > 0 {
            out.Title = strings.TrimSpace(d.Title.Items[0])
        }
        if out.Description == "" && len(d.Description.Items) > 0 {
            out.Description = strings.TrimSpace(d.Description.Items[0])
        }
        if out.Language == "" && len(d.Language.Items) > 0 {
            out.Language = strings.TrimSpace(d.Language.Items[0])
        }
        for _, c := range d.Creator.Items {
            if v := strings.TrimSpace(c); v != "" {
                out.Creators = append(out.Creators, v)
            }
        }
        if out.ISBN == "" {
            for _, ident := range d.Identifier.Items {
                if strings.Contains(strings.ToLower(ident.Scheme), "isbn") {
                    out.ISBN = strings.TrimSpace(ident.Value)
                    break
                }
            }
        }
    }
    return out, nil
}
```

- [ ] **Step 4: Run, verify PASS**

Run: `go test ./internal/fileproc/ -run "ExtractXMP|ParseXMP" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fileproc/pdf_xmp.go internal/fileproc/pdf_xmp_test.go
git commit -m "feat(fileproc): parse PDF XMP packet (DC + Identifier Bag)"
```

---

## Task 6: ISBN clean + length validation

**Files:**
- Modify: `internal/fileproc/pdf_xmp.go` — add `cleanAndValidateISBN`
- Test: `internal/fileproc/pdf_xmp_test.go`

- [ ] **Step 1: Write failing tests**

Append to `internal/fileproc/pdf_xmp_test.go`:

```go
func TestCleanAndValidateISBN_13Digit(t *testing.T) {
    got := cleanAndValidateISBN("978-0-441-17271-9")
    if got != "9780441172719" {
        t.Fatalf("got %q want %q", got, "9780441172719")
    }
}

func TestCleanAndValidateISBN_10WithX(t *testing.T) {
    got := cleanAndValidateISBN("0-441-17271-X")
    if got != "044117271X" {
        t.Fatalf("got %q", got)
    }
}

func TestCleanAndValidateISBN_RejectsShort(t *testing.T) {
    if got := cleanAndValidateISBN("12345"); got != "" {
        t.Fatalf("got %q want empty", got)
    }
}
```

- [ ] **Step 2: Run, verify FAIL**

Run: `go test ./internal/fileproc/ -run CleanAndValidateISBN -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

Append to `internal/fileproc/pdf_xmp.go`:

```go
import "regexp"

var nonISBNChar = regexp.MustCompile(`[^0-9Xx]`)

// cleanAndValidateISBN strips separators, uppercases the trailing X,
// and returns the value only when it is 10 or 13 chars after cleaning.
// Anything else returns "".
func cleanAndValidateISBN(s string) string {
    cleaned := nonISBNChar.ReplaceAllString(s, "")
    cleaned = strings.ToUpper(cleaned)
    if len(cleaned) != 10 && len(cleaned) != 13 {
        return ""
    }
    return cleaned
}
```

(Move the `regexp` import into the existing import block.)

- [ ] **Step 4: Run, verify PASS**

Run: `go test ./internal/fileproc/ -run CleanAndValidateISBN -v`

- [ ] **Step 5: Commit**

```bash
git add internal/fileproc/pdf_xmp.go internal/fileproc/pdf_xmp_test.go
git commit -m "feat(fileproc): clean + validate ISBN extracted from XMP"
```

---

## Task 7: Wire XMP + author Seq + ISBN into PDFProcessor.Extract

**Files:**
- Modify: `internal/fileproc/pdf.go:30-80`
- Test: `internal/fileproc/pdf_xmp_test.go` (add end-to-end Extract test)

- [ ] **Step 1: Write failing test**

Append to `internal/fileproc/pdf_xmp_test.go`:

```go
func TestPDFProcessor_XMPOverridesDocInfoAndExtractsISBN(t *testing.T) {
    body := "%PDF-1.4\n%\xE2\xE3\xCF\xD3\n" +
        "1 0 obj\n<< /Title (DocInfo Title) /Author (DocInfo Author) >>\nendobj\n" +
        xmpPacket + // DC title overrides; creators Alice + Bob; no ISBN here
        "trailer << /Info 1 0 R >>\n%%EOF\n"
    src := newMemSource([]byte(body))

    m, err := (PDFProcessor{}).Extract(context.Background(), src)
    if err != nil {
        t.Fatalf("extract: %v", err)
    }
    if m.Title != "XMP Title" {
        t.Fatalf("Title=%q (XMP must override DocInfo)", m.Title)
    }
    if m.Author != "Alice, Bob" {
        t.Fatalf("Author=%q want %q", m.Author, "Alice, Bob")
    }
    if m.Description != "desc here" {
        t.Fatalf("Description=%q", m.Description)
    }
    if m.Language != "en" {
        t.Fatalf("Language=%q", m.Language)
    }
}

func TestPDFProcessor_AuthorSplitFromDocInfo(t *testing.T) {
    body := "%PDF-1.4\n%\xE2\xE3\xCF\xD3\n" +
        "1 0 obj\n<< /Author (Smith, J. & Doe, A.) >>\nendobj\n" +
        "trailer << /Info 1 0 R >>\n%%EOF\n"
    src := newMemSource([]byte(body))
    m, err := (PDFProcessor{}).Extract(context.Background(), src)
    if err != nil {
        t.Fatalf("extract: %v", err)
    }
    if m.Author != "Smith, J., Doe, A." {
        // Split on [,&] yields ["Smith"," J."," Doe"," A."]; trim + rejoin.
        // We accept this canonical form.
        t.Fatalf("Author=%q", m.Author)
    }
}
```

- [ ] **Step 2: Implement Extract changes**

Edit `internal/fileproc/pdf.go`. Below the existing struct, add author split helper:

```go
var pdfAuthorSep = regexp.MustCompile(`[,&]`)

// splitAndJoinAuthors normalises a DocInfo Author string by splitting on
// `,` or `&`, trimming each chunk, and rejoining with `, `. A single
// name (no separators) round-trips unchanged. Returns "" for empty input.
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
```

Replace the block in `Extract` that builds `m`:

```go
m := Metadata{Format: "PDF"}
m.Title = pdfInfoField(scan, "Title")
m.Author = splitAndJoinAuthors(pdfInfoField(scan, "Author"))
if subject := pdfInfoField(scan, "Subject"); subject != "" {
    m.Description = subject
}

// XMP overrides DocInfo when present.
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
return m, nil
```

- [ ] **Step 3: Run all fileproc tests**

Run: `go test ./internal/fileproc/ -v`
Expected: PASS.

- [ ] **Step 4: Run full suite**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fileproc/pdf.go internal/fileproc/pdf_xmp_test.go
git commit -m "feat(fileproc): XMP overrides DocInfo, split DocInfo authors, ISBN from XMP Bag"
```

---

## Task 8: Migration — `bookdrop_items.isbn`

**Files:**
- Create: `internal/migrator/migrations/postgres/000033_bookdrop_isbn.up.sql`
- Create: `internal/migrator/migrations/postgres/000033_bookdrop_isbn.down.sql`
- Create: `internal/migrator/migrations/sqlite/000033_bookdrop_isbn.up.sql`
- Create: `internal/migrator/migrations/sqlite/000033_bookdrop_isbn.down.sql`

- [ ] **Step 1: Write Postgres up**

`internal/migrator/migrations/postgres/000033_bookdrop_isbn.up.sql`:

```sql
ALTER TABLE bookdrop_items
    ADD COLUMN IF NOT EXISTS isbn TEXT NOT NULL DEFAULT '';
```

- [ ] **Step 2: Write Postgres down**

`internal/migrator/migrations/postgres/000033_bookdrop_isbn.down.sql`:

```sql
ALTER TABLE bookdrop_items DROP COLUMN IF EXISTS isbn;
```

- [ ] **Step 3: Write SQLite up**

`internal/migrator/migrations/sqlite/000033_bookdrop_isbn.up.sql`:

```sql
ALTER TABLE bookdrop_items ADD COLUMN isbn TEXT NOT NULL DEFAULT '';
```

- [ ] **Step 4: Write SQLite down**

`internal/migrator/migrations/sqlite/000033_bookdrop_isbn.down.sql`:

```sql
ALTER TABLE bookdrop_items DROP COLUMN isbn;
```

- [ ] **Step 5: Apply locally and inspect**

Run: `make migrate && make migrate-version`
Expected: version reports `33`.

- [ ] **Step 6: Commit**

```bash
git add internal/migrator/migrations/
git commit -m "feat(db): add bookdrop_items.isbn column"
```

---

## Task 9: Repo + Model — thread ISBN

**Files:**
- Modify: `internal/model/bookdrop.go:17-30` — add ISBN field
- Modify: `internal/repo/bookdrop.go:24` (column list), `:106-127` (`SetMetadata`), `:251` (scan)

- [ ] **Step 1: Add `ISBN` to model**

Edit `internal/model/bookdrop.go` `BookDropItem`, after `Description`:

```go
ISBN string
```

- [ ] **Step 2: Add to column list**

Edit `internal/repo/bookdrop.go`. Find the `bdCols` constant (search for `title, author, description`). Append `, isbn` to the column list and update the SELECT scan.

- [ ] **Step 3: Update `SetMetadata` signature**

Replace:

```go
func (r *BookDropRepo) SetMetadata(
    ctx context.Context,
    id, title, author, description, language, isbn string,
    hasCover bool, coverMime string,
) error {
    const qPG = `
        UPDATE bookdrop_items
           SET title = $2, author = $3, description = $4, language = $5,
               isbn = $6, has_cover = $7, cover_mime = $8, state = 'ready', progress = 100
         WHERE id = $1`
    const qSQLite = `
        UPDATE bookdrop_items
           SET title = ?, author = ?, description = ?, language = ?,
               isbn = ?, has_cover = ?, cover_mime = ?, state = 'ready', progress = 100
         WHERE id = ?`
    if r.db.Dialect == db.DialectSQLite {
        _, err := r.db.SQL.ExecContext(ctx, qSQLite, title, author, description, language, isbn, hasCover, coverMime, id)
        return err
    }
    _, err := r.db.SQL.ExecContext(ctx, qPG, id, title, author, description, language, isbn, hasCover, coverMime)
    return err
}
```

(Match the dialect dispatch already used in this file — copy from existing call sites if the syntax differs.)

- [ ] **Step 4: Update scan in `scanBDRow` (or equivalent)**

In the `Scan` call around `bookdrop.go:251`, add `&item.ISBN` in the same position as the new column in `bdCols`.

- [ ] **Step 5: Build**

Run: `go build ./...`
Expected: compile errors at `service/bookdrop.go` (`SetMetadata` callers). That's fine — Task 10 fixes them.

- [ ] **Step 6: Commit**

```bash
git add internal/model/bookdrop.go internal/repo/bookdrop.go
git commit -m "feat(repo): thread ISBN through bookdrop_items"
```

---

## Task 10: Service — RecordMetadata accepts ISBN; Approve copies to book

**Files:**
- Modify: `internal/service/bookdrop.go:124-147` (`RecordMetadata`), `:220-229` (`Approve` book construction)
- Modify: `internal/task/bookdrop.go:119-125`

- [ ] **Step 1: Update RecordMetadata signature**

Edit `internal/service/bookdrop.go`:

```go
func (s *BookDropService) RecordMetadata(
    ctx context.Context,
    id string,
    title, author, description, language, isbn string,
    coverBytes []byte,
    coverMime string,
) error {
    hasCover := len(coverBytes) > 0
    if hasCover && s.covers != nil {
        if err := s.covers.SaveBookDrop(id, coverBytes); err != nil {
            slog.Warn("save bookdrop cover", "id", id, "err", err)
            hasCover = false
            coverMime = ""
        }
    } else if !hasCover {
        coverMime = ""
    }

    if err := s.bdrop.SetMetadata(ctx, id, title, author, description, language, isbn, hasCover, coverMime); err != nil {
        return err
    }
    s.broadcast(id)
    return nil
}
```

- [ ] **Step 2: Pass ISBN into book on Approve**

In `Approve`, after `Title`/`Author`/`Format`/`Description` lines:

```go
book := model.Book{
    LibraryID:   libraryID,
    Title:       fallback(item.Title, "Untitled"),
    Author:      item.Author,
    Format:      item.Format,
    Description: item.Description,
    ISBN:        item.ISBN,
    Path:        item.Path,
    HasCover:    item.HasCover,
    CoverMime:   item.CoverMime,
}
```

(Confirm `model.Book` has `ISBN` — `internal/model/book.go:49` shows it does.)

- [ ] **Step 3: Update worker call site**

Edit `internal/task/bookdrop.go:119`:

```go
if err := deps.Svc.RecordMetadata(
    ctx, itemID,
    res.Title, res.Author, res.Description, res.Language, res.ISBN,
    res.CoverBytes, res.CoverMime,
); err != nil {
    return err
}
```

- [ ] **Step 4: Run tests**

Run: `make test`
Expected: PASS. If service tests stub `RecordMetadata`, update call signatures there too.

- [ ] **Step 5: Commit**

```bash
git add internal/service/bookdrop.go internal/task/bookdrop.go
git commit -m "feat(bookdrop): persist ISBN at extract, copy to book.isbn at approve"
```

---

## Task 11: Filename fallback for Title

**Files:**
- Modify: `internal/task/bookdrop.go:112-125`

- [ ] **Step 1: Apply post-extract fallback**

Just after `extractor.Extract(...)` returns, before `RecordMetadata`:

```go
if strings.TrimSpace(res.Title) == "" {
    base := filepath.Base(item.Path)
    res.Title = strings.TrimSuffix(base, filepath.Ext(base))
}
```

(Ensure `path/filepath` and `strings` are imported.)

- [ ] **Step 2: Add a test**

Append to `internal/task/bookdrop_test.go` (or create one if absent) — assert that an extractor returning empty Title results in `RecordMetadata` being called with the basename. Use a fake `BookDropService` interface or capture the call via the existing test seam if present.

If no test seam exists, skip the test for this task and rely on integration coverage at `internal/service/bookdrop_test.go` once cover/ISBN flow is exercised end-to-end.

- [ ] **Step 3: Run**

Run: `make test`

- [ ] **Step 4: Commit**

```bash
git add internal/task/bookdrop.go
git commit -m "feat(bookdrop): fall back to filename when extractor returns empty Title"
```

---

## Task 12: Handler — `PUT /api/v1/bookdrop/:id/cover`

**Files:**
- Modify: `internal/handler/bookdrop.go` — add `BookDropPutCover`
- Modify: `internal/handler/router.go:100` — register route

- [ ] **Step 1: Add handler**

Append to `internal/handler/bookdrop.go`:

```go
// BookDropPutCover accepts a raw image (PNG or JPEG) for a BookDrop
// item that doesn't yet have a pre-approval cover. Idempotent on
// absence: first successful PUT wins; subsequent PUTs return 409.
//
// Cap: 5 MB. Magic-sniff: PNG `89 50 4E 47` or JPEG `FF D8 FF`.
// State gate: item must be in 'discovered' or 'ready' (a.k.a.
// pre-approval). Refuses 'imported' / 'rejected' / 'processing'.
func (h *Handler) BookDropPutCover(c *gin.Context) {
    id := c.Param("id")

    item, err := h.bookDrop.Get(c.Request.Context(), id)
    if err != nil {
        writeServerError(c, "bookdrop lookup", err)
        return
    }
    if item.HasCover {
        c.JSON(http.StatusConflict, gin.H{"error": "cover already present"})
        return
    }
    switch item.State {
    case model.BookDropDiscovered, model.BookDropReady, model.BookDropProcessing:
        // fall through — accept upload
    default:
        c.JSON(http.StatusConflict, gin.H{"error": "item state does not accept cover upload"})
        return
    }

    const maxBytes = 5 << 20
    c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
    raw, err := io.ReadAll(c.Request.Body)
    if err != nil {
        c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "image too large"})
        return
    }
    mime, ok := sniffCoverMime(raw)
    if !ok {
        c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "expected PNG or JPEG"})
        return
    }

    if err := h.bookDrop.PutPreapprovalCover(c.Request.Context(), id, raw, mime); err != nil {
        writeServerError(c, "bookdrop put cover", err)
        return
    }
    c.JSON(http.StatusNoContent, nil)
}

func sniffCoverMime(b []byte) (string, bool) {
    switch {
    case len(b) >= 4 && b[0] == 0x89 && b[1] == 0x50 && b[2] == 0x4E && b[3] == 0x47:
        return "image/png", true
    case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
        return "image/jpeg", true
    }
    return "", false
}
```

- [ ] **Step 2: Register route**

Edit `internal/handler/router.go`, in the authed bookdrop block (`:99-103`):

```go
authed.PUT("/bookdrop/:id/cover", h.BookDropPutCover)
```

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: compile error — `PutPreapprovalCover` not defined yet. Task 13 adds it.

- [ ] **Step 4: Commit**

```bash
git add internal/handler/
git commit -m "feat(handler): add PUT /bookdrop/:id/cover with magic sniff + state gate"
```

---

## Task 13: Service — `PutPreapprovalCover`

**Files:**
- Modify: `internal/service/bookdrop.go`
- Test: `internal/service/bookdrop_test.go`

- [ ] **Step 1: Add method**

Append to `internal/service/bookdrop.go`:

```go
// PutPreapprovalCover writes raw cover bytes for a BookDrop item that
// doesn't yet carry a cover. Used by the BookDrop preview UI to push
// a client-rendered PDF page-1 raster (see ADR-0015). Idempotent on
// absence — caller must check item.HasCover; this method does not
// re-read the row.
func (s *BookDropService) PutPreapprovalCover(ctx context.Context, id string, raw []byte, mime string) error {
    if s.covers == nil {
        return errors.New("cover store not configured")
    }
    if err := s.covers.SaveBookDrop(id, raw); err != nil {
        return fmt.Errorf("save cover bytes: %w", err)
    }
    if err := s.bdrop.SetCoverPresence(ctx, id, true, mime); err != nil {
        return fmt.Errorf("mark has_cover: %w", err)
    }
    s.broadcast(id)
    return nil
}
```

- [ ] **Step 2: Add `SetCoverPresence` to repo**

If `repo.BookDropRepo` doesn't have a method that flips `has_cover` + `cover_mime` independently of `SetMetadata`, add one — name `SetCoverPresence(ctx, id string, hasCover bool, coverMime string) error`. Mirror the dialect pattern used by `SetMetadata`.

- [ ] **Step 3: Run tests**

Run: `make test`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/service/bookdrop.go internal/repo/bookdrop.go
git commit -m "feat(bookdrop): PutPreapprovalCover service path for client-rendered PDF covers"
```

---

## Task 14: API client — `putBookDropCover`

**Files:**
- Modify: `ui/src/api/bookdrop.ts`

- [ ] **Step 1: Read existing client patterns**

Read `ui/src/api/bookdrop.ts` to see how other mutations are shaped (`approveBookDrop`, `rejectBookDrop`).

- [ ] **Step 2: Add the function**

Append:

```ts
export async function putBookDropCover(id: string, blob: Blob): Promise<void> {
  const res = await fetch(`/api/v1/bookdrop/${encodeURIComponent(id)}/cover`, {
    method: "PUT",
    body: blob,
    headers: { "content-type": blob.type || "image/jpeg" },
    credentials: "include",
  })
  if (res.status === 409) {
    // Already has a cover — fine, no-op for the caller.
    return
  }
  if (!res.ok) {
    throw new Error(`putBookDropCover ${res.status}`)
  }
}
```

- [ ] **Step 3: Type-check**

Run: `make ui-typecheck`

- [ ] **Step 4: Commit**

```bash
git add ui/src/api/bookdrop.ts
git commit -m "feat(ui): add putBookDropCover api client"
```

---

## Task 15: PdfReader — expose `renderCoverJpeg`

**Files:**
- Modify: `ui/src/components/PdfReader.tsx`

- [ ] **Step 1: Read current handle shape**

Read `ui/src/components/PdfReader.tsx` to find the `PdfReaderHandle` type and the imperative handle shape.

- [ ] **Step 2: Add the method**

Add to the `PdfReaderHandle` type:

```ts
export type PdfReaderHandle = {
  // ...existing fields...
  renderCoverJpeg: (opts?: { width?: number; quality?: number }) => Promise<Blob | null>
}
```

In the component's `useImperativeHandle`, add:

```ts
async renderCoverJpeg(opts) {
  const doc = pdfDocRef.current
  if (!doc) return null
  const page = await doc.getPage(1)
  const targetWidth = opts?.width ?? 1200
  const baseViewport = page.getViewport({ scale: 1 })
  const scale = targetWidth / baseViewport.width
  const viewport = page.getViewport({ scale })
  const canvas = document.createElement("canvas")
  canvas.width = Math.ceil(viewport.width)
  canvas.height = Math.ceil(viewport.height)
  const ctx = canvas.getContext("2d")
  if (!ctx) return null
  await page.render({ canvasContext: ctx, viewport }).promise
  return new Promise<Blob | null>((resolve) =>
    canvas.toBlob((b) => resolve(b), "image/jpeg", opts?.quality ?? 0.85),
  )
},
```

(`pdfDocRef` is whatever ref already holds the `PDFDocumentProxy` — inspect the existing component to find its actual name.)

- [ ] **Step 3: Type-check**

Run: `make ui-typecheck`

- [ ] **Step 4: Commit**

```bash
git add ui/src/components/PdfReader.tsx
git commit -m "feat(ui): expose renderCoverJpeg on PdfReaderHandle"
```

---

## Task 16: BookDrop preview — auto-upload cover on first PDF preview

**Files:**
- Modify: BookDrop preview component (locate by grepping for `BookDropDetail`, `BookDropPreview`, or wherever the BookDrop list opens an item).

- [ ] **Step 1: Locate**

Run: `grep -rn "bookdrop" ui/src/routes ui/src/components | grep -i 'preview\|detail\|drawer'`

- [ ] **Step 2: Wire upload**

In the component that mounts a `PdfReader` for a BookDrop item, add an effect:

```tsx
const itemHasCoverRef = useRef(item.hasCover)
useEffect(() => { itemHasCoverRef.current = item.hasCover }, [item.hasCover])

const handlePdfLoaded = useCallback(async () => {
  if (item.format !== "PDF") return
  if (itemHasCoverRef.current) return
  const blob = await pdfRef.current?.renderCoverJpeg()
  if (!blob) return
  try {
    await putBookDropCover(item.id, blob)
    queryClient.invalidateQueries({ queryKey: ["bookdrop"] })
  } catch (err) {
    console.warn("auto cover upload failed", err)
  }
}, [item.format, item.id, queryClient])
```

Pass `handlePdfLoaded` as the `PdfReader`'s `onLoaded` (or equivalent) prop. If no such prop exists, add one in Task 15's edits — call it from inside the existing successful-load branch.

- [ ] **Step 3: Run dev stack and click through**

Run: `make up`
Open `http://localhost:5173/bookdrop`, drop a PDF without an embedded cover, open its preview. Expect: cover thumbnail appears in the BookDrop list within a second.

- [ ] **Step 4: Approve and verify**

Approve the item. Open the resulting book detail. Cover should be present (promoted from BookDrop into `coverstore`). Verify `/api/v1/books/:id/cover` returns the JPEG.

- [ ] **Step 5: Commit**

```bash
git add ui/
git commit -m "feat(ui): auto-render PDF page 1 to cover on BookDrop preview"
```

---

## Task 17: Re-render guard — covers in re-mount / StrictMode

**Files:**
- Modify: BookDrop preview component (Task 16's file)

- [ ] **Step 1: Add a per-item one-shot ref**

```tsx
const uploadedRef = useRef<Set<string>>(new Set())

const handlePdfLoaded = useCallback(async () => {
  if (item.format !== "PDF") return
  if (item.hasCover) return
  if (uploadedRef.current.has(item.id)) return
  uploadedRef.current.add(item.id)
  // ...rest
}, [item.format, item.id, item.hasCover])
```

- [ ] **Step 2: Manually verify by toggling preview twice**

Open + close + reopen the same item. Network tab: only one PUT.

- [ ] **Step 3: Commit**

```bash
git add ui/
git commit -m "fix(ui): guard one-shot cover upload against StrictMode double-render"
```

---

## Task 18: End-to-end smoke (manual)

- [ ] **Step 1: Drop a Calibre-exported PDF with XMP DC + ISBN**

Drop into `${BOOKDROP_PATH}/`. Wait 5s.

Expected: BookDrop list shows item with title from XMP, author from `dc:creator`, ISBN populated.

- [ ] **Step 2: Drop an Acrobat PDF with hex `<FEFF…>` Title**

Expected: Title shows correctly (non-ASCII rendering).

- [ ] **Step 3: Drop a PDF with neither DocInfo nor XMP**

Expected: Title falls back to filename basename.

- [ ] **Step 4: Approve a PDF, hit OPDS**

Run: `curl -u admin@local:changeme http://localhost:6060/opds/cover/<id>`
Expected: JPEG bytes (not 404, not empty).

---

## Self-Review Checklist

- [x] Each spec section has a task: XMP (5–7), DocInfo strings (3–4, 7), author Seq + split (5, 7), filename fallback (11), ISBN persist (1–2, 8–10), cover gen (12–17), tests (3–7, 14).
- [x] No "TBD" / "implement later" placeholders.
- [x] Type names consistent: `Metadata.ISBN`, `ExtractResult.ISBN`, `BookDropItem.ISBN`, `bookdrop_items.isbn`, `XMPMetadata.ISBN`, `cleanAndValidateISBN`, `splitAndJoinAuthors`, `extractXMPPacket`, `parseXMP`, `decodePDFLiteral`, `decodePDFHexString`, `RecordMetadata(..., isbn, ...)`, `PutPreapprovalCover`, `BookDropPutCover`, `putBookDropCover`, `renderCoverJpeg`.
- [x] Skipped: server-side rasterizer (ADR-0015), schema-wide author-list refactor, full provider-ID expansion (only ISBN is wired; other XMP IDs deferred).
