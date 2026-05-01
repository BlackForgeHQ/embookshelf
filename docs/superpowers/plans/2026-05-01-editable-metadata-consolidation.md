# Editable Metadata Consolidation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Collapse the three overlapping editable-book-metadata structs (`fileproc.Metadata` editable subset, `sidecar.Sidecar`, `fileproc.EmbedInput`) into a single canonical shape `model.EditableMetadata`. Drop dead `TitleSort` field along the way.

**Architecture:** New `model.EditableMetadata` carries the union of editable scalar fields (Title, Subtitle, Author, Description, Language, Publisher, PublishedDate, ISBN, Series, SeriesIndex, Tags, Genres). `sidecar.Sidecar` becomes a type alias to `model.EditableMetadata`. `fileproc.EmbedInput` embeds `EditableMetadata` plus cover bytes. `model.Book` stays flat; gains helpers `Editable() EditableMetadata` and `ApplyEditable(em)` for boundary conversion. `MetadataWriter` and scan re-extract use the helpers — one canonical shape, no field-by-field copy.

**Tech Stack:** Go 1.25 stdlib + existing project packages.

**Companion docs:** none new. CONTEXT.md doesn't reference these struct shapes; spec §4.1 lists the JSON envelope `fields` keys which already match the proposed `EditableMetadata` JSON tags.

**Out of scope:** UI shape changes, DB schema changes (Book table layout untouched), enrichment provider DTOs, audio-only fields (DurationSeconds/Narrator stay on `fileproc.Metadata`).

---

## File Structure

| Path | Change |
|---|---|
| `internal/model/book.go` | **Modify.** Add `EditableMetadata` struct + `Book.Editable()` + `(*Book).ApplyEditable(em)` methods. |
| `internal/sidecar/sidecar.go` | **Modify.** Replace `Sidecar` struct with type alias `type Sidecar = model.EditableMetadata`. Move `Merge` + `IsZero` to `EditableMetadata` (in `model/book.go`). Drop `TitleSort` field. |
| `internal/sidecar/opf.go` | **Modify.** Drop `TitleSort` write/read in `ParseOPF` (the `calibre:title_sort` case). |
| `internal/sidecar/sidecar_test.go` | **Modify.** Remove `TitleSort` from test fixtures + assertions. |
| `internal/fileproc/embedder.go` | **Modify.** `EmbedInput` becomes `struct { model.EditableMetadata; CoverBytes []byte; CoverMime string }`. |
| `internal/service/metadata_writer.go` | **Modify.** `writeSidecar` + `tryEmbedFile` use `b.Editable()` to fill both Sidecar and EmbedInput from one source. |
| `internal/task/library_scan.go` | **Modify.** `reExtractAndMerge` builds `EditableMetadata` (overlay sidecar on extracted) then `current.ApplyEditable(merged)`. |
| `internal/task/bookdrop.go` | **Modify.** `layerSidecar` becomes `EditableMetadata`-aware (or removed if redundant). |
| `internal/fileproc/embedder_test.go` | **Modify.** EmbedInput struct literals updated to use embedded shape. |

---

## Phase 1 — Canonical type + helpers

### Task 1: `model.EditableMetadata` type + Merge/IsZero methods

**Files:**
- Modify: `internal/model/book.go`
- Test: `internal/model/book_test.go` (create)

- [ ] **Step 1: Write failing test**

Create `internal/model/book_test.go`:

