# Hash-Stamp + Lock-Aware Re-Extract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the metadata-write loop. (1) After `MetadataWriter` rewrites a book file, persist the new content's sha256 to `files.content_hash` so the library scan recognizes "we just wrote this." (2) When the scan detects a *real* external file change (hash mismatches), re-extract metadata from the file + sidecar and merge into DB **lock-aware** — `*_locked` fields keep their DB value, the rest are overwritten. Per `docs/spec/sidecar-write.spec.md` §6.2 (step 4), §7.

**Architecture:** Two integration points. (1) `MetadataWriter.tryEmbedFile` (Plan 4) computes sha256 of the rezipped/repatched bytes and records it via `FileRepo.SetContentHash` before returning. (2) `task.LibraryScan` `Changed` branch (Plan C) re-extracts metadata from the file (`fileproc.Dispatch` + `sidecar.Read`), pulls the current DB book row, applies a lock-aware merge, and writes the merged shape via `BookRepo.UpdateMetadata`. The hash-stamp short-circuit lives in the Changed branch — if the file's actual hash matches the DB-recorded hash, skip extract entirely (covers the "scan saw our own write" case).

**Tech Stack:** Go 1.25 stdlib + existing project packages. No new third-party deps.

**Companion docs:**
- `docs/spec/sidecar-write.spec.md` §6.2 step 4 (hash-stamp), §7 (lock-aware re-extract).
- `docs/adr/0001-edit-side-metadata-write-back.md` (lock-aware re-extract rationale).

**Depends on:**
- Plan 1 (sidecar JSON cutover) — `sidecar.Read(ctx, store, bookKey)` already exists.
- Plan 2/3 (Embedders) — extracted format → write side; not directly used by scan re-extract, only by MetadataWriter's file step.
- Plan 4 (MetadataWriter) — `MetadataWriter.tryEmbedFile` is where step 4 hash-stamp lands.
- Existing Plan B (`files.content_hash` column + `FileRepo.SetContentHash`) and Plan C (`task.LibraryScan` w/ `scan.Diff` Changed branch).

**Out of scope:**
- Sidecar repair worker (Phase 2 candidate per spec §10).
- New cover-edit pipeline (covers still travel through coverstore directly today).
- Multi-instance write coordination (Plan F+ topic).

---

## File Structure

| Path | Change |
|---|---|
| `internal/service/metadata_writer.go` | **Modify.** `tryEmbedFile` computes sha256 of `out`, calls `FileRepo.SetContentHash` for the book's primary file row. |
| `internal/repo/file.go` | **Read-only.** Confirms existing `FileRepo.SetContentHash` shape. No code change. |
| `internal/scan/diff.go` | **Read-only.** Confirms existing `Changeset.Changed` shape. No code change. |
| `internal/task/library_scan.go` | **Modify.** `Changed` branch: after re-hashing the file, compare `hash` vs `ce.DB.ContentHash`. Match → no-op (skip both row update + metadata re-extract). Mismatch → re-extract via fileproc + sidecar.Read + lock-aware merge → `BookRepo.UpdateMetadata`. Update `files.content_hash` to the new hash regardless. |
| `internal/service/lock_merge.go` | **Create.** `MergeLocked(current, extracted model.Book) model.Book` — pure function applying the lock-aware merge per §7. Lives in service so both scan re-extract and any future caller can share it. |
| `internal/service/lock_merge_test.go` | **Create.** Field-by-field tests covering each `*_locked` flag. |
| `internal/task/library_scan.go` (new deps) | **Modify.** `LibraryScanDeps` gains `Books *repo.BookRepo` (re-extract update target) and `LockMerger func(current, extracted model.Book) model.Book` (injectable for tests, default `service.MergeLocked`). |
| `cmd/embookshelf/main.go` | **Modify.** Pass `bookRepo` + `service.MergeLocked` into the LibraryScanDeps wherever it's constructed. |

---

## Phase 1 — MetadataWriter hash-stamp

### Task 1: `MetadataWriter.tryEmbedFile` computes + persists sha256

**Files:**
- Modify: `internal/service/metadata_writer.go`
- Test: append to `internal/service/metadata_writer_test.go`

- [ ] **Step 1: Confirm FileRepo.SetContentHash signature**

Run: `grep -n "func.*FileRepo.*SetContentHash" internal/repo/file.go`
Expected: a single match like `func (r *FileRepo) SetContentHash(ctx context.Context, fileID string, hash []byte, size int64, mtime time.Time) error`.

- [ ] **Step 2: Confirm we can resolve "the file row for this book"**

Run: `grep -n "func.*FileRepo.*ListByBook\|GetByBook" internal/repo/file.go`
Expected: a method like `ListByBook(ctx, bookID) ([]model.File, error)` (Plan G already added this for the BookSource decision).

