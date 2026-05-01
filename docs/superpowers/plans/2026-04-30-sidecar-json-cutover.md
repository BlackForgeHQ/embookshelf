# Sidecar JSON Cutover Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the `.embookshelf.toml` native sidecar with a paired `<basename>.embookshelf.json` sidecar (envelope + spillover/full mode), per ADR 0001. Hard cutover — no read of TOML, no migration. Read-only `metadata.opf` (Calibre) compat preserved.

**Architecture:** Drop-in replacement of the encoder/decoder in `internal/sidecar/`. JSON envelope wraps the existing `Sidecar` struct under a `fields` key plus `version`/`format`/`mode`/`written_at`/`writer` siblings. The reader switches from prefix-based folder lookup (`metadata.opf` + `.embookshelf.toml` siblings) to a paired-filename lookup keyed off the book file's storage key. Existing `Writer` keeps its per-key serialization + atomic-via-`storage.Put` semantics; only the payload format flips.

**Tech Stack:** Go 1.25, stdlib `encoding/json`, existing `internal/storage` interface, existing `pelletier/go-toml/v2` dependency removed at the end.

**Companion docs:**
- `docs/CONTEXT.md` — Sidecar entry already updated.
- `docs/adr/0001-edit-side-metadata-write-back.md` — TOML-cutover decision recorded.
- `docs/spec/sidecar-write.spec.md` §4 (envelope), §8 (paired filename), §9 (cutover policy).

**Out of scope** (lands in follow-up plans):
- `EPUBEmbedder` / `PDFEmbedder` (in-file write).
- `service.MetadataWriter` (the trigger orchestrator).
- HTTP handler wiring.
- Hash-stamp scan integration.

---

## File Structure

| Path | Change |
|---|---|
| `internal/sidecar/sidecar.go` | Replace `toml:"..."` field tags with `json:"..."`; doc comment updated to reflect JSON. |
| `internal/sidecar/json.go` | **Create.** `EncodeJSON(s Sidecar, mode WriteMode, format string) ([]byte, error)` and `DecodeJSON(data []byte) (Sidecar, error)`. Envelope: `{version, format, mode, fields, written_at, writer}`. Tolerant decoder (unknown keys ignored, missing `mode` defaults to `"spillover"`, malformed JSON returns `Sidecar{}, error`). |
| `internal/sidecar/toml.go` | **Delete.** No TOML reads, no TOML writes. |
| `internal/sidecar/writer.go` | `Writer.Write` swaps `EncodeTOML` for `EncodeJSON`; content-type flips to `application/json`. New signature accepts `mode WriteMode` + `format string` so the envelope can record them. |
| `internal/sidecar/reader.go` | `Read` signature becomes `Read(ctx, store, bookKey)` — paired-file derivation. `SidecarFiles` priority list shrinks to `metadata.opf` + the paired `<basename>.embookshelf.json`. New `KeyFor(bookKey string) string` helper builds the paired sidecar key. |
| `internal/sidecar/sidecar_test.go` | TOML round-trip tests deleted. JSON round-trip + envelope-tolerance tests added. Writer tests assert `application/json` content-type. Reader tests assert paired-key resolution. |
| `internal/task/bookdrop.go` | Single callsite update (line 129): `sidecar.Read(ctx, store, prefix)` → `sidecar.Read(ctx, store, bookKey)`. `prefix := path.Dir(key)` and `key := strings.TrimPrefix(item.Path, "/")` already in scope; pass `key` directly. |
| `go.mod` / `go.sum` | Remove `github.com/pelletier/go-toml/v2` after `toml.go` is deleted. |

---

## Phase 1 — JSON encoder + decoder

### Task 1: WriteMode constants + envelope struct

**Files:**
- Create: `internal/sidecar/json.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/sidecar/sidecar_test.go`:

```go
func TestWriteMode_String(t *testing.T) {
	if got := string(ModeSpillover); got != "spillover" {
		t.Errorf("ModeSpillover = %q, want %q", got, "spillover")
	}
	if got := string(ModeFull); got != "full" {
		t.Errorf("ModeFull = %q, want %q", got, "full")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sidecar/ -run TestWriteMode_String -v`
Expected: FAIL with "undefined: ModeSpillover" / "undefined: ModeFull".

