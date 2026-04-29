# Storage Interface & Local Backend — Implementation Plan (Plan A of 8)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce a backend-agnostic `internal/storage` package with a single `Storage` interface, a local filesystem implementation rooted at any absolute path, and a contract test suite. Replace `filepath.WalkDir` in the library scan worker with `Storage.List`. **Zero behavior change** end-to-end — the seam is established but only the scan walker uses it. Files handler, coverstore, fileproc, and bookdrop reads stay on direct `os` calls and are migrated in later plans.

**Architecture:** The interface mirrors the lowest common denominator between local FS and S3 (list/head/get/put/delete/copy) plus capability flags for backend-specific extensions (presign, storage class, versioning, notify). All methods take a `key string` interpreted relative to the backend's configured root. The `LocalFS` impl wraps `os` and `filepath` calls; rooted at "/" today so callers passing absolute paths are unaffected. Capabilities are returned as a bitset; nothing in this plan depends on them. A contract test suite (`storagetest.RunSuite`) exercises any backend through the public interface — same suite will run against the S3 backend in Plan F.

**Tech Stack:** Go 1.25 stdlib only (`io`, `io/fs`, `os`, `path`, `time`, `context`). No new dependencies.

**Companion spec:** [`docs/spec/storage.spec.md`](../../spec/storage.spec.md). Sections 2 (storage backends), 2.1 (interface), and 9 (operational guarantees) drive the surface area.

**Locked decisions** (from breakdown discussion 2026-04-29):
- Content hash: SHA-256 (not BLAKE3) — no new dep. Lands in Plan B.
- `books.path` hard-drop in Plan B; this plan does not touch it.
- Multi-library-per-backend (1:N) modeled in Plan B.
- SQLite + S3 combo refused at config-load in Plan F.
- Sidecar filename: `.embookshelf.toml` (not `.grimmory.toml`).
- One PR per plan, merged before next plan starts.

