# Sidecar Read + Atomic Writes — Implementation Plan (Plan D of 8)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Establish portable, user-editable metadata sidecars next to each book on disk. Two formats: `metadata.opf` (Calibre-compatible XML) and `.embookshelf.toml` (native). Both are read at ingest time and merged with embedded metadata; the bookdrop pipeline picks them up before approval. Atomic writes via the `storage.Storage` interface (write-tmp-fsync-rename on local FS) with per-key serialization so concurrent edits queue cleanly.

**Architecture:** A new `internal/sidecar` package owns parsers, the merge policy, and the atomic writer. Read flow: scanner asks `sidecar.Read(store, folderPrefix)` which lists sibling keys, parses any matches, and merges (TOML over OPF). The merged `Sidecar` struct is then layered over `Metadata` from `internal/fileproc` (sidecar wins when non-empty). Write flow: `sidecar.Writer.Write(store, key, s)` serializes per-key via a `sync.Map` of mutexes, then calls `storage.Put` which already does write-temp-then-rename for local FS. S3 conditional `If-Match` writes land in Plan F.

**Tech Stack:** Go 1.25 stdlib `encoding/xml` (already used by fileproc), `github.com/pelletier/go-toml/v2` (already a transitive dep — promoted to direct here), `internal/storage`, `internal/fileproc` for shared metadata struct.

**Companion spec:** [`docs/spec/storage.spec.md`](../../spec/storage.spec.md) §3.3 (sidecar files), §6.1 (layering), §6.2 (atomic sidecar writes).

**Locked decisions:**
- Sidecar filenames: `metadata.opf` and `.embookshelf.toml` (native; spec said `.grimmory.toml` — repo is canonical).
- Merge order at read time: embedded < OPF < TOML (TOML is the user-editable native format; OPF is for interop). User app edits go to TOML; OPF stays as imported.
- Plan D ships READ + atomic-write **primitives**, plus the **bookdrop ingest read hook**. Plan D2 wires the user-edit write hook (`PUT /books/:id/metadata`) once the edit-metadata UI is verified.
- Write conditional precondition: deferred. Local writes are tmp+rename (already atomic); S3 conditional writes land with Plan F.

**Depends on:** Plan A (`storage.Storage`), Plan B (location keys), Plan C unrelated but compatible.

**Out of scope:**
- Wiring sidecar writes into `PUT /books/:id/metadata` (Plan D2).
- Writing OPF (we only read OPF; native edits go to TOML).
- Conflict resolution UI for concurrent edits across instances (single-instance assumed).
- Cover image inside sidecar — `metadata.opf` cover refs are followed in Plan E (cover store reorg).

---

## File Structure

### Files created

| Path | Responsibility |
|---|---|
| `internal/sidecar/sidecar.go` | `Sidecar` struct + `Layer(into *fileproc.Metadata)` helper. |
| `internal/sidecar/opf.go` | `ParseOPF([]byte) (Sidecar, error)` — Dublin Core XML. |
| `internal/sidecar/toml.go` | `ParseTOML([]byte) (Sidecar, error)`, `EncodeTOML(Sidecar) ([]byte, error)`. |
| `internal/sidecar/reader.go` | `Read(ctx, store, prefix) (Sidecar, error)` — locates sibling files in storage, parses, merges. |
| `internal/sidecar/writer.go` | `Writer` with per-key mutex. `Write(ctx, store, key, s)` calls `EncodeTOML` then `storage.Put`. |
| `internal/sidecar/sidecar_test.go` | Combined test file: parsers, merge, writer concurrency. |

### Files modified

