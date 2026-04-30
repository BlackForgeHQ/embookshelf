# `fileproc.Processor` Source Refactor — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Widen `fileproc.Processor.Extract` from `path string` to `storage.Source` (random-access byte view). Add `Storage.Open(ctx, key) (Source, error)` to the storage interface; LocalFS returns `*os.File` wrapped with `Size()`; S3 returns a per-Read range-fetcher. Drop the local-filesystem assumption baked into every format processor.

**Architecture:** New `storage.Source` interface = `io.ReaderAt + io.Closer + Size() int64`. `Storage.Open` returns it. The four format processors (EPUB, PDF, CBZ, Audio) accept `Source` instead of `path string` and use `archive/zip`'s `NewReader(r io.ReaderAt, size int64)` / equivalent. Hash computation stays separate (sequential `storage.Get`). Tests use a `bytes.Reader`-backed in-memory `Source` so format parsers run with zero filesystem I/O.

**Tech stack:** Go 1.25 stdlib. Reuses Plan F's `aws-sdk-go-v2/service/s3` for the S3 range-fetcher. No new third-party deps.

**Companion reference:** `docs/CONTEXT.md` (Source vocabulary), spec storage.spec.md §2.1.

**Locked decisions:**
- `Source = io.ReaderAt + io.Closer + Size() int64`. Lives in `internal/storage`.
- `Storage.Open(ctx, key) (Source, error)` is added to the interface — every backend must implement.
- S3 implementation: per-`ReadAt` issues `GetObject` with `Range` header. Size from one `HeadObject` at Open.
- Hash + extract stay separate code paths. Bookdrop ingest opens twice (once `Get` for sha256, once `Open` for extract).
- Hard cut on `Processor.Extract` — no transition shim. Two callers update.
- Comic page extraction (`internal/handler/comic.go`) stays on `zip.OpenReader(book.Path)` — out of scope; future refactor.
- Naming: `storage.Source`. Distinct from `handler.BookSource` (Plan G — delivery decision).

**Out of scope:**
- Comic page extraction (separate refactor).
- Multipart upload threshold tuning on S3 (Plan F2).
- Caching layer for repeated Reads on the same key.
- Multipart download chunking — small format header reads suffice.

---

## File Structure

### Files modified

| Path | Change |
|---|---|
| `internal/storage/storage.go` | Add `Source` interface + `Open(ctx, key) (Source, error)` method to `Storage`. |
| `internal/storage/local/local.go` | Implement `Open`: `os.Open` returns `*os.File` (already `io.ReaderAt + io.Closer`); wrap with a `localSource` struct that adds `Size()` from `Stat`. |
| `internal/storage/s3/s3.go` (or new `source.go`) | Implement `Open`: one `HeadObject` for size, return an `s3Source` whose `ReadAt` issues `GetObject` with `Range`. |
| `internal/storage/storagetest/suite.go` | Add `OpenReadsBytesAtRandomOffsets` contract test. |
| `internal/fileproc/processor.go` | Change `Processor.Extract(ctx, path string)` → `Extract(ctx, src storage.Source)`. |
| `internal/fileproc/epub.go` | Use `zip.NewReader(src, src.Size())` instead of `zip.OpenReader(path)`. |
| `internal/fileproc/cbz.go` | Same `zip.NewReader` switch. |
| `internal/fileproc/pdf.go` | Pass Source to whatever PDF parser is used (verify the lib accepts ReaderAt). |
| `internal/fileproc/audio.go` | Pass Source to the audio parser; many libs accept `io.ReadSeeker` — wrap a Source with `io.NewSectionReader(src, 0, src.Size())` if needed. |
| `internal/fileproc/cbz_test.go` | Switch tests to use `memSource` (new helper). |
| `internal/fileproc/source_test.go` (new) | `memSource` helper + small smoke. |
| `internal/task/bookdrop.go` | Replace `proc.Extract(ctx, item.Path)` with `proc.Extract(ctx, src)` after `store.Open`. |
| `internal/service/bookdrop.go` | Same change in the audio re-extraction block (line ~252). |

### Files NOT touched

- `internal/handler/comic.go` — comic-page reads still use `zip.OpenReader`. Out of scope.
- `internal/scan/*` — pure logic, no Source dependency.
- `internal/hashing/hasher.go` — sequential Get path, unchanged.
- `internal/storage/s3/methods.go` — methods stay; `Open` lives in the new file or s3.go.

---

## Tasks