**Out of scope for this plan:**
- S3 backend (Plan F).
- Range read API (`GetOption WithRange`) — interface accepts the option but `LocalFS` returns `ErrUnsupportedOption` for it; real Range support lands in Plan G with the handler migration.
- Sidecar atomicity protocol (Plan D).
- Conditional PUT (`WithIfMatch` / `WithIfNoneMatch`) — interface accepts options, `LocalFS` returns `ErrUnsupportedOption` (atomic-rename semantics aren't compatible with ETag-style preconditions; Plan D revisits this with a content-hash precondition).
- Capability-gated extensions (`presign_get`, `set_storage_class`, `notify_subscribe`) — capability flags reserved, no methods yet.
- Migrating `internal/handler/files.go`, `internal/coverstore`, `internal/fileproc`, or the bookdrop ingest read path. Those land in Plans B–G.
- Construction of multiple backends. `main.go` constructs exactly one `LocalFS` rooted at "/" and passes it everywhere a backend is needed.

---

## File Structure

### Files created

| Path | Responsibility |
|---|---|
| `internal/storage/storage.go` | `Storage` interface, `ObjectInfo`, `Capability` bitset, options (`GetOption`, `PutOption`, `DeleteOption`), `Iterator`, sentinel errors (`ErrNotFound`, `ErrPreconditionFailed`, `ErrUnsupportedOption`). |
| `internal/storage/options.go` | Option constructors (`WithRange`, `WithIfMatch`, `WithIfNoneMatch`, `WithContentType`, `WithVersionID`) and internal `getOpts`/`putOpts`/`deleteOpts` structs. |
| `internal/storage/local/local.go` | `LocalFS` struct, `New(root string) (*LocalFS, error)`, all interface methods. |
| `internal/storage/local/local_test.go` | LocalFS-specific unit tests (path traversal, missing root, mtime preservation on copy). |
| `internal/storage/storagetest/suite.go` | `RunSuite(t, makeBackend)` — interface contract suite reusable by every backend. |
| `internal/storage/storagetest/suite_test.go` | Smoke: `RunSuite` against a fresh `LocalFS`. |

### Files modified

| Path | Change |
|---|---|
| `internal/task/library_scan.go` | `LibraryScanDeps` gains `Storage storage.Storage`. Replace `filepath.WalkDir(root, ...)` (lines 59-101) with `storage.List(ctx, root)` iteration. Per-entry logic (`IsSupported`, `BookExistsByPath`, `Enqueue`, `EnqueueBookDrop`) unchanged. |
| `cmd/embookshelf/main.go` | After `db.Open` and before queue setup, construct `localStore, err := local.New("/")`. Pass `localStore` into `task.LibraryScanDeps` via the queue wiring. |
| `internal/queue/queue.go` | If `LibraryScanDeps` is constructed inside `queue.New`, add `Storage` to the params struct and forward it. (Verify during implementation — the worker may be wired in `main.go` directly; in that case this file is untouched.) |

### Files NOT touched

These are explicitly deferred to later plans even though they read/write disk today:

- `internal/handler/files.go` — keeps `c.File(absPath)` (preserves native Range support) until Plan G.
- `internal/coverstore/store.go` — local-only by design; key change to content-hash lands in Plan E.
- `internal/fileproc/*.go` — `Processor.Extract(ctx, path string)` keeps its path-based signature; the seam moves to a streaming source in Plan F when EPUB/PDF format processors gain a `ReaderAt` adapter.
- `internal/task/bookdrop.go` — reads via `proc.Extract(ctx, item.Path)`; unchanged.

---

## Phase 1 — Interface & Types

### Task 1: Define the `Storage` interface and core types

**Files:**
- Create: `internal/storage/storage.go`

- [ ] **Step 1: Write the package doc and core types**

Create `internal/storage/storage.go`:

```go
// Package storage defines a backend-agnostic interface for reading and
// writing book bytes and sidecar files. Implementations live in
// subpackages (local, s3). The DB layer never touches files directly —
// it calls this interface.
//
// Keys are slash-separated paths relative to the backend's configured
// root. The interface is intentionally minimal; capability-gated
// extensions (presigned URLs, storage class, change notifications) are
// declared via Capability bits and live on backend-specific types
// returned by type assertion.
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// ObjectInfo is the metadata returned by List and Head.
type ObjectInfo struct {
	// Key is the object's key relative to the backend root.
	Key string
	// Size is the object size in bytes. -1 means unknown.
	Size int64
	// ETag is an opaque change token. "" when the backend does not
	// expose one (e.g., LocalFS). Never use ETag as a content hash.
	ETag string
	// ModTime is the object's last-modified time.
	ModTime time.Time
	// ContentType is best-effort. "" when unknown. Backends may not
	// persist this faithfully.
	ContentType string
}

// Capability is a bitset of optional features a backend may advertise.
// Callers gate optional code paths with Storage.Capabilities() & Cap*.
type Capability uint32

const (
	// CapPresign indicates the backend can issue presigned URLs.
	CapPresign Capability = 1 << iota
	// CapStorageClass indicates objects can be tagged with a storage
	// class (S3 standard / IA / glacier).
	CapStorageClass
	// CapVersioning indicates the backend stores prior versions of
	// overwritten objects.
	CapVersioning
	// CapNotify indicates the backend can stream change events.
	CapNotify
	// CapConditional indicates the backend supports If-Match /
	// If-None-Match preconditions on Put.
	CapConditional
	// CapRange indicates the backend supports byte-range reads on Get.
	CapRange
)

// PutResult is returned by Storage.Put.
type PutResult struct {
	ETag      string
	VersionID string
}

// CopyResult is returned by Storage.Copy.
type CopyResult struct {
	ETag string
}

// Iterator yields objects from List. Callers must Close it.
type Iterator interface {
	// Next returns the next object. Returns io.EOF when the iteration
	// is exhausted. Returning a non-EOF error invalidates the iterator;
	// callers should still Close.
	Next(ctx context.Context) (ObjectInfo, error)
	// Close releases iterator resources. Safe to call multiple times.
	Close() error
}

// Storage is the backend-agnostic interface. All keys are relative to
// the backend's configured root and use forward slashes regardless of
// host OS.
type Storage interface {
	// Capabilities reports which optional features this backend supports.
	Capabilities() Capability

	// List walks the backend recursively under prefix. An empty prefix
	// lists from the root. Iteration order is unspecified.
	List(ctx context.Context, prefix string) (Iterator, error)

	// Head returns metadata for a single key. Returns ErrNotFound when
	// the key does not exist.
	Head(ctx context.Context, key string) (ObjectInfo, error)

	// Get returns a stream for the given key. The returned ReadCloser
	// must be Closed by the caller. Returns ErrNotFound when missing.
	Get(ctx context.Context, key string, opts ...GetOption) (io.ReadCloser, error)

	// Put writes r to key. The reader is consumed in full (no length
	// hint required). Conditional options (WithIfMatch / WithIfNoneMatch)
	// return ErrPreconditionFailed when the precondition is not met,
	// or ErrUnsupportedOption when the backend lacks CapConditional.
	Put(ctx context.Context, key string, r io.Reader, opts ...PutOption) (PutResult, error)

	// Delete removes a key. Removing a missing key is not an error.
	Delete(ctx context.Context, key string, opts ...DeleteOption) error

	// Copy duplicates srcKey to dstKey. On LocalFS this is rename(2)
	// when src and dst share a filesystem, falling back to copy + unlink.
	// On S3 it is a server-side copy.
	Copy(ctx context.Context, srcKey, dstKey string) (CopyResult, error)
}

// Sentinel errors. Backends wrap their underlying error with
// errors.Join(ErrXxx, original) so callers can use errors.Is.
var (
	ErrNotFound           = errors.New("storage: not found")
	ErrPreconditionFailed = errors.New("storage: precondition failed")
	ErrUnsupportedOption  = errors.New("storage: unsupported option for this backend")
	ErrInvalidKey         = errors.New("storage: invalid key")
)
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/storage/`

Expected: no output, exit 0. (The interface is unimplemented; nothing to test yet.)

- [ ] **Step 3: Commit**

```bash
git checkout -b feat/storage-interface
git add internal/storage/storage.go
git commit -m "feat(storage): define backend-agnostic Storage interface

Adds the Storage interface, ObjectInfo, Capability bitset, sentinel
errors, and PutResult/CopyResult types. No implementation yet.

See docs/spec/storage.spec.md §2.1 for the interface design."
```

---

### Task 2: Define option types

**Files:**
- Create: `internal/storage/options.go`

- [ ] **Step 1: Write the option types**

The `*Opts` structs are **exported** so backend implementations in subpackages (`local`, future `s3`) can read the values. The `Apply*` collectors are also exported. This is intentional: option types and their collectors are part of the package's public API.

Create `internal/storage/options.go`:

```go
package storage

// GetOpts holds the resolved values of all GetOption values applied
// to a Get call. Exported so backend packages can inspect requested
// behavior. Field types are stable.
type GetOpts struct {
	RangeSet    bool
	RangeOffset int64
	RangeLength int64 // -1 means "until EOF"
}

// PutOpts holds the resolved values of all PutOption values applied.
type PutOpts struct {
	IfMatch        string
	IfMatchSet     bool
	IfNoneMatch    string
	IfNoneMatchSet bool
	ContentType    string
}

// DeleteOpts holds the resolved values of all DeleteOption values applied.
type DeleteOpts struct {
	VersionID string
}

// GetOption configures a Get call.
type GetOption func(*GetOpts)

// PutOption configures a Put call.
type PutOption func(*PutOpts)

// DeleteOption configures a Delete call.
type DeleteOption func(*DeleteOpts)

// WithRange limits Get to the byte range [offset, offset+length). A
// length of -1 reads from offset to EOF. Backends without CapRange
// return ErrUnsupportedOption.
func WithRange(offset, length int64) GetOption {
	return func(o *GetOpts) {
		o.RangeSet = true
		o.RangeOffset = offset
		o.RangeLength = length
	}
}

// WithIfMatch makes Put conditional on the object's current ETag.
// Returns ErrPreconditionFailed if the ETag does not match.
func WithIfMatch(etag string) PutOption {
	return func(o *PutOpts) {
		o.IfMatch = etag
		o.IfMatchSet = true
	}
}

// WithIfNoneMatch makes Put conditional on the object NOT existing
// when value is "*", or on its ETag NOT matching otherwise.
func WithIfNoneMatch(etag string) PutOption {
	return func(o *PutOpts) {
		o.IfNoneMatch = etag
		o.IfNoneMatchSet = true
	}
}

// WithContentType sets the object's Content-Type. LocalFS ignores it
// (no xattr storage); S3 persists it.
func WithContentType(ct string) PutOption {
	return func(o *PutOpts) {
		o.ContentType = ct
	}
}

// WithVersionID targets a specific historical version on Delete.
// Backends without CapVersioning return ErrUnsupportedOption.
func WithVersionID(id string) DeleteOption {
	return func(o *DeleteOpts) {
		o.VersionID = id
	}
}

// ApplyGet collects opts into a GetOpts. Backend Get implementations
// call this to read what the caller requested.
func ApplyGet(opts []GetOption) GetOpts {
	o := GetOpts{RangeLength: -1}
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// ApplyPut collects opts into a PutOpts.
func ApplyPut(opts []PutOption) PutOpts {
	var o PutOpts
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// ApplyDelete collects opts into a DeleteOpts.
func ApplyDelete(opts []DeleteOption) DeleteOpts {
	var o DeleteOpts
	for _, fn := range opts {
		fn(&o)
	}
	return o
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/storage/`

Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/storage/options.go
git commit -m "feat(storage): add Get/Put/Delete options

Functional options for byte ranges, conditional writes, content type,
and version targeting. Internal apply* helpers collect them into
typed structs that backends consume."
```

---

## Phase 2 — Local Filesystem Backend

### Task 3: Skeleton `LocalFS` with `New` and key validation

**Files:**
- Create: `internal/storage/local/local.go`
- Create: `internal/storage/local/local_test.go`

- [ ] **Step 1: Write the failing test for `New`**

Create `internal/storage/local/local_test.go`:

```go
package local

import (
	"path/filepath"
	"testing"
)

func TestNew_RejectsRelativeRoot(t *testing.T) {
	_, err := New("relative/path")
	if err == nil {
		t.Fatal("expected error for relative root, got nil")
	}
}

func TestNew_AcceptsAbsoluteRoot(t *testing.T) {
	root := t.TempDir()
	if !filepath.IsAbs(root) {
		t.Fatalf("t.TempDir returned non-absolute path: %q", root)
	}
	fs, err := New(root)
	if err != nil {
		t.Fatalf("New(%q): %v", root, err)
	}
	if fs == nil {
		t.Fatal("New returned nil")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/storage/local/ -run TestNew -v`

Expected: FAIL with "undefined: New" (compile error).

- [ ] **Step 3: Implement the skeleton**

Create `internal/storage/local/local.go`:

```go
// Package local implements storage.Storage against a local filesystem
// rooted at a configurable absolute path. Keys are interpreted as
// slash-separated paths relative to the root; the implementation
// translates them to filesystem paths via filepath.FromSlash and
// guards against parent traversal.
package local

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/blackforge/embookshelf/internal/storage"
)

// LocalFS is a Storage backed by an OS filesystem.
type LocalFS struct {
	root string
}

// New returns a LocalFS rooted at root. root must be absolute.
func New(root string) (*LocalFS, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("local: root must be absolute, got %q", root)
	}
	return &LocalFS{root: filepath.Clean(root)}, nil
}

// Capabilities reports the features LocalFS supports. None of the
// optional capabilities are implemented in Plan A.
func (fs *LocalFS) Capabilities() storage.Capability { return 0 }

// resolve translates a slash-separated key into an absolute filesystem
// path under fs.root, rejecting anything that would escape the root.
func (fs *LocalFS) resolve(key string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(key))
	abs := filepath.Join(fs.root, clean)
	rel, err := filepath.Rel(fs.root, abs)
	if err != nil || rel == ".." || (len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator)) {
		return "", storage.ErrInvalidKey
	}
	return abs, nil
}