```go
package model_test

import (
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
)

func TestEditableMetadata_IsZero(t *testing.T) {
	if !(model.EditableMetadata{}).IsZero() {
		t.Error("zero EditableMetadata: IsZero=false, want true")
	}
	if (model.EditableMetadata{Title: "x"}).IsZero() {
		t.Error("non-zero Title: IsZero=true, want false")
	}
	if (model.EditableMetadata{Tags: []string{"a"}}).IsZero() {
		t.Error("non-zero Tags: IsZero=true, want false")
	}
}

func TestEditableMetadata_Merge_OverlayWins(t *testing.T) {
	base := model.EditableMetadata{Title: "Base", Author: "Base"}
	overlay := model.EditableMetadata{Title: "Overlay"}
	got := model.MergeEditable(base, overlay)
	if got.Title != "Overlay" {
		t.Errorf("Title=%q want Overlay", got.Title)
	}
	if got.Author != "Base" {
		t.Errorf("Author=%q want Base (overlay zero)", got.Author)
	}
}

func TestEditableMetadata_Merge_TagsOverwriteWhenNonEmpty(t *testing.T) {
	base := model.EditableMetadata{Tags: []string{"a", "b"}}
	overlay := model.EditableMetadata{Tags: []string{"x"}}
	got := model.MergeEditable(base, overlay)
	if len(got.Tags) != 1 || got.Tags[0] != "x" {
		t.Errorf("Tags=%v want [x]", got.Tags)
	}
}

func TestBook_Editable_RoundTrip(t *testing.T) {
	b := model.Book{
		ID:          "b1",
		Title:       "T",
		Author:      "A",
		Description: "D",
		Tags:        []string{"x", "y"},
	}
	em := b.Editable()
	if em.Title != "T" || em.Author != "A" {
		t.Errorf("Editable() lost scalars: %+v", em)
	}
	var b2 model.Book
	b2.ApplyEditable(em)
	if b2.Title != "T" || b2.Author != "A" {
		t.Errorf("ApplyEditable lost scalars: %+v", b2)
	}
	if b2.ID != "" {
		t.Errorf("ApplyEditable touched ID: %q", b2.ID)
	}
}
```

- [ ] **Step 2: Run FAIL** — `go test ./internal/model/ -v` → "undefined: EditableMetadata" / "MergeEditable" / "Editable" / "ApplyEditable".

- [ ] **Step 3: Append to `internal/model/book.go`** (after existing types):

```go
// EditableMetadata is the canonical shape for the editable subset of
// a book's metadata — the fields a user can change in the edit-metadata
// UI, the fields a sidecar carries, the fields an in-file Embedder
// writes back into the book file. Read-only audio fields (Duration,
// Narrator) and structural fields (ID, LibraryID, Path) live on Book,
// not here.
//
// JSON tags match docs/spec/sidecar-write.spec.md §4.1's wire format.
type EditableMetadata struct {
	Title         string   `json:"title,omitempty"`
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

// IsZero reports whether em carries no information. Used to short-
// circuit the merge when no sidecar/payload was present.
func (em EditableMetadata) IsZero() bool {
	return em.Title == "" && em.Subtitle == "" && em.Author == "" &&
		em.Description == "" && em.Language == "" && em.Publisher == "" &&
		em.PublishedDate == "" && em.ISBN == "" && em.Series == "" &&
		em.SeriesIndex == 0 && len(em.Tags) == 0 && len(em.Genres) == 0
}

// MergeEditable overlays b on a: any non-zero field in b wins.
func MergeEditable(a, b EditableMetadata) EditableMetadata {
	out := a
	if b.Title != "" {
		out.Title = b.Title
	}
	if b.Subtitle != "" {
		out.Subtitle = b.Subtitle
	}
	if b.Author != "" {
		out.Author = b.Author
	}
	if b.Description != "" {
		out.Description = b.Description
	}
	if b.Language != "" {
		out.Language = b.Language
	}
	if b.Publisher != "" {
		out.Publisher = b.Publisher
	}
	if b.PublishedDate != "" {
		out.PublishedDate = b.PublishedDate
	}
	if b.ISBN != "" {
		out.ISBN = b.ISBN
	}
	if b.Series != "" {
		out.Series = b.Series
	}
	if b.SeriesIndex != 0 {
		out.SeriesIndex = b.SeriesIndex
	}
	if len(b.Tags) > 0 {
		out.Tags = b.Tags
	}
	if len(b.Genres) > 0 {
		out.Genres = b.Genres
	}
	return out
}

// Editable returns the editable scalar subset of b. Drops IDs,
// structural fields, audio fields, locks.
func (b Book) Editable() EditableMetadata {
	return EditableMetadata{
		Title:       b.Title,
		Subtitle:    b.Subtitle,
		Author:      b.Author,
		Description: b.Description,
		Language:    b.Language,
		Publisher:   b.Publisher,
		ISBN:        b.ISBN,
		Series:      b.Series,
		SeriesIndex: b.SeriesIndex,
		Tags:        b.Tags,
		Genres:      b.Genres,
		// PublishedDate left blank — Book.PublishDate is *time.Time
		// (different shape; conversion lives at the boundary callers).
	}
}

// ApplyEditable copies em's fields onto b. Does not touch IDs,
// structural fields, audio fields, locks, or PublishDate (caller
// converts string ↔ *time.Time at the boundary).
func (b *Book) ApplyEditable(em EditableMetadata) {
	b.Title = em.Title
	b.Subtitle = em.Subtitle
	b.Author = em.Author
	b.Description = em.Description
	b.Language = em.Language
	b.Publisher = em.Publisher
	b.ISBN = em.ISBN
	b.Series = em.Series
	b.SeriesIndex = em.SeriesIndex
	b.Tags = em.Tags
	b.Genres = em.Genres
}
```