### Task 1: `storage.Source` + `Storage.Open` on the interface

**Files:**
- Modify: `internal/storage/storage.go`

Add to `internal/storage/storage.go`:

```go
// Source is the random-access byte view of an object. Returned by
// Storage.Open. Distinct from Storage.Get (which returns a sequential
// io.ReadCloser) — Source is for callers that need to seek to read a
// container format's directory or footer.
//
// Size is the total object size in bytes. Implementations must return
// the same value for repeated calls.
type Source interface {
    io.ReaderAt
    io.Closer
    Size() int64
}
```

Add method to the `Storage` interface:

```go
// Open returns a random-access view of the object at key. Returns
// ErrNotFound when missing. Callers must Close the returned Source.
Open(ctx context.Context, key string) (Source, error)
```

Build will fail: `LocalFS` and S3 `Backend` don't satisfy the interface yet. Tasks 2-3 fix.

Commit: `feat(storage): Source interface + Storage.Open`

### Task 2: LocalFS `Open` + storagetest contract test

**Files:**
- Modify: `internal/storage/local/local.go`
- Modify: `internal/storage/local/local_test.go` (add `TestOpen_*`)
- Modify: `internal/storage/storagetest/suite.go` (add `OpenReadsBytesAtRandomOffsets`)

```go
// internal/storage/local/local.go

// localSource wraps *os.File with Size() so it satisfies storage.Source.
// *os.File already provides ReadAt + Close.
type localSource struct {
    *os.File
    size int64
}

func (s *localSource) Size() int64 { return s.size }

func (fs *LocalFS) Open(ctx context.Context, key string) (storage.Source, error) {
    abs, err := fs.resolve(key)
    if err != nil {
        return nil, err
    }
    f, err := os.Open(abs)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, errors.Join(storage.ErrNotFound, err)
        }
        return nil, err
    }
    st, err := f.Stat()
    if err != nil {
        _ = f.Close()
        return nil, err
    }
    return &localSource{File: f, size: st.Size()}, nil
}
```

LocalFS unit test:

```go
func TestOpen_RandomAccess(t *testing.T) {
    root := t.TempDir()
    fs, _ := New(root)
    ctx := context.Background()
    _, _ = fs.Put(ctx, "f", strings.NewReader("0123456789"))
    src, err := fs.Open(ctx, "f")
    if err != nil { t.Fatal(err) }
    defer func() { _ = src.Close() }()
    if src.Size() != 10 { t.Errorf("Size=%d, want 10", src.Size()) }
    buf := make([]byte, 4)
    n, err := src.ReadAt(buf, 3)
    if err != nil && err != io.EOF { t.Fatal(err) }
    if n != 4 || string(buf) != "3456" { t.Errorf("got %q, want %q", buf[:n], "3456") }
}

func TestOpen_MissingReturnsErrNotFound(t *testing.T) {
    fs, _ := New(t.TempDir())
    _, err := fs.Open(context.Background(), "nope")
    if !errors.Is(err, storage.ErrNotFound) { t.Fatalf("got %v, want ErrNotFound", err) }
}
```

Storagetest contract addition:

```go
// In internal/storage/storagetest/suite.go RunSuite:
t.Run("OpenReadsBytesAtRandomOffsets", func(t *testing.T) { testOpenRandomAccess(t, make) })

func testOpenRandomAccess(t *testing.T, mk MakeBackend) {
    s, cleanup := mk(t)
    defer cleanup()
    ctx := context.Background()
    _, _ = s.Put(ctx, "obj", bytes.NewReader([]byte("ABCDEFGHIJ")))
    src, err := s.Open(ctx, "obj")
    if err != nil { t.Fatal(err) }
    defer func() { _ = src.Close() }()
    if src.Size() != 10 { t.Errorf("size=%d, want 10", src.Size()) }
    buf := make([]byte, 3)
    n, err := src.ReadAt(buf, 5)
    if err != nil && err != io.EOF { t.Fatal(err) }
    if n != 3 || string(buf) != "FGH" { t.Errorf("got %q at offset 5", buf[:n]) }
}
```

Commit: `feat(storage/local): implement Open via *os.File + Size`

### Task 3: S3 `Open` — per-Read range fetcher

**Files:**
- Create: `internal/storage/s3/source.go`
- Modify: `internal/storage/s3/methods.go` (add `Open` method on `Backend`)