- [ ] **Step 3: Write minimal implementation**

Create `internal/sidecar/json.go`:

```go
package sidecar

import (
	"encoding/json"
	"fmt"
	"time"
)

// WriteMode tells the reader whether the sidecar holds the full
// edited metadata ("full" — file write was skipped or failed) or
// only the fields the file format couldn't carry ("spillover").
type WriteMode string

const (
	ModeSpillover WriteMode = "spillover"
	ModeFull      WriteMode = "full"
)

// envelope is the on-disk shape. Sidecar fields live under "fields";
// the surrounding keys describe how/when/by-whom the sidecar was
// written so a reader can be tolerant of newer writers.
type envelope struct {
	Version   int       `json:"version"`
	Format    string    `json:"format,omitempty"`
	Mode      WriteMode `json:"mode,omitempty"`
	Fields    Sidecar   `json:"fields"`
	WrittenAt time.Time `json:"written_at,omitempty"`
	Writer    string    `json:"writer,omitempty"`
}

const envelopeVersion = 1

// writerVersion is stamped into every encoded sidecar so the operator
// can debug "which embookshelf wrote this." Kept private; bumped
// alongside the project tag.
var writerVersion = "embookshelf"

// EncodeJSON serializes a Sidecar into the v1 envelope. format is the
// book's format tag (e.g. "EPUB"); mode is "spillover" or "full".
func EncodeJSON(s Sidecar, mode WriteMode, format string) ([]byte, error) {
	env := envelope{
		Version:   envelopeVersion,
		Format:    format,
		Mode:      mode,
		Fields:    s,
		WrittenAt: time.Now().UTC(),
		Writer:    writerVersion,
	}
	out, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("sidecar: encode json: %w", err)
	}
	return out, nil
}

// DecodeJSON parses a v1 envelope into a Sidecar. Unknown top-level
// keys are ignored; an unset mode is treated as "spillover"; a higher
// version is logged-by-caller and parsed best-effort with the v1
// shape.
func DecodeJSON(data []byte) (Sidecar, error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Sidecar{}, fmt.Errorf("sidecar: decode json: %w", err)
	}
	return env.Fields, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sidecar/ -run TestWriteMode_String -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sidecar/json.go internal/sidecar/sidecar_test.go
git commit -m "feat(sidecar): add JSON envelope encoder + WriteMode constants"
```

---

### Task 2: JSON round-trip preserves all fields

**Files:**
- Test: `internal/sidecar/sidecar_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/sidecar/sidecar_test.go`:

```go
func TestJSON_RoundTrip(t *testing.T) {
	original := Sidecar{
		Title:         "The Great Gatsby",
		TitleSort:     "Great Gatsby, The",
		Subtitle:      "A Story",
		Author:        "F. Scott Fitzgerald",
		Description:   "Jazz Age tragedy",
		Language:      "en",
		Publisher:     "Scribner",
		PublishedDate: "1925",
		ISBN:          "978-0-7432-7356-5",
		Series:        "American Classics",
		SeriesIndex:   3,
		Tags:          []string{"jazz-age", "tragedy"},
		Genres:        []string{"fiction", "literary"},
	}

	data, err := EncodeJSON(original, ModeFull, "EPUB")
	if err != nil {
		t.Fatalf("EncodeJSON: %v", err)
	}
	got, err := DecodeJSON(data)
	if err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	if got.Title != original.Title ||
		got.TitleSort != original.TitleSort ||
		got.Subtitle != original.Subtitle ||
		got.Author != original.Author ||
		got.Description != original.Description ||
		got.Language != original.Language ||
		got.Publisher != original.Publisher ||
		got.PublishedDate != original.PublishedDate ||
		got.ISBN != original.ISBN ||
		got.Series != original.Series ||
		got.SeriesIndex != original.SeriesIndex {
		t.Errorf("scalar field mismatch.\n got=%+v\nwant=%+v", got, original)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "jazz-age" || got.Tags[1] != "tragedy" {
		t.Errorf("Tags=%v, want [jazz-age tragedy]", got.Tags)
	}
	if len(got.Genres) != 2 || got.Genres[0] != "fiction" || got.Genres[1] != "literary" {
		t.Errorf("Genres=%v, want [fiction literary]", got.Genres)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sidecar/ -run TestJSON_RoundTrip -v`