- [ ] **Step 4: Run PASS** — `go test ./internal/model/ -v`.

- [ ] **Step 5: Commit:**
```
git add internal/model/book.go internal/model/book_test.go
git commit -m "feat(model): EditableMetadata + Book.Editable/ApplyEditable helpers"
```

---

## Phase 2 — Sidecar collapse

### Task 2: Drop `TitleSort` from sidecar

**Files:**
- Modify: `internal/sidecar/sidecar.go`
- Modify: `internal/sidecar/opf.go`
- Modify: `internal/sidecar/sidecar_test.go`

- [ ] **Step 1: Find all TitleSort references**

Run: `grep -rn "TitleSort\|title_sort" internal/sidecar/`. Expected matches in sidecar.go (struct + Merge + IsZero), opf.go (calibre:title_sort case), sidecar_test.go (fixtures + assertions).

- [ ] **Step 2: Edit `internal/sidecar/sidecar.go`** — drop TitleSort from struct, IsZero, Merge:

Find:
```go
TitleSort     string   `json:"title_sort,omitempty"`
```
Delete that field.

In `IsZero`, drop `s.TitleSort == "" &&`.

In `Merge`, drop:
```go
if b.TitleSort != "" {
    out.TitleSort = b.TitleSort
}
```

- [ ] **Step 3: Edit `internal/sidecar/opf.go`** — drop the `calibre:title_sort` parse branch. Find the `switch m.Name` block w/ `case "calibre:title_sort":`; delete that case (and its body).

- [ ] **Step 4: Edit `internal/sidecar/sidecar_test.go`** — drop TitleSort fixtures + assertions.

Find every `TitleSort: "..."` line in fixture literals → delete. Find every `s.TitleSort` assertion → delete. Find every `"title_sort"` string in expected-key lists → delete. Find OPF fixture XML containing `<meta name="calibre:title_sort" content="..."/>` → delete those lines.

Specifically:
- `TestParseOPF_AllSubjects` (or similar): drop the `calibre:title_sort` line in the OPF fixture and the corresponding assertion.
- `TestJSON_RoundTrip` field set: remove `TitleSort: "Great Gatsby, The"` and the `got.TitleSort != original.TitleSort` clause.
- `TestJSON_RoundTrip` snake_case key list: drop `"title_sort"`.
- Any other `TitleSort` reference (e.g. line 32 `{"TitleSort", Sidecar{TitleSort: "x"}}` table entry).

- [ ] **Step 5: Build + test sidecar package:**

```
go build ./internal/sidecar/...
go test ./internal/sidecar/ -v
```

All sidecar tests should PASS.

- [ ] **Step 6: Commit:**
```
git add internal/sidecar/sidecar.go internal/sidecar/opf.go internal/sidecar/sidecar_test.go
git commit -m "refactor(sidecar): drop unused TitleSort field"
```

---

### Task 3: Replace `Sidecar` struct with type alias to `EditableMetadata`