- [ ] **Step 3: Write the failing test**

Append to `internal/service/metadata_writer_test.go`:

```go
import "crypto/sha256"

// fakeFileRepo records SetContentHash calls.
type fakeFileRepo struct {
	files     []model.File // returned by ListByBook
	listErr   error
	stamped   []hashStamp
	stampErr  error
}

type hashStamp struct {
	FileID string
	Hash   []byte
	Size   int64
}

func (f *fakeFileRepo) ListByBook(ctx context.Context, bookID string) ([]model.File, error) {
	return f.files, f.listErr
}
func (f *fakeFileRepo) SetContentHash(ctx context.Context, fileID string, hash []byte, size int64, mtime time.Time) error {
	f.stamped = append(f.stamped, hashStamp{FileID: fileID, Hash: hash, Size: size})
	return f.stampErr
}

func TestMetadataWriter_HashStampAfterFileWrite(t *testing.T) {
	books := &fakeBookWriter{}
	rec := &recordingSidecarWriter{}

	root := t.TempDir()
	fs, err := local.New(root)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	bookKey := "books/x.epub"
	if _, err := fs.Put(context.Background(), bookKey, strings.NewReader("placeholder")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	handle := &service.LibraryHandle{
		Library: model.Library{ID: "lib1", BackendID: nil},
	}
	handle.Storage = fs

	emb := &fakeEmbedder{out: []byte("rezipped-bytes")}
	files := &fakeFileRepo{files: []model.File{{ID: "f1", BookID: "b1", Format: "EPUB"}}}

	mw := service.NewMetadataWriter(service.MetadataWriterDeps{
		Books:    books,
		LibStore: &fakeLibStore{handle: handle},
		Sidecar:  rec,
		Files:    files,
		Dispatch: func(format string) (fileproc.Embedder, error) { return emb, nil },
	})

	book := model.Book{
		ID: "b1", LibraryID: "lib1",
		Path: bookKey, Format: "EPUB", Title: "X",
	}
	if err := mw.Write(context.Background(), book, service.TriggerManualEdit); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if len(files.stamped) != 1 {
		t.Fatalf("SetContentHash called %d times; want 1", len(files.stamped))
	}
	got := files.stamped[0]
	wantHash := sha256.Sum256([]byte("rezipped-bytes"))
	if !bytes.Equal(got.Hash, wantHash[:]) {
		t.Errorf("hash=%x want %x", got.Hash, wantHash)
	}
	if got.Size != int64(len("rezipped-bytes")) {
		t.Errorf("size=%d want %d", got.Size, len("rezipped-bytes"))
	}
	if got.FileID != "f1" {
		t.Errorf("file_id=%q want f1", got.FileID)
	}
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `go test ./internal/service/ -run TestMetadataWriter_HashStampAfterFileWrite -v`
Expected: FAIL — `MetadataWriterDeps.Files` field doesn't exist.

- [ ] **Step 5: Write minimal implementation**

Edit `internal/service/metadata_writer.go`:

```go
import (
	"crypto/sha256"
	"time"
)

// FileMetadataRepo is the slice of *repo.FileRepo we depend on.
// Defined here so tests can fake it.
type FileMetadataRepo interface {
	ListByBook(ctx context.Context, bookID string) ([]model.File, error)
	SetContentHash(ctx context.Context, fileID string, hash []byte, size int64, mtime time.Time) error
}

type MetadataWriterDeps struct {
	Books    BookMetadataWriter
	LibStore LibraryStoreFor
	Sidecar  SidecarWriterFor
	Dispatch EmbedderDispatcher
	Files    FileMetadataRepo
}
```

Update `tryEmbedFile` — append after the successful `Put`:

```go
	if _, err := handle.Storage.Put(ctx, b.Path, bytes.NewReader(out)); err != nil {
		slog.Warn("metadata writer: put", "book_id", b.ID, "path", b.Path, "err", err)
		return false
	}
	// Hash-stamp: record the new file's sha256 so library scan
	// recognizes "we wrote this" and doesn't re-extract on the next
	// tick. Best-effort; failure here means scan re-extracts and
	// lock-aware merge takes over.
	if w.deps.Files != nil {
		w.stampFileHash(ctx, b, out)
	}
	return true
}

