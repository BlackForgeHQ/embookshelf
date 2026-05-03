# S3 Storage Support — Feature Specification

> Introduce S3 (and S3-compatible) object storage as a first-class backend for library book files, the BookDrop inbox, and the cover store — alongside the current local filesystem — selectable per-library and per-process at configuration time.

- **Status:** Draft
- **Scope:** `embookshelf` Go backend (storage, ingest, handler, task, coverstore, service, repo, migrator) + React (TanStack Start) SPA (Settings → Libraries dialog)
- **Permission required:** `admin` role (library creation, BookDrop / covers bucket configuration)
- **Entry point:** `/settings → Libraries` panel → **New library** dialog (new "Backend" selector) + new `STORAGE_*` env vars

---

## 1. Purpose

Today embookshelf assumes every book file, BookDrop item, and extracted cover lives on the same POSIX filesystem the binary can reach. That works for a single host, but it blocks three deployment shapes we keep getting asked about:

- Multi-replica deployments where the app runs on ephemeral containers (Fly, Kubernetes, Fargate) and needs shared, durable storage for covers and BookDrop.
- Users whose libraries already live in S3-compatible buckets (MinIO in a homelab, Backblaze B2, Cloudflare R2, Wasabi, AWS S3).
- Backups / disaster-recovery setups that want the cover store replicated to an object store without a sidecar.

This spec introduces a **storage backend abstraction** with two concrete implementations — `fs` (the current behavior, kept as default) and `s3` (AWS SDK v2, any S3-compatible endpoint) — and wires it through the three filesystem touchpoints that matter: the `coverstore` package, the BookDrop watcher/service, and the library scan + file-serve handlers.

Design choices worth flagging up front:

- **Per-library backend, not global.** `libraries.storage` picks the backend for that library's book files. A homelab user keeps their EPUB tree on disk and a cloud user points a new library at `s3://my-books/epubs/`. The two coexist in one instance.
- **Covers and BookDrop are instance-scoped, not per-library.** One cover bucket, one BookDrop bucket (or one cover dir and one BookDrop dir). A per-library split would fragment the hot path for no real gain.
- **Interface-first, not "inject an `afero.Fs`".** We don't need the full filesystem surface area. The three packages touch storage differently enough (stream vs atomic-write vs listing vs serve) that three narrow interfaces beat one fat one.
- **No rewrite of on-disk paths.** Existing `libraries.path` rows continue to be treated as `fs` backends; migrations default `storage = 'fs'`. Upgrading doesn't touch the filesystem.
- **Downloads proxy through the app by default.** S3 presigned URLs are an opt-in mode (per bucket), because the default deployment shouldn't leak bucket URLs, and some clients (readers streaming via the proxy) assume same-origin.
- **No S3-side "move" illusion.** S3 has no atomic rename; we implement "promote" and "apply naming pattern" as copy+delete with the DB row flipped between them, and we document the non-atomicity rather than paper over it.

Non-goals (out of scope for this spec):

- Filesystem snapshotting / versioning. S3 bucket versioning is orthogonal; if the admin turns it on, we don't touch it.
- Cross-backend migration (move a library from `fs` to `s3` in place). See §11.
- Encryption at rest beyond what S3 provides natively (SSE-S3 / SSE-KMS). We don't client-side encrypt objects. `EMBOOKSHELF_SECRET_KEY` continues to be a KEK for provider credentials only.
- Multipart-upload tuning for huge files. Default SDK chunking is fine for books up to a few GB.

---

## 2. User Stories

| # | As a … | I want to … | So that … |
|---|--------|-------------|-----------|
| 1 | Admin | Create a library that reads EPUBs from an S3 bucket + prefix | I can run embookshelf without mounting the bucket as a filesystem |
| 2 | Admin | Keep my existing on-disk library untouched after upgrade | Adopting S3 is opt-in, not a forced migration |
| 3 | Admin | Point cover storage at an S3 bucket | Cover files survive container restarts without a persistent volume |
| 4 | Admin | Point BookDrop at an S3 prefix | Cloud workflows (email-to-S3, rclone sync) can drop books and the watcher picks them up |
| 5 | Operator | Use any S3-compatible endpoint (MinIO, R2, B2) | I'm not locked into AWS |
| 6 | Reader | Stream EPUBs to my reader the same way I do today | Switching the backend is invisible to the UI and to e-reader apps using range requests |
| 7 | Admin | See a clear error if credentials or bucket permissions are wrong | I can fix my config before users hit broken covers |

---

## 3. Configuration Model

### 3.1 Environment variables (new)

The existing `DATA_PATH` / `BOOKDROP_PATH` stay. New vars introduce backend selection for **covers** and **bookdrop**:

| Var | Default | Meaning |
|---|---|---|
| `STORAGE_COVERS_BACKEND` | `fs` | `fs` uses `DATA_PATH/covers`. `s3` uses the S3 settings below under `covers/` prefix. |
| `STORAGE_BOOKDROP_BACKEND` | `fs` | `fs` uses `BOOKDROP_PATH`. `s3` uses the S3 settings below under the `bookdrop/` prefix. |
| `S3_ENDPOINT` | `""` | Override for non-AWS S3. e.g. `https://minio.local:9000` or `https://<accountid>.r2.cloudflarestorage.com`. Empty = AWS regional endpoint. |
| `S3_REGION` | `us-east-1` | Required even for MinIO (SDK needs *some* region signed in). |
| `S3_BUCKET` | `""` | Single bucket for the embookshelf instance. Covers and BookDrop live under `covers/` and `bookdrop/` prefixes. Required when either backend is `s3`. |
| `S3_ACCESS_KEY_ID` | `""` | Static credential. Omit to use the default AWS credential chain (IRSA, EC2 instance profile, `~/.aws/credentials`, etc.). |
| `S3_SECRET_ACCESS_KEY` | `""` | Paired with the above. |
| `S3_USE_PATH_STYLE` | `false` | MinIO / older S3 clones. True when the SDK must use `https://endpoint/bucket/key` instead of `https://bucket.endpoint/key`. |
| `S3_FORCE_HTTPS` | `true` | If false, allow `http://` endpoints (lab only). |
| `S3_PRESIGN_DOWNLOADS` | `false` | When true, book / cover downloads redirect to a presigned URL instead of proxying bytes through the app. TTL is `S3_PRESIGN_TTL_SECONDS`. |
| `S3_PRESIGN_TTL_SECONDS` | `300` | TTL for presigned GETs. Clamped to `[30, 3600]`. |

Library backends (per-library S3 settings) live in the DB (§6), not env. Only the **cover** and **bookdrop** backends are env-configured because those are instance-scoped.

### 3.2 Validation rules (Config.Load)

- If `STORAGE_COVERS_BACKEND == "s3"` or `STORAGE_BOOKDROP_BACKEND == "s3"`, `S3_BUCKET` must be non-empty.
- If `S3_ENDPOINT != ""` and `S3_FORCE_HTTPS == true`, the endpoint must be `https://…`.
- Unknown `STORAGE_*_BACKEND` values reject with `errors.New("STORAGE_..._BACKEND must be one of: fs, s3")`.
- Presence of `S3_ACCESS_KEY_ID` xor `S3_SECRET_ACCESS_KEY` is an error (partial static creds).
- `S3_PRESIGN_TTL_SECONDS` outside `[30, 3600]` is an error.

All validation lives in `internal/config/config.go` next to the existing OIDC checks.

### 3.3 Per-library backend (new fields)

Today `Library.Path` is the one field. We add two more:

```go
type Library struct {
    // ... existing fields ...
    Storage    string         // "fs" | "s3"; default "fs"
    S3         *LibraryS3     // nil unless Storage == "s3"
}

type LibraryS3 struct {
    Endpoint      string   // "" = AWS regional
    Region        string
    Bucket        string   // required
    Prefix        string   // e.g. "epubs/" — trailing slash required on input, stored verbatim
    UsePathStyle  bool
    ForceHTTPS    bool     // default true
    CredentialRef string   // "" = fall back to instance-level S3 creds; "env" / "iam" reserved; future: UUID of a stored-creds row
}
```

- `Path` stays and is still the UI label for `fs` libraries. For `s3` libraries the UI derives a label like `s3://bucket/prefix` for display, and `Path` is `""`.
- The existing partial unique index `libraries_path_key` (ON path WHERE path <> '') continues to block duplicate `fs` paths. A new partial unique index `libraries_s3_target_key` blocks duplicate `(endpoint, bucket, prefix)` triples (§6).
- `CredentialRef` is forward-looking. For this first iteration only the empty string ("use instance creds") is accepted; we persist the column so a later spec can add a `library_storage_credentials` table without another migration.

### 3.4 Settings UI

The **New library** dialog grows a **Backend** radio with two options:

| Backend | Fields | Notes |
|---|---|---|
| Local filesystem (default) | Name, Path | Existing behavior verbatim. |
| S3 / S3-compatible | Name, Endpoint (optional), Region, Bucket, Prefix, Use path-style, HTTPS | Submit is blocked until `bucket` non-empty and `prefix` ends with `/`. |

The "Count files" pre-scan button becomes backend-aware: for `s3` libraries it calls `ListObjectsV2` against the bucket/prefix and counts supported keys, with the same 10s hard timeout the filesystem walk already uses.

---

## 4. API Surface