**Files:**
- Modify: `internal/sidecar/sidecar.go`
- Modify: `internal/sidecar/sidecar_test.go` (drop now-redundant Merge/IsZero tests)

- [ ] **Step 1: Edit `internal/sidecar/sidecar.go`** — replace the struct definition + IsZero + Merge with a type alias:

```go
package sidecar

import "github.com/blackforge/embookshelf/internal/model"

// Sidecar is the editable metadata payload carried by a JSON sidecar
// file. Aliased to model.EditableMetadata so write-side EmbedInput
// and read-side Sidecar share one canonical shape.
type Sidecar = model.EditableMetadata

// IsZero is exposed as a package-level helper for callers that want
// the same name as before. Delegates to EditableMetadata.IsZero.
func IsZero(s Sidecar) bool {
	return s.IsZero()
}

// Merge overlays b on a. Delegates to model.MergeEditable.
func Merge(a, b Sidecar) Sidecar {
	return model.MergeEditable(a, b)
}
```

The package doc comment at the top stays unchanged.

- [ ] **Step 2: Edit `internal/sidecar/sidecar_test.go`** — drop tests redundant with `model.TestEditableMetadata_*`:
- `TestSidecar_IsZero` (or similar) — delete; covered by model test.
- `TestSidecar_Merge_*` — delete; covered by model.
- Tests that exercise the *sidecar package's own* behavior (Read, Writer, OPF parser, JSON round-trip, KeyFor) stay.
- Tests using `Sidecar{...}` literals still compile because of the alias.

If after deletion any imports become unused (e.g. `testing` is still needed for the survivors), `goimports` handles it.

- [ ] **Step 3: Build + test:**

```
go test ./internal/sidecar/ -v
```

All survivors PASS.

- [ ] **Step 4: Commit:**
```
git add internal/sidecar/sidecar.go internal/sidecar/sidecar_test.go
git commit -m "refactor(sidecar): Sidecar = model.EditableMetadata type alias"
```

---

## Phase 3 — fileproc.EmbedInput compose

### Task 4: `EmbedInput` embeds `EditableMetadata`

**Files:**
- Modify: `internal/fileproc/embedder.go`
- Modify: `internal/fileproc/embedder_test.go`
- Modify: `internal/fileproc/epub_embed.go` (callsites)
- Modify: `internal/fileproc/pdf_embed.go` (callsites)

- [ ] **Step 1: Edit `internal/fileproc/embedder.go`** — replace `EmbedInput` definition:

```go
import "github.com/blackforge/embookshelf/internal/model"

// EmbedInput is the editable metadata payload an Embedder writes
// back into a book file. Composes model.EditableMetadata (the
// canonical editable scalar set) with the cover bytes — covers
// don't fit the Sidecar shape because they live in coverstore,
// not in the JSON envelope.
type EmbedInput struct {
	model.EditableMetadata
	CoverBytes []byte
	CoverMime  string
}
```

- [ ] **Step 2: Field access at callsites preserved by promotion**

Inside `epub_embed.go` `mutateOPF`, callers read `in.Title`, `in.Author`, etc — those still work via Go field promotion through embedded `EditableMetadata`. **No change needed in those files.**

Same for `pdf_embed.go` `buildInfoBody` — `in.Title`, `in.Author`, etc still work.

- [ ] **Step 3: Edit `internal/fileproc/embedder_test.go`** — find every `EmbedInput{` literal. Replace flat-field literals like:

```go
EmbedInput{
    Title:    "Curated Title",
    Author:   "Curated Author",
    Tags:     []string{"alpha"},
}
```

With composed-struct form:

```go
EmbedInput{
    EditableMetadata: model.EditableMetadata{
        Title:  "Curated Title",
        Author: "Curated Author",
        Tags:   []string{"alpha"},
    },
}
```

Update import to add `"github.com/blackforge/embookshelf/internal/model"` if not present.

Same edit needed in `internal/fileproc/pdf_embed_test.go`.

- [ ] **Step 4: Build + test:**

```
go build ./internal/fileproc/...
go test ./internal/fileproc/ -v
```

