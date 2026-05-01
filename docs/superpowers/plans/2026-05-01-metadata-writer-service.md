# MetadataWriter Service + HTTP Wiring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `service.MetadataWriter` — the orchestrator that runs the **DB → JSON sidecar → file embedded** pipeline on user-driven metadata edits. Wire the existing `LibraryService.UpdateBookMetadata` (manual edit) and `EnrichmentService.ApplyMatch` (apply enrichment) callsites through it. Auto-enrichment still runs DB-only. Per `docs/spec/sidecar-write.spec.md` §6, §11.3, §11.4.

**Architecture:** `MetadataWriter` is a thin coordinator. Three sequential steps: (1) DB write via existing `BookRepo.UpdateMetadata` — transactional, user-facing response returns after this. (2) Sidecar JSON write via `sidecar.Writer.Write` (Plan 1). (3) In-file write via `fileproc.DispatchEmbedder` (Plans 2-3) gated on `LibraryHandle.CanWriteInFile()`. Steps 2-3 are best-effort post-commit; failures are logged. The trigger value (`manual_edit`, `apply_enrichment`, `auto_enrichment`) gates which steps run. Sidecar mode is `spillover` when in-file write succeeds, `full` when it's skipped or fails.

**Tech Stack:** Go 1.25 stdlib + existing project packages (`internal/sidecar`, `internal/fileproc`, `internal/service`, `internal/repo`, `internal/storage`).

**Companion docs:**
- `docs/spec/sidecar-write.spec.md` §6 (trigger contract), §11.3 (MetadataWriter), §11.4 (LibraryHandle helpers).
- `docs/adr/0001-edit-side-metadata-write-back.md` (write order + trigger scope rationale).

**Depends on:**
- Plan 1 (sidecar JSON cutover) — `Writer.Write(ctx, store, key, s, mode, format)` signature.
- Plan 2 (EPUB embedder) — `Embedder` interface + `EmbedInput` struct.
- Plan 3 (PDF embedder) — `PDFEmbedder` registered in `DispatchEmbedder`.

**Out of scope:**
- Hash-stamp scan integration (Plan 5). This plan **does not** update `files.content_hash` after the file write — Plan 5 wires that, plus the lock-aware re-extract verification.
- Cover replacement. EPUB embedder accepts `EmbedInput.CoverBytes` but the cover-edit UI flow is unchanged here; covers are still written via `coverstore` in the existing handler. Phase 2 unifies them.
- Sidecar repair worker (Phase 2 candidate per spec §10).
- Schema migration of `LibraryHandle` callers — `SidecarKey` and `CanWriteInFile` are added without breaking existing handle consumers.

---

## File Structure

| Path | Change |
|---|---|
| `internal/service/library_store.go` | **Modify.** Add `SidecarKey(bookLocation string) string` and `CanWriteInFile() bool` methods on `*LibraryHandle`. |
| `internal/service/metadata_writer.go` | **Create.** `MetadataWriter` struct + `Trigger` enum + `Write` method. Coordinates DB → sidecar → file pipeline. |
| `internal/service/metadata_writer_test.go` | **Create.** Unit tests w/ fake `LibraryStore` + fake `BookRepo` so the pipeline is exercised without disk I/O. |
| `internal/service/library.go` | **Modify.** `UpdateBookMetadata` accepts an optional `MetadataWriter` (or routes through it directly via dependency). Existing direct call to `s.books.UpdateMetadata` is replaced by `s.writer.Write(...)` when wired. |
| `internal/service/enrichment.go` | **Modify.** `ApplyMatch` gains a `Trigger` parameter; `AutoEnrich` passes `TriggerAutoEnrichment`. Existing direct `s.books.UpdateMetadata` call routes through `MetadataWriter` when present. |
| `internal/handler/library.go:355` | **Modify.** Manual-edit handler passes `TriggerManualEdit`. |
| `internal/handler/enrich.go:262` | **Modify.** Apply-match handler passes `TriggerApplyEnrichment`. |
| `cmd/embookshelf/main.go` | **Modify.** Construct `MetadataWriter` after `libStore` + `sidecar.Writer` + `bookRepo` are available. Inject into `LibraryService` and `EnrichmentService`. |

---

## Phase 1 — LibraryHandle helpers

### Task 1: `LibraryHandle.SidecarKey` + `CanWriteInFile`

**Files:**
- Modify: `internal/service/library_store.go`
- Test: append to existing `internal/service/library_store_test.go` (or create if missing)

- [ ] **Step 1: Write the failing test**

Append (or create) `internal/service/library_store_test.go`:

```go
package service_test

import (
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

func TestLibraryHandle_SidecarKey(t *testing.T) {
	h := &service.LibraryHandle{Library: model.Library{ID: "lib1"}}
	cases := []struct {
		bookKey string
		want    string
	}{
		{"folder/harry-potter.epub", "folder/harry-potter.embookshelf.json"},
		{"books/dune.pdf", "books/dune.embookshelf.json"},
		{"audio/dune/disc-1.m4b", "audio/dune/disc-1.embookshelf.json"},
		{"flat-file.epub", "flat-file.embookshelf.json"},
	}
	for _, c := range cases {
		if got := h.SidecarKey(c.bookKey); got != c.want {
			t.Errorf("SidecarKey(%q) = %q, want %q", c.bookKey, got, c.want)
		}
	}
}

func TestLibraryHandle_CanWriteInFile_LocalBackend(t *testing.T) {
	fs, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	// Local-backed library: BackendID nil. Storage non-nil.
	h := &service.LibraryHandle{
		Library: model.Library{BackendID: nil},
	}
	h.SetTestStorage(fs) // exposed in Task 1 step 3
	if !h.CanWriteInFile() {
		t.Error("local backend should allow in-file write")
	}
}

func TestLibraryHandle_CanWriteInFile_S3Backend(t *testing.T) {
	bid := "backend-1"
	h := &service.LibraryHandle{
		Library: model.Library{BackendID: &bid},
	}
	if h.CanWriteInFile() {
		t.Error("S3-backed library must NOT allow in-file write in Phase 1")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/service/ -run "TestLibraryHandle_SidecarKey|TestLibraryHandle_CanWriteInFile" -v`
Expected: FAIL with "undefined: SidecarKey", "undefined: CanWriteInFile", "undefined: SetTestStorage".

- [ ] **Step 3: Write minimal implementation**

Append to `internal/service/library_store.go`:

```go
import "github.com/blackforge/embookshelf/internal/sidecar"

// SidecarKey returns the paired JSON sidecar storage key for a book
// file's storage key. Delegates to sidecar.KeyFor (Plan 1) so the
// derivation rule lives in one place.
func (h *LibraryHandle) SidecarKey(bookLocation string) string {
	return sidecar.KeyFor(bookLocation)
}

// CanWriteInFile reports whether this handle's library accepts
// in-file metadata writes. Phase 1: only local-backed libraries
// (BackendID == nil) qualify; S3 backends skip in-file write to
// avoid Get+Put bandwidth churn per edit.
func (h *LibraryHandle) CanWriteInFile() bool {
	return h.Library.BackendID == nil
}

// SetTestStorage is exposed for tests that need to construct a
// LibraryHandle without going through LibraryStore.For. NEVER call
// from production code; the field is set internally by For.
func (h *LibraryHandle) SetTestStorage(s storage.Storage) {
	h.Storage = s
}
```