// Stub implementations follow in subsequent tasks.
func (fs *LocalFS) List(ctx context.Context, prefix string) (storage.Iterator, error) {
	return nil, fmt.Errorf("not implemented")
}
func (fs *LocalFS) Head(ctx context.Context, key string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, fmt.Errorf("not implemented")
}
func (fs *LocalFS) Get(ctx context.Context, key string, opts ...storage.GetOption) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not implemented")
}
func (fs *LocalFS) Put(ctx context.Context, key string, r io.Reader, opts ...storage.PutOption) (storage.PutResult, error) {
	return storage.PutResult{}, fmt.Errorf("not implemented")
}
func (fs *LocalFS) Delete(ctx context.Context, key string, opts ...storage.DeleteOption) error {
	return fmt.Errorf("not implemented")
}
func (fs *LocalFS) Copy(ctx context.Context, srcKey, dstKey string) (storage.CopyResult, error) {
	return storage.CopyResult{}, fmt.Errorf("not implemented")
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/storage/local/ -run TestNew -v`

Expected: PASS for both `TestNew_RejectsRelativeRoot` and `TestNew_AcceptsAbsoluteRoot`.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/local/local.go internal/storage/local/local_test.go
git commit -m "feat(storage/local): add LocalFS skeleton

New(root) requires an absolute path. resolve() translates slash-keys
to filesystem paths and rejects parent-traversal. All interface
methods are stubbed and return 'not implemented'; real impls follow."
```

---

### Task 4: Implement `Put` (write-tmp-then-rename)

**Files:**
- Modify: `internal/storage/local/local.go`
- Modify: `internal/storage/local/local_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/storage/local/local_test.go`:

```go
import (
	"bytes"
	"context"
	"os"
	"strings"
)

func TestPut_WritesBytesAtomically(t *testing.T) {
	root := t.TempDir()
	fs, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	res, err := fs.Put(ctx, "a/b/file.txt", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "a", "b", "file.txt"))
	if err != nil {
		t.Fatalf("read after Put: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("file contents = %q, want %q", got, "hello")
	}
	if res.ETag != "" {
		t.Logf("ETag returned = %q (informational; LocalFS may leave empty)", res.ETag)
	}
}

func TestPut_NoTempFilesLeftOnSuccess(t *testing.T) {
	root := t.TempDir()
	fs, _ := New(root)
	if _, err := fs.Put(context.Background(), "x.txt", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/storage/local/ -run TestPut -v`

Expected: FAIL with "not implemented".

- [ ] **Step 3: Implement `Put`**

Replace the stub `Put` in `internal/storage/local/local.go`:

```go
// Put writes r to key atomically using write-temp-then-rename.
// LocalFS does not support conditional writes (CapConditional is off);
// passing WithIfMatch or WithIfNoneMatch returns ErrUnsupportedOption.
func (fs *LocalFS) Put(ctx context.Context, key string, r io.Reader, opts ...storage.PutOption) (storage.PutResult, error) {
	o := storage.ApplyPut(opts)
	if o.IfMatchSet || o.IfNoneMatchSet {
		return storage.PutResult{}, storage.ErrUnsupportedOption
	}
	abs, err := fs.resolve(key)
	if err != nil {
		return storage.PutResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return storage.PutResult{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), filepath.Base(abs)+".*.tmp")
	if err != nil {
		return storage.PutResult{}, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		return storage.PutResult{}, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return storage.PutResult{}, err
	}
	if err := tmp.Close(); err != nil {
		return storage.PutResult{}, err
	}
	if err := os.Rename(tmpName, abs); err != nil {
		return storage.PutResult{}, err
	}
	return storage.PutResult{}, nil
}
```

Add `"os"` and `"io"` to the imports in `local.go` if not already present.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/storage/local/ -v`

Expected: PASS for `TestPut_*`.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/local/local.go internal/storage/local/local_test.go
git commit -m "feat(storage/local): implement Put with atomic write-tmp-rename

LocalFS rejects conditional writes (WithIfMatch/WithIfNoneMatch) with
ErrUnsupportedOption — atomic-rename semantics aren't compatible with
ETag preconditions; Plan D revisits with a content-hash precondition."
```

---

### Task 5: Implement `Get`, `Head`, `Delete`, `Copy`

**Files:**
- Modify: `internal/storage/local/local.go`
- Modify: `internal/storage/local/local_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/storage/local/local_test.go`:

```go
func TestGet_ReturnsBytes(t *testing.T) {
	root := t.TempDir()
	fs, _ := New(root)
	ctx := context.Background()
	if _, err := fs.Put(ctx, "f", strings.NewReader("contents")); err != nil {
		t.Fatal(err)
	}
	rc, err := fs.Get(ctx, "f")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "contents" {
		t.Fatalf("got %q, want %q", got, "contents")
	}
}

func TestGet_MissingReturnsErrNotFound(t *testing.T) {
	fs, _ := New(t.TempDir())
	_, err := fs.Get(context.Background(), "nope")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("got %v, want storage.ErrNotFound", err)
	}
}

func TestGet_RangeReturnsErrUnsupported(t *testing.T) {
	root := t.TempDir()
	fs, _ := New(root)
	ctx := context.Background()
	_, _ = fs.Put(ctx, "f", strings.NewReader("hello"))
	_, err := fs.Get(ctx, "f", storage.WithRange(0, 3))
	if !errors.Is(err, storage.ErrUnsupportedOption) {
		t.Fatalf("got %v, want ErrUnsupportedOption", err)
	}
}

func TestHead_ReturnsSizeAndMtime(t *testing.T) {
	root := t.TempDir()
	fs, _ := New(root)
	ctx := context.Background()
	_, _ = fs.Put(ctx, "f", strings.NewReader("hi"))
	info, err := fs.Head(ctx, "f")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != 2 {
		t.Errorf("Size = %d, want 2", info.Size)
	}
	if info.ModTime.IsZero() {
		t.Error("ModTime is zero")
	}
	if info.Key != "f" {
		t.Errorf("Key = %q, want %q", info.Key, "f")
	}
}

func TestDelete_RemovesFile(t *testing.T) {
	root := t.TempDir()
	fs, _ := New(root)
	ctx := context.Background()
	_, _ = fs.Put(ctx, "f", strings.NewReader("x"))
	if err := fs.Delete(ctx, "f"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Head(ctx, "f"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("after Delete, Head returned %v, want ErrNotFound", err)
	}
}

func TestDelete_MissingIsNoError(t *testing.T) {
	fs, _ := New(t.TempDir())
	if err := fs.Delete(context.Background(), "nope"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestCopy_DuplicatesContent(t *testing.T) {
	root := t.TempDir()
	fs, _ := New(root)
	ctx := context.Background()
	_, _ = fs.Put(ctx, "src", strings.NewReader("payload"))
	if _, err := fs.Copy(ctx, "src", "dst/sub/copy"); err != nil {
		t.Fatal(err)
	}
	rc, err := fs.Get(ctx, "dst/sub/copy")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "payload" {
		t.Fatalf("got %q, want %q", got, "payload")
	}
}
```

Add `"errors"`, `"io"`, `"github.com/blackforge/embookshelf/internal/storage"` to the test file's imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/storage/local/ -v`

Expected: FAIL on the new tests with "not implemented".

- [ ] **Step 3: Implement `Get`, `Head`, `Delete`, `Copy`**

Replace the four stubs in `internal/storage/local/local.go`:

```go
func (fs *LocalFS) Get(ctx context.Context, key string, opts ...storage.GetOption) (io.ReadCloser, error) {
	o := storage.ApplyGet(opts)
	if o.RangeSet {
		return nil, storage.ErrUnsupportedOption
	}
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
	return f, nil
}

func (fs *LocalFS) Head(ctx context.Context, key string) (storage.ObjectInfo, error) {
	abs, err := fs.resolve(key)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return storage.ObjectInfo{}, errors.Join(storage.ErrNotFound, err)
		}
		return storage.ObjectInfo{}, err
	}
	return storage.ObjectInfo{
		Key:     key,
		Size:    st.Size(),
		ModTime: st.ModTime(),
	}, nil
}

func (fs *LocalFS) Delete(ctx context.Context, key string, opts ...storage.DeleteOption) error {
	abs, err := fs.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (fs *LocalFS) Copy(ctx context.Context, srcKey, dstKey string) (storage.CopyResult, error) {
	srcAbs, err := fs.resolve(srcKey)
	if err != nil {
		return storage.CopyResult{}, err
	}
	dstAbs, err := fs.resolve(dstKey)
	if err != nil {
		return storage.CopyResult{}, err
	}
	src, err := os.Open(srcAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return storage.CopyResult{}, errors.Join(storage.ErrNotFound, err)
		}
		return storage.CopyResult{}, err
	}
	defer src.Close()
	if err := os.MkdirAll(filepath.Dir(dstAbs), 0o755); err != nil {
		return storage.CopyResult{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dstAbs), filepath.Base(dstAbs)+".*.tmp")
	if err != nil {
		return storage.CopyResult{}, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		return storage.CopyResult{}, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return storage.CopyResult{}, err
	}
	if err := tmp.Close(); err != nil {
		return storage.CopyResult{}, err
	}
	if err := os.Rename(tmpName, dstAbs); err != nil {
		return storage.CopyResult{}, err
	}
	return storage.CopyResult{}, nil
}
```

Add `"errors"` to the imports in `local.go`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/storage/local/ -v`

Expected: PASS for all tests written so far.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/local/local.go internal/storage/local/local_test.go
git commit -m "feat(storage/local): implement Get/Head/Delete/Copy

ErrNotFound is returned via errors.Join so callers can use errors.Is.
Range reads return ErrUnsupportedOption — Plan G adds Range support
when migrating the files handler. Copy uses tmp+rename for atomicity."
```

---

### Task 6: Implement `List` (recursive walk)

**Files:**
- Modify: `internal/storage/local/local.go`
- Modify: `internal/storage/local/local_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/storage/local/local_test.go`:

```go
import "io/fs"

func TestList_WalksRecursivelyAndYieldsRelativeKeys(t *testing.T) {
	root := t.TempDir()
	fsys, _ := New(root)
	ctx := context.Background()
	for _, k := range []string{"a.txt", "sub/b.txt", "sub/deep/c.txt"} {
		if _, err := fsys.Put(ctx, k, strings.NewReader(k)); err != nil {
			t.Fatal(err)
		}
	}
	it, err := fsys.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	got := map[string]bool{}
	for {
		o, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got[o.Key] = true
	}
	for _, want := range []string{"a.txt", "sub/b.txt", "sub/deep/c.txt"} {
		if !got[want] {
			t.Errorf("missing key %q", want)
		}
	}
}

func TestList_PrefixFilter(t *testing.T) {
	root := t.TempDir()
	fsys, _ := New(root)
	ctx := context.Background()
	for _, k := range []string{"a/x", "a/y", "b/z"} {
		_, _ = fsys.Put(ctx, k, strings.NewReader(""))
	}
	it, err := fsys.List(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	count := 0
	for {
		_, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
	}
	if count != 2 {
		t.Errorf("got %d entries, want 2", count)
	}
}

func TestList_MissingPrefixReturnsEmpty(t *testing.T) {
	fsys, _ := New(t.TempDir())
	it, err := fsys.List(context.Background(), "missing")
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	_, err = it.Next(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("got %v, want io.EOF", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/storage/local/ -v -run TestList`

Expected: FAIL with "not implemented".

- [ ] **Step 3: Implement `List` and the iterator**

Replace the stub `List` and add an iterator type in `internal/storage/local/local.go`:

```go
func (fs *LocalFS) List(ctx context.Context, prefix string) (storage.Iterator, error) {
	prefixAbs, err := fs.resolve(prefix)
	if err != nil {
		return nil, err
	}
	st, statErr := os.Stat(prefixAbs)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return &localIter{done: true}, nil
		}
		return nil, statErr
	}
	if !st.IsDir() {
		// Listing a single file yields just that one entry.
		return &localIter{
			fs:      fs,
			pending: []string{prefixAbs},
		}, nil
	}
	return &localIter{
		fs:      fs,
		pending: []string{prefixAbs},
		isDir:   map[string]bool{prefixAbs: true},
	}, nil
}

// localIter is a depth-first iterator over a directory tree. It reads
// each directory eagerly via os.ReadDir and pushes children onto a
// stack; for very large trees this is O(depth) memory rather than
// O(total entries), at the cost of one ReadDir call per directory.
type localIter struct {
	fs      *LocalFS
	pending []string
	isDir   map[string]bool
	done    bool
	closed  bool
}

func (it *localIter) Next(ctx context.Context) (storage.ObjectInfo, error) {
	if it.closed {
		return storage.ObjectInfo{}, fmt.Errorf("iterator closed")
	}
	for !it.done {
		if err := ctx.Err(); err != nil {
			return storage.ObjectInfo{}, err
		}
		if len(it.pending) == 0 {
			it.done = true
			return storage.ObjectInfo{}, io.EOF
		}
		// Pop.
		n := len(it.pending) - 1
		next := it.pending[n]
		it.pending = it.pending[:n]

		st, err := os.Stat(next)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return storage.ObjectInfo{}, err
		}
		if st.IsDir() {
			entries, err := os.ReadDir(next)
			if err != nil {
				return storage.ObjectInfo{}, err
			}
			for _, e := range entries {
				it.pending = append(it.pending, filepath.Join(next, e.Name()))
			}
			continue
		}
		rel, err := filepath.Rel(it.fs.root, next)
		if err != nil {
			return storage.ObjectInfo{}, err
		}
		return storage.ObjectInfo{
			Key:     filepath.ToSlash(rel),
			Size:    st.Size(),
			ModTime: st.ModTime(),
		}, nil
	}
	return storage.ObjectInfo{}, io.EOF
}

func (it *localIter) Close() error {
	it.closed = true
	it.pending = nil
	return nil
}
```

Add `"io"` to the `local.go` imports.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/storage/local/ -v`

Expected: PASS for all tests including the three `TestList_*` cases.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/local/local.go internal/storage/local/local_test.go
git commit -m "feat(storage/local): implement recursive List with iterator

Depth-first walker yields slash-keyed ObjectInfo entries. Missing
prefixes return an empty iterator (not an error) — matches S3
ListObjectsV2 semantics. Iterator respects ctx.Done()."
```

---

## Phase 3 — Contract Test Suite

### Task 7: Define `storagetest.RunSuite`

**Files:**
- Create: `internal/storage/storagetest/suite.go`
- Create: `internal/storage/storagetest/suite_test.go`

- [ ] **Step 1: Write the contract suite**

Create `internal/storage/storagetest/suite.go`:

```go
// Package storagetest provides a contract test suite that any
// storage.Storage implementation must pass. Backends call RunSuite
// from their own test packages with a factory that returns a fresh
// backend rooted at a clean state.
package storagetest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/storage"
)

// MakeBackend returns a fresh, empty Storage. The cleanup func is
// called by the suite when each subtest finishes.
type MakeBackend func(t *testing.T) (storage.Storage, func())

// RunSuite runs every contract test against backend factories. Backend
// authors call:
//
//	storagetest.RunSuite(t, func(t *testing.T) (storage.Storage, func()) {
//	    fs, _ := local.New(t.TempDir())
//	    return fs, func() {}
//	})
func RunSuite(t *testing.T, make MakeBackend) {
	t.Helper()
	t.Run("PutThenGet", func(t *testing.T) { testPutThenGet(t, make) })
	t.Run("HeadReturnsSize", func(t *testing.T) { testHeadReturnsSize(t, make) })
	t.Run("GetMissingNotFound", func(t *testing.T) { testGetMissingNotFound(t, make) })
	t.Run("DeleteRemovesObject", func(t *testing.T) { testDeleteRemovesObject(t, make) })
	t.Run("DeleteMissingIsNoError", func(t *testing.T) { testDeleteMissingIsNoError(t, make) })
	t.Run("CopyDuplicates", func(t *testing.T) { testCopyDuplicates(t, make) })
	t.Run("ListYieldsAllKeys", func(t *testing.T) { testListYieldsAllKeys(t, make) })
	t.Run("ListPrefixFilters", func(t *testing.T) { testListPrefixFilters(t, make) })
	t.Run("ListEmptyOnMissingPrefix", func(t *testing.T) { testListEmptyOnMissingPrefix(t, make) })
	t.Run("CapabilitiesIsStable", func(t *testing.T) { testCapabilitiesIsStable(t, make) })
}

func testPutThenGet(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := s.Put(ctx, "k", strings.NewReader("v")); err != nil {
		t.Fatal(err)
	}
	rc, err := s.Get(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "v" {
		t.Fatalf("got %q, want %q", got, "v")
	}
}

func testHeadReturnsSize(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	ctx := context.Background()
	_, _ = s.Put(ctx, "k", bytes.NewReader([]byte("hello")))
	info, err := s.Head(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != 5 {
		t.Errorf("Size = %d, want 5", info.Size)
	}
}

func testGetMissingNotFound(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	_, err := s.Get(context.Background(), "missing")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func testDeleteRemovesObject(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	ctx := context.Background()
	_, _ = s.Put(ctx, "k", strings.NewReader("x"))
	if err := s.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Head(ctx, "k"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("after Delete, Head = %v, want ErrNotFound", err)
	}
}

func testDeleteMissingIsNoError(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	if err := s.Delete(context.Background(), "missing"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

func testCopyDuplicates(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	ctx := context.Background()
	_, _ = s.Put(ctx, "src", strings.NewReader("data"))
	if _, err := s.Copy(ctx, "src", "dst"); err != nil {
		t.Fatal(err)
	}
	rc, err := s.Get(ctx, "dst")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "data" {
		t.Fatalf("got %q, want %q", got, "data")
	}
}

func testListYieldsAllKeys(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	ctx := context.Background()
	for _, k := range []string{"a", "b/c", "b/d/e"} {
		_, _ = s.Put(ctx, k, strings.NewReader(""))
	}
	it, err := s.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	seen := map[string]bool{}
	for {
		o, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		seen[o.Key] = true
	}
	for _, k := range []string{"a", "b/c", "b/d/e"} {
		if !seen[k] {
			t.Errorf("missing %q in list", k)
		}
	}
}

func testListPrefixFilters(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	ctx := context.Background()
	_, _ = s.Put(ctx, "a/1", strings.NewReader(""))
	_, _ = s.Put(ctx, "a/2", strings.NewReader(""))
	_, _ = s.Put(ctx, "b/3", strings.NewReader(""))
	it, _ := s.List(ctx, "a")
	defer it.Close()
	count := 0
	for {
		_, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
	}
	if count != 2 {
		t.Errorf("got %d, want 2", count)
	}
}

func testListEmptyOnMissingPrefix(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	it, err := s.List(context.Background(), "nope")
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	if _, err := it.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("got %v, want io.EOF", err)
	}
}

func testCapabilitiesIsStable(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	c1 := s.Capabilities()
	c2 := s.Capabilities()
	if c1 != c2 {
		t.Fatalf("Capabilities() unstable: %v vs %v", c1, c2)
	}
}
```

- [ ] **Step 2: Wire LocalFS into the suite**

Create `internal/storage/storagetest/suite_test.go`:

```go
package storagetest_test

import (
	"testing"

	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
	"github.com/blackforge/embookshelf/internal/storage/storagetest"
)

func TestLocalFS_Contract(t *testing.T) {
	storagetest.RunSuite(t, func(t *testing.T) (storage.Storage, func()) {
		fs, err := local.New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return fs, func() {}
	})
}
```

- [ ] **Step 3: Run the suite**

Run: `go test ./internal/storage/storagetest/ -v`

Expected: PASS for every `TestLocalFS_Contract/*` subtest.

- [ ] **Step 4: Commit**

```bash
git add internal/storage/storagetest/suite.go internal/storage/storagetest/suite_test.go
git commit -m "test(storage): add backend contract suite

storagetest.RunSuite exercises the Storage interface end-to-end via
public methods. The S3 backend in Plan F will reuse this same suite
against a minio fixture."
```

---

## Phase 4 — Wire Into Library Scan

### Task 8: Plumb `Storage` into `LibraryScanDeps`

**Files:**
- Modify: `internal/task/library_scan.go`

- [ ] **Step 1: Modify `LibraryScanDeps`**

Edit `internal/task/library_scan.go`:

Replace the existing `LibraryScanDeps` struct (lines 35-39) with:

```go
type LibraryScanDeps struct {
	BookDrop *service.BookDropService
	Lib      *service.LibraryService
	Queue    BookDropEnqueuer
	// Storage reads the library's filesystem (or object store) during
	// the walk. Plan A only uses List; future plans use Get/Head for
	// content-hash computation and metadata extraction.
	Storage storage.Storage
}
```

Add the import at the top:

```go
"github.com/blackforge/embookshelf/internal/storage"
```

- [ ] **Step 2: Replace `filepath.WalkDir` with `Storage.List`**

Replace lines 58-101 (`var fileCount, discovered int` through the closing brace of `filepath.WalkDir`) with:

```go
	var fileCount, discovered int
	it, err := deps.Storage.List(ctx, root)
	if err != nil {
		slog.Warn("library scan: list failed", "path", root, "err", err)
		// Persist the touch so the UI doesn't show a stale "scanning…"
		_ = deps.Lib.TouchScan(ctx, lib.ID, 0, 0)
		return nil
	}
	defer it.Close()

	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			slog.Warn("library scan: iteration error", "path", root, "err", err)
			break
		}
		// LocalFS keys are slash-paths under the backend root; the
		// backend is rooted at "/" today (Plan A) so obj.Key starts
		// with the absolute library path. Plan B reroots backends
		// per-library and obj.Key becomes library-relative.
		p := "/" + obj.Key
		if !fileproc.IsSupported(p) {
			continue
		}
		fileCount++

		already, err := deps.Lib.BookExistsByPath(ctx, p)
		if err != nil {
			slog.Warn("library scan: book exists check", "path", p, "err", err)
			continue
		}
		if already {
			continue
		}

		format := fileproc.FormatForExt(filepath.Ext(p))

		item, created, err := deps.BookDrop.Enqueue(ctx, p, format, obj.Size)
		if err != nil {
			slog.Warn("library scan: enqueue", "path", p, "err", err)
			continue
		}
		if !created {
			continue
		}
		if deps.Queue != nil {
			if err := deps.Queue.EnqueueBookDrop(ctx, item.ID); err != nil {
				slog.Warn("library scan: enqueue queue job", "id", item.ID, "err", err)
			}
		}
		discovered++
	}
```

Add `"errors"` and `"io"` to the imports if not already present. Remove `"io/fs"` (no longer used).

- [ ] **Step 3: Update wiring in `cmd/embookshelf/main.go`**

In `cmd/embookshelf/main.go`, find where `task.LibraryScanDeps` is constructed (search for `LibraryScan` — it's likely passed via `queue.New(...)` or similar). Add a `local.New("/")` step near the top, right after `db.Open`:

```go
import (
	// ...
	"github.com/blackforge/embookshelf/internal/storage/local"
)

// After dbh, before the queue wiring:
fileStorage, err := local.New("/")
if err != nil {
	slog.Error("storage init", "err", err)
	os.Exit(1)
}
```

Then forward `fileStorage` into the place that constructs `LibraryScanDeps`. Inspect `internal/queue/queue.go` to see where `LibraryScanWorker` is registered; mirror the pattern for `Storage`.

- [ ] **Step 4: Build and run existing tests**

Run: `go build ./... && go test ./...`

Expected: build succeeds; all existing tests pass. The library scan does not have a unit test today (verified in baseline survey), so this is a smoke check.

- [ ] **Step 5: Manual smoke**

```bash
make db-up
make seed
make up &
# In another terminal:
curl http://localhost:6060/api/libraries
# Trigger a scan via the UI or:
curl -X POST http://localhost:6060/api/libraries/$LIB_ID/scan
```

Expected: scan completes; books appear in the bookdrop queue exactly as before.

- [ ] **Step 6: Commit**

```bash
git add internal/task/library_scan.go cmd/embookshelf/main.go internal/queue/queue.go
git commit -m "refactor(task): library_scan walks via storage.Storage

LibraryScanDeps gains a Storage field. The walker uses storage.List
instead of filepath.WalkDir. main.go constructs a LocalFS rooted at
'/' so absolute library paths still resolve correctly.

This is the seam for Plan B's per-library backend wiring; behavior
is unchanged in Plan A."
```

---

## Phase 5 — Self-Review and Open PR

### Task 9: Final verification

- [ ] **Step 1: Run the full check matrix**

```bash
make ci-local
```

Expected: all four lanes pass (`go-lint`, `ui-lint`, `ui-typecheck`, `test`).

- [ ] **Step 2: Verify capability flags compile out cleanly**

Run: `go vet ./internal/storage/...`

Expected: no warnings. The `Capability` constants `CapPresign` etc. are unused in this plan but exported for Plans F–H; `go vet` should not flag them since they're exported identifiers.

- [ ] **Step 3: Inspect the diff for over-reach**

```bash
git diff main..HEAD --stat
```

Expected: changes confined to `internal/storage/`, `internal/task/library_scan.go`, `cmd/embookshelf/main.go`, and possibly `internal/queue/queue.go`. No changes to `internal/handler/`, `internal/coverstore/`, `internal/fileproc/`, `internal/repo/`, or the migrator.

- [ ] **Step 4: Open the PR**

```bash
git push -u origin feat/storage-interface
gh pr create --base main --title "feat(storage): backend-agnostic Storage interface (Plan A of 8)" \
  --body "$(cat <<'EOF'
## Summary
- Adds `internal/storage` package with the `Storage` interface, `Capability` bitset, sentinel errors, and option types.
- Adds `internal/storage/local` with `LocalFS` rooted at any absolute path. Atomic `Put` via tmp+fsync+rename. `Copy` likewise. `List` is a depth-first walk.
- Adds `internal/storage/storagetest` contract suite. The S3 backend in Plan F reuses it.
- Migrates the library scan walker from `filepath.WalkDir` to `Storage.List`. No behavior change — backend is rooted at `/` so absolute library paths still resolve.

## What this does NOT change
- Files handler still uses `c.File(absPath)` (preserves Range support; migrated in Plan G).
- Coverstore stays local-only and book-id-keyed (migrated in Plan E).
- Fileproc and bookdrop ingest still take `path string` (migrated when format processors gain a `ReaderAt` adapter in Plan F).
- DB schema unchanged. `books.path` still authoritative (Plan B drops it).

## Plan
See `docs/superpowers/plans/2026-04-29-storage-plan-a-interface.md`. This is Plan A of 8; spec is `docs/spec/storage.spec.md`.

## Test plan
- [ ] `go test ./internal/storage/...` passes.
- [ ] `make ci-local` green.
- [ ] Manual: `make up` + trigger a library scan; books appear in the bookdrop queue.
EOF
)"
```

Expected: PR opened, CI green.

---

## Self-Review Checklist

Per the writing-plans skill self-review pass:

**Spec coverage (this plan):**
- §2 storage backends → covered: interface + local impl, capability flags reserved.
- §2.1 storage interface → covered: `list/head/get/put/delete/copy` plus reserved capabilities.
- §9 operational guarantees → partially covered: atomic writes (Put/Copy via tmp+rename). Idempotent re-scan, transactional ingest, etc. land in Plans B–C.

**Spec coverage (deferred to later plans, intentionally):**
- §3 layout (folder structure, sidecar placement) → Plan B.
- §4 DB schema → Plan B.
- §5 identity & ingestion (sha256, two-phase scan, ETag fast-path) → Plans B + C.
- §5.4 change notification (inotify, S3 events) → Plan F (S3) + future enhancement (local inotify).
- §5.5 book-boundary resolution → Plan B (codifies current logic in DB) + Plan D (sidecar manifest).
- §6 sidecar layering & atomicity → Plan D.
- §7 cache layout → Plan E.
- §8 streaming & access → Plan G.
- §10 tradeoffs / §11 out of scope → no implementation needed.

**Placeholder scan:** none. Every step has runnable code or commands.

**Type consistency:** `PutOpts`/`GetOpts`/`DeleteOpts` exported (Task 4 refactor) so backends can read option values. `ApplyPut`/`ApplyGet`/`ApplyDelete` exported. `Capability` constants stable. `LocalFS` returns `(*LocalFS, error)` from `New`; tests expect that signature.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-29-storage-plan-a-interface.md`.

Two execution options:

1. **Subagent-Driven (recommended)** — fresh subagent per task, two-stage review between tasks, fast iteration.
2. **Inline Execution** — execute tasks in this session using `superpowers:executing-plans`, batch with checkpoints.

Which approach?