| Path | Change |
|---|---|
| `go.mod` | Promote `github.com/pelletier/go-toml/v2` from indirect to direct. |
| `internal/task/bookdrop.go` | After `proc.Extract` returns metadata, look up sidecars via `sidecar.Read(ctx, store, dir(item.Path))` and layer them in. The Storage handle comes from a new field on `BookDropDeps`. |
| `internal/queue/queue.go` & `internal/queue/sqlite.go` | Pass `Storage` into `BookDropDeps` (extending the deps struct already used by Plan A's library scan). |
| `cmd/embookshelf/main.go` | Wire `fileStorage` into `BookDropDeps`. |

---

## Phase 1 — Parsers and Layering

### Task 1: `Sidecar` struct + TOML round-trip

**Files:**
- Create: `internal/sidecar/sidecar.go`
- Create: `internal/sidecar/toml.go`
- Create: `internal/sidecar/sidecar_test.go` (initially with TOML cases)

```go
// Package sidecar reads and writes per-book metadata files that live
// next to the book bytes on disk (or in object storage). Two formats:
// metadata.opf (Calibre-compatible XML, read-only) and
// .embookshelf.toml (native, read+write).
package sidecar

import (
    "fmt"

    "github.com/pelletier/go-toml/v2"
)

// Sidecar holds the editable subset of a book's metadata. Fields
// match what the edit-metadata UI exposes; anything ground-truth-
// derived (page count, duration, cover bytes) stays in the embedded
// extractor's output and is not overwritten by sidecars.
type Sidecar struct {
    Title         string   `toml:"title"`
    TitleSort     string   `toml:"title_sort"`
    Subtitle      string   `toml:"subtitle"`
    Author        string   `toml:"author"`
    Description   string   `toml:"description"`
    Language      string   `toml:"language"`
    Publisher     string   `toml:"publisher"`
    PublishedDate string   `toml:"published_date"` // free text per spec §4
    ISBN          string   `toml:"isbn"`
    Series        string   `toml:"series"`
    SeriesIndex   int      `toml:"series_index"`
    Tags          []string `toml:"tags"`
    Genres        []string `toml:"genres"`
}

// IsZero reports whether s carries no information. Used to short-
// circuit the merge when no sidecar was present.
func (s Sidecar) IsZero() bool {
    return s.Title == "" && s.TitleSort == "" && s.Subtitle == "" &&
        s.Author == "" && s.Description == "" && s.Language == "" &&
        s.Publisher == "" && s.PublishedDate == "" && s.ISBN == "" &&
        s.Series == "" && s.SeriesIndex == 0 &&
        len(s.Tags) == 0 && len(s.Genres) == 0
}

// Merge overlays b on a: any non-zero field in b wins.
func Merge(a, b Sidecar) Sidecar {
    out := a
    if b.Title != "" { out.Title = b.Title }
    if b.TitleSort != "" { out.TitleSort = b.TitleSort }
    if b.Subtitle != "" { out.Subtitle = b.Subtitle }
    if b.Author != "" { out.Author = b.Author }
    if b.Description != "" { out.Description = b.Description }
    if b.Language != "" { out.Language = b.Language }
    if b.Publisher != "" { out.Publisher = b.Publisher }
    if b.PublishedDate != "" { out.PublishedDate = b.PublishedDate }
    if b.ISBN != "" { out.ISBN = b.ISBN }
    if b.Series != "" { out.Series = b.Series }
    if b.SeriesIndex != 0 { out.SeriesIndex = b.SeriesIndex }
    if len(b.Tags) > 0 { out.Tags = b.Tags }
    if len(b.Genres) > 0 { out.Genres = b.Genres }
    return out
}

// ParseTOML deserializes TOML bytes into a Sidecar.
func ParseTOML(data []byte) (Sidecar, error) {
    var s Sidecar
    if err := toml.Unmarshal(data, &s); err != nil {
        return Sidecar{}, fmt.Errorf("sidecar: parse toml: %w", err)
    }
    return s, nil
}

// EncodeTOML serializes a Sidecar to TOML for atomic write.
func EncodeTOML(s Sidecar) ([]byte, error) {
    out, err := toml.Marshal(s)
    if err != nil {
        return nil, fmt.Errorf("sidecar: encode toml: %w", err)
    }
    return out, nil
}
```

Promote the TOML dep to direct: edit `go.mod` so `github.com/pelletier/go-toml/v2` is in the `require` block without `// indirect`. Run `go mod tidy` to verify.

**Tests:**
- Encode then parse round-trip preserves every field.
- ParseTOML on malformed input returns an error.
- `IsZero` is true on `Sidecar{}` and false after setting any field.
- `Merge` cases: empty over set (no change); set over empty (b wins); set over set (b wins); empty slices don't clobber non-empty.

- [ ] Commit:

```bash
git add internal/sidecar/sidecar.go internal/sidecar/toml.go internal/sidecar/sidecar_test.go go.mod go.sum
git commit -m "feat(sidecar): TOML round-trip + Sidecar struct + Merge"
```

---

### Task 2: OPF parser

**Files:**
- Create: `internal/sidecar/opf.go`
- Modify: `internal/sidecar/sidecar_test.go` (add OPF cases)

`metadata.opf` is the Calibre-style XML format. Reuse the structural pattern from `internal/fileproc/epub.go` but with the entry point being raw bytes (the file already extracted from the zip).

```go
package sidecar

import (
    "encoding/xml"
    "fmt"
    "strings"
)

// opfPackage is the root <package> element of a metadata.opf file.
// We only consume the metadata block; manifest/spine are EPUB-only.
type opfPackage struct {
    XMLName  xml.Name `xml:"package"`
    Metadata opfMetadata `xml:"metadata"`
}

type opfMetadata struct {
    Title       string       `xml:"title"`
    Creator     []opfCreator `xml:"creator"`
    Description string       `xml:"description"`
    Language    string       `xml:"language"`
    Publisher   string       `xml:"publisher"`
    Date        string       `xml:"date"`
    Identifier  []opfIdent   `xml:"identifier"`
    Subject     []string     `xml:"subject"`
    Meta        []opfMetaKV  `xml:"meta"`
}

type opfCreator struct {
    Role  string `xml:"role,attr"`
    Value string `xml:",chardata"`
}

type opfIdent struct {
    Scheme string `xml:"scheme,attr"`
    Value  string `xml:",chardata"`
}

type opfMetaKV struct {
    Name    string `xml:"name,attr"`
    Content string `xml:"content,attr"`
}

// ParseOPF deserializes an OPF metadata file (the standalone sibling
// kind, not the one embedded in an .epub) into a Sidecar.
func ParseOPF(data []byte) (Sidecar, error) {
    var pkg opfPackage
    if err := xml.Unmarshal(data, &pkg); err != nil {
        return Sidecar{}, fmt.Errorf("sidecar: parse opf: %w", err)
    }
    s := Sidecar{
        Title:         strings.TrimSpace(pkg.Metadata.Title),
        Description:   strings.TrimSpace(pkg.Metadata.Description),
        Language:      strings.TrimSpace(pkg.Metadata.Language),
        Publisher:     strings.TrimSpace(pkg.Metadata.Publisher),
        PublishedDate: strings.TrimSpace(pkg.Metadata.Date),
    }
    // Author = first creator with role="aut" or, failing that, the
    // first creator entry.
    for _, c := range pkg.Metadata.Creator {
        if c.Role == "aut" && c.Value != "" {
            s.Author = c.Value
            break
        }
    }
    if s.Author == "" {
        for _, c := range pkg.Metadata.Creator {
            if c.Value != "" {
                s.Author = c.Value
                break
            }
        }
    }
    // ISBN = first identifier with scheme="ISBN" (case-insensitive).
    for _, i := range pkg.Metadata.Identifier {
        if strings.EqualFold(i.Scheme, "ISBN") && i.Value != "" {
            s.ISBN = i.Value
            break
        }
    }
    // Tags = <subject> entries.
    if len(pkg.Metadata.Subject) > 0 {
        s.Tags = append(s.Tags, pkg.Metadata.Subject...)
    }
    // Calibre encodes series via <meta name="calibre:series" content="…"/>.
    for _, m := range pkg.Metadata.Meta {
        switch m.Name {
        case "calibre:series":
            s.Series = m.Content
        case "calibre:series_index":
            // Best-effort parse; silently drop on error.
            var idx int
            _, _ = fmt.Sscanf(m.Content, "%d", &idx)
            s.SeriesIndex = idx
        case "calibre:title_sort":
            s.TitleSort = m.Content
        }
    }
    return s, nil
}
```

**Tests:**
- Minimal Calibre OPF (provided as a literal string in the test) parses to expected `Sidecar` fields.
- Multiple `<creator>` tags: role="aut" wins; if none has the role, first entry wins.
- ISBN identifier with various scheme casings (ISBN, isbn, ISBN-13).
- Calibre-specific `<meta>` extraction (series, series_index, title_sort).
- Malformed XML returns an error.

- [ ] Commit:

```bash
git add internal/sidecar/opf.go internal/sidecar/sidecar_test.go
git commit -m "feat(sidecar): OPF (Calibre) reader"
```

---

## Phase 2 — Storage Integration

### Task 3: Atomic writer with per-key serialization

**Files:**
- Create: `internal/sidecar/writer.go`
- Modify: `internal/sidecar/sidecar_test.go` (add writer cases)

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
//
// Multi-process / multi-instance coordination is out of scope for
// Plan D; conditional PUT lands in Plan F.
type Writer struct {
    locks sync.Map // map[string]*sync.Mutex
}

// NewWriter constructs a fresh writer.
func NewWriter() *Writer { return &Writer{} }

// keyLock returns the per-key mutex, creating it on first reference.
func (w *Writer) keyLock(key string) *sync.Mutex {
    actual, _ := w.locks.LoadOrStore(key, &sync.Mutex{})
    return actual.(*sync.Mutex)
}

// Write encodes s as TOML and stores it at key. Concurrent calls for
// the same key are serialized; calls for different keys run in
// parallel. The encoded bytes are written via storage.Put so the
// underlying backend's atomic-write semantics apply.
func (w *Writer) Write(ctx context.Context, store storage.Storage, key string, s Sidecar) error {
    data, err := EncodeTOML(s)
    if err != nil {
        return err
    }
    mu := w.keyLock(key)
    mu.Lock()
    defer mu.Unlock()
    _, err = store.Put(ctx, key, bytes.NewReader(data), storage.WithContentType("application/toml"))
    return err
}
```

**Tests:**
- Single Write happy path: bytes land at key, parsable on Get.
- 100 concurrent Writes to the same key: all complete; the final read parses to one of the inputs (no torn write). Use `sync.WaitGroup`.
- 100 concurrent Writes to 100 different keys: all complete; each key's read returns its own input.

- [ ] Commit:

```bash
git add internal/sidecar/writer.go internal/sidecar/sidecar_test.go
git commit -m "feat(sidecar): per-key serialized atomic writer"
```

---

### Task 4: Sibling-file reader

**Files:**
- Create: `internal/sidecar/reader.go`
- Modify: `internal/sidecar/sidecar_test.go` (add reader cases)

```go
package sidecar

import (
    "context"
    "errors"
    "io"
    "strings"

    "github.com/blackforge/embookshelf/internal/storage"
)

// SidecarFiles is the set of sibling filenames the reader looks for,
// in priority order. TOML wins (it's the native, app-edited format).
var SidecarFiles = []string{
    "metadata.opf",
    ".embookshelf.toml",
}

// Read locates sidecar files under the given storage prefix
// (typically the directory containing a book). It parses each one
// it finds and merges them in priority order: OPF first, then TOML
// over it. A missing prefix or no sidecars present is not an error;
// the function returns Sidecar{}, nil.
func Read(ctx context.Context, store storage.Storage, prefix string) (Sidecar, error) {
    var merged Sidecar
    for _, name := range SidecarFiles {
        key := strings.TrimRight(prefix, "/") + "/" + name
        rc, err := store.Get(ctx, key)
        if err != nil {
            if errors.Is(err, storage.ErrNotFound) {
                continue
            }
            return Sidecar{}, err
        }
        data, readErr := io.ReadAll(rc)
        _ = rc.Close()
        if readErr != nil {
            return Sidecar{}, readErr
        }

        var parsed Sidecar
        var parseErr error
        switch name {
        case "metadata.opf":
            parsed, parseErr = ParseOPF(data)
        case ".embookshelf.toml":
            parsed, parseErr = ParseTOML(data)
        }
        if parseErr != nil {
            // A malformed sidecar is logged via the caller; here we
            // surface the error so the scan worker can record it.
            return merged, parseErr
        }
        merged = Merge(merged, parsed)
    }
    return merged, nil
}
```

**Tests** (use `local.LocalFS` rooted at `t.TempDir()`):
- No sidecars → `Sidecar{}, nil`.
- Only `metadata.opf` → fields from OPF populated.
- Only `.embookshelf.toml` → fields from TOML populated.
- Both present → TOML wins on overlapping fields, OPF fills the rest.
- Malformed TOML → error surfaces.

- [ ] Commit:

```bash
git add internal/sidecar/reader.go internal/sidecar/sidecar_test.go
git commit -m "feat(sidecar): reader merges sibling OPF + TOML via storage.List"
```

---

## Phase 3 — Bookdrop Ingest Hook

### Task 5: Wire `sidecar.Read` into bookdrop ingest

**Files:**
- Modify: `internal/task/bookdrop.go`
- Modify: `internal/queue/queue.go`
- Modify: `internal/queue/sqlite.go`
- Modify: `cmd/embookshelf/main.go`

Add a Storage field to `BookDropDeps`:

```go
type BookDropDeps struct {
    Svc     *service.BookDropService
    Storage storage.Storage // optional; nil disables sidecar lookup
}
```

After `proc.Extract` succeeds, look up sidecars from the same directory and overlay them on the extracted metadata (only fields the sidecar carries — never overwrite cover bytes, duration, etc.):

```go
if deps.Storage != nil {
    // Convert the absolute path into a storage key (Plan A LocalFS
    // is rooted at "/" so we strip the leading slash). Then take
    // the directory portion as the lookup prefix.
    key := strings.TrimPrefix(item.Path, "/")
    prefix := path.Dir(key)
    if sc, err := sidecar.Read(ctx, deps.Storage, prefix); err == nil && !sc.IsZero() {
        meta = layerSidecar(meta, sc)
    } else if err != nil {
        slog.Warn("bookdrop sidecar read failed", "item_id", itemID, "prefix", prefix, "err", err)
        // non-fatal — proceed with embedded metadata only
    }
}
```

`layerSidecar` lives in `internal/task/bookdrop.go`:

```go
func layerSidecar(m fileproc.Metadata, s sidecar.Sidecar) fileproc.Metadata {
    if s.Title != "" { m.Title = s.Title }
    if s.Author != "" { m.Author = s.Author }
    if s.Description != "" { m.Description = s.Description }
    if s.Language != "" { m.Language = s.Language }
    return m
}
```

Plumb `Storage` from `main.go` → `queue.New` → `BookDropDeps`. The library scan deps already carry a `Storage` field (Plan A); reuse the same construction.

**Tests:** the existing bookdrop test suite tolerates a nil `Storage`, so this is mainly an integration-level concern. The unit test for `layerSidecar` is the simplest:

```go
func TestLayerSidecar_NonEmptyOverlays(t *testing.T) {
    m := fileproc.Metadata{Title: "Original", Author: "A"}
    s := sidecar.Sidecar{Title: "Override", Description: "hello"}
    out := layerSidecar(m, s)
    if out.Title != "Override" { t.Errorf("title not overridden") }
    if out.Author != "A" { t.Errorf("author should be unchanged") }
    if out.Description != "hello" { t.Errorf("description not overlaid") }
}
```

- [ ] Commit:

```bash
git add internal/task/bookdrop.go internal/queue/queue.go internal/queue/sqlite.go cmd/embookshelf/main.go internal/queue/sqlite_test.go
git commit -m "feat(task): bookdrop ingest reads sibling sidecars

After proc.Extract, sidecar.Read pulls metadata.opf / .embookshelf.toml
from the file's directory and overlays them on the extracted metadata.
Embedded fields stay where the sidecar is silent. Storage handle
flows from main.go through BookDropDeps."
```

---

## Phase 4 — Verification

### Task 6: Verify and PR

- [ ] `go test ./internal/sidecar/...` — all tests pass.
- [ ] `make ci-local` — green.
- [ ] `git diff --stat origin/main..HEAD` confirms scope.
- [ ] Push and open PR.

```bash
gh pr create --base main --title "feat(sidecar): metadata.opf + .embookshelf.toml read pipeline (Plan D of 8)"
```

---

## Self-Review

**Spec coverage:**
- §3.3 sidecar files (formats, locations) → covered: OPF + TOML parsers, sibling-key lookup.
- §6.1 layering — embedded ⊕ sidecar ⊕ user-edits → covered for embedded ⊕ sidecar; user-edits write hook is Plan D2.
- §6.2 atomic sidecar writes (local) → covered via `storage.Put`'s tmp+fsync+rename. Per-key mutex linearizes in-process concurrency.
- §6.2 conditional PUT for S3 → deferred to Plan F.

**Out of scope (deferred, with linkage):**
- The user-edit write hook on `PUT /books/:id/metadata` — Plan D2.
- Cover image inside OPF → Plan E (cover store reorg).
- S3 conditional writes for sidecars → Plan F.

**Risks:**
- The bookdrop ingest's `item.Path` is an absolute path; the LocalFS Plan A rooted at `/` so stripping the leading slash gives the correct key. When library backends become per-library rooted (Plan B2 / Plan F), this stripping needs to take `library.root` into account.
- A malformed `.embookshelf.toml` aborts the merge for that book. The current behavior logs and proceeds with embedded only — acceptable.
- Cover bytes in OPF are not yet followed (cover stays from embedded extraction). Plan E reorganizes cover storage and can revisit.

**Type consistency:** `Sidecar`, `Merge`, `ParseTOML/EncodeTOML`, `ParseOPF`, `Read`, `Writer.Write`, `layerSidecar` consistent across tasks.