```go
// internal/storage/s3/source.go

// s3Source is a random-access view of an S3 object. Each ReadAt
// issues a GetObject with a Range header.
//
// This is appropriate for small reads (zip directory at EOF, OPF
// rootfile, PDF XREF table) where the alternative would be downloading
// the entire object. For full-file streaming use Backend.Get instead.
type s3Source struct {
    cli    *s3.Client
    bucket string
    key    string
    size   int64
    closed bool
}

func (s *s3Source) Size() int64 { return s.size }

func (s *s3Source) ReadAt(p []byte, off int64) (int, error) {
    if s.closed { return 0, errors.New("s3 source: closed") }
    if off >= s.size { return 0, io.EOF }
    end := off + int64(len(p)) - 1
    if end >= s.size { end = s.size - 1 }
    out, err := s.cli.GetObject(context.Background(), &s3.GetObjectInput{
        Bucket: &s.bucket,
        Key:    &s.key,
        Range:  aws.String(fmt.Sprintf("bytes=%d-%d", off, end)),
    })
    if err != nil { return 0, err }
    defer func() { _ = out.Body.Close() }()
    n, rerr := io.ReadFull(out.Body, p[:end-off+1])
    if rerr == io.ErrUnexpectedEOF { rerr = nil }
    if n < len(p) && rerr == nil { rerr = io.EOF }
    return n, rerr
}

func (s *s3Source) Close() error { s.closed = true; return nil }
```

```go
// internal/storage/s3/methods.go (or s3.go)

func (b *Backend) Open(ctx context.Context, key string) (storage.Source, error) {
    out, err := b.cli.HeadObject(ctx, &s3.HeadObjectInput{
        Bucket: &b.bucket,
        Key:    aws.String(b.keyFor(key)),
    })
    if err != nil {
        var nf *types.NotFound
        if errors.As(err, &nf) {
            return nil, errors.Join(storage.ErrNotFound, err)
        }
        return nil, err
    }
    return &s3Source{
        cli:    b.cli,
        bucket: b.bucket,
        key:    b.keyFor(key),
        size:   valueOr(out.ContentLength, 0),
    }, nil
}
```

Note: `ReadAt` uses `context.Background()` because `io.ReaderAt` doesn't propagate ctx. For metadata extraction this is acceptable — calls are short and cancellation matters less than the higher-level Open ctx. If cancellation becomes important, the s3Source can stash the ctx from Open.

S3 contract test runs automatically when `TEST_S3_ENDPOINT` is set (build tag `s3integration`).

Commit: `feat(storage/s3): implement Open via HeadObject + per-Read range`

### Task 4: `Processor.Extract` interface change + 4 impls

**Files:**
- Modify: `internal/fileproc/processor.go`
- Modify: `internal/fileproc/epub.go`, `cbz.go`, `pdf.go`, `audio.go`

Change interface:

```go
type Processor interface {
    Extract(ctx context.Context, src storage.Source) (Metadata, error)
}
```

EPUB and CBZ — switch from `zip.OpenReader(filePath)` to `zip.NewReader(src, src.Size())`:

```go
func (EPUBProcessor) Extract(ctx context.Context, src storage.Source) (Metadata, error) {
    zr, err := zip.NewReader(src, src.Size())
    if err != nil { return Metadata{}, fmt.Errorf("open epub: %w", err) }
    // … rest is unchanged; zr is *zip.Reader (NOT *zip.ReadCloser);
    // no Close call needed (Source is closed by caller).
    // … existing OPF parsing logic
}
```

PDF — verify the PDF lib used. If it's `ledongthuc/pdf`, it accepts `(*os.File)` only — investigate alternatives or use `os.Open` after copying via `io.Copy(tmpfile, io.NewSectionReader(src, 0, src.Size()))`. **First check the actual lib import** before committing to the Source signature for this processor.

Audio — `dhowden/tag` and `dmulholl/mp3lib` both accept `io.ReadSeeker`. Wrap with `io.NewSectionReader(src, 0, src.Size())` which is a `ReadSeeker`. M4B parser may need its own treatment.

If a parser refuses Source-shaped input, fall back to: read all bytes into memory (`io.ReadAll(io.NewSectionReader(...))`), wrap in `bytes.NewReader`. Acceptable for files <1 GB.

Commit: `refactor(fileproc): processors take storage.Source instead of path`

### Task 5: Test helpers + processor test refactor

**Files:**
- Create: `internal/fileproc/source_test.go`
- Modify: `internal/fileproc/cbz_test.go` (and any other `*_test.go` that calls `Extract`)

