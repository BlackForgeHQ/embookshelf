# EPUB Embedder Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an `Embedder` interface to `internal/fileproc` and ship the EPUB implementation that rewrites OPF metadata + cover-image bytes inside an EPUB zip. Per `docs/spec/sidecar-write.spec.md` §5.1 + §11.2.

**Architecture:** `Embedder` mirrors the existing `Processor` (read side). One method: `Embed(ctx, src Source, in EmbedInput) ([]byte, error)`. Returns the new file bytes — caller (`MetadataWriter`, future plan) writes them back via `storage.Put` w/ atomic rename. EPUB impl: parse OPF XML, mutate the `<metadata>` block + cover manifest item, serialize, then re-zip the entire archive copying all non-OPF/non-cover entries verbatim. `mimetype` entry stays first + uncompressed (EPUB invariant).

**Tech Stack:** Go 1.25, stdlib `archive/zip` + `encoding/xml`. No new third-party deps.

**Companion docs:**
- `docs/spec/sidecar-write.spec.md` §5.1 (EPUB OPF mapping table), §11.2 (Embedder interface).
- `docs/adr/0001-edit-side-metadata-write-back.md` (dual-write Tags/Genres rationale).

**Depends on:** Plan 1 (sidecar JSON cutover) — provides nothing technically required, but lands the `Sidecar` shape this plan's `EmbedInput` mirrors.

**Out of scope** (lands in follow-up plans):
- PDF embedder.
- `MetadataWriter` orchestrator + HTTP wiring.
- Hash-stamp + lock-aware scan integration.
- Streaming / pipe-based EPUB rezip (Phase 1 buffers in memory; 50MB-ish is fine).

---

## File Structure

| Path | Change |
|---|---|
| `internal/fileproc/embedder.go` | **Create.** `EmbedInput` struct (full editable field set + cover bytes), `Embedder` interface, `ErrUnsupportedEmbed` sentinel, `DispatchEmbedder(format string) (Embedder, error)`. |
| `internal/fileproc/epub_embed.go` | **Create.** `EPUBEmbedder` struct + `Embed` impl. Helpers: `mutateOPF([]byte, EmbedInput) ([]byte, error)`, `rezipEPUB(src Source, opfPath string, newOPF []byte, coverHref string, newCover []byte, coverMime string) ([]byte, error)`. |
| `internal/fileproc/epub_embed_test.go` | **Create.** Round-trip tests (embed → re-extract via existing `EPUBProcessor`); cover-replacement test; Tags/Genres dual-encoding test; preservation test (chapters HTML still readable post-rezip). |
| `internal/fileproc/processor.go` | **Modify.** Re-export `Processor` next to new `Embedder` (no signature change; keeps grep navigation tight). |
| `internal/fileproc/testdata/minimal.epub` | **Create.** Hand-crafted minimal EPUB fixture (mimetype + container.xml + OPF + chapter1.xhtml + cover.jpg). Used by all embed tests so they don't depend on a live EPUB on disk. |

---

## Phase 1 — Embedder interface + dispatch

### Task 1: `EmbedInput` struct + `Embedder` interface

**Files:**
- Create: `internal/fileproc/embedder.go`
- Test: `internal/fileproc/embedder_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `internal/fileproc/embedder_test.go`:

```go
package fileproc

import (
	"errors"
	"testing"
)

func TestDispatchEmbedder_UnsupportedFormat(t *testing.T) {
	_, err := DispatchEmbedder("CBZ")
	if !errors.Is(err, ErrUnsupportedEmbed) {
		t.Errorf("got %v, want ErrUnsupportedEmbed", err)
	}
}