All PASS.

- [ ] **Step 5: Commit:**
```
git add internal/fileproc/embedder.go internal/fileproc/embedder_test.go internal/fileproc/pdf_embed_test.go
git commit -m "refactor(fileproc): EmbedInput embeds model.EditableMetadata"
```

---

## Phase 4 — Callsite simplification

### Task 5: Refactor `MetadataWriter.writeSidecar` + `tryEmbedFile`

**Files:**
- Modify: `internal/service/metadata_writer.go`

- [ ] **Step 1: Find current shape**

Run: `grep -n "writeSidecar\|tryEmbedFile" internal/service/metadata_writer.go`. Both functions build their payloads field-by-field from `b model.Book`.

- [ ] **Step 2: Edit `writeSidecar`** — replace the field-by-field `sidecar.Sidecar{...}` literal with `b.Editable()`:

Find:
```go
side := sidecar.Sidecar{
    Title:         b.Title,
    Subtitle:      b.Subtitle,
    Author:        b.Author,
    Description:   b.Description,
    Language:      b.Language,
    Publisher:     b.Publisher,
    PublishedDate: dateString(b.PublishDate),
    ISBN:          b.ISBN,
    Series:        b.Series,
    SeriesIndex:   b.SeriesIndex,
    Tags:          b.Tags,
    Genres:        b.Genres,
}
```

Replace with:
```go
side := b.Editable()
side.PublishedDate = dateString(b.PublishDate)
```

(`PublishedDate` is the only field that differs — `Book.PublishDate` is `*time.Time`, `EditableMetadata.PublishedDate` is `string`. Set after `Editable()` builds the rest.)

- [ ] **Step 3: Edit `tryEmbedFile`** — same pattern. Replace:

```go
in := fileproc.EmbedInput{
    Title:         b.Title,
    Subtitle:      b.Subtitle,
    Author:        b.Author,
    Description:   b.Description,
    Language:      b.Language,
    Publisher:     b.Publisher,
    PublishedDate: dateString(b.PublishDate),
    ISBN:          b.ISBN,
    Series:        b.Series,
    SeriesIndex:   b.SeriesIndex,
    Tags:          b.Tags,
    Genres:        b.Genres,
}
```

With:
```go
em := b.Editable()
em.PublishedDate = dateString(b.PublishDate)
in := fileproc.EmbedInput{EditableMetadata: em}
```

- [ ] **Step 4: Build + test:**

```
go build ./internal/service/...
go test ./internal/service/ -v
```

All MetadataWriter tests PASS.

- [ ] **Step 5: Commit:**
```
git add internal/service/metadata_writer.go
git commit -m "refactor(service): MetadataWriter uses Book.Editable() helper"
```

---

### Task 6: Refactor `reExtractAndMerge` in scan

**Files:**
- Modify: `internal/task/library_scan.go`

- [ ] **Step 1: Find current shape**

Run: `grep -n "reExtractAndMerge" internal/task/library_scan.go`. The helper builds an `extracted model.Book` from `Metadata + Sidecar` overlay via `firstNonEmpty`, loads `current` book, calls `LockMerger(current, extracted)`.

- [ ] **Step 2: Replace the build-and-merge block.**

Find inside `reExtractAndMerge`:

```go
extracted := model.Book{
    Title:       firstNonEmpty(side.Title, meta.Title),
    Subtitle:    side.Subtitle,
    Author:      firstNonEmpty(side.Author, meta.Author),
    Description: firstNonEmpty(side.Description, meta.Description),
    Language:    firstNonEmpty(side.Language, meta.Language),
    Publisher:   side.Publisher,
    ISBN:        side.ISBN,
    Series:      side.Series,
    SeriesIndex: side.SeriesIndex,
    Tags:        side.Tags,
    Genres:      side.Genres,
}
```

Replace with:

```go
// Build the extracted shape from processor + sidecar overlay.
// Sidecar wins on conflicts (model.MergeEditable b-wins-on-non-zero).
processorEM := model.EditableMetadata{
    Title:       meta.Title,
    Author:      meta.Author,
    Description: meta.Description,
    Language:    meta.Language,
}
extractedEM := model.MergeEditable(processorEM, model.EditableMetadata(side))

var extracted model.Book
extracted.ApplyEditable(extractedEM)
```