All endpoints live under `/api/v1/settings` and are admin-gated ([internal/handler/router.go:117](internal/handler/router.go#L117)).

### 4.1 Pre-create file count (extended)

```
POST /api/v1/settings/libraries/scan
Auth:     admin
Body:     { "storage": "fs", "path": "<string>" }
       |  { "storage": "s3", "s3": { "endpoint":"", "region":"", "bucket":"", "prefix":"", "usePathStyle":false, "forceHTTPS":true } }
Response: { "count": <int> }
Errors:   400 on malformed body, missing required fields for the chosen backend
          502 on S3 auth / network failures (the count is best-effort and we surface the underlying error class, not the SDK message)
```

For `fs` the behavior is unchanged. For `s3` we call `ListObjectsV2` paginated, counting keys whose suffix clears `fileproc.IsSupported`. No DB writes.

### 4.2 Create (extended)

```
POST /api/v1/settings/libraries
Auth:     admin
Body:     CreateLibraryRequest  // shape below
Response: 201 Created → { "library": SettingsLibraryDTO }
Errors:   400 missing / malformed fields for the chosen backend
          409 ErrLibraryNameTaken  | ErrLibraryPathTaken | ErrLibraryS3TargetTaken
```

`createLibraryReq` grows:

```go
type createLibraryReq struct {
    Name    string                `json:"name"`
    Path    string                `json:"path"`    // fs only
    Scan    bool                  `json:"scan"`
    Storage string                `json:"storage"` // "fs" | "s3"; default "fs"
    S3      *createLibraryReqS3   `json:"s3,omitempty"`
}

type createLibraryReqS3 struct {
    Endpoint     string `json:"endpoint"`
    Region       string `json:"region"`
    Bucket       string `json:"bucket"`
    Prefix       string `json:"prefix"`
    UsePathStyle bool   `json:"usePathStyle"`
    ForceHTTPS   bool   `json:"forceHTTPS"`
}
```

Handler validation:

- `Storage` defaults to `"fs"` when absent (back-compat with older clients).
- `Storage == "fs"` → require non-empty `Path` (unchanged logic).
- `Storage == "s3"` → require non-empty `S3.Bucket` and `S3.Region`; trim `Prefix` and reject if it starts with `/`; auto-append `/` if missing.
- `S3.Endpoint`, if present, must parse as a URL and match the `S3_FORCE_HTTPS` global.

### 4.3 DTO (extended)

`SettingsLibraryDTO` response:

```json
{
  "library": {
    "id": "<uuid>",
    "name": "string",
    "slug": "string",
    "storage": "fs" | "s3",
    "path": "string",
    "s3": null | {
      "endpoint": "string",
      "region": "string",
      "bucket": "string",
      "prefix": "string",
      "usePathStyle": false,
      "forceHTTPS": true
    },
    "lastScannedAt": null,
    "fileCount": 0,
    "discoveredCount": 0,
    "fileNamingPattern": null,
    "bookCount": 0,
    "createdAt": "<RFC3339>"
  }
}
```

Credentials are never returned in the DTO (they don't live on the library row in this iteration anyway).

### 4.4 Related endpoints (unchanged wire-level)

Rescan, delete, and pattern endpoints keep their shapes. Behavior changes under the hood:

- Rescan: `task.LibraryScanWorker.Work` dispatches on `Library.Storage`.
- Delete: still cascades in Postgres. Cover cleanup for the deleted books uses whatever backend the cover store is configured with (instance-level).

---

## 5. Storage Abstraction

The core of this spec. Three narrow interfaces, each owned by the package that needs it. Avoids an all-encompassing "Fs" wrapper and keeps each implementation honest about what it actually supports.

### 5.1 Interfaces

**[internal/storage/storage.go](internal/storage/storage.go)** (new package):

```go
// ObjectStore is the cover-store shape: atomic-write, promote, open, delete.
// Keys are opaque strings; no directory semantics are assumed.
type ObjectStore interface {
    // Put writes data under key. Must be atomic from a reader's POV:
    // a concurrent Open either sees the prior value or the new one,
    // never a truncated write.
    Put(ctx context.Context, key string, data []byte, contentType string) error

    // Copy duplicates src -> dst. Used by PromoteBookDropToBook.
    // Missing src returns ErrNotFound.
    Copy(ctx context.Context, src, dst string) error

    // Open returns a reader for key. Caller Close()s it.
    Open(ctx context.Context, key string) (io.ReadCloser, error)

    // Stat returns metadata (size, content-type, etag). Used for cover serving.
    Stat(ctx context.Context, key string) (ObjectInfo, error)

    // Delete removes key. Missing key is not an error (idempotent).
    Delete(ctx context.Context, key string) error

    // PresignGet returns a short-lived URL for direct client download.
    // Implementations that don't support it (fs) return ErrPresignUnsupported.
    PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// Lister is the library-scan / bookdrop-watcher shape: enumerate keys by prefix.
type Lister interface {
    // List streams keys whose name starts with prefix. Implementations must
    // surface both key and size; ordering is not guaranteed.
    List(ctx context.Context, prefix string, fn func(Entry) error) error
}

// Streamer is the HTTP-handler shape: serve bytes with range-request support.
type Streamer interface {
    // ServeObject writes the object at key to w with Range support,
    // Content-Type from contentType (or stored metadata if ""), and
    // Cache-Control from the caller.
    ServeObject(ctx context.Context, w http.ResponseWriter, r *http.Request, key, contentType string) error
}

type ObjectInfo struct {
    Size        int64
    ContentType string
    ETag        string
    ModTime     time.Time
}

type Entry struct {
    Key  string
    Size int64
}

var (
    ErrNotFound            = errors.New("storage: object not found")
    ErrPresignUnsupported  = errors.New("storage: presign not supported by this backend")
)
```

An `ObjectStore` usually also implements `Lister` and `Streamer`; we model them separately so a future read-only HTTP backend (public archive URL, etc.) can implement only `Streamer`.

### 5.2 Implementations

**Filesystem** — [internal/storage/fs.go](internal/storage/fs.go):

- `Put` = existing `writeAtomic` (temp file + `os.Rename`). Kept verbatim from [internal/coverstore/store.go:95](internal/coverstore/store.go#L95).
- `Copy` = `os.Rename` with copy-and-unlink fallback, reusing the `moveFile` dance already in [internal/service/bookdrop.go:310](internal/service/bookdrop.go#L310). Moved here so bookdrop service becomes a thin caller.
- `Open` = `os.Open`.
- `Stat` = `os.Stat` + a `mime.TypeByExtension` guess for `ContentType`.
- `Delete` = `os.Remove` with `ErrNotExist` swallowed (matches current `removeIfExists`).
- `PresignGet` = returns `ErrPresignUnsupported`; callers fall back to proxy.
- `List` = `filepath.WalkDir`; keys are returned as `path[len(root):]`-style relative paths.
- `ServeObject` = `c.File(path)` via Gin — already handles range requests. Preserves the current `serveBookFile` behavior ([internal/handler/files.go:69](internal/handler/files.go#L69)).

**S3** — [internal/storage/s3.go](internal/storage/s3.go), uses `github.com/aws/aws-sdk-go-v2`:

- `Put` = `PutObject` with `ContentType` set; atomic by S3 semantics.
- `Copy` = `CopyObject`; for cross-bucket we don't support (covers/bookdrop and each library get their own client, but a single logical `ObjectStore` is always one bucket+prefix).
- `Open` = `GetObject` → returns the `Body` as the `io.ReadCloser`.
- `Stat` = `HeadObject`.
- `Delete` = `DeleteObject`; swallow `NoSuchKey`.
- `PresignGet` = `s3.NewPresignClient(...).PresignGetObject` with the configured TTL.
- `List` = `ListObjectsV2` paginated until `IsTruncated == false`, calling `fn` per entry.
- `ServeObject`:
  - If `S3_PRESIGN_DOWNLOADS == true`, issue a `302` to `PresignGet`. Skip the proxy entirely.
  - Otherwise, proxy: `GetObject` → copy `Range`, `If-Range`, `If-None-Match` request headers into the SDK call, stream the response body to `w`, forward `Content-Range`, `Content-Length`, `ETag`, `Accept-Ranges`. This mirrors what `c.File` does for disk files.

### 5.3 Wiring

Two factories live in [internal/storage/build.go](internal/storage/build.go):

```go
// BuildCoverStore returns the instance-scoped cover store from cfg.
// Replaces the direct coverstore.New(filepath.Join(cfg.DataPath, "covers")) call.
func BuildCoverStore(cfg config.Config) (ObjectStore, error)

// BuildBookDropStore returns the instance-scoped bookdrop store from cfg.
func BuildBookDropStore(cfg config.Config) (ObjectStore, Lister, error)

// BuildLibraryStore returns the per-library store for a library row.
// Errors on unknown lib.Storage; fs libraries return a fs-rooted store;
// s3 libraries return an s3 store scoped to lib.S3.Bucket + lib.S3.Prefix,
// using instance-level S3 credentials if lib.S3.CredentialRef is empty.
func BuildLibraryStore(cfg config.Config, lib model.Library) (ObjectStore, Lister, Streamer, error)
```

`main.go` calls the first two at boot and caches them on the `Handler` / service structs. The third is invoked per-request inside `serveBookFile`, inside `task.LibraryScanWorker.Work`, and inside the rescan path. A small sync.Map cache keyed by `lib.ID` avoids rebuilding SDK clients on every hit; entries evict on library update or delete (we don't support editing today so delete is the only invalidation).

### 5.4 `coverstore` refactor

The existing `coverstore.Store` becomes a thin wrapper around an `ObjectStore`:

```go
type Store struct { os ObjectStore }

func (s *Store) SaveBookDrop(ctx context.Context, id string, data []byte, mime string) error {
    return s.os.Put(ctx, "bookdrop/"+id, data, mime)
}
func (s *Store) PromoteBookDropToBook(ctx context.Context, bookdropID, bookID string) error {
    if err := s.os.Copy(ctx, "bookdrop/"+bookdropID, "books/"+bookID); err != nil {
        if errors.Is(err, ErrNotFound) {
            return nil
        }
        return err
    }
    return s.os.Delete(ctx, "bookdrop/"+bookdropID)
}
// OpenBook, DeleteBook, etc. follow the same pattern.
```

Callers now pass `ctx` (they already have one). Method signatures gain `context.Context`; the current signatures are internal (see [internal/coverstore/store.go:41](internal/coverstore/store.go#L41) and [:54](internal/coverstore/store.go#L54)) so the only callers to update are in `internal/service` and `internal/handler`.

`PromoteBookDropToBook` **is not atomic on S3**. If the copy succeeds and the delete fails we log the orphan key; a nightly reaper (out of scope) can sweep `bookdrop/` keys older than N days that have no matching `bookdrop_items` row.

### 5.5 BookDrop watcher refactor

[internal/ingest/watcher.go](internal/ingest/watcher.go) stops calling `filepath.WalkDir` directly. It takes a `Lister` and iterates:

```go
type Watcher struct {
    Store    storage.Lister
    // plus: Svc, Queue, Interval
}

func (w *Watcher) scan(ctx context.Context) {
    err := w.Store.List(ctx, "", func(e storage.Entry) error {
        if !fileproc.IsSupported(e.Key) { return nil }
        format := fileproc.FormatForExt(filepath.Ext(e.Key))
        item, created, err := w.Svc.Enqueue(ctx, e.Key, format, e.Size)
        // ... (unchanged)
    })
    // ...
}
```

Two knock-on effects:

- `bookdrop_items.path` now stores an **opaque key** under the bookdrop store's root, not an absolute filesystem path. For `fs` that's the same relative path under `BOOKDROP_PATH`. For `s3` it's the object key under `bookdrop/` prefix.
- `repo.BookExistsByPath` still compares strings, which is fine — we just need ingest and library scan to be consistent about using keys (and to not mix an `fs` library's absolute paths with an `s3` library's keys, which they don't because each library scan resolves its own store).

### 5.6 Library scan refactor

[internal/task/library_scan.go](internal/task/library_scan.go) grows a `BuildLibraryStore(cfg, lib)` call at the top and loops via `Lister.List` in place of `filepath.WalkDir`. `info.Size` comes from the entry; the rest of the pipeline (supported-format filter, `BookExistsByPath` dedup, `BookDropService.Enqueue`, river fan-out) is untouched.

### 5.7 File-serve refactor

[internal/handler/files.go](internal/handler/files.go) changes:

- Path validation (must be under a registered library root) becomes backend validation (must belong to a library the user can access and whose store produced this key). For `fs` libraries that still means "abs path is under `lib.Path`"; for `s3` libraries that means "key starts with `lib.S3.Prefix`".
- `c.File(absPath)` becomes `streamer.ServeObject(ctx, c.Writer, c.Request, key, mime)`.
- `S3_PRESIGN_DOWNLOADS == true` short-circuits through a `302` — the client hits S3 directly. Callers who need same-origin (the in-app reader) set a request header / query arg `proxy=1` to force the proxy path; on presign-enabled deployments this is still safe because only admins can control the knob.

### 5.8 BookDrop approval (apply naming pattern)

`applyNamingPattern` ([internal/service/bookdrop.go:224](internal/service/bookdrop.go#L224)) is the bridge between the BookDrop store and a library store: today it's `moveFile(src, dst)` inside one filesystem. With mixed backends we now have four cases:

| Source | Destination | Strategy |
|---|---|---|
| `fs` bookdrop | `fs` library | `moveFile` — same as today. |
| `fs` bookdrop | `s3` library | Upload via `ObjectStore.Put` (or multipart for large files), then `os.Remove`. |
| `s3` bookdrop | `fs` library | `GetObject` stream → temp file + rename, then `DeleteObject`. |
| `s3` bookdrop | `s3` library (same bucket) | `CopyObject` + `DeleteObject`. |
| `s3` bookdrop | `s3` library (different bucket) | `CopyObject` (SDK handles cross-bucket) or `GetObject` → `PutObject` for cross-endpoint, then `DeleteObject`. |

We pick at runtime by reflecting on the source and destination `ObjectStore` concrete types (type-switch on `*fs.Store` / `*s3.Store`) rather than adding a cross-backend transfer method on the interface. The matrix is small and the type switch is documented with a comment in the service.

---

## 6. Data Model

### 6.1 Migration `000024_library_storage.up.sql`

```sql
ALTER TABLE libraries
    ADD COLUMN storage              TEXT        NOT NULL DEFAULT 'fs',
    ADD COLUMN s3_endpoint          TEXT        NOT NULL DEFAULT '',
    ADD COLUMN s3_region            TEXT        NOT NULL DEFAULT '',
    ADD COLUMN s3_bucket            TEXT        NOT NULL DEFAULT '',
    ADD COLUMN s3_prefix            TEXT        NOT NULL DEFAULT '',
    ADD COLUMN s3_use_path_style    BOOLEAN     NOT NULL DEFAULT FALSE,
    ADD COLUMN s3_force_https       BOOLEAN     NOT NULL DEFAULT TRUE,
    ADD COLUMN s3_credential_ref    TEXT        NOT NULL DEFAULT '';

ALTER TABLE libraries
    ADD CONSTRAINT libraries_storage_enum CHECK (storage IN ('fs','s3'));

CREATE UNIQUE INDEX libraries_s3_target_key
    ON libraries (s3_endpoint, s3_bucket, s3_prefix)
    WHERE storage = 's3';

-- Sanity: fs libraries must have a path; s3 libraries must have a bucket.
ALTER TABLE libraries
    ADD CONSTRAINT libraries_backend_shape CHECK (
        (storage = 'fs' AND path <> '')
        OR
        (storage = 's3' AND s3_bucket <> '')
    );
```

The partial unique index mirrors `libraries_path_key`'s shape ([library-creation.spec.md §6.1](spec/library-creation.spec.md)) so both coexist cleanly.

### 6.2 Final columns (added fields only)

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `storage` | TEXT | NOT NULL DEFAULT `'fs'`, CHECK in (`fs`,`s3`) | Backend discriminator |
| `s3_endpoint` | TEXT | NOT NULL DEFAULT `''` | `""` = AWS regional endpoint |
| `s3_region` | TEXT | NOT NULL DEFAULT `''` | Required when `storage='s3'` (enforced in service layer, not DB) |
| `s3_bucket` | TEXT | NOT NULL DEFAULT `''`, CHECK via `libraries_backend_shape` | |
| `s3_prefix` | TEXT | NOT NULL DEFAULT `''` | Trailing-slash normalized in service layer |
| `s3_use_path_style` | BOOLEAN | NOT NULL DEFAULT false | |
| `s3_force_https` | BOOLEAN | NOT NULL DEFAULT true | |
| `s3_credential_ref` | TEXT | NOT NULL DEFAULT `''` | Reserved; `''` = use instance env creds |

### 6.3 Model struct

```go
type Library struct {
    ID                string
    Name              string
    Slug              string
    Storage           string      // "fs" | "s3"
    Path              string      // fs only; "" when Storage == "s3"
    S3                *LibraryS3  // populated when Storage == "s3"
    LastScannedAt     *time.Time
    FileCount         int
    DiscoveredCount   int
    BookCount         int         // computed via subquery
    CreatedAt         time.Time
    FileNamingPattern *string
}
```

`libCols` in [internal/repo/library.go](internal/repo/library.go) grows the new columns; `scanLibrary` hydrates `S3` only when `storage = 's3'` (nil pointer otherwise so JSON emits `null`).

### 6.4 `bookdrop_items.path` semantics

Type stays `TEXT`. Semantics documented in a comment on the column:

> *storage key when the BookDrop store is `s3`; absolute filesystem path when `fs`. Uniqueness across storage types is not enforced — the backend type is implicit in the server's `STORAGE_BOOKDROP_BACKEND` config.*

No migration needed; only a migration comment bump.

---

## 7. Edge Cases

| Case | Outcome |
|---|---|
| `STORAGE_COVERS_BACKEND=s3` with no `S3_BUCKET` | Boot fails in `config.Load` with a clear message; no half-initialized state. |
| Instance S3 creds valid for covers, invalid for a specific library's bucket | Library row is created (we don't pre-validate). Scan job fails with a 403 and logs; `last_scanned_at` stays null. Same shape as an unreachable `fs` path. |
| Presign enabled, reader tab opens EPUB | 302 to presigned URL. The reader's `fetch` follows the redirect; works cross-origin because S3 sets `Access-Control-Allow-Origin` per its CORS config. If the admin hasn't configured CORS, the reader breaks — so the docs call this out and we ship a default-safe `S3_PRESIGN_DOWNLOADS=false`. |
| BookDrop S3 bucket has a lifecycle rule that deletes keys after N days | We don't interfere. Items that disappear before approval show `ErrNotFound` on approve — the handler surfaces a 410 and the UI prompts the admin to dismiss. |
| S3 eventual consistency (legacy non-AWS clones) | AWS S3 has been strongly consistent since 2020; other clones vary. `ListObjectsV2` after a `PutObject` may lag on some backends. We document this and recommend MinIO ≥ 2021 / R2 / B2 for BookDrop ingest. |
| Cross-bucket `CopyObject` between different endpoints | SDK `CopyObject` only works within the same S3 endpoint. For cross-endpoint we fall back to `GetObject → PutObject` (§5.8). Slower; streams through the app process. |
| Orphan `bookdrop/{id}` cover after approval when `DeleteObject` 500s | Copy already succeeded; book has a cover. We log `cover_promotion_orphan` and leave the bookdrop key. A future reaper job cleans them. |
| Library delete: cover cleanup fails on S3 | Best-effort, same as today on disk. Each `DeleteObject` error is logged; the library row is gone, the cascade ran, and the admin can re-run a cleanup later. |
| Two libraries pointing at overlapping S3 prefixes (`foo/` and `foo/sub/`) | Not blocked at the DB level (the unique index is exact-triple). Same policy as overlapping `fs` paths today — admin problem, not an ingest problem, because `BookExistsByPath` dedup handles duplicates. |
| `S3_PRESIGN_TTL_SECONDS=0` | Rejected at boot. Non-zero minimum is `30`. |
| Network blip mid-scan | `ListObjectsV2` paginator returns an error; the scan worker logs, River retries the job, dedup keeps it idempotent. |
| Admin edits `.env` to flip `STORAGE_COVERS_BACKEND` from `fs` to `s3` without moving the bytes | Covers appear missing until the admin copies `${DATA_PATH}/covers/` to `s3://.../covers/`. We don't auto-migrate. Documented. |

---

## 8. Validation Summary

| Layer | Rule |
|---|---|
| Env | `STORAGE_*_BACKEND` ∈ {`fs`, `s3`}; `S3_BUCKET` required when any backend is `s3`; credentials paired; TTL clamped |
| UI | `storage` radio selects the required field set; `bucket` non-empty; `prefix` ends with `/` after trim |
| Handler | Mirror UI rules server-side; trim/normalize `prefix`; reject path-style + force-HTTPS mismatches |
| Service | Derive slug; normalize prefix (strip leading `/`, add trailing `/`); validate endpoint URL parses |
| Repository | Two partial unique indexes (`libraries_path_key`, `libraries_s3_target_key`) + `libraries_backend_shape` CHECK |
| Auth | Admin-only for all library / storage config endpoints |

---

## 9. Security Considerations

- **Credentials on disk**: static S3 creds come from env. In Kubernetes / ECS prefer IRSA / task roles (empty `S3_ACCESS_KEY_ID` triggers the default credential chain). Instance creds are never returned from any endpoint.
- **Presigned URL leakage**: `S3_PRESIGN_DOWNLOADS=true` means every download request surfaces a URL that's valid for the TTL. TTL defaults to 5 minutes. A leaked URL grants read until expiry. Keep TTL small; don't log signed URLs. The proxy mode (default) sidesteps this entirely.
- **Path traversal on keys**: S3 keys can contain `..` literally (no filesystem semantics), so traversal isn't a concern — but we still reject `prefix` values that begin with `/` or contain `..` segments to avoid admin typos that would read the whole bucket. The library-scan lister is explicitly scoped to `lib.S3.Prefix`.
- **Endpoint spoofing**: if `S3_FORCE_HTTPS` is true (default), http:// endpoints are rejected at boot.
- **SSRF via custom endpoint**: the per-library `endpoint` field is admin-supplied. We don't resolve it on the main request path (only on scan and on proxied downloads). Still, an admin could point it at `http://169.254.169.254/…` on AWS. The admin-only gate is the only defense here; we document it.
- **Same-origin for the reader**: when presign is on, the in-app reader (which uses `fetch` with `credentials: 'include'` today) may break on cross-origin responses. Deployments that enable presign must configure S3 CORS; docs describe the exact rule set.
- **Encryption at rest**: we rely on S3 SSE (SSE-S3 or SSE-KMS configured on the bucket). No client-side encryption in this iteration; `EMBOOKSHELF_SECRET_KEY` is unchanged.

---

## 10. Cross-feature Interactions

- **BookDrop**: its store is instance-wide. A library with `storage='s3'` and an `fs` BookDrop is a valid combo (admin keeps their drop folder on disk and promotes into a bucket). §5.8 matrix covers it.
- **File Naming Patterns**: patterns produce a relative path under the library root. For `s3` that becomes an object key (forward-slash separators); we rely on the pattern grammar already producing `/`-based output ([spec/file-naming-patterns.spec.md](spec/file-naming-patterns.spec.md)). No grammar changes.
- **Cover storage**: instance-level; a single `ObjectStore` handle powers the cover-serve handler regardless of which library a book belongs to.
- **OIDC / auth**: unaffected.
- **SSE**: scan events shape stays the same; the UI doesn't need to distinguish backends for progress display.
- **Metadata providers**: unaffected — they don't touch storage.

---

## 11. Open / Future Work

1. **Per-library credentials**: add a `library_storage_credentials` row type keyed by `s3_credential_ref`, encrypted at rest via `EMBOOKSHELF_SECRET_KEY`. Unblocks multi-tenant deployments where each library's bucket belongs to a different user.
2. **Cross-backend migration tool**: admin-triggered job that copies a library's bytes from `fs` to `s3` (or vice-versa), updates the DB row transactionally, and re-validates every `books.path`.
3. **Reaper for orphan cover keys**: nightly job that lists `bookdrop/` in the cover store and deletes keys older than 7 days with no matching `bookdrop_items` row.
4. **Event-driven BookDrop** (S3 → SQS / webhook): replaces polling with push, especially valuable when the drop bucket fronts an email-to-S3 pipeline.
5. **Range-request optimization**: proxied `ServeObject` currently streams whole responses. For very large PDFs we could short-circuit to a presigned-URL 302 even when `S3_PRESIGN_DOWNLOADS=false`, on a per-request `?direct=1` opt-in.
6. **GCS / Azure Blob adapters**: the `ObjectStore` / `Lister` / `Streamer` interfaces are deliberately S3-agnostic. Adding a second cloud is "implement three methods, register a factory."
7. **ETag-based dedup**: today library scan dedups by key. S3 ETag gives us a cheap content hash; storing it on `books` would let us detect moved-but-unchanged files and avoid re-ingesting them under a new key.
8. **Multipart tuning** (`S3_MULTIPART_THRESHOLD`, `S3_MULTIPART_PART_SIZE`) — defaults cover books up to multi-GB, but operators running 10GB+ scan rigs may want knobs.

---

## 12. Testing

| Layer | Test | Tool |
|---|---|---|
| Unit | `storage.FS` round-trips (`Put`→`Open`→`Stat`→`Delete`) against `t.TempDir()` | `go test` |
| Unit | `storage.FS.Copy` atomic same-FS + cross-FS fallback path | `go test` |
| Integration | `storage.S3` round-trips against MinIO via `testcontainers-go` | `go test` (tagged `integration`) |
| Integration | `storage.S3.List` pagination with >1000 keys | `go test` (tagged `integration`) |
| Integration | `coverstore.Store` with an injected `storage.S3` — Save/Promote/Open/Delete | `go test` (tagged `integration`) |
| Integration | BookDrop end-to-end: upload key to MinIO → watcher picks it up → item appears in DB | `go test` (tagged `integration`) |
| Integration | Library scan on an `s3` library with 500 supported + 50 unsupported keys; dedup on rescan | `go test` (tagged `integration`) |
| Integration | BookDrop approval matrix (§5.8) — fs→fs, fs→s3, s3→fs, s3→s3 | `go test` (tagged `integration`) |
| HTTP | `GET /api/v1/books/:id/file` serves bytes identically between `fs` and `s3` (same checksum) | `go test` |
| HTTP | `S3_PRESIGN_DOWNLOADS=true` yields a 302 with a presigned URL | `go test` |
| UI | Settings → New library dialog: backend radio toggles field set; submit posts the right body | Playwright, see [docs/adr/0006-playwright-e2e-against-built-binary.md](../adr/0006-playwright-e2e-against-built-binary.md) |
| UI | Pre-scan file count against `s3` | Playwright |

CI runs MinIO via the existing [compose.dev.yml](compose.dev.yml) (add a `minio` service); unit tests keep running without it via a `-short` gate that skips the integration build tag.

---

## 13. Key References

- Existing config: [internal/config/config.go](internal/config/config.go) (lines 11–108)
- Existing cover store: [internal/coverstore/store.go](internal/coverstore/store.go)
- Existing BookDrop watcher: [internal/ingest/watcher.go](internal/ingest/watcher.go)
- Existing library scan: [internal/task/library_scan.go](internal/task/library_scan.go)
- Existing file-serve: [internal/handler/files.go](internal/handler/files.go)
- BookDrop approval / move logic: [internal/service/bookdrop.go:224](internal/service/bookdrop.go#L224), [:310](internal/service/bookdrop.go#L310)
- Library repo: [internal/repo/library.go](internal/repo/library.go)
- Library model: [internal/model/library.go](internal/model/library.go)
- Settings handlers: [internal/handler/settings.go](internal/handler/settings.go)
- Settings UI: [ui/src/routes/_app.settings.tsx](ui/src/routes/_app.settings.tsx)
- Related specs: [spec/library-creation.spec.md](spec/library-creation.spec.md), [spec/file-naming-patterns.spec.md](spec/file-naming-patterns.spec.md)
- AWS SDK v2: `github.com/aws/aws-sdk-go-v2`, `.../service/s3`, `.../service/s3/types`

---

## 14. Glossary

- **Backend** — concrete storage implementation: `fs` or `s3`. Selected per-library for book files and per-process (env) for covers and BookDrop.
- **Object key** — opaque string identifying an object inside a store; slash-delimited for grouping but not a path. `books/abc123`, `bookdrop/xyz`, `epubs/Author Name/Title.epub`.
- **Prefix** — leading substring of a key; the S3 analog of a directory root. `s3_prefix` on a library row scopes scans and serves to keys starting with that prefix.
- **Presigned URL** — short-lived S3 URL that grants direct-to-S3 read without routing through embookshelf. Optional (`S3_PRESIGN_DOWNLOADS`).
- **Instance-level S3 creds** — the `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` pair (or the default AWS credential chain) used by the cover store, BookDrop store, and any library whose `s3_credential_ref` is empty.
- **Promote** (cover) — copy `bookdrop/{id}` → `books/{id}` then delete the bookdrop key. Atomic on `fs` (rename); best-effort on `s3`.