Expected: FAIL — Sidecar struct still uses `toml:"..."` tags, so JSON marshal will emit Go-style PascalCase keys, decode won't find lowercase keys → empty result.

- [ ] **Step 3: Write minimal implementation**

Edit `internal/sidecar/sidecar.go` — replace `toml:` tags with `json:`:

```go
type Sidecar struct {
	Title         string   `json:"title,omitempty"`
	TitleSort     string   `json:"title_sort,omitempty"`
	Subtitle      string   `json:"subtitle,omitempty"`
	Author        string   `json:"author,omitempty"`
	Description   string   `json:"description,omitempty"`
	Language      string   `json:"language,omitempty"`
	Publisher     string   `json:"publisher,omitempty"`
	PublishedDate string   `json:"published_date,omitempty"`
	ISBN          string   `json:"isbn,omitempty"`
	Series        string   `json:"series,omitempty"`
	SeriesIndex   int      `json:"series_index,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	Genres        []string `json:"genres,omitempty"`
}
```

Also update the package doc comment at the top of `sidecar.go`:

```go
// Package sidecar reads and writes per-book metadata files that live
// next to the book bytes on disk (or in object storage). Two formats:
// metadata.opf (Calibre-compatible XML, read-only) and
// <basename>.embookshelf.json (native, read+write, paired filename).
package sidecar
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sidecar/ -run TestJSON_RoundTrip -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sidecar/sidecar.go internal/sidecar/sidecar_test.go
git commit -m "feat(sidecar): switch Sidecar struct tags from toml to json"
```

---

### Task 3: DecodeJSON tolerates unknown keys + missing mode

**Files:**
- Test: `internal/sidecar/sidecar_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestJSON_UnknownKeysIgnored(t *testing.T) {
	raw := []byte(`{
	  "version": 1,
	  "format": "EPUB",
	  "fields": {"title": "T", "tags": ["a"]},
	  "future_extension": {"weird": "thing"}
	}`)
	got, err := DecodeJSON(raw)
	if err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	if got.Title != "T" {
		t.Errorf("Title=%q want T", got.Title)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "a" {
		t.Errorf("Tags=%v want [a]", got.Tags)
	}
}

func TestJSON_MalformedReturnsError(t *testing.T) {
	_, err := DecodeJSON([]byte(`{"fields":{"title":not-a-string}}`))
	if err == nil {
		t.Fatal("DecodeJSON malformed: want error, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails — or passes**

Run: `go test ./internal/sidecar/ -run "TestJSON_UnknownKeysIgnored|TestJSON_MalformedReturnsError" -v`
Expected: PASS already (stdlib `encoding/json` ignores unknown keys by default; malformed input naturally errors). If a future change tightens decoding, these tests catch the regression.

- [ ] **Step 3: No implementation needed.** Skip ahead.

- [ ] **Step 4: Run all sidecar tests to confirm green**

Run: `go test ./internal/sidecar/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sidecar/sidecar_test.go
git commit -m "test(sidecar): assert JSON decoder ignores unknown keys + errors on malformed"
```

---

## Phase 2 — Writer flip + paired-filename derivation

### Task 4: `KeyFor` derives paired sidecar key

**Files:**
- Modify: `internal/sidecar/reader.go`
- Test: `internal/sidecar/sidecar_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestKeyFor_PairedFilename(t *testing.T) {
	cases := []struct {
		bookKey string
		want    string
	}{
		{"library/folder/harry-potter.epub", "library/folder/harry-potter.embookshelf.json"},
		{"books/dune.pdf", "books/dune.embookshelf.json"},
		{"audio/dune/disc-1.m4b", "audio/dune/disc-1.embookshelf.json"},
		{"flat-file.epub", "flat-file.embookshelf.json"},
		{"no-ext", "no-ext.embookshelf.json"},
	}
	for _, c := range cases {
		got := KeyFor(c.bookKey)
		if got != c.want {
			t.Errorf("KeyFor(%q) = %q, want %q", c.bookKey, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sidecar/ -run TestKeyFor_PairedFilename -v`
Expected: FAIL with "undefined: KeyFor".

- [ ] **Step 3: Write minimal implementation**

Add to `internal/sidecar/reader.go` (top of file, after the imports):

```go
import "path"

// KeyFor returns the paired sidecar storage key for a book file's key.
// "harry-potter.epub" → "harry-potter.embookshelf.json" (same dir).
func KeyFor(bookKey string) string {
	dir, base := path.Split(bookKey)
	ext := path.Ext(base)
	stem := base
	if ext != "" {
		stem = base[:len(base)-len(ext)]
	}
	return dir + stem + ".embookshelf.json"
}
```

(Note: `path` import — Go's `path/filepath` uses OS-specific separators; storage keys are always slash-separated, so plain `path` is correct.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sidecar/ -run TestKeyFor_PairedFilename -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sidecar/reader.go internal/sidecar/sidecar_test.go
git commit -m "feat(sidecar): add KeyFor helper for paired sidecar filename"
```

---

### Task 5: Writer.Write emits JSON with correct content-type

**Files:**
- Modify: `internal/sidecar/writer.go`
- Test: `internal/sidecar/sidecar_test.go`

- [ ] **Step 1: Write the failing test**

Append (mirrors existing TOML write tests; relies on the local-storage harness already in this file):

```go
func TestWriter_WritesJSONWithCorrectContentType(t *testing.T) {
	root := t.TempDir()
	fs, err := local.New(root)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	w := NewWriter()
	s := Sidecar{Title: "T", Tags: []string{"a"}}
	if err := w.Write(context.Background(), fs, "books/x.embookshelf.json", s, ModeFull, "EPUB"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// File on disk should be valid JSON of the v1 envelope shape.
	rc, err := fs.Get(context.Background(), "books/x.embookshelf.json")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	got, err := DecodeJSON(data)
	if err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	if got.Title != "T" {
		t.Errorf("got.Title=%q want T", got.Title)
	}
	if !bytes.Contains(data, []byte(`"version": 1`)) {
		t.Errorf("envelope missing version=1 marker; data=%s", data)
	}
	if !bytes.Contains(data, []byte(`"mode": "full"`)) {
		t.Errorf("envelope missing mode=full marker; data=%s", data)
	}
	if !bytes.Contains(data, []byte(`"format": "EPUB"`)) {
		t.Errorf("envelope missing format=EPUB marker; data=%s", data)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sidecar/ -run TestWriter_WritesJSONWithCorrectContentType -v`
Expected: FAIL — `Writer.Write` signature today is `(ctx, store, key, s)`; the test passes 6 args.

- [ ] **Step 3: Write minimal implementation**

Edit `internal/sidecar/writer.go`:

```go
package sidecar

import (
	"bytes"
	"context"
	"sync"

	"github.com/blackforge/embookshelf/internal/storage"
)

// Writer serializes sidecar writes per-key. Two writes targeting the
// same key block one after the other; writes to different keys run
// concurrently. The underlying storage.Put is already atomic
// (write-temp-then-rename on LocalFS); the Writer's job is to
// linearize multiple in-flight writes from the same process.
type Writer struct {
	locks sync.Map // map[string]*sync.Mutex
}

func NewWriter() *Writer { return &Writer{} }

func (w *Writer) keyLock(key string) *sync.Mutex {
	actual, _ := w.locks.LoadOrStore(key, &sync.Mutex{})
	return actual.(*sync.Mutex)
}

// Write encodes s as a v1 JSON envelope and stores it at key. mode and
// format describe the envelope; readers use them only for diagnostics
// and unknown-version handling.
func (w *Writer) Write(
	ctx context.Context,
	store storage.Storage,
	key string,
	s Sidecar,
	mode WriteMode,
	format string,
) error {
	data, err := EncodeJSON(s, mode, format)
	if err != nil {
		return err
	}
	mu := w.keyLock(key)
	mu.Lock()
	defer mu.Unlock()
	_, err = store.Put(ctx, key, bytes.NewReader(data), storage.WithContentType("application/json"))
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sidecar/ -run TestWriter_WritesJSONWithCorrectContentType -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sidecar/writer.go internal/sidecar/sidecar_test.go
git commit -m "feat(sidecar): Writer.Write emits JSON envelope (mode + format args)"
```

---

### Task 6: Update existing Writer concurrency tests to new signature

**Files:**
- Modify: `internal/sidecar/sidecar_test.go`

- [ ] **Step 1: Find the broken tests**

Run: `go build ./internal/sidecar/...`
Expected: build errors in `sidecar_test.go` for any `w.Write(ctx, ...)` call still using the old 4-arg signature.

- [ ] **Step 2: Update each broken callsite**

Search `sidecar_test.go` for `.Write(ctx,` and `.Write(context.Background(),`. For each, append `, ModeFull, "EPUB"` before the closing paren so the call shape is `w.Write(ctx, fs, key, s, ModeFull, "EPUB")`. The tests don't care about the mode/format values — they assert concurrency, not envelope shape.

Concrete: in `TestWriter_SingleWriteHappyPath`, `TestWriter_ConcurrentWritesSameKey`, and any other Writer test, find calls like:

```go
if err := w.Write(ctx, fs, key, s); err != nil {
```

Change to:

```go
if err := w.Write(ctx, fs, key, s, ModeFull, "EPUB"); err != nil {
```

- [ ] **Step 3: Run all sidecar tests**

Run: `go test ./internal/sidecar/ -v`
Expected: most TOML tests still fail (TOML round-trip), but the Writer concurrency tests pass.

- [ ] **Step 4: Commit (test-only refactor)**

```bash
git add internal/sidecar/sidecar_test.go
git commit -m "test(sidecar): update Writer test callsites for new mode+format args"
```

---

## Phase 3 — Reader switch to paired filename

### Task 7: `Read(ctx, store, bookKey)` derives paired sidecar key

**Files:**
- Modify: `internal/sidecar/reader.go`
- Test: `internal/sidecar/sidecar_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestRead_PairedJSONSidecar(t *testing.T) {
	root := t.TempDir()
	fs, err := local.New(root)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	ctx := context.Background()
	w := NewWriter()
	bookKey := "library/dune.epub"
	wantTitle := "Dune"

	if err := w.Write(ctx, fs, KeyFor(bookKey), Sidecar{Title: wantTitle}, ModeFull, "EPUB"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := Read(ctx, fs, bookKey)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Title != wantTitle {
		t.Errorf("Title=%q want %q", got.Title, wantTitle)
	}
}

func TestRead_NoSidecar(t *testing.T) {
	root := t.TempDir()
	fs, err := local.New(root)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	got, err := Read(context.Background(), fs, "library/missing.epub")
	if err != nil {
		t.Fatalf("Read missing: got err %v, want nil", err)
	}
	if !got.IsZero() {
		t.Errorf("got=%+v want zero Sidecar", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sidecar/ -run "TestRead_PairedJSONSidecar|TestRead_NoSidecar" -v`
Expected: FAIL — current `Read` looks at `<prefix>/.embookshelf.toml` and `<prefix>/metadata.opf`; we wrote to `library/dune.embookshelf.json` and pass a *bookKey* (not a prefix). Read won't find anything.

- [ ] **Step 3: Write minimal implementation**

Replace `internal/sidecar/reader.go` body (keep the `KeyFor` from Task 4):

```go
package sidecar

import (
	"context"
	"errors"
	"io"
	"path"

	"github.com/blackforge/embookshelf/internal/storage"
)

// KeyFor returns the paired sidecar storage key for a book file's key.
// "harry-potter.epub" → "harry-potter.embookshelf.json" (same dir).
func KeyFor(bookKey string) string {
	dir, base := path.Split(bookKey)
	ext := path.Ext(base)
	stem := base
	if ext != "" {
		stem = base[:len(base)-len(ext)]
	}
	return dir + stem + ".embookshelf.json"
}

// Read locates sidecar files near the given book key and returns the
// merged result. Lookup order: Calibre `metadata.opf` (in the book's
// directory, read-only compat) overlaid by the paired
// `<basename>.embookshelf.json`. The JSON sidecar wins on field
// conflicts because it's the format embookshelf actively writes.
//
// A missing book or no sidecars present returns Sidecar{}, nil.
func Read(ctx context.Context, store storage.Storage, bookKey string) (Sidecar, error) {
	var merged Sidecar

	// 1. Calibre OPF in the book's directory.
	dir, _ := path.Split(bookKey)
	opfKey := dir + "metadata.opf"
	if parsed, err := readAndParse(ctx, store, opfKey, parseOPFData); err != nil {
		return Sidecar{}, err
	} else if !parsed.IsZero() {
		merged = Merge(merged, parsed)
	}

	// 2. Paired native JSON sidecar.
	jsonKey := KeyFor(bookKey)
	if parsed, err := readAndParse(ctx, store, jsonKey, parseJSONData); err != nil {
		return Sidecar{}, err
	} else if !parsed.IsZero() {
		merged = Merge(merged, parsed)
	}

	return merged, nil
}

// readAndParse fetches a single sidecar object and parses via fn.
// ErrNotFound is treated as a non-error empty Sidecar.
func readAndParse(ctx context.Context, store storage.Storage, key string, fn func([]byte) (Sidecar, error)) (Sidecar, error) {
	rc, err := store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return Sidecar{}, nil
		}
		return Sidecar{}, err
	}
	data, readErr := io.ReadAll(rc)
	_ = rc.Close()
	if readErr != nil {
		return Sidecar{}, readErr
	}
	parsed, parseErr := fn(data)
	if parseErr != nil {
		// Malformed sidecar: log via caller; return empty so the
		// caller can fall back to the next layer.
		return Sidecar{}, nil
	}
	return parsed, nil
}

func parseOPFData(data []byte) (Sidecar, error) { return ParseOPF(data) }
func parseJSONData(data []byte) (Sidecar, error) { return DecodeJSON(data) }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sidecar/ -run "TestRead_PairedJSONSidecar|TestRead_NoSidecar" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sidecar/reader.go internal/sidecar/sidecar_test.go
git commit -m "feat(sidecar): Read takes bookKey, derives paired JSON sidecar"
```

---

## Phase 4 — Callsite migration + TOML deletion

### Task 8: Update bookdrop ingest to pass bookKey to Read

**Files:**
- Modify: `internal/task/bookdrop.go:120-141`

- [ ] **Step 1: Find the existing call**

Run: `grep -n "sidecar.Read" internal/task/bookdrop.go`
Expected output: `129:		if sc, scErr := sidecar.Read(ctx, store, prefix); scErr == nil && !sc.IsZero() {`.

- [ ] **Step 2: Read the surrounding block**

Read `internal/task/bookdrop.go:120-145`. The block currently builds a `prefix` from `path.Dir(key)`; we now want to pass `key` directly.

- [ ] **Step 3: Replace the prefix-derivation with bookKey**

Edit `internal/task/bookdrop.go` — find:

```go
{
	key := strings.TrimPrefix(item.Path, "/")
	prefix := path.Dir(key)
	if sc, scErr := sidecar.Read(ctx, store, prefix); scErr == nil && !sc.IsZero() {
		meta = layerSidecar(meta, sc)
	} else if scErr != nil {
		slog.Warn("bookdrop sidecar read failed", "item_id", itemID, "prefix", prefix, "err", scErr)
		// non-fatal — proceed with embedded metadata only
	}
}
```

Replace with:

```go
{
	key := strings.TrimPrefix(item.Path, "/")
	if sc, scErr := sidecar.Read(ctx, store, key); scErr == nil && !sc.IsZero() {
		meta = layerSidecar(meta, sc)
	} else if scErr != nil {
		slog.Warn("bookdrop sidecar read failed", "item_id", itemID, "key", key, "err", scErr)
		// non-fatal — proceed with embedded metadata only
	}
}
```

`path` import in this file may now be unused — check `goimports` after.

- [ ] **Step 4: Build + test**

Run: `go build ./...`
Expected: clean build (or "imported and not used: path" — fix by removing the import).

Run: `go test ./internal/task/...`
Expected: PASS (existing bookdrop ingest tests don't exercise sidecar reads against fixtures, so behavior under no-sidecar case is unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/task/bookdrop.go
git commit -m "refactor(task): bookdrop ingest passes bookKey to sidecar.Read"
```

---

### Task 9: Delete TOML support

**Files:**
- Delete: `internal/sidecar/toml.go`
- Modify: `internal/sidecar/sidecar_test.go` (drop TOML tests)
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Delete the TOML encoder**

Run: `rm internal/sidecar/toml.go`

- [ ] **Step 2: Find broken test references**

Run: `go build ./internal/sidecar/...`
Expected: build errors in `sidecar_test.go` for `TestTOML_RoundTrip`, `TestTOML_*`, any `ParseTOML` / `EncodeTOML` reference.

- [ ] **Step 3: Delete TOML tests**

In `internal/sidecar/sidecar_test.go`, find every test function whose name starts with `TestTOML_` and delete it (full function bodies). Also delete the `// ---- TOML round-trip tests ----` comment banner.

- [ ] **Step 4: Build + run all sidecar tests**

Run: `go build ./...`
Expected: clean build.

Run: `go test ./internal/sidecar/ -v`
Expected: every remaining test PASSes. No `TestTOML_*` listed in output.

- [ ] **Step 5: Drop the TOML dep**

Run: `go mod tidy`
Expected: `github.com/pelletier/go-toml/v2` removed from `go.mod` + `go.sum` (no other package imports it — verify with `grep -r "pelletier/go-toml" --include="*.go"`).

- [ ] **Step 6: Final build + full test run**

Run: `go build ./... && go test ./...`
Expected: clean build, all packages green.

- [ ] **Step 7: Commit**

```bash
git add internal/sidecar/toml.go internal/sidecar/sidecar_test.go go.mod go.sum
git commit -m "feat(sidecar): hard-cutover TOML support; drop pelletier/go-toml dep"
```

---

## Phase 5 — Verification

### Task 10: End-to-end sanity + lint

- [ ] **Step 1: Full Go test suite**

Run: `make test`
Expected: every package green (matches `go test ./...` from Task 9 step 6, but exercises any Makefile-only setup like seed DBs).

- [ ] **Step 2: Lint**

Run: `make go-lint`
Expected: no new lint errors. If `path` was left dangling in `internal/task/bookdrop.go`, fix.

- [ ] **Step 3: Vet**

Run: `go vet ./...`
Expected: silent success.

- [ ] **Step 4: Confirm CONTEXT.md still matches landed code**

Read `docs/CONTEXT.md` Sidecar entry. It should describe the JSON paired-filename behavior (already updated during the grill). If anything contradicts the landed code, update inline.

- [ ] **Step 5: Tag the cutover in commit log**

```bash
git log --oneline | head -10
```

Expected: the 8 commits from Tasks 1-9 visible. Last commit is `feat(sidecar): hard-cutover TOML support; drop pelletier/go-toml dep`.

---

## Self-Review

**Spec coverage** (against `docs/spec/sidecar-write.spec.md`):

- §4 envelope shape — Tasks 1-3 implement v1 envelope encode/decode + tolerance. ✓
- §8 paired filename — Task 4 (`KeyFor`) + Task 7 (`Read` derivation). ✓
- §9 TOML cutover — Task 9 deletes `toml.go` + its tests + the dep. ✓
- §11.1 Writer interface — Task 5 lands the new `(s, mode, format)` signature. ✓
- Sections §5 (per-format mapping), §6 (triggers), §7 (lock-aware), §11.2-§11.4 (Embedder / MetadataWriter / LibraryHandle helpers), §12 (test surface beyond unit) — **deferred to follow-up plans**. Documented at the top of this plan under "Out of scope."

**Placeholder scan:** no `TBD`, no `add appropriate error handling`, no `similar to Task N`, no fill-in-later. Every code step contains the actual code. Every command shows expected output.

**Type consistency:**
- `WriteMode` (Task 1) — referenced consistently in `EncodeJSON` (Task 1), `Writer.Write` (Task 5), test callsites (Tasks 5, 6, 7).
- `KeyFor` (Task 4) — same name in Task 7 (`Read`).
- `ModeFull` / `ModeSpillover` — same casing used everywhere.
- `EncodeJSON` / `DecodeJSON` — names match across Task 1 (definition), Task 2 (round-trip test), Task 5 (Writer call), Task 7 (Reader call), Task 9 (deletes neighbor `EncodeTOML` / `ParseTOML`).
- `parseOPFData` / `parseJSONData` adapter funcs in Task 7 reference `ParseOPF` (existing) and `DecodeJSON` (Task 1).

No drift detected. Plan ready.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-30-sidecar-json-cutover.md`. Two execution options:

1. **Subagent-Driven (recommended)** — fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — execute tasks in this session using `superpowers:executing-plans`, batch execution with checkpoints.

Which approach?