```go
// internal/fileproc/source_test.go
package fileproc

import (
    "bytes"
    "os"
    "testing"

    "github.com/blackforge/embookshelf/internal/storage"
)

// memSource wraps a byte slice as a storage.Source for tests.
type memSource struct {
    *bytes.Reader
    size int64
}

func (m *memSource) Size() int64  { return m.size }
func (m *memSource) Close() error { return nil }

func memSourceFromBytes(b []byte) storage.Source {
    return &memSource{Reader: bytes.NewReader(b), size: int64(len(b))}
}

func memSourceFromFile(t *testing.T, path string) storage.Source {
    t.Helper()
    b, err := os.ReadFile(path)
    if err != nil { t.Fatal(err) }
    return memSourceFromBytes(b)
}
```

Update existing tests:

```go
// internal/fileproc/cbz_test.go (and similar)
func TestCBZ_ExtractCover(t *testing.T) {
    src := memSourceFromFile(t, "testdata/sample.cbz")
    defer func() { _ = src.Close() }()
    meta, err := CBZProcessor{}.Extract(context.Background(), src)
    // … rest unchanged
}
```

Run: `go test ./internal/fileproc/...` — all green before commit.

Commit: `test(fileproc): memSource helper + storage-naive tests`

### Task 6: Caller updates

**Files:**
- Modify: `internal/task/bookdrop.go`
- Modify: `internal/service/bookdrop.go`
- Modify: `internal/queue/queue.go` and `internal/queue/sqlite.go` if `BookDropDeps` needs a Resolver field (check first — Plan F probably already added it).

`task/bookdrop.go:91` — current line:

```go
meta, err := proc.Extract(ctx, item.Path)
```

Replace with:

```go
// Open the staged file via the default storage backend (bookdrop
// items don't yet carry a backend id; default is fine for the
// pre-approval staging area).
store, err := deps.Resolver.Resolve("")
if err != nil {
    slog.Warn("bookdrop: resolve default backend", "err", err)
    _ = deps.Svc.Fail(ctx, itemID, err)
    return nil
}
key := strings.TrimPrefix(item.Path, "/")
src, err := store.Open(ctx, key)
if err != nil {
    slog.Warn("bookdrop: open source", "path", item.Path, "err", err)
    _ = deps.Svc.Fail(ctx, itemID, err)
    return nil
}
defer func() { _ = src.Close() }()

meta, err := proc.Extract(ctx, src)
```

`service/bookdrop.go` audio re-extract block (around line 252):

```go
// Before:
if meta, err := (fileproc.AudioProcessor{}).Extract(ctx, created.Path); err == nil {

// After:
store, rerr := s.resolver.Resolve(orZero(lib.BackendID))
if rerr != nil {
    slog.Warn("approve: resolve backend for audio re-extract", "err", rerr)
} else {
    src, oerr := store.Open(ctx, relativizeBookLocation(ctx, s.libs, libraryID, created.Path))
    if oerr == nil {
        defer func() { _ = src.Close() }()
        if meta, err := (fileproc.AudioProcessor{}).Extract(ctx, src); err == nil {
            // … rest unchanged
        }
    }
}
```

`s.resolver` field needs to be added to `BookDropService` if not present. The constructor takes it; main.go passes the same `storageResolver` already wired into the queue.

Commit: `refactor(bookdrop): open source via Resolver before Extract`

### Task 7: Verify

```bash
go build ./...
go test ./...
make ci-local
```

All green.

Commit message format for any final fixup commits: `fix(fileproc): …` or similar.

---

## Self-Review

**Spec coverage:**
- spec §2.1 storage interface → adds `Open` and `Source`; backends still gate on capabilities for everything else.
- §5.1 content hashing → unchanged; hash uses sequential Get.

**Risks:**
- The PDF library may not accept `io.ReaderAt` directly — Task 4 has a fallback (read-all + bytes.Reader). Validate during implementation.
- S3 ReadAt uses `context.Background()` — long-running metadata extraction would not honor caller ctx mid-Range-fetch. Acceptable for header reads (sub-second).
- Bookdrop ingest now requires the default backend to be set. Pre-Plan-F installs had it implicitly; Plan F's `LoadStorageBackends` returns a default fallback so this is safe.
- Comic-page extraction in `internal/handler/comic.go` keeps using path-based zip access. Documented as out-of-scope.

**Type consistency:** `storage.Source`, `Storage.Open`, `localSource`, `s3Source`, `memSource` consistent across files.