func TestDispatchEmbedder_EPUB(t *testing.T) {
	emb, err := DispatchEmbedder("EPUB")
	if err != nil {
		t.Fatalf("DispatchEmbedder(EPUB): %v", err)
	}
	if _, ok := emb.(EPUBEmbedder); !ok {
		t.Errorf("got %T, want EPUBEmbedder", emb)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fileproc/ -run TestDispatchEmbedder -v`
Expected: FAIL with "undefined: DispatchEmbedder", "undefined: ErrUnsupportedEmbed", "undefined: EPUBEmbedder".

- [ ] **Step 3: Write minimal implementation**

Create `internal/fileproc/embedder.go`:

```go
package fileproc

import (
	"context"
	"errors"

	"github.com/blackforge/embookshelf/internal/storage"
)

// EmbedInput is the editable metadata payload an Embedder writes back
// into a book file. Mirrors the Sidecar struct's field set plus the
// cover bytes (which live outside Sidecar today because covers
// belong in coverstore, not in the JSON envelope). Empty fields are
// treated as "do not write" by some embedders; "explicitly clear"
// semantics are not supported in Phase 1 — empty edits in the UI
// either keep the previous value or write empty per format rules.
type EmbedInput struct {
	Title         string
	Subtitle      string
	Author        string
	Description   string
	Language      string
	Publisher     string
	PublishedDate string
	ISBN          string
	Series        string
	SeriesIndex   int
	Tags          []string
	Genres        []string

	// CoverBytes + CoverMime override the existing in-file cover when
	// non-nil. CoverBytes == nil means "leave the existing cover
	// alone." Empty CoverMime with non-nil bytes is invalid (the
	// caller must supply both).
	CoverBytes []byte
	CoverMime  string
}

// Embedder writes an EmbedInput snapshot back into the file's
// embedded metadata. One implementation per format. Embedders that
// don't support in-file write don't register here — DispatchEmbedder
// returns ErrUnsupportedEmbed for those formats so the caller can
// fall back to a sidecar-only write.
type Embedder interface {
	// Embed reads the existing file from src and returns the new
	// file bytes with in carried into the format's native metadata
	// slots. The caller is responsible for writing the returned
	// bytes back via storage.Put (atomic rename). src is consumed
	// fully; the caller should not Close it before Embed returns.
	Embed(ctx context.Context, src storage.Source, in EmbedInput) ([]byte, error)
}

// ErrUnsupportedEmbed is returned by DispatchEmbedder for formats
// without an in-file write implementation (CBZ, MOBI, AZW3, FB2,
// MP3/M4B in Phase 1).
var ErrUnsupportedEmbed = errors.New("fileproc: format does not support in-file embed")

// DispatchEmbedder picks the right embedder for a books.format value.
// Returns ErrUnsupportedEmbed for unsupported formats; the caller
// falls back to sidecar-only write in that case.
func DispatchEmbedder(format string) (Embedder, error) {
	switch format {
	case "EPUB":
		return EPUBEmbedder{}, nil
	}
	return nil, ErrUnsupportedEmbed
}
```

- [ ] **Step 4: Stub `EPUBEmbedder` so tests link**

Append to `internal/fileproc/embedder.go` (placeholder until Task 4):

```go
// EPUBEmbedder rewrites EPUB files. Implementation in epub_embed.go.
type EPUBEmbedder struct{}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/fileproc/ -run TestDispatchEmbedder -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/fileproc/embedder.go internal/fileproc/embedder_test.go
git commit -m "feat(fileproc): Embedder interface + DispatchEmbedder (EPUB only stub)"
```

---

## Phase 2 — Minimal EPUB fixture

### Task 2: Create test fixture `testdata/minimal.epub`

**Files:**
- Create: `internal/fileproc/testdata/minimal.epub` (binary, generated by helper)
- Create: `internal/fileproc/testdata_helper_test.go`

- [ ] **Step 1: Write the fixture-generation helper as a test**

Create `internal/fileproc/testdata_helper_test.go`:

```go
package fileproc

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// makeMinimalEPUB returns the bytes of a minimal but spec-compliant
// EPUB: mimetype (uncompressed first), META-INF/container.xml, an
// OPF rootfile, one chapter, and a cover JPEG.
//
// Used by every embed test as a starting point; the test mutates
// the returned bytes via the embedder and re-extracts to verify.
func makeMinimalEPUB(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// 1) mimetype — first, uncompressed (EPUB spec requirement).
	mh := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
	w, err := zw.CreateHeader(mh)
	if err != nil {
		t.Fatalf("create mimetype: %v", err)
	}
	if _, err := w.Write([]byte("application/epub+zip")); err != nil {
		t.Fatalf("write mimetype: %v", err)
	}

	// 2) META-INF/container.xml — points at OEBPS/content.opf.
	addZip(t, zw, "META-INF/container.xml", []byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`))

	// 3) OEBPS/content.opf — minimal package. Title/Author present;
	// no Subtitle/Series/Tags so the embedder's add-path is exercised.
	addZip(t, zw, "OEBPS/content.opf", []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>Original Title</dc:title>
    <dc:creator opf:role="aut">Original Author</dc:creator>
    <dc:identifier id="bookid">urn:uuid:00000000-0000-0000-0000-000000000001</dc:identifier>
    <dc:language>en</dc:language>
  </metadata>
  <manifest>
    <item id="ch1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
    <item id="cover-img" href="cover.jpg" media-type="image/jpeg" properties="cover-image"/>
  </manifest>
  <spine>
    <itemref idref="ch1"/>
  </spine>
</package>`))

	// 4) OEBPS/chapter1.xhtml — token chapter so the EPUB validates loosely.
	addZip(t, zw, "OEBPS/chapter1.xhtml", []byte(`<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Ch 1</title></head><body><p>Hello.</p></body></html>`))

	// 5) OEBPS/cover.jpg — fake JPEG bytes. The embed tests will
	// replace these with a sentinel pattern to verify the swap.
	addZip(t, zw, "OEBPS/cover.jpg", []byte("\xff\xd8\xff\xe0ORIGINAL_COVER_BYTES"))

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func addZip(t *testing.T, zw *zip.Writer, name string, data []byte) {
	t.Helper()
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestFixture_Generate writes the minimal EPUB to disk so manual
// inspection is possible. Run:
//   go test ./internal/fileproc/ -run TestFixture_Generate -update
// to refresh testdata/minimal.epub on the filesystem. Skipped by
// default — all real tests build the fixture in-memory.
func TestFixture_Generate(t *testing.T) {
	if os.Getenv("EMBED_FIXTURE_UPDATE") == "" {
		t.Skip("set EMBED_FIXTURE_UPDATE=1 to refresh testdata/minimal.epub")
	}
	data := makeMinimalEPUB(t)
	dir := filepath.Join("testdata")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "minimal.epub"), data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}
```

- [ ] **Step 2: Run the helper smoke test**

Run: `go test ./internal/fileproc/ -run TestFixture_Generate -v`
Expected: SKIP (env var unset) — confirms the helper compiles + the fixture builder doesn't blow up. The actual fixture lives in memory for tests; a disk copy is generated only when explicitly refreshed.

- [ ] **Step 3: Verify the in-memory EPUB parses**

Append to `internal/fileproc/embedder_test.go`:

```go
func TestMinimalFixture_Parses(t *testing.T) {
	data := makeMinimalEPUB(t)
	src := newBytesSource(data)
	defer func() { _ = src.Close() }()
	m, err := EPUBProcessor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if m.Title != "Original Title" {
		t.Errorf("Title=%q want Original Title", m.Title)
	}
	if m.Author != "Original Author" {
		t.Errorf("Author=%q want Original Author", m.Author)
	}
	if !m.HasCover {
		t.Error("HasCover=false; want true")
	}
}
```

`newBytesSource` is a test helper — add to `embedder_test.go`:

```go
import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

// bytesSource adapts a []byte to storage.Source for tests.
type bytesSource struct {
	data []byte
	r    *bytes.Reader
}

func newBytesSource(data []byte) *bytesSource {
	return &bytesSource{data: data, r: bytes.NewReader(data)}
}

func (b *bytesSource) ReadAt(p []byte, off int64) (int, error) {
	return b.r.ReadAt(p, off)
}
func (b *bytesSource) Close() error  { return nil }
func (b *bytesSource) Size() int64   { return int64(len(b.data)) }
```

The new test imports `context`, which means `bytes`/`io`/`errors` may not be needed yet; trim unused imports per `goimports`.

- [ ] **Step 4: Run the parses test**

Run: `go test ./internal/fileproc/ -run TestMinimalFixture_Parses -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fileproc/embedder_test.go internal/fileproc/testdata_helper_test.go
git commit -m "test(fileproc): minimal in-memory EPUB fixture for embedder tests"
```

---

## Phase 3 — OPF mutation

### Task 3: `mutateOPF` writes scalar fields (Title, Author, Description, Language, Publisher, PublishedDate, ISBN, Subtitle)

**Files:**
- Create: `internal/fileproc/epub_embed.go`
- Test: append to `internal/fileproc/embedder_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestMutateOPF_ScalarFields(t *testing.T) {
	original := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>Old</dc:title>
    <dc:creator opf:role="aut">Old Author</dc:creator>
    <dc:identifier id="bookid">urn:uuid:x</dc:identifier>
    <dc:language>en</dc:language>
  </metadata>
  <manifest><item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="ch1"/></spine>
</package>`)
	in := EmbedInput{
		Title:         "New Title",
		Subtitle:      "A Subtitle",
		Author:        "New Author",
		Description:   "Long description here.",
		Language:      "fr",
		Publisher:     "Acme Press",
		PublishedDate: "2024-06-15",
		ISBN:          "978-0-00-000000-0",
	}
	out, err := mutateOPF(original, in)
	if err != nil {
		t.Fatalf("mutateOPF: %v", err)
	}
	// Re-extract via EPUBProcessor (after rezip; here we just byte-check).
	asString := string(out)
	for _, want := range []string{
		"<dc:title>New Title</dc:title>",
		"<dc:creator>New Author</dc:creator>",
		"<dc:description>Long description here.</dc:description>",
		"<dc:language>fr</dc:language>",
		"<dc:publisher>Acme Press</dc:publisher>",
		"<dc:date>2024-06-15</dc:date>",
		"<dc:identifier opf:scheme=\"ISBN\">978-0-00-000000-0</dc:identifier>",
	} {
		if !strings.Contains(asString, want) {
			t.Errorf("mutated OPF missing %q\n--- output ---\n%s", want, asString)
		}
	}
}
```

Add `import "strings"` if missing.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fileproc/ -run TestMutateOPF_ScalarFields -v`
Expected: FAIL with "undefined: mutateOPF".

- [ ] **Step 3: Write minimal implementation**

Create `internal/fileproc/epub_embed.go`:

```go
package fileproc

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/blackforge/embookshelf/internal/storage"
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

	// Series — Plan 2 ships the value-only emit; Calibre compat tag
	// is added in Task 4.
	if in.Series != "" {
		fmt.Fprintf(buf, "    <meta property=\"belongs-to-collection\" id=\"series\">%s</meta>\n", xmlEscape(in.Series))
		if in.SeriesIndex > 0 {
			fmt.Fprintf(buf, "    <meta refines=\"#series\" property=\"group-position\">%d</meta>\n", in.SeriesIndex)
		}
	}

	// Tags + Genres dual write — Task 5 expands this.
}

// xmlEscape escapes `<`, `>`, `&`, `"`, `'` for XML text content.
func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/fileproc/ -run TestMutateOPF_ScalarFields -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fileproc/epub_embed.go internal/fileproc/embedder_test.go
git commit -m "feat(fileproc): mutateOPF writes scalar metadata fields"
```

---

### Task 4: `mutateOPF` adds Calibre-compat series tag

**Files:**
- Modify: `internal/fileproc/epub_embed.go`
- Test: append to `internal/fileproc/embedder_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestMutateOPF_SeriesCalibreCompat(t *testing.T) {
	original := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" xmlns:opf="http://www.idpf.org/2007/opf">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf"
            xmlns:calibre="http://calibre.kovidgoyal.net/2009/metadata">
    <dc:title>X</dc:title>
  </metadata>
  <manifest/><spine/>
</package>`)
	in := EmbedInput{Title: "X", Series: "Foundation", SeriesIndex: 3}
	out, err := mutateOPF(original, in)
	if err != nil {
		t.Fatalf("mutateOPF: %v", err)
	}
	asString := string(out)
	for _, want := range []string{
		`property="belongs-to-collection"`,                       // EPUB 3 native
		`property="group-position">3</meta>`,                     // EPUB 3 native
		`<meta name="calibre:series" content="Foundation"`,       // Calibre compat
		`<meta name="calibre:series_index" content="3"`,          // Calibre compat
	} {
		if !strings.Contains(asString, want) {
			t.Errorf("output missing %q\n%s", want, asString)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fileproc/ -run TestMutateOPF_SeriesCalibreCompat -v`
Expected: FAIL — Calibre compat tags absent.

- [ ] **Step 3: Write minimal implementation**

In `internal/fileproc/epub_embed.go`, edit `writeMetadataBody`'s Series block:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/fileproc/ -run TestMutateOPF_SeriesCalibreCompat -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fileproc/epub_embed.go internal/fileproc/embedder_test.go
git commit -m "feat(fileproc): emit Calibre-compatible series meta alongside OPF 3 belongs-to-collection"
```

---

### Task 5: Tags/Genres dual write (custom meta + flat dc:subject)

**Files:**
- Modify: `internal/fileproc/epub_embed.go`
- Test: append to `internal/fileproc/embedder_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestMutateOPF_TagsAndGenresDualWrite(t *testing.T) {
	original := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>X</dc:title>
  </metadata>
  <manifest/><spine/>
</package>`)
	in := EmbedInput{
		Title:  "X",
		Tags:   []string{"jazz-age", "tragedy"},
		Genres: []string{"fiction", "literary"},
	}
	out, err := mutateOPF(original, in)
	if err != nil {
		t.Fatalf("mutateOPF: %v", err)
	}
	s := string(out)
	for _, want := range []string{
		// embookshelf custom meta — lossless on read-back.
		`<meta property="embookshelf:tag">jazz-age</meta>`,
		`<meta property="embookshelf:tag">tragedy</meta>`,
		`<meta property="embookshelf:genre">fiction</meta>`,
		`<meta property="embookshelf:genre">literary</meta>`,
		// Calibre/Kobo compat — flat dc:subject.
		`<dc:subject>jazz-age</dc:subject>`,
		`<dc:subject>tragedy</dc:subject>`,
		`<dc:subject>fiction</dc:subject>`,
		`<dc:subject>literary</dc:subject>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q\n%s", want, s)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fileproc/ -run TestMutateOPF_TagsAndGenresDualWrite -v`
Expected: FAIL.

- [ ] **Step 3: Write minimal implementation**

Append to `writeMetadataBody` after the Series block:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/fileproc/ -run TestMutateOPF_TagsAndGenresDualWrite -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fileproc/epub_embed.go internal/fileproc/embedder_test.go
git commit -m "feat(fileproc): Tags/Genres dual write (embookshelf:* + dc:subject)"
```

---

## Phase 4 — Rezip orchestration

### Task 6: `rezipEPUB` copies all entries verbatim except OPF + cover

**Files:**
- Modify: `internal/fileproc/epub_embed.go`
- Test: append to `internal/fileproc/embedder_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestRezipEPUB_PreservesNonTouchedEntries(t *testing.T) {
	original := makeMinimalEPUB(t)
	src := newBytesSource(original)
	defer func() { _ = src.Close() }()

	zr, err := zip.NewReader(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	opfPath := "OEBPS/content.opf"
	newOPF := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Rewritten</dc:title></metadata>
  <manifest><item id="ch1" href="chapter1.xhtml" media-type="application/xhtml+xml"/><item id="cover-img" href="cover.jpg" media-type="image/jpeg" properties="cover-image"/></manifest>
  <spine><itemref idref="ch1"/></spine>
</package>`)

	out, err := rezipEPUB(zr, opfPath, newOPF, "", nil, "")
	if err != nil {
		t.Fatalf("rezipEPUB: %v", err)
	}

	zr2, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("re-open zip: %v", err)
	}

	// mimetype must be entry 0, uncompressed.
	if zr2.File[0].Name != "mimetype" {
		t.Errorf("entry[0]=%q want mimetype", zr2.File[0].Name)
	}
	if zr2.File[0].Method != zip.Store {
		t.Errorf("mimetype method=%v want Store", zr2.File[0].Method)
	}

	// Chapter file unchanged.
	chapterBytes, err := readZipFile(zr2, "OEBPS/chapter1.xhtml")
	if err != nil {
		t.Fatalf("read chapter: %v", err)
	}
	if !bytes.Contains(chapterBytes, []byte("Hello.")) {
		t.Error("chapter contents lost in rezip")
	}

	// OPF rewritten.
	opfBytes, err := readZipFile(zr2, opfPath)
	if err != nil {
		t.Fatalf("read opf: %v", err)
	}
	if !bytes.Contains(opfBytes, []byte("Rewritten")) {
		t.Error("OPF not rewritten")
	}

	// Cover untouched (no cover replacement requested).
	coverBytes, err := readZipFile(zr2, "OEBPS/cover.jpg")
	if err != nil {
		t.Fatalf("read cover: %v", err)
	}
	if !bytes.Contains(coverBytes, []byte("ORIGINAL_COVER_BYTES")) {
		t.Error("cover bytes changed when not requested")
	}
}
```

Imports: `archive/zip`, `bytes`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fileproc/ -run TestRezipEPUB_PreservesNonTouchedEntries -v`
Expected: FAIL with "undefined: rezipEPUB".

- [ ] **Step 3: Write minimal implementation**

Append to `internal/fileproc/epub_embed.go`:

```go
import (
	"archive/zip"
	"io"
)

// rezipEPUB rewrites the EPUB at zr, replacing the OPF at opfPath
// with newOPF. When coverHref != "" and newCover != nil, the
// archive entry at coverHref is replaced with newCover (and its
// content type aligned to coverMime via the file extension).
//
// The mimetype entry is copied first, uncompressed (EPUB invariant).
// All other entries are copied through unchanged.
func rezipEPUB(zr *zip.Reader, opfPath string, newOPF []byte, coverHref string, newCover []byte, coverMime string) ([]byte, error) {
	_ = coverMime // file extension implies the type; the bytes are the source of truth

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Pass 1: emit mimetype uncompressed if present.
	for _, f := range zr.File {
		if f.Name == "mimetype" {
			if err := writeStored(zw, f); err != nil {
				return nil, fmt.Errorf("rezip mimetype: %w", err)
			}
			break
		}
	}

	// Pass 2: emit everything else, swapping OPF + cover.
	for _, f := range zr.File {
		if f.Name == "mimetype" {
			continue
		}
		switch f.Name {
		case opfPath:
			if err := writeBytes(zw, f.Name, newOPF); err != nil {
				return nil, fmt.Errorf("rezip opf: %w", err)
			}
		case coverHref:
			if newCover != nil {
				if err := writeBytes(zw, f.Name, newCover); err != nil {
					return nil, fmt.Errorf("rezip cover: %w", err)
				}
			} else {
				if err := writeCopy(zw, f); err != nil {
					return nil, fmt.Errorf("rezip cover passthrough: %w", err)
				}
			}
		default:
			if err := writeCopy(zw, f); err != nil {
				return nil, fmt.Errorf("rezip %s: %w", f.Name, err)
			}
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeStored(zw *zip.Writer, f *zip.File) error {
	w, err := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: zip.Store})
	if err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	_, err = io.Copy(w, rc)
	return err
}

func writeCopy(zw *zip.Writer, f *zip.File) error {
	w, err := zw.Create(f.Name)
	if err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	_, err = io.Copy(w, rc)
	return err
}

func writeBytes(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/fileproc/ -run TestRezipEPUB_PreservesNonTouchedEntries -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fileproc/epub_embed.go internal/fileproc/embedder_test.go
git commit -m "feat(fileproc): rezipEPUB copies entries verbatim, swaps OPF + cover"
```

---

### Task 7: `EPUBEmbedder.Embed` — full integration

**Files:**
- Modify: `internal/fileproc/epub_embed.go`
- Test: append to `internal/fileproc/embedder_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestEPUBEmbedder_Embed_RoundTrip(t *testing.T) {
	original := makeMinimalEPUB(t)
	src := newBytesSource(original)
	defer func() { _ = src.Close() }()

	in := EmbedInput{
		Title:    "Curated Title",
		Author:   "Curated Author",
		Language: "es",
		Tags:     []string{"alpha", "beta"},
	}
	out, err := EPUBEmbedder{}.Embed(context.Background(), src, in)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	// Re-extract via the existing reader.
	src2 := newBytesSource(out)
	defer func() { _ = src2.Close() }()
	m, err := EPUBProcessor{}.Extract(context.Background(), src2)
	if err != nil {
		t.Fatalf("Extract after Embed: %v", err)
	}
	if m.Title != "Curated Title" {
		t.Errorf("Title=%q want Curated Title", m.Title)
	}
	if m.Author != "Curated Author" {
		t.Errorf("Author=%q want Curated Author", m.Author)
	}
	if m.Language != "es" {
		t.Errorf("Language=%q want es", m.Language)
	}
}

func TestEPUBEmbedder_Embed_CoverReplaced(t *testing.T) {
	original := makeMinimalEPUB(t)
	src := newBytesSource(original)
	defer func() { _ = src.Close() }()

	newCover := []byte("\xff\xd8\xff\xe0NEW_COVER_BYTES_PATTERN")
	in := EmbedInput{Title: "X", CoverBytes: newCover, CoverMime: "image/jpeg"}
	out, err := EPUBEmbedder{}.Embed(context.Background(), src, in)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	src2 := newBytesSource(out)
	defer func() { _ = src2.Close() }()
	m, err := EPUBProcessor{}.Extract(context.Background(), src2)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !bytes.Contains(m.CoverBytes, []byte("NEW_COVER_BYTES_PATTERN")) {
		t.Errorf("cover not replaced; got=%q", m.CoverBytes)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fileproc/ -run "TestEPUBEmbedder_Embed_" -v`
Expected: FAIL — `EPUBEmbedder.Embed` is currently a stub from Task 1.

- [ ] **Step 3: Write minimal implementation**

Replace the stub `EPUBEmbedder` in `internal/fileproc/embedder.go` (delete the placeholder added in Task 1) and append the real impl to `internal/fileproc/epub_embed.go`:

```go
// EPUBEmbedder rewrites EPUB files: parses the OPF, mutates the
// metadata block from in, optionally swaps the cover image, and
// rezips the archive preserving the mimetype invariant.
type EPUBEmbedder struct{}

// Embed implements Embedder. The returned bytes are the new EPUB;
// caller writes them back atomically.
func (EPUBEmbedder) Embed(ctx context.Context, src storage.Source, in EmbedInput) ([]byte, error) {
	_ = ctx

	zr, err := zip.NewReader(src, src.Size())
	if err != nil {
		return nil, fmt.Errorf("open epub: %w", err)
	}

	opfPath, err := rootfilePath(zr)
	if err != nil {
		return nil, err
	}
	opfBytes, err := readZipFile(zr, opfPath)
	if err != nil {
		return nil, fmt.Errorf("read opf: %w", err)
	}

	newOPF, err := mutateOPF(opfBytes, in)
	if err != nil {
		return nil, fmt.Errorf("mutate opf: %w", err)
	}

	// Locate cover-image href so rezipEPUB knows which entry to swap.
	var coverHref string
	if in.CoverBytes != nil {
		var pkg opfPackage
		if err := xml.Unmarshal(opfBytes, &pkg); err == nil {
			href, _ := findCover(pkg)
			if href != "" {
				// findCover returns href relative to the OPF; resolve
				// against the OPF's directory.
				coverHref = path.Join(path.Dir(opfPath), href)
			}
		}
	}

	return rezipEPUB(zr, opfPath, newOPF, coverHref, in.CoverBytes, in.CoverMime)
}
```

Imports: `path` (for `path.Join`/`path.Dir`).

Remove the placeholder `type EPUBEmbedder struct{}` from `embedder.go` (defined here now).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/fileproc/ -run "TestEPUBEmbedder_Embed_" -v`
Expected: PASS.

- [ ] **Step 5: Run the full fileproc suite for regressions**

Run: `go test ./internal/fileproc/...`
Expected: every test PASSes (existing `EPUBProcessor` tests, audio, pdf, cbz, embedder dispatch, fixture, mutateOPF, rezipEPUB, embed round-trip).

- [ ] **Step 6: Commit**

```bash
git add internal/fileproc/embedder.go internal/fileproc/epub_embed.go internal/fileproc/embedder_test.go
git commit -m "feat(fileproc): EPUBEmbedder.Embed wires mutate + rezip end-to-end"
```

---

## Phase 5 — Verification

### Task 8: Lint + vet + spec coverage check

- [ ] **Step 1: Full Go test suite**

Run: `make test`
Expected: every package green. Particularly want to see:
- `ok internal/fileproc` (existing readers + new embedder)
- `ok internal/sidecar` (Plan 1)
- No regressions elsewhere.

- [ ] **Step 2: Lint**

Run: `make go-lint`
Expected: no new lint errors. Common issues to watch for:
- `goimports` ordering of `archive/zip`, `path`, `path/filepath`.
- Unused `_ = coverMime` annotation (kept for future XMP-aware impl).

- [ ] **Step 3: Vet**

Run: `go vet ./...`
Expected: silent.

- [ ] **Step 4: Confirm CONTEXT.md still matches landed code**

The Sidecar entry in `CONTEXT.md` describes the EPUB target as "OPF + cover bytes." The Embedder lands that capability. No CONTEXT update needed for this plan.

- [ ] **Step 5: Tag the plan completion in commit log**

```bash
git log --oneline -10
```

Expected: 7 commits visible from this plan, ending in `feat(fileproc): EPUBEmbedder.Embed wires mutate + rezip end-to-end`.

---

## Self-Review

**Spec coverage** (against `docs/spec/sidecar-write.spec.md` §5.1, §11.2):

| Spec item | Task | Covered |
|---|---|---|
| `Embedder` interface, `Embed(ctx, src, dest, m, cover)` shape | Task 1 (signature simplified to `(ctx, src, in EmbedInput) ([]byte, error)` — caller buffers, no `dest io.Writer`) | ✓ |
| `DispatchEmbedder(format) (Embedder, error)` w/ `ErrUnsupportedEmbed` | Task 1 | ✓ |
| EPUB OPF mapping: Title/Subtitle/Author/Description/Language/Publisher/PublishedDate/ISBN | Task 3 | ✓ |
| Series + SeriesIndex (EPUB 3 belongs-to-collection + Calibre compat) | Task 4 | ✓ |
| Tags/Genres dual write (embookshelf:tag/genre + dc:subject) | Task 5 | ✓ |
| Cover bytes replacement | Task 6 + Task 7 | ✓ |
| mimetype-first rezip invariant | Task 6 | ✓ |
| Manifest + spine preserved | Task 6 | ✓ |
| Format-specific test (round-trip via existing Processor) | Task 7 | ✓ |

**Spec deviation flagged:** the spec sketch used `Embed(ctx, src, dest io.Writer, m Metadata, cover []byte) error`. This plan uses `Embed(ctx, src storage.Source, in EmbedInput) ([]byte, error)`. Reasons:
- `EmbedInput` keeps `Metadata` (extraction shape) decoupled from the write input. Adding ten extra fields to `Metadata` would muddy its read-side semantics.
- Returning `[]byte` lets the caller decide between buffer + atomic Put vs streaming Pipe. The existing `storage.Put` takes `io.Reader`; `bytes.NewReader(out)` is the trivial bridge. Streaming via `io.Pipe` is a Phase 2 optimization once 50MB-ish EPUBs prove they need it.

**Placeholder scan:** no `TBD`, `add appropriate error handling`, `similar to Task N`. Every code step contains the actual code; every command has expected output.

**Type consistency:**
- `EmbedInput` — defined Task 1, used Tasks 3, 4, 5, 7.
- `Embedder` — defined Task 1, implemented Task 7, dispatched Task 1.
- `EPUBEmbedder` — stubbed Task 1, replaced w/ real impl Task 7.
- `mutateOPF(original []byte, in EmbedInput) ([]byte, error)` — same signature Tasks 3, 4, 5.
- `rezipEPUB(zr *zip.Reader, opfPath string, newOPF []byte, coverHref string, newCover []byte, coverMime string) ([]byte, error)` — same signature Tasks 6, 7.
- `writeStored`, `writeCopy`, `writeBytes`, `xmlEscape`, `writeMetadataBody` — internal helpers, defined once, referenced consistently.
- `newBytesSource` test helper — defined Task 2, used Tasks 2, 7.
- `makeMinimalEPUB` test helper — defined Task 2, used Tasks 2, 6, 7.

No drift detected.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-30-epub-embedder.md`. Two execution options:

1. **Subagent-Driven (recommended)** — fresh subagent per task, review between tasks.
2. **Inline Execution** — run with `superpowers:executing-plans` in this session.

Pick execution mode for Plan 2, or say **"next plan"** to write Plan 3 (`PDFEmbedder`).