(`side` is `sidecar.Sidecar` which is now type-aliased to `model.EditableMetadata`; the conversion `model.EditableMetadata(side)` is a no-op type cast.)

- [ ] **Step 3: Drop now-unused `firstNonEmpty` helper**

Run: `grep -n "firstNonEmpty" internal/task/library_scan.go`. If only one callsite (the one we just removed), delete the helper. If still used, leave.

- [ ] **Step 4: Build + test:**

```
go build ./internal/task/...
go test ./internal/task/ -v
```

All PASS.

- [ ] **Step 5: Commit:**
```
git add internal/task/library_scan.go
git commit -m "refactor(task): reExtractAndMerge uses MergeEditable + ApplyEditable"
```

---

### Task 7: Refactor `layerSidecar` in bookdrop ingest

**Files:**
- Modify: `internal/task/bookdrop.go`

- [ ] **Step 1: Find current shape**

Run: `grep -n "layerSidecar" internal/task/bookdrop.go`. The function overlays non-empty `Sidecar` fields on `fileproc.Metadata`. Current shape:

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
    return m
}
```

- [ ] **Step 2: Decide: keep or simplify?**

`fileproc.Metadata` has only Title/Author/Description/Language editable fields (the rest are read-only audio + cover). `Sidecar` (now `EditableMetadata`) has more, but `Metadata` doesn't carry them. The sidecar→Metadata overlay is intentionally lossy at this seam — Metadata's "shape that came out of the file" doesn't grow with sidecar additions.

Keep `layerSidecar` as-is — it's already small + correct. Sidecar's new fields (Publisher, ISBN, Series, etc) are picked up later in Approve / scan re-extract paths via `Sidecar`'s native shape, not through the truncated `Metadata` overlay.

If anything: rename `s.Description` reads etc. — they still work via the alias.

**No code change needed for Task 7. Skip the commit.** Note in the task report.

---

## Phase 5 — Verification

### Task 8: Full lint + test + log

- [ ] `make test` — all packages green.
- [ ] `make go-lint` — 0 issues.
- [ ] `go vet ./...` — silent.
- [ ] `git log --oneline -10` — see Tasks 1-6 commits (5 commits if Task 7 was skipped).

If any blocked, report. No commit.

---

## Self-Review

**Spec coverage:**
- Plan goal (collapse 3 structs to 1) — Tasks 1-4 land canonical type + alias + embed.
- Callsite simplification — Tasks 5-7.
- TitleSort cleanup — Task 2.
- Verification — Task 8.

**Placeholder scan:** no `TBD`, no `add appropriate handling`. Every code step has the actual code.

**Type consistency:**
- `model.EditableMetadata` — defined Task 1, used Tasks 3, 4, 5, 6.
- `model.MergeEditable` — defined Task 1, used Task 6.
- `Book.Editable()` / `ApplyEditable` — defined Task 1, used Tasks 5, 6.
- `sidecar.Sidecar` — type alias to `model.EditableMetadata` Task 3; old struct (Task 2 dropped TitleSort) → alias (Task 3).
- `fileproc.EmbedInput` — embeds `EditableMetadata` Task 4. Field promotion preserves all `in.Title` etc accesses.

**Ordering invariants:**
- Task 2 (drop TitleSort) MUST land before Task 3 (alias). The alias drops the struct entirely; if TitleSort survives in struct definition pre-alias, the field disappears at alias-time without intermediate test guard.
- Task 1 (model.EditableMetadata) MUST land before Tasks 3-6.
- Task 4 (EmbedInput compose) MUST land before Task 5 (uses new EmbedInput shape).

---

## Execution Handoff

Plan saved to `docs/superpowers/plans/2026-05-01-editable-metadata-consolidation.md`. Single PR scope: 5-6 commits.

User said: write plan + execute. Proceed via subagent-driven dev, batching where safe.