// stampFileHash computes sha256 of the freshly-written file bytes and
// updates files.content_hash for the book's primary file row.
// "Primary" = files row whose format matches the book's. Fallback to
// the first row when no format match exists (mirrors the BookSource
// primary-file rule from Plan G).
func (w *MetadataWriter) stampFileHash(ctx context.Context, b model.Book, out []byte) {
	files, err := w.deps.Files.ListByBook(ctx, b.ID)
	if err != nil {
		slog.Warn("metadata writer: list files", "book_id", b.ID, "err", err)
		return
	}
	var primary model.File
	primary.ID = "" // sentinel
	for _, f := range files {
		if f.Format == b.Format {
			primary = f
			break
		}
	}
	if primary.ID == "" && len(files) > 0 {
		primary = files[0]
	}
	if primary.ID == "" {
		// No files row yet (pre-files-backfill). Skip; the next
		// boot-time backfill will hash the file from disk and
		// catch up.
		return
	}
	sum := sha256.Sum256(out)
	if err := w.deps.Files.SetContentHash(ctx, primary.ID, sum[:], int64(len(out)), time.Now().UTC()); err != nil {
		slog.Warn("metadata writer: set content hash", "file_id", primary.ID, "err", err)
	}
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/service/ -run TestMetadataWriter_HashStampAfterFileWrite -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/service/metadata_writer.go internal/service/metadata_writer_test.go
git commit -m "feat(service): MetadataWriter hash-stamps files.content_hash after file write"
```

---

### Task 2: Wire `Files` into main.go's MetadataWriter construction

**Files:**
- Modify: `cmd/embookshelf/main.go`

- [ ] **Step 1: Locate the MetadataWriter construction**

Run: `grep -n "NewMetadataWriter\|MetadataWriterDeps" cmd/embookshelf/main.go`
Expected: a single block (added in Plan 4 Task 8).

- [ ] **Step 2: Add `Files: fileRepo` to the deps**

Edit the construction:

```go
metadataWriter := service.NewMetadataWriter(service.MetadataWriterDeps{
	Books:    bookRepo,
	LibStore: libStore,
	Sidecar:  sidecarWriter,
	Dispatch: fileproc.DispatchEmbedder,
	Files:    fileRepo,
})
```

- [ ] **Step 3: Build + boot smoke**

Run: `go build ./cmd/embookshelf/`
Expected: clean build.

- [ ] **Step 4: Commit**

```bash
git add cmd/embookshelf/main.go
git commit -m "feat(main): wire FileRepo into MetadataWriter for hash-stamping"
```

---

## Phase 2 — Lock-aware merge

### Task 3: `service.MergeLocked` pure function

**Files:**
- Create: `internal/service/lock_merge.go`
- Create: `internal/service/lock_merge_test.go`

- [ ] **Step 1: Confirm books table lock columns**

Run: `grep -n "Locks\b" internal/model/book.go`
Expected: `Locks BookLocks` field on `model.Book` with `BookLocks` struct fields like `Title`, `Author`, etc. (booleans).

- [ ] **Step 2: Write the failing tests**

Create `internal/service/lock_merge_test.go`:

```go
package service_test

import (
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
)

func TestMergeLocked_UnlockedFieldsOverwritten(t *testing.T) {
	current := model.Book{
		Title:  "DB Title",
		Author: "DB Author",
		Locks:  model.BookLocks{}, // nothing locked
	}
	extracted := model.Book{
		Title:  "File Title",
		Author: "File Author",
	}
	got := service.MergeLocked(current, extracted)
	if got.Title != "File Title" {
		t.Errorf("Title=%q want File Title (unlocked: file wins)", got.Title)
	}
	if got.Author != "File Author" {
		t.Errorf("Author=%q want File Author (unlocked: file wins)", got.Author)
	}
}

func TestMergeLocked_LockedFieldsKeepDB(t *testing.T) {
	current := model.Book{
		Title:  "DB Title",
		Author: "DB Author",
		Locks: model.BookLocks{
			Title: true,
		},
	}
	extracted := model.Book{
		Title:  "File Title",
		Author: "File Author",
	}
	got := service.MergeLocked(current, extracted)
	if got.Title != "DB Title" {
		t.Errorf("Title=%q want DB Title (locked: DB wins)", got.Title)
	}
	if got.Author != "File Author" {
		t.Errorf("Author=%q want File Author (unlocked: file wins)", got.Author)
	}
}

func TestMergeLocked_PreservesIDsAndStructural(t *testing.T) {
	current := model.Book{
		ID:        "b1",
		LibraryID: "lib1",
		Path:      "books/x.epub",
		Format:    "EPUB",
		Locks:     model.BookLocks{},
	}
	extracted := model.Book{
		// Extracted shape carries no ID; structural fields must
		// stay from current.
		Title: "T",
	}
	got := service.MergeLocked(current, extracted)
	if got.ID != "b1" {
		t.Errorf("ID lost: %q", got.ID)
	}
	if got.LibraryID != "lib1" {
		t.Errorf("LibraryID lost: %q", got.LibraryID)
	}
	if got.Path != "books/x.epub" {
		t.Errorf("Path lost: %q", got.Path)
	}
	if got.Format != "EPUB" {
		t.Errorf("Format lost: %q", got.Format)
	}
	if got.Title != "T" {
		t.Errorf("Title not applied: %q", got.Title)
	}
}

func TestMergeLocked_AllLockableFields(t *testing.T) {
	current := model.Book{
		ID: "b1",
		Locks: model.BookLocks{
			Title:       true,
			Subtitle:    true,
			Author:      true,
			Description: true,
			Publisher:   true,
			Series:      true,
			ISBN:        true,
			ISBN10:      true,
			Language:    true,
			PublishDate: true,
			Genres:      true,
			Moods:       true,
			Tags:        true,
			Pages:       true,
			Cover:       true,
		},
		Title: "DB", Subtitle: "DB", Author: "DB", Description: "DB",
		Publisher: "DB", Series: "DB", ISBN: "DB", ISBN10: "DB",
		Language: "DB",
		Genres: []string{"db-g"}, Moods: []string{"db-m"}, Tags: []string{"db-t"},
		Pages: 100,
	}
	extracted := model.Book{
		Title: "FILE", Subtitle: "FILE", Author: "FILE", Description: "FILE",
		Publisher: "FILE", Series: "FILE", ISBN: "FILE", ISBN10: "FILE",
		Language: "FILE",
		Genres: []string{"file-g"}, Moods: []string{"file-m"}, Tags: []string{"file-t"},
		Pages: 200,
	}
	got := service.MergeLocked(current, extracted)
	cases := []struct {
		field string
		got   string
	}{
		{"Title", got.Title}, {"Subtitle", got.Subtitle}, {"Author", got.Author},
		{"Description", got.Description}, {"Publisher", got.Publisher},
		{"Series", got.Series}, {"ISBN", got.ISBN}, {"ISBN10", got.ISBN10},
		{"Language", got.Language},
	}
	for _, c := range cases {
		if c.got != "DB" {
			t.Errorf("%s=%q; locked → want DB", c.field, c.got)
		}
	}
	if len(got.Genres) != 1 || got.Genres[0] != "db-g" {
		t.Errorf("Genres=%v; locked → want [db-g]", got.Genres)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "db-t" {
		t.Errorf("Tags=%v; locked → want [db-t]", got.Tags)
	}
	if got.Pages != 100 {
		t.Errorf("Pages=%d; locked → want 100", got.Pages)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/service/ -run TestMergeLocked -v`
Expected: FAIL with "undefined: service.MergeLocked".

- [ ] **Step 4: Write minimal implementation**

Create `internal/service/lock_merge.go`:

```go
package service

import "github.com/blackforge/embookshelf/internal/model"

// MergeLocked applies a lock-aware merge of file-extracted book
// metadata onto the current DB row. For each editable field, if
// the corresponding *_locked flag on current is true, current's
// value wins; otherwise extracted's value wins.
//
// Structural fields (ID, LibraryID, Path, Format, CreatedAt) are
// always preserved from current — extracted is the "shape that
// came out of the file/sidecar," not a complete book row.
//
// This is a pure function so callers (currently the library scan's
// re-extract path; possibly future enrichment paths) can compose
// it consistently. The lock contract is documented in
// docs/spec/sidecar-write.spec.md §7 and ADR 0001.
func MergeLocked(current, extracted model.Book) model.Book {
	out := current // start from DB row, preserves IDs + locks + non-extracted fields

	if !current.Locks.Title {
		out.Title = extracted.Title
	}
	if !current.Locks.Subtitle {
		out.Subtitle = extracted.Subtitle
	}
	if !current.Locks.Author {
		out.Author = extracted.Author
	}
	if !current.Locks.Description {
		out.Description = extracted.Description
	}
	if !current.Locks.Publisher {
		out.Publisher = extracted.Publisher
	}
	if !current.Locks.Series {
		out.Series = extracted.Series
		out.SeriesIndex = extracted.SeriesIndex
		out.SeriesTotal = extracted.SeriesTotal
	}
	if !current.Locks.ISBN {
		out.ISBN = extracted.ISBN
	}
	if !current.Locks.ISBN10 {
		out.ISBN10 = extracted.ISBN10
	}
	if !current.Locks.Language {
		out.Language = extracted.Language
	}
	if !current.Locks.PublishDate {
		out.PublishDate = extracted.PublishDate
	}
	if !current.Locks.Genres {
		out.Genres = extracted.Genres
	}
	if !current.Locks.Moods {
		out.Moods = extracted.Moods
	}
	if !current.Locks.Tags {
		out.Tags = extracted.Tags
	}
	if !current.Locks.Pages {
		out.Pages = extracted.Pages
	}
	// Cover lock blocks cover replacement; cover bytes/hash are handled
	// separately by the scan path (not in this scalar merge), so
	// nothing to do here.
	_ = current.Locks.Cover
	return out
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/service/ -run TestMergeLocked -v`
Expected: PASS (4 tests).

- [ ] **Step 6: Commit**

```bash
git add internal/service/lock_merge.go internal/service/lock_merge_test.go
git commit -m "feat(service): MergeLocked applies per-field lock-aware merge"
```

---

## Phase 3 — Scan re-extract integration

### Task 4: Library scan re-extracts metadata for Changed files

**Files:**
- Modify: `internal/task/library_scan.go`
- Modify: `internal/task/library_scan_test.go` (or create)

- [ ] **Step 1: Read the existing Changed branch**

Run: `grep -n "cs.Changed\|MaybeReattach\|SetContentHash" internal/task/library_scan.go`
Expected: a loop around `cs.Changed` that re-hashes + checks reattach + writes content_hash.

- [ ] **Step 2: Write the failing test**

Create `internal/task/library_scan_test.go` (or append if exists):

```go
package task_test

import (
	"context"
	"crypto/sha256"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
	"github.com/blackforge/embookshelf/internal/task"
)

// stubLockMerger asserts MergeLocked is invoked for re-extract.
type lockMergerCall struct {
	current   model.Book
	extracted model.Book
}

func TestLibraryScan_HashMatch_SkipsReExtract(t *testing.T) {
	// When the file's actual hash matches the DB-recorded
	// content_hash, the scan must NOT call extract or merge.
	// This test wires a recording lock-merger and asserts it
	// was never called.

	d := repotest.NewWithDialect(t, "sqlite")
	libRepo := repo.NewLibraryRepo(d)
	bookRepo := repo.NewBookRepo(d)
	fileRepo := repo.NewFileRepo(d)
	ctx := context.Background()

	tmp := t.TempDir()
	fs, err := local.New(tmp)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	libStore := service.NewLibraryStore(service.LibraryStoreDeps{
		Libs:     libRepo,
		Resolver: storage.ConstantResolver{S: fs},
	})

	lib, err := libRepo.CreateLibrary(ctx, "Test", "test", tmp, nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	// Seed file on disk + corresponding files row + book row whose
	// content_hash matches the file.
	bookKey := "x.epub"
	bytesOnDisk := []byte("seed-bytes")
	if _, err := fs.Put(ctx, bookKey, bytesReader(bytesOnDisk)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	hashSeed := sha256.Sum256(bytesOnDisk)

	book, err := bookRepo.Create(ctx, model.Book{
		LibraryID: lib.ID, Title: "Original", Format: "EPUB", Path: bookKey,
	})
	if err != nil {
		t.Fatalf("Create book: %v", err)
	}
	_, err = fileRepo.Insert(ctx, model.File{
		LibraryID:   lib.ID,
		BookID:      book.ID,
		Location:    bookKey,
		Size:        int64(len(bytesOnDisk)),
		ContentHash: hashSeed[:],
		Format:      "EPUB",
	})
	if err != nil {
		t.Fatalf("Insert file: %v", err)
	}

	mergerCalls := 0
	deps := task.LibraryScanDeps{
		BookDrop: nil,
		Lib:      service.NewLibraryService(libRepo, bookRepo, service.LibraryServiceDeps{}),
		LibStore: libStore,
		Files:    fileRepo,
		Books:    bookRepo,
		LockMerger: func(current, extracted model.Book) model.Book {
			mergerCalls++
			return current
		},
	}
	if err := task.LibraryScan(ctx, task.LibraryScanArgs{LibraryID: lib.ID}, deps); err != nil {
		t.Fatalf("LibraryScan: %v", err)
	}

	if mergerCalls != 0 {
		t.Errorf("LockMerger called %d times; want 0 (hash matched, no re-extract)", mergerCalls)
	}
	// Title in DB should be unchanged.
	got, err := bookRepo.GetByID(ctx, "", book.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Title != "Original" {
		t.Errorf("Title=%q want Original (re-extract should not have run)", got.Title)
	}
}

func TestLibraryScan_HashMismatch_RunsLockMerger(t *testing.T) {
	d := repotest.NewWithDialect(t, "sqlite")
	libRepo := repo.NewLibraryRepo(d)
	bookRepo := repo.NewBookRepo(d)
	fileRepo := repo.NewFileRepo(d)
	ctx := context.Background()

	tmp := t.TempDir()
	fs, err := local.New(tmp)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	libStore := service.NewLibraryStore(service.LibraryStoreDeps{
		Libs:     libRepo,
		Resolver: storage.ConstantResolver{S: fs},
	})

	lib, err := libRepo.CreateLibrary(ctx, "Test", "test", tmp, nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	bookKey := "x.epub"
	if _, err := fs.Put(ctx, bookKey, bytesReader([]byte("on-disk-bytes-different"))); err != nil {
		t.Fatalf("Put: %v", err)
	}

	book, err := bookRepo.Create(ctx, model.Book{
		LibraryID: lib.ID, Title: "Old DB Title", Format: "EPUB", Path: bookKey,
	})
	if err != nil {
		t.Fatalf("Create book: %v", err)
	}
	staleHash := sha256.Sum256([]byte("a-different-thing"))
	_, err = fileRepo.Insert(ctx, model.File{
		LibraryID:   lib.ID,
		BookID:      book.ID,
		Location:    bookKey,
		Size:        100,
		ContentHash: staleHash[:],
		Format:      "EPUB",
	})
	if err != nil {
		t.Fatalf("Insert file: %v", err)
	}

	mergerCalls := 0
	deps := task.LibraryScanDeps{
		Lib:      service.NewLibraryService(libRepo, bookRepo, service.LibraryServiceDeps{}),
		LibStore: libStore,
		Files:    fileRepo,
		Books:    bookRepo,
		LockMerger: func(current, extracted model.Book) model.Book {
			mergerCalls++
			return current // keep DB title for the assertion below
		},
	}
	if err := task.LibraryScan(ctx, task.LibraryScanArgs{LibraryID: lib.ID}, deps); err != nil {
		t.Fatalf("LibraryScan: %v", err)
	}

	if mergerCalls != 1 {
		t.Errorf("LockMerger called %d times; want 1 (hash mismatch → re-extract)", mergerCalls)
	}
}

// bytesReader is a tiny wrapper so the existing test helper file
// compiles even if `bytes.NewReader` isn't already imported.
func bytesReader(b []byte) *bytesReadSeeker {
	return &bytesReadSeeker{data: b}
}
type bytesReadSeeker struct {
	data []byte
	off  int
}
func (b *bytesReadSeeker) Read(p []byte) (int, error) {
	n := copy(p, b.data[b.off:])
	b.off += n
	if n == 0 { return 0, errEOF{} }
	return n, nil
}
type errEOF struct{}
func (errEOF) Error() string { return "EOF" }
```

Replace `bytesReader` with `bytes.NewReader` if `bytes` is already imported in this file.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/task/ -run "TestLibraryScan_HashMatch_SkipsReExtract|TestLibraryScan_HashMismatch_RunsLockMerger" -v`
Expected: FAIL — `LibraryScanDeps.Books` and `LockMerger` fields don't exist; the scan also doesn't dispatch fileproc on Changed.

- [ ] **Step 4: Write minimal implementation**

Edit `internal/task/library_scan.go`:

```go
import (
	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/sidecar"
)

type LibraryScanDeps struct {
	BookDrop *service.BookDropService
	Lib      *service.LibraryService
	Queue    BookDropEnqueuer
	LibStore service.LibraryStore
	Files    *repo.FileRepo
	Books    *repo.BookRepo
	// LockMerger applies the lock-aware merge of file-extracted
	// metadata onto a DB row. Default service.MergeLocked.
	LockMerger func(current, extracted model.Book) model.Book
}
```

Update the `cs.Changed` loop:

```go
for _, ce := range cs.Changed {
	key := joinKey(root, ce.Walk.Location)
	hash, size, herr := hashing.HashFile(ctx, store, key)
	if herr != nil {
		slog.Warn("library scan: rehash failed", "loc", ce.Walk.Location, "err", herr)
		continue
	}

	// Hash-stamp short-circuit: if the file's actual hash matches
	// what the DB already recorded, this is "we just wrote it" —
	// no metadata re-extract needed. Either the scan tick raced
	// the MetadataWriter's hash-stamp call (rare) or scan was
	// triggered by an mtime change without bytes changing.
	if len(ce.DB.ContentHash) > 0 && bytes.Equal(hash, ce.DB.ContentHash) {
		continue
	}

	reattached, rerr := scan.MaybeReattach(ctx, deps.Files, lib.ID, hash, ce.Walk.Location, ce.DB.ID)
	if rerr != nil {
		slog.Warn("library scan: reattach failed", "loc", ce.Walk.Location, "err", rerr)
	} else if reattached {
		continue
	}

	// External edit (or first-time hash on a never-stamped row).
	// Re-extract and merge with locks.
	if deps.Books != nil && deps.LockMerger != nil {
		reExtractAndMerge(ctx, deps, ce.DB.BookID, key, store, handle)
	}

	if err := deps.Files.SetContentHash(ctx, ce.DB.ID, hash, size, ce.Walk.Mtime); err != nil {
		slog.Warn("library scan: update changed row", "id", ce.DB.ID, "err", err)
	}
}
```

(Note: `handle` is the `LibraryHandle` already in scope from earlier in `LibraryScan`. `bytes` import for `bytes.Equal`.)

Add the helper:

```go
// reExtractAndMerge runs the file-format-specific extractor on a
// changed file, overlays the sidecar, and applies the lock-aware
// merge into DB. Best-effort: errors are logged, never aborting
// the scan.
func reExtractAndMerge(
	ctx context.Context,
	deps LibraryScanDeps,
	bookID, fileKey string,
	store storage.Storage,
	handle *service.LibraryHandle,
) {
	if bookID == "" {
		return // pre-Plan-B file with no book linkage
	}
	src, err := store.Open(ctx, fileKey)
	if err != nil {
		slog.Warn("library scan: open for re-extract", "key", fileKey, "err", err)
		return
	}
	defer func() { _ = src.Close() }()

	proc, _, err := fileproc.Dispatch(fileKey)
	if err != nil {
		// Unsupported format — sidecar overlay only.
		slog.Debug("library scan: no processor for re-extract", "key", fileKey, "err", err)
	}
	var meta fileproc.Metadata
	if proc != nil {
		meta, err = proc.Extract(ctx, src)
		if err != nil {
			slog.Warn("library scan: extract failed", "key", fileKey, "err", err)
			// Continue with sidecar overlay alone.
		}
	}

	// Sidecar overlay (Plan 1: bidirectional read).
	side, sErr := sidecar.Read(ctx, store, fileKey)
	if sErr != nil {
		slog.Warn("library scan: sidecar read", "key", fileKey, "err", sErr)
	}

	// Translate Metadata + Sidecar into a model.Book shape suitable
	// for the merger. Only the editable scalar fields land here;
	// IDs/locks/structural fields are preserved by MergeLocked from
	// the current DB row.
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
	// Note: PublishDate format conversion (string → *time.Time) and
	// Pages from PDF reader land as a follow-up; the current
	// extractor doesn't fill those.

	current, err := deps.Books.GetByID(ctx, "", bookID)
	if err != nil {
		slog.Warn("library scan: load current book", "book_id", bookID, "err", err)
		return
	}
	merged := deps.LockMerger(current, extracted)
	if err := deps.Books.UpdateMetadata(ctx, merged); err != nil {
		slog.Warn("library scan: update merged metadata", "book_id", bookID, "err", err)
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/task/ -run "TestLibraryScan_HashMatch_SkipsReExtract|TestLibraryScan_HashMismatch_RunsLockMerger" -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/task/library_scan.go internal/task/library_scan_test.go
git commit -m "feat(task): library scan re-extracts + lock-aware merges on file change"
```

---

### Task 5: Wire `Books` + `LockMerger` into main.go's LibraryScanDeps

**Files:**
- Modify: `cmd/embookshelf/main.go`

- [ ] **Step 1: Locate scan deps construction**

Run: `grep -n "LibraryScanDeps\|task.LibraryScan\b" cmd/embookshelf/main.go`
Expected: deps blob constructed and passed to either River worker registration or the SQLite queue dispatcher.

- [ ] **Step 2: Add fields**

```go
scanDeps := task.LibraryScanDeps{
	BookDrop: bdropSvc,
	Lib:      libSvc,
	Queue:    queueClient,
	LibStore: libStore,
	Files:    fileRepo,
	Books:    bookRepo,
	LockMerger: service.MergeLocked,
}
```

- [ ] **Step 3: Build + run tests**

Run: `go build ./... && go test ./...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add cmd/embookshelf/main.go
git commit -m "feat(main): wire BookRepo + MergeLocked into LibraryScanDeps"
```

---

## Phase 4 — Verification

### Task 6: End-to-end sanity + lint + spec coverage

- [ ] **Step 1: Full test suite**

Run: `make test`
Expected: every package green. Particularly:
- `ok internal/service` (MergeLocked + MetadataWriter hash-stamp tests).
- `ok internal/task` (scan hash-match + hash-mismatch tests).

- [ ] **Step 2: Lint**

Run: `make go-lint`
Expected: silent.

- [ ] **Step 3: Vet**

Run: `go vet ./...`
Expected: silent.

- [ ] **Step 4: End-to-end smoke (manual, optional)**

```bash
make up
# 1. Approve a test EPUB. Note the title in UI.
# 2. Open the EPUB externally (Sigil or `unzip` + edit OPF + `zip` back). Change the dc:title.
# 3. Trigger a library scan (POST /api/settings/libraries/:id/scan).
# 4. Expect the UI title to update to the externally-edited title.
# 5. Now lock the title in the UI. Change the EPUB externally again.
# 6. Trigger scan. Expect the UI title NOT to change.
```

- [ ] **Step 5: Tag commit log**

```bash
git log --oneline -10
```

Expected: 5 commits visible from this plan.

---

## Self-Review

**Spec coverage** (against `docs/spec/sidecar-write.spec.md` §6.2 step 4 + §7):

| Spec item | Task | Covered |
|---|---|---|
| Hash-stamp `files.content_hash` after file write | Task 1 (MetadataWriter) | ✓ |
| Hash-stamp short-circuit in scan (skip re-extract on match) | Task 4 (`bytes.Equal(hash, ce.DB.ContentHash)` early-continue) | ✓ |
| External-edit re-extract (file embedded + sidecar overlay) | Task 4 (`reExtractAndMerge`) | ✓ |
| Lock-aware merge: locked → DB wins, unlocked → file wins | Task 3 (`MergeLocked`) | ✓ |
| Files.content_hash advances after external edit | Task 4 (existing `SetContentHash` retained after merge) | ✓ |
| Cover lock blocks cover replacement | Task 3 (`_ = current.Locks.Cover` flagged; cover bytes path is separate from scalar merge — Phase 2 candidate) | partial |
| `LibraryHandle` flow (LibraryStore.For inside scan) | Task 4 (handle already in scope from existing scan code) | ✓ |

**Spec deviation flagged (cover lock):** the scalar `MergeLocked` doesn't touch cover bytes/hash because covers go through `coverstore`, not the books row's scalar fields. The `cover_locked` flag will need a separate enforcement point when a cover-replace path lands (today there's no scan-side cover replacement, so the lock is dormant). Task 3's test coverage explicitly skips `Cover` for that reason. Spec §7 mentions cover handling in the lock list; the dormant-until-implemented status is documented in the merger's source comment.

**Spec deviation flagged (extracted shape coverage):** `reExtractAndMerge` builds an `extracted model.Book` from the union of `fileproc.Metadata` + `sidecar.Sidecar`. Today `Metadata` doesn't carry Series/Tags/Genres/Publisher/ISBN; only the sidecar overlay does. After this plan ships, an external editor changing those fields *inside an EPUB OPF* won't be detected by re-extract until the existing `EPUBProcessor.Extract` is extended to read the new fields. Tracked as a follow-up; not blocking the lock-aware infrastructure.

**Placeholder scan:** no `TBD`, no `add appropriate error handling`, no `similar to Task N`. Note that Task 4's helper relies on `firstNonEmpty` to bridge sidecar-vs-file precedence — that's the documented overlay precedence from spec §3 (sidecar wins where non-empty).

**Type consistency:**
- `MergeLocked(current, extracted model.Book) model.Book` — defined Task 3, used Task 4 + main.go (Task 5).
- `LibraryScanDeps.{Books, LockMerger}` — defined Task 4, populated Task 5.
- `MetadataWriterDeps.Files` — defined Task 1, populated Task 2.
- `FileMetadataRepo` interface — Task 1 only.
- `reExtractAndMerge` / `firstNonEmpty` — internal helpers in `library_scan.go`, Task 4 only.
- Test helpers (`fakeBookWriter`, `fakeFileRepo`, `fakeLibStore`, etc.) — defined in `metadata_writer_test.go` (Plan 4 + this plan); reused.

No drift detected.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-01-hash-stamp-scan-integration.md`. **This is the final plan in the sidecar-write feature set.** Two execution options:

1. **Subagent-Driven (recommended)** — fresh subagent per task, review between tasks.
2. **Inline Execution** — `superpowers:executing-plans` w/ checkpoints.

Pick execution mode for Plan 5, or run all five plans in order via `superpowers:subagent-driven-development`.

---

## Plan-set summary

The five plans cover the sidecar-write feature end-to-end:

| Plan | Path | Subsystem | Tasks |
|---|---|---|---|
| 1 | `2026-04-30-sidecar-json-cutover.md` | TOML → JSON cutover, paired filename | 10 |
| 2 | `2026-04-30-epub-embedder.md` | `Embedder` interface + EPUB OPF/cover write | 8 |
| 3 | `2026-04-30-pdf-embedder.md` | PDF `/Info` incremental update, Keywords prefix | 9 |
| 4 | `2026-05-01-metadata-writer-service.md` | `MetadataWriter` orchestrator + HTTP wiring | 9 |
| 5 | `2026-05-01-hash-stamp-scan-integration.md` | hash-stamp + lock-aware re-extract | 6 |

**Total: 42 tasks across 5 PRs.** Each plan ships independently; ordering is 1 → 2 → 3 → 4 → 5 (each depends on the prior plans' interfaces).