(`storage` already imported at top of file. If `LibraryHandle.Storage` is exported, `SetTestStorage` is unnecessary — drop it. Check by reading the struct definition.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/service/ -run "TestLibraryHandle_SidecarKey|TestLibraryHandle_CanWriteInFile" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/library_store.go internal/service/library_store_test.go
git commit -m "feat(service): LibraryHandle.SidecarKey + CanWriteInFile helpers"
```

---

## Phase 2 — MetadataWriter skeleton

### Task 2: `MetadataWriter` struct + `Trigger` enum + DB-only Write

**Files:**
- Create: `internal/service/metadata_writer.go`
- Test: `internal/service/metadata_writer_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/service/metadata_writer_test.go`:

```go
package service_test

import (
	"context"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
)

// fakeBookWriter records UpdateMetadata calls for the test.
type fakeBookWriter struct {
	called []model.Book
	err    error
}

func (f *fakeBookWriter) UpdateMetadata(ctx context.Context, b model.Book) error {
	f.called = append(f.called, b)
	return f.err
}

func TestMetadataWriter_Write_AutoEnrichment_DBOnly(t *testing.T) {
	books := &fakeBookWriter{}
	mw := service.NewMetadataWriter(service.MetadataWriterDeps{Books: books})
	book := model.Book{ID: "b1", Title: "Auto-applied"}
	if err := mw.Write(context.Background(), book, service.TriggerAutoEnrichment); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(books.called) != 1 {
		t.Fatalf("UpdateMetadata called %d times; want 1", len(books.called))
	}
	if books.called[0].Title != "Auto-applied" {
		t.Errorf("Title=%q", books.called[0].Title)
	}
}

func TestMetadataWriter_Write_DBFails_ErrorReturned(t *testing.T) {
	books := &fakeBookWriter{err: context.Canceled}
	mw := service.NewMetadataWriter(service.MetadataWriterDeps{Books: books})
	err := mw.Write(context.Background(), model.Book{ID: "b1"}, service.TriggerManualEdit)
	if err == nil {
		t.Fatal("Write: want error, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/service/ -run TestMetadataWriter_Write -v`
Expected: FAIL with "undefined: NewMetadataWriter".

- [ ] **Step 3: Write minimal implementation**

Create `internal/service/metadata_writer.go`:

```go
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/blackforge/embookshelf/internal/model"
)

// Trigger identifies the upstream action that drove a metadata
// write. Different triggers cause different steps to fire in the
// pipeline; the per-step gating lives in MetadataWriter.Write.
type Trigger string

const (
	// TriggerManualEdit is set by the manual edit-metadata UI
	// handler. Fires DB + sidecar + file (gated by backend kind).
	TriggerManualEdit Trigger = "manual_edit"
	// TriggerApplyEnrichment is set by the apply-match UI flow.
	// Same coverage as TriggerManualEdit — explicit user intent.
	TriggerApplyEnrichment Trigger = "apply_enrichment"
	// TriggerAutoEnrichment is set by the headless auto-enrichment
	// background worker. Fires DB only — no sidecar/file write to
	// avoid stampedes on bulk auto-applies.
	TriggerAutoEnrichment Trigger = "auto_enrichment"
)

// BookMetadataWriter is the slice of *repo.BookRepo MetadataWriter
// needs. Defined here so tests can fake it without standing up a DB.
type BookMetadataWriter interface {
	UpdateMetadata(ctx context.Context, b model.Book) error
}

// MetadataWriterDeps groups the dependencies MetadataWriter needs.
// LibStore + Sidecar are nil-tolerant for the auto-enrichment-only
// case (DB write succeeds without them).
type MetadataWriterDeps struct {
	Books BookMetadataWriter
	// LibStore + Sidecar wired in Tasks 3-5. Nil here in this task.
}

// MetadataWriter coordinates the DB → sidecar → file pipeline for
// user-driven book metadata edits. Caller invokes Write with a
// fully-formed model.Book (the new desired state) and a Trigger
// value; the writer figures out which side-effect steps fire.
type MetadataWriter struct {
	deps MetadataWriterDeps
}

func NewMetadataWriter(deps MetadataWriterDeps) *MetadataWriter {
	return &MetadataWriter{deps: deps}
}

// Write persists the book's edited metadata. Returns nil after the
// DB step (step 1) succeeds; subsequent steps (sidecar, file) are
// post-commit best-effort and their failures are logged via slog.
//
// Trigger gating:
//   - manual_edit / apply_enrichment → DB + sidecar + file
//   - auto_enrichment → DB only
func (w *MetadataWriter) Write(ctx context.Context, b model.Book, trigger Trigger) error {
	if w.deps.Books == nil {
		return errors.New("metadata writer: no book repo configured")
	}

	// Step 1: DB write (transactional). User-facing response returns
	// after this succeeds.
	if err := w.deps.Books.UpdateMetadata(ctx, b); err != nil {
		return fmt.Errorf("metadata writer: db: %w", err)
	}

	// Auto-enrichment short-circuits here. Sidecar/file writes only
	// fire on explicit user actions.
	if trigger == TriggerAutoEnrichment {
		return nil
	}

	// Steps 2-3 land in Tasks 3-5. Stub for now — no-op logged.
	slog.Debug("metadata writer: post-commit steps stubbed",
		"book_id", b.ID, "trigger", string(trigger))
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/service/ -run TestMetadataWriter_Write -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/metadata_writer.go internal/service/metadata_writer_test.go
git commit -m "feat(service): MetadataWriter w/ Trigger enum; DB-only step wired"
```

---

## Phase 3 — Sidecar step

### Task 3: Sidecar write fires on manual edit + apply enrichment

**Files:**
- Modify: `internal/service/metadata_writer.go`
- Test: append to `internal/service/metadata_writer_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
import (
	"github.com/blackforge/embookshelf/internal/sidecar"
	"github.com/blackforge/embookshelf/internal/storage"
)

// fakeLibStore returns a fixed handle for any libraryID.
type fakeLibStore struct {
	handle *service.LibraryHandle
	err    error
}

func (f *fakeLibStore) For(ctx context.Context, libraryID string) (*service.LibraryHandle, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.handle, nil
}

// recordingSidecarWriter captures the last Write call args.
type recordingSidecarWriter struct {
	calls []sidecarCall
	err   error
}

type sidecarCall struct {
	Key    string
	Side   sidecar.Sidecar
	Mode   sidecar.WriteMode
	Format string
}

func (r *recordingSidecarWriter) Write(
	ctx context.Context,
	store storage.Storage,
	key string,
	s sidecar.Sidecar,
	mode sidecar.WriteMode,
	format string,
) error {
	r.calls = append(r.calls, sidecarCall{Key: key, Side: s, Mode: mode, Format: format})
	return r.err
}

func TestMetadataWriter_Write_ManualEdit_FiresSidecar(t *testing.T) {
	books := &fakeBookWriter{}
	rec := &recordingSidecarWriter{}
	// Library w/ no BackendID = local; Storage nil → CanWriteInFile=true
	// but the embedder step (Task 5) won't run yet.
	handle := &service.LibraryHandle{
		Library: model.Library{ID: "lib1"},
	}
	mw := service.NewMetadataWriter(service.MetadataWriterDeps{
		Books:    books,
		LibStore: &fakeLibStore{handle: handle},
		Sidecar:  rec,
	})
	book := model.Book{
		ID:        "b1",
		LibraryID: "lib1",
		Path:      "books/dune.pdf",
		Title:     "Dune",
		Format:    "PDF",
		Tags:      []string{"sf"},
	}
	if err := mw.Write(context.Background(), book, service.TriggerManualEdit); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("Sidecar.Write called %d times; want 1", len(rec.calls))
	}
	got := rec.calls[0]
	if got.Key != "books/dune.embookshelf.json" {
		t.Errorf("Key=%q want books/dune.embookshelf.json", got.Key)
	}
	if got.Format != "PDF" {
		t.Errorf("Format=%q want PDF", got.Format)
	}
}

func TestMetadataWriter_Write_AutoEnrichment_SkipsSidecar(t *testing.T) {
	books := &fakeBookWriter{}
	rec := &recordingSidecarWriter{}
	mw := service.NewMetadataWriter(service.MetadataWriterDeps{
		Books:    books,
		LibStore: &fakeLibStore{handle: &service.LibraryHandle{}},
		Sidecar:  rec,
	})
	if err := mw.Write(context.Background(), model.Book{ID: "b1"}, service.TriggerAutoEnrichment); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("Sidecar.Write called %d times on auto-enrichment; want 0", len(rec.calls))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/service/ -run "TestMetadataWriter_Write_ManualEdit_FiresSidecar|TestMetadataWriter_Write_AutoEnrichment_SkipsSidecar" -v`
Expected: FAIL — `MetadataWriterDeps.LibStore` and `Sidecar` fields don't exist yet.

- [ ] **Step 3: Write minimal implementation**

Edit `internal/service/metadata_writer.go`:

```go
import (
	"github.com/blackforge/embookshelf/internal/sidecar"
	"github.com/blackforge/embookshelf/internal/storage"
)

// LibraryStoreFor is the slice of LibraryStore we depend on.
// Avoids a hard import of *defaultLibraryStore so tests can fake it.
type LibraryStoreFor interface {
	For(ctx context.Context, libraryID string) (*LibraryHandle, error)
}

// SidecarWriterFor is the slice of *sidecar.Writer we depend on.
// Mirrors the Plan 1 signature exactly.
type SidecarWriterFor interface {
	Write(ctx context.Context, store storage.Storage, key string, s sidecar.Sidecar, mode sidecar.WriteMode, format string) error
}

type MetadataWriterDeps struct {
	Books    BookMetadataWriter
	LibStore LibraryStoreFor
	Sidecar  SidecarWriterFor
	// Embedder dispatch lands in Task 5.
}
```

Update `Write` to fire sidecar after DB:

```go
func (w *MetadataWriter) Write(ctx context.Context, b model.Book, trigger Trigger) error {
	if w.deps.Books == nil {
		return errors.New("metadata writer: no book repo configured")
	}
	if err := w.deps.Books.UpdateMetadata(ctx, b); err != nil {
		return fmt.Errorf("metadata writer: db: %w", err)
	}
	if trigger == TriggerAutoEnrichment {
		return nil
	}

	// Step 2: sidecar write. Best-effort; a failure here doesn't
	// block step 3 or surface to the caller.
	w.writeSidecar(ctx, b, sidecar.ModeFull)
	return nil
}

// writeSidecar persists the JSON sidecar. mode is decided by the
// caller per the spillover-vs-full rule: when the in-file write
// step succeeds it will be "spillover" (Task 5 flips this); when
// the file step is skipped or fails it stays "full". For Phase 3
// we always use ModeFull because the file step is still stubbed.
func (w *MetadataWriter) writeSidecar(ctx context.Context, b model.Book, mode sidecar.WriteMode) {
	if w.deps.LibStore == nil || w.deps.Sidecar == nil {
		return // not wired (e.g. test that doesn't supply them)
	}
	handle, err := w.deps.LibStore.For(ctx, b.LibraryID)
	if err != nil {
		slog.Warn("metadata writer: lib store lookup", "book_id", b.ID, "err", err)
		return
	}
	key := handle.SidecarKey(b.Path)
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
	if err := w.deps.Sidecar.Write(ctx, handle.Storage, key, side, mode, b.Format); err != nil {
		slog.Warn("metadata writer: sidecar write", "book_id", b.ID, "key", key, "err", err)
	}
}

// dateString returns the publish-date sidecar field's string form.
// model.Book.PublishDate is *time.Time; nil → "".
func dateString(t any) string {
	// Adapt to model.Book's actual PublishDate type.
	// If model.Book.PublishDate is *time.Time, the implementation:
	//   if t == nil { return "" }
	//   return t.Format("2006-01-02")
	// Verify via `grep -n "PublishDate" internal/model/book.go` and
	// adjust the cast accordingly when implementing.
	return fmt.Sprintf("%v", t)
}
```

(Note on `dateString`: confirm the actual `model.Book.PublishDate` type during implementation; the spec ships free-text per `Sidecar.PublishedDate`. If the model uses `*time.Time`, format as `"2006-01-02"`; if `string`, pass through.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/service/ -run "TestMetadataWriter_Write_ManualEdit_FiresSidecar|TestMetadataWriter_Write_AutoEnrichment_SkipsSidecar" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/metadata_writer.go internal/service/metadata_writer_test.go
git commit -m "feat(service): MetadataWriter sidecar step (manual_edit + apply_enrichment)"
```

---

## Phase 4 — File embed step

### Task 4: Spillover-mode resolution + embedder dispatch

**Files:**
- Modify: `internal/service/metadata_writer.go`
- Test: append to `internal/service/metadata_writer_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
// fakeEmbedder stubs fileproc.DispatchEmbedder. ErrNoFile mirrors
// fileproc.ErrUnsupportedEmbed.
type fakeEmbedder struct {
	embeddedFor []string // book paths it was asked to embed
	out         []byte
	err         error
}

func (f *fakeEmbedder) Embed(ctx context.Context, src storage.Source, in fileproc.EmbedInput) ([]byte, error) {
	f.embeddedFor = append(f.embeddedFor, in.Title)
	return f.out, f.err
}

func TestMetadataWriter_Write_EPUBLocal_FullPipeline(t *testing.T) {
	books := &fakeBookWriter{}
	rec := &recordingSidecarWriter{}
	emb := &fakeEmbedder{out: []byte("rezipped-epub-bytes")}
	// Provide a stub Storage (nil OK for this test — Embed mock
	// doesn't read it; Put would be called via storage but we'll
	// assert sidecar-mode + embed-call only).
	handle := &service.LibraryHandle{
		Library: model.Library{ID: "lib1", BackendID: nil},
		// Storage is nil; the test relies on the Dispatch detail
		// that EPUB path skips the file step when Storage is nil.
		// (See implementation: file step requires non-nil Storage.)
	}
	mw := service.NewMetadataWriter(service.MetadataWriterDeps{
		Books:    books,
		LibStore: &fakeLibStore{handle: handle},
		Sidecar:  rec,
		// Storage missing → file step skipped → mode stays Full.
	})
	book := model.Book{
		ID: "b1", LibraryID: "lib1",
		Path: "books/x.epub", Format: "EPUB", Title: "X",
	}
	if err := mw.Write(context.Background(), book, service.TriggerManualEdit); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("sidecar calls: %d want 1", len(rec.calls))
	}
	if rec.calls[0].Mode != sidecar.ModeFull {
		t.Errorf("mode=%q want full (file step skipped)", rec.calls[0].Mode)
	}
	if len(emb.embeddedFor) != 0 {
		t.Errorf("embedder called %d times; want 0 (no Storage)", len(emb.embeddedFor))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/service/ -run TestMetadataWriter_Write_EPUBLocal_FullPipeline -v`
Expected: FAIL — `fakeEmbedder` references `fileproc.EmbedInput` which the test imports but the writer doesn't yet thread an embedder.

- [ ] **Step 3: Write minimal implementation**

Edit `internal/service/metadata_writer.go`:

```go
import (
	"github.com/blackforge/embookshelf/internal/fileproc"
)

// EmbedderDispatcher is the slice of fileproc.DispatchEmbedder we
// depend on. Default impl wraps fileproc.DispatchEmbedder; tests
// inject a fake.
type EmbedderDispatcher func(format string) (fileproc.Embedder, error)

type MetadataWriterDeps struct {
	Books     BookMetadataWriter
	LibStore  LibraryStoreFor
	Sidecar   SidecarWriterFor
	Dispatch  EmbedderDispatcher
}

func (w *MetadataWriter) Write(ctx context.Context, b model.Book, trigger Trigger) error {
	if w.deps.Books == nil {
		return errors.New("metadata writer: no book repo configured")
	}
	if err := w.deps.Books.UpdateMetadata(ctx, b); err != nil {
		return fmt.Errorf("metadata writer: db: %w", err)
	}
	if trigger == TriggerAutoEnrichment {
		return nil
	}

	mode := sidecar.ModeFull // default; flips to Spillover on file-write success
	if w.tryEmbedFile(ctx, b) {
		mode = sidecar.ModeSpillover
	}
	w.writeSidecar(ctx, b, mode)
	return nil
}

// tryEmbedFile attempts the in-file write step. Returns true when the
// write succeeded and the sidecar may downgrade to spillover; false
// (with side effects logged) when the step was skipped, the format is
// unsupported, or the embed/Put failed.
func (w *MetadataWriter) tryEmbedFile(ctx context.Context, b model.Book) bool {
	if w.deps.LibStore == nil || w.deps.Dispatch == nil {
		return false
	}
	handle, err := w.deps.LibStore.For(ctx, b.LibraryID)
	if err != nil {
		slog.Warn("metadata writer: lib store lookup", "book_id", b.ID, "err", err)
		return false
	}
	if !handle.CanWriteInFile() || handle.Storage == nil {
		return false // S3 backend or unwired storage → sidecar full mirror
	}
	emb, err := w.deps.Dispatch(b.Format)
	if err != nil {
		// Unsupported format (CBZ, MOBI, etc.) — sidecar carries everything.
		return false
	}
	src, err := handle.Storage.Open(ctx, b.Path)
	if err != nil {
		slog.Warn("metadata writer: open source", "book_id", b.ID, "path", b.Path, "err", err)
		return false
	}
	defer func() { _ = src.Close() }()
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
		// Cover edits travel through coverstore today; not threaded here.
	}
	out, err := emb.Embed(ctx, src, in)
	if err != nil {
		slog.Warn("metadata writer: embed", "book_id", b.ID, "format", b.Format, "err", err)
		return false
	}
	if _, err := handle.Storage.Put(ctx, b.Path, bytes.NewReader(out)); err != nil {
		slog.Warn("metadata writer: put", "book_id", b.ID, "path", b.Path, "err", err)
		return false
	}
	return true
}
```

Imports: `bytes`, `github.com/blackforge/embookshelf/internal/fileproc`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/service/ -run TestMetadataWriter_Write_EPUBLocal_FullPipeline -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/metadata_writer.go internal/service/metadata_writer_test.go
git commit -m "feat(service): MetadataWriter file embed step + spillover mode resolution"
```

---

### Task 5: Spillover mode test (real Storage path)

**Files:**
- Test: append to `internal/service/metadata_writer_test.go`

- [ ] **Step 1: Write the test**

Append:

```go
func TestMetadataWriter_Write_EPUBLocal_SpilloverMode(t *testing.T) {
	books := &fakeBookWriter{}
	rec := &recordingSidecarWriter{}

	// Provide a real LocalFS so handle.Storage is non-nil and Put works.
	root := t.TempDir()
	fs, err := local.New(root)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	// Seed an EPUB-shaped file at the path we'll edit, so Storage.Open
	// doesn't 404. The fakeEmbedder ignores the bytes.
	bookKey := "books/x.epub"
	if _, err := fs.Put(context.Background(), bookKey, strings.NewReader("placeholder-epub-bytes")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	handle := &service.LibraryHandle{
		Library: model.Library{ID: "lib1", BackendID: nil},
	}
	handle.Storage = fs

	emb := &fakeEmbedder{out: []byte("rezipped-epub-bytes")}
	mw := service.NewMetadataWriter(service.MetadataWriterDeps{
		Books:    books,
		LibStore: &fakeLibStore{handle: handle},
		Sidecar:  rec,
		Dispatch: func(format string) (fileproc.Embedder, error) {
			if format == "EPUB" {
				return emb, nil
			}
			return nil, fileproc.ErrUnsupportedEmbed
		},
	})

	book := model.Book{
		ID: "b1", LibraryID: "lib1",
		Path: bookKey, Format: "EPUB", Title: "Curated",
	}
	if err := mw.Write(context.Background(), book, service.TriggerManualEdit); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if len(emb.embeddedFor) != 1 {
		t.Fatalf("Embed called %d times; want 1", len(emb.embeddedFor))
	}
	if emb.embeddedFor[0] != "Curated" {
		t.Errorf("Embed input title=%q want Curated", emb.embeddedFor[0])
	}
	if rec.calls[0].Mode != sidecar.ModeSpillover {
		t.Errorf("sidecar mode=%q want spillover (file write succeeded)", rec.calls[0].Mode)
	}

	// File on disk should now contain the rezipped bytes.
	rc, err := fs.Get(context.Background(), bookKey)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "rezipped-epub-bytes" {
		t.Errorf("file bytes=%q want rezipped-epub-bytes", got)
	}
}
```

Imports: `io`, `strings`.

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./internal/service/ -run TestMetadataWriter_Write_EPUBLocal_SpilloverMode -v`
Expected: PASS (Task 4 implementation already supports this — this test asserts the success path explicitly).

- [ ] **Step 3: Commit**

```bash
git add internal/service/metadata_writer_test.go
git commit -m "test(service): MetadataWriter spillover-mode happy path"
```

---

## Phase 5 — Integration

### Task 6: Wire `LibraryService.UpdateBookMetadata` through `MetadataWriter`

**Files:**
- Modify: `internal/service/library.go`
- Test: append to `internal/service/library_test.go` (or skip — integration covered by handler test)

- [ ] **Step 1: Add `MetadataWriter` field + setter to `LibraryService`**

Edit `internal/service/library.go` near the existing struct definition:

```go
type LibraryService struct {
	repo   *repo.LibraryRepo
	books  *repo.BookRepo
	writer *MetadataWriter // optional; nil falls back to direct repo write
	deps   LibraryServiceDeps
}

// WithMetadataWriter wires the post-edit pipeline (DB → sidecar →
// file). main.go injects after MetadataWriter is constructed; tests
// that don't need the pipeline pass nil.
func (s *LibraryService) WithMetadataWriter(w *MetadataWriter) *LibraryService {
	s.writer = w
	return s
}

func (s *LibraryService) UpdateBookMetadata(ctx context.Context, b model.Book) error {
	if s.writer != nil {
		return s.writer.Write(ctx, b, TriggerManualEdit)
	}
	return s.books.UpdateMetadata(ctx, b)
}
```

- [ ] **Step 2: Run existing service tests**

Run: `go test ./internal/service/...`
Expected: PASS — fallback path keeps behavior identical for tests that don't wire `WithMetadataWriter`.

- [ ] **Step 3: Commit**

```bash
git add internal/service/library.go
git commit -m "feat(service): LibraryService.UpdateBookMetadata routes via MetadataWriter when wired"
```

---

### Task 7: Wire `EnrichmentService.ApplyMatch` + `AutoEnrich`

**Files:**
- Modify: `internal/service/enrichment.go`

- [ ] **Step 1: Add `MetadataWriter` field + setter**

Edit the `EnrichmentService` struct in `internal/service/enrichment.go`:

```go
type EnrichmentService struct {
	// ...existing fields...
	writer *MetadataWriter
}

func (s *EnrichmentService) WithMetadataWriter(w *MetadataWriter) *EnrichmentService {
	s.writer = w
	return s
}
```

- [ ] **Step 2: Thread Trigger into ApplyMatch**

`ApplyMatch` is called from two sites: the UI handler (apply enrichment) and `AutoEnrich`. Add a `Trigger` parameter:

```go
func (s *EnrichmentService) ApplyMatch(ctx context.Context, book model.Book, m provider.Match, opts ApplyOptions, trigger Trigger) (model.Book, error) {
	// ...existing merge logic...

	// Replace the existing direct call:
	//   if err := s.books.UpdateMetadata(ctx, book); err != nil { ... }
	// with the MetadataWriter route:
	if s.writer != nil {
		if err := s.writer.Write(ctx, book, trigger); err != nil {
			return book, err
		}
	} else {
		if err := s.books.UpdateMetadata(ctx, book); err != nil {
			return book, err
		}
	}
	// ...rest of method (cover-from-url, etc.)...
}
```

- [ ] **Step 3: Update `AutoEnrich` callsite**

Find `AutoEnrich`'s internal call to `ApplyMatch` and pass `TriggerAutoEnrichment`:

```go
if _, err := s.ApplyMatch(ctx, book, *match, ApplyOptions{...}, TriggerAutoEnrichment); err != nil {
```

- [ ] **Step 4: Update the handler callsite**

Edit `internal/handler/enrich.go:262`:

```go
updated, err := h.enrich.ApplyMatch(c.Request.Context(), book, match, service.ApplyOptions{...}, service.TriggerApplyEnrichment)
```

- [ ] **Step 5: Build + run tests**

Run: `go build ./... && go test ./internal/...`
Expected: clean build (compiler verifies signature change covered everywhere); all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/service/enrichment.go internal/handler/enrich.go
git commit -m "feat(service+handler): ApplyMatch threads Trigger; AutoEnrich passes auto_enrichment"
```

---

### Task 8: Construct `MetadataWriter` in `main.go`

**Files:**
- Modify: `cmd/embookshelf/main.go`

- [ ] **Step 1: Read the boot ordering**

Run: `grep -n "libStore\|sidecar\.\|MetadataWriter\|NewBookDropService" cmd/embookshelf/main.go | head`

Locate the line where `libStore` is constructed (Plan F+ added it). `MetadataWriter` lands right after, before `LibraryService.WithMetadataWriter` and `EnrichmentService.WithMetadataWriter` are called.

- [ ] **Step 2: Wire MetadataWriter**

After `libStore` is built and after `bookRepo` exists, add:

```go
sidecarWriter := sidecar.NewWriter()
metadataWriter := service.NewMetadataWriter(service.MetadataWriterDeps{
	Books:    bookRepo,
	LibStore: libStore,
	Sidecar:  sidecarWriter,
	Dispatch: fileproc.DispatchEmbedder,
})

// Wire into LibraryService + EnrichmentService.
libSvc.WithMetadataWriter(metadataWriter)
enrichSvc.WithMetadataWriter(metadataWriter)
```

Imports: `github.com/blackforge/embookshelf/internal/sidecar`, `github.com/blackforge/embookshelf/internal/fileproc` if not already present.

- [ ] **Step 3: Build + smoke**

Run: `go build ./cmd/embookshelf/`
Expected: clean build.

Run: `make test`
Expected: all packages green.

- [ ] **Step 4: Commit**

```bash
git add cmd/embookshelf/main.go
git commit -m "feat(main): wire MetadataWriter; inject into LibraryService + EnrichmentService"
```

---

## Phase 6 — Verification

### Task 9: End-to-end sanity + lint + spec coverage

- [ ] **Step 1: Full test suite**

Run: `make test`
Expected: every package green. Particularly want:
- `ok internal/service` (new MetadataWriter tests + existing service tests).
- `ok internal/handler` (enrich apply path now passes Trigger).

- [ ] **Step 2: Lint**

Run: `make go-lint`
Expected: no new lint errors.

- [ ] **Step 3: Vet**

Run: `go vet ./...`
Expected: silent.

- [ ] **Step 4: Manual smoke against dev stack**

Optional, but recommended to confirm end-to-end on a real EPUB:

```bash
make up   # backend + ui
# 1. Add a test EPUB to ./bookdrop, approve it.
# 2. Edit metadata in UI → Save.
# 3. Inspect the EPUB: `unzip -p path/to/book.epub OEBPS/content.opf | grep dc:title`.
#    Should show the curated title.
# 4. Inspect the sidecar: `cat path/to/book.embookshelf.json` — version: 1, mode: spillover.
```

- [ ] **Step 5: Tag commit log**

```bash
git log --oneline -10
```

Expected: 8 commits visible from this plan.

---

## Self-Review

**Spec coverage** (against `docs/spec/sidecar-write.spec.md` §6, §11.3, §11.4):

| Spec item | Task | Covered |
|---|---|---|
| `LibraryHandle.SidecarKey(bookLocation)` | Task 1 | ✓ |
| `LibraryHandle.CanWriteInFile()` | Task 1 | ✓ |
| `MetadataWriter.Write(ctx, fields, trigger)` signature | Task 2-4 (signature passes `model.Book` instead of a separate `Metadata` shape — see deviation below) | ✓ |
| `Trigger` enum: manual_edit / apply_enrichment / auto_enrichment | Task 2 | ✓ |
| Trigger contract table (manual_edit + apply_enrichment fire file/sidecar; auto skips) | Task 2 + Task 3 + Task 4 | ✓ |
| Sidecar `mode` resolution (spillover on success, full on skip/fail) | Task 4 | ✓ |
| Sequential write order (DB → sidecar → file) | The implementation runs DB → file → sidecar to know `mode` before sidecar write. **Spec deviation** — see below. |  ⚠ |
| Failure semantics (DB error returns to caller; steps 2-3 best-effort) | Task 2 (DB error returns) + Tasks 3-4 (slog.Warn on side-effect failure) | ✓ |
| HTTP wiring: manual edit + apply enrichment | Tasks 6 + 7 | ✓ |
| Hash-stamp `files.content_hash` update after file write | **Deferred to Plan 5.** | Plan 5 |

**Spec deviation (write order):** the spec describes the order as "DB → sidecar → file." This plan flips the latter two: DB → file → sidecar. Reason: the sidecar `mode` (spillover vs full) is a function of whether the file write succeeded. To stamp the right `mode`, we need to know the file outcome first. Spec author can amend §6.2 to describe the actual order, or add a "preliminary sidecar write" + "fixup write on file failure" two-pass dance; the current shape avoids the fixup step and the resulting double-write.

**Spec deviation (Write input type):** spec uses `fields Metadata` per §11.3. This plan passes `b model.Book`. Reason: the existing `BookRepo.UpdateMetadata` takes `model.Book`; introducing a new struct would force a translation at every callsite. `model.Book` already carries every editable field plus the IDs the writer needs (`b.ID`, `b.LibraryID`, `b.Path`, `b.Format`). Spec can amend §11.3 to read `book model.Book`.

**Placeholder scan:** no `TBD`, no `add appropriate error handling`, no `similar to Task N`. Note on `dateString`: I included a doc-comment caveat that the implementer must verify `model.Book.PublishDate`'s actual type and adjust the string formatting accordingly. Marked clearly; not a placeholder.

**Type consistency:**
- `Trigger`, `TriggerManualEdit`, `TriggerApplyEnrichment`, `TriggerAutoEnrichment` — defined Task 2, used Tasks 3/4/6/7.
- `MetadataWriterDeps.{Books, LibStore, Sidecar, Dispatch}` — defined incrementally Tasks 2/3/4; final shape used in Task 8.
- `BookMetadataWriter`, `LibraryStoreFor`, `SidecarWriterFor`, `EmbedderDispatcher` — interface aliases, defined once each.
- `LibraryHandle.SidecarKey`, `LibraryHandle.CanWriteInFile` — Task 1, used Tasks 3-4.
- `fileproc.EmbedInput` — used in Task 4 + 5; defined in Plan 2.
- `sidecar.WriteMode`, `sidecar.ModeFull`, `sidecar.ModeSpillover` — from Plan 1; consumed by Tasks 3, 4.
- `MetadataWriter.tryEmbedFile` — internal helper, Task 4 only.
- `MetadataWriter.writeSidecar` — internal helper, Tasks 3 + 4.

No drift detected.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-01-metadata-writer-service.md`. Two execution options:

1. **Subagent-Driven (recommended)** — fresh subagent per task, review between tasks.
2. **Inline Execution** — `superpowers:executing-plans` w/ checkpoints.

Pick execution mode for Plan 4, or say **"next plan"** to write Plan 5 (hash-stamp scan integration + lock-aware re-extract verification).
