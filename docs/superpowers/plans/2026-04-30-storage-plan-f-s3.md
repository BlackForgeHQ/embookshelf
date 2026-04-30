# S3 Backend — Implementation Plan (Plan F of 8)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a second `storage.Storage` implementation against S3-compatible object stores (AWS S3 + S3-API-compatible like minio, R2, Backblaze, Wasabi). Slot it in behind the existing interface so the scan worker, bookdrop ingest, hashing backfill, sidecar reader, and cover store all transparently support an S3-rooted library. Every consumer that today receives a single `storage.Storage` now receives a backend `Resolver` that maps a `library_id` to the right Storage handle.

**Architecture:** A new `internal/storage/s3` package implementing `storage.Storage` via `aws-sdk-go-v2`. Capability bits set: `CapConditional` (PutObject `If-Match` / `If-None-Match`), `CapVersioning` (the bucket is required to have versioning enabled), `CapPresign` (reserved; the actual presign endpoint lands in Plan G), `CapStorageClass` (reserved; lifecycle tagging in Plan H). At boot, `cmd/embookshelf/main.go` reads `storage_backends` rows, constructs the appropriate impl per row (kind=`local` → `local.LocalFS`, kind=`s3` → `s3.Backend`), and wires a `storage.Resolver` into every consumer. The legacy single-backend path (LocalFS rooted at `/`) becomes the default-resolver fallback so existing single-library deployments keep working without a config change. SQLite + any S3 backend = config-load error (per spec §10).

**Tech Stack:** Go 1.25, `github.com/aws/aws-sdk-go-v2/{config,credentials,service/s3}` (~6 MB cumulative; standard deps in the Go ecosystem). minio container for CI integration tests.

**Companion spec:** [`docs/spec/storage.spec.md`](../../spec/storage.spec.md) §2.1 (interface), §2.2 (S3-specific notes), §3.2 (S3 layout), §5.2 (ETag-isn't-enough), §6.2 (conditional PUT), §10 (SQLite+S3 refusal).

**Locked decisions:**
- AWS SDK v2 (not v1). v2 is the current lineage; v1 is in maintenance.
- One bucket per environment, libraries are prefixes (per spec §3.2).
- Bucket versioning + SSE enforced at backend-construction time: `s3.New` calls `GetBucketVersioning` and refuses to start when versioning is `Suspended`/`""`. Same for `GetBucketEncryption` returning empty config.
- Conditional `PutObject` uses S3's `IfMatch` / `IfNoneMatch` request fields directly (S3 supports them as of 2024+).
- Range reads (`GetObject` with `Range:` header) are implemented; `CapRange` flips on. The `serveBookFile` handler change to actually USE Range stays in Plan G.
- Presigned URLs: `s3.PresignGet(key, ttl)` is implemented; the handler call site is Plan G. `CapPresign` flips on now.
- Storage class / lifecycle / event notifications: deferred to Plan H. `CapStorageClass`/`CapNotify` stay off in this plan.
- SQLite + S3 combination at boot → fatal config error with explanation pointing at the spec.
- Multipart upload: not implemented in Plan F. Single-PUT works up to 5 GB. Books can hit that for some audiobooks; we accept the limit and add multipart in a follow-up if it bites.

**Depends on:** Plans A–E. Plan B's `storage_backends` table is the configuration source; Plan B's `LibraryRepo.LibraryBackend` is the lookup helper.

**Out of scope:**
- Multipart upload (>5 GB single objects).
- S3 events / SQS notification (Plan H).
- Bucket lifecycle rules (Plan H).
- Server-side cross-region replication setup.
- IAM policy boilerplate / docs (separate ops concern).
- Range reads in the files handler (interface supports Range; handler is Plan G).
- Presigned URLs in the files handler (interface supports Presign; handler is Plan G).
- Migrating existing local libraries to S3 — that's a manual `aws s3 sync` plus a DB update; we document it but don't automate.

---

## File Structure

### Files created

| Path | Responsibility |
|---|---|
| `internal/storage/s3/s3.go` | `Backend` struct + `New(ctx, Config) (*Backend, error)`. Builds the AWS SDK client, validates bucket versioning + SSE on construction. |
| `internal/storage/s3/methods.go` | `List`, `Head`, `Get`, `Put`, `Delete`, `Copy` implementations. |
| `internal/storage/s3/iter.go` | `s3Iter` — wraps `s3.ListObjectsV2Paginator`. |
| `internal/storage/s3/presign.go` | `PresignGet(ctx, key, ttl) (string, error)` — capability-gated extension. |
| `internal/storage/s3/s3_test.go` | Unit tests for argument shaping. |
| `internal/storage/s3/integration_test.go` | Integration tests via minio when `TEST_S3_ENDPOINT` is set; uses `storagetest.RunSuite` for parity with LocalFS. |
| `internal/storage/resolver.go` | `Resolver` interface + `MapResolver`/`ConstantResolver` implementations. |
| `internal/storage/resolver_test.go` | Resolver tests. |
| `internal/config/storage.go` | `LoadStorageBackends(ctx, repo, dialect) (map[string]storage.Storage, error)` — boot helper that reads the storage_backends table and returns a map keyed by backend id. Refuses SQLite+S3. |
| `internal/config/storage_test.go` | Tests for the SQLite+S3 refusal. |

### Files modified

| Path | Change |
|---|---|
| `go.mod` / `go.sum` | Add `github.com/aws/aws-sdk-go-v2`, `…/v2/config`, `…/v2/credentials`, `…/v2/service/s3`. |
| `cmd/embookshelf/main.go` | Replace the single `local.New("/")` construction with `config.LoadStorageBackends(...)` → builds a `storage.Resolver`. Pass the resolver everywhere a `storage.Storage` is consumed today. |
| `internal/queue/queue.go`, `internal/queue/sqlite.go` | Accept a `storage.Resolver` instead of a single `storage.Storage` (or alongside it during transition). The scan worker resolves per-library at run time. |
| `internal/task/library_scan.go` | Replace the single `deps.Storage` with `deps.Resolver.Resolve(lib.BackendID)`. Keep `legacyScan` fallback. |
| `internal/task/files_backfill.go` | Iterate libraries; resolve per library before hashing. |
| `internal/task/covers_backfill.go` | Unchanged — covers are local-only by design (cache is local per spec §7). |
| `internal/task/bookdrop.go` | Replace `deps.Storage` with `deps.Resolver`; resolve based on the bookdrop item's library. |
| `internal/storage/storage.go` | Add `Resolver` interface declaration here (or in `resolver.go` as listed above; keep one canonical home). |

### Files NOT touched

- `internal/handler/files.go` — Range support is Plan G.
- `internal/handler/cover.go` — covers stay local.
- `internal/sidecar/*` — Plan D's writer already uses the storage interface; conditional PUT is now actually honored on the S3 path.

---

## Phase 1 — Resolver

### Task 1: `Resolver` interface + LocalFS-only impl

**Files:**
- Create: `internal/storage/resolver.go`
- Create: `internal/storage/resolver_test.go`

```go
package storage

import "fmt"

// Resolver maps a logical context (library_id, backend_id, or both)
// to a concrete Storage instance. The scan worker, bookdrop ingest,
// hashing backfill, and sidecar reader/writer all take a Resolver
// instead of a single Storage so they can target the right backend
// for each library.
type Resolver interface {
    // Resolve returns the Storage for the given backend id. The
    // backend id is what's stored in libraries.backend_id; an empty
    // string returns the default Storage (the one that doesn't
    // belong to any specific backend, used during the transition
    // before backfill assigns backend_id to every library).
    Resolve(backendID string) (Storage, error)
}

// ResolverFunc adapts a plain function to the Resolver interface.
type ResolverFunc func(backendID string) (Storage, error)

func (f ResolverFunc) Resolve(backendID string) (Storage, error) { return f(backendID) }

// MapResolver dispatches by backend id. The empty string maps to a
// configured default (used pre-backfill).
type MapResolver struct {
    Default  Storage
    Backends map[string]Storage // keyed by storage_backends.id
}

func (r *MapResolver) Resolve(backendID string) (Storage, error) {
    if backendID == "" {
        if r.Default == nil {
            return nil, fmt.Errorf("storage: no default backend configured")
        }
        return r.Default, nil
    }
    s, ok := r.Backends[backendID]
    if !ok {
        return nil, fmt.Errorf("storage: unknown backend id %q", backendID)
    }
    return s, nil
}

// ConstantResolver returns the same Storage for every Resolve call.
// Used by the boot code when only a default backend exists.
type ConstantResolver struct{ S Storage }

func (r ConstantResolver) Resolve(_ string) (Storage, error) {
    if r.S == nil {
        return nil, fmt.Errorf("storage: no backend configured")
    }
    return r.S, nil
}
```

Tests cover: ConstantResolver returns the same storage; MapResolver routes by id; missing id returns error; default-fallback works.

Commit:
```bash
git commit -m "feat(storage): Resolver interface + Map/Constant impls"
```

---

## Phase 2 — S3 Backend

### Task 2: Add AWS SDK deps + skeleton

**Files:**
- Create: `internal/storage/s3/s3.go`
- Modify: `go.mod`, `go.sum`

```bash
go get github.com/aws/aws-sdk-go-v2/config@latest
go get github.com/aws/aws-sdk-go-v2/credentials@latest
go get github.com/aws/aws-sdk-go-v2/service/s3@latest
go mod tidy
```

```go
// Package s3 implements storage.Storage against an S3-compatible
// object store (AWS S3, minio, Cloudflare R2, Backblaze B2, Wasabi).
// Bucket versioning + server-side encryption are required at
// construction time per spec §3.2.
package s3

import (
    "context"
    "errors"
    "fmt"
    "time"

    awsconfig "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/credentials"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    "github.com/aws/aws-sdk-go-v2/service/s3/types"

    "github.com/blackforge/embookshelf/internal/storage"
)

// Config carries the construction-time parameters for a Backend.
type Config struct {
    // Endpoint is the S3 service endpoint. Empty for AWS regional
    // endpoints; set for minio / R2 / B2 / Wasabi.
    Endpoint string
    // Region is required for AWS; ignored by some compatible services
    // but a placeholder ("us-east-1") is safe.
    Region string
    // Bucket is required.
    Bucket string
    // Prefix is the optional library-prefix inside the bucket. Leading
    // and trailing slashes are normalized away.
    Prefix string
    // AccessKeyID + SecretAccessKey are static credentials. When
    // empty, the AWS default credential chain is used (env vars,
    // shared config, IRSA, etc.).
    AccessKeyID     string
    SecretAccessKey string
    // ForcePathStyle is needed for minio and most non-AWS S3-compat
    // backends. AWS itself accepts both virtual-host and path-style.
    ForcePathStyle bool
    // SkipValidation, when true, skips the bucket-versioning + SSE
    // checks at construction. Test-only.
    SkipValidation bool
}

// Backend is the storage.Storage implementation for S3.
type Backend struct {
    cli      *s3.Client
    bucket   string
    prefix   string // normalized: no leading slash, single trailing slash if non-empty
    presign  *s3.PresignClient
    capabil  storage.Capability
}

// New constructs a Backend. Validates bucket versioning + SSE unless
// Config.SkipValidation is set.
func New(ctx context.Context, cfg Config) (*Backend, error) {
    if cfg.Bucket == "" {
        return nil, errors.New("s3: bucket is required")
    }
    awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
        awsconfig.WithRegion(orDefault(cfg.Region, "us-east-1")),
    )
    if err != nil { return nil, fmt.Errorf("s3 load aws config: %w", err) }
    if cfg.AccessKeyID != "" && cfg.SecretAccessKey != "" {
        awsCfg.Credentials = credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")
    }

    cli := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
        if cfg.Endpoint != "" {
            o.BaseEndpoint = &cfg.Endpoint
        }
        o.UsePathStyle = cfg.ForcePathStyle
    })

    b := &Backend{
        cli:    cli,
        bucket: cfg.Bucket,
        prefix: normalizePrefix(cfg.Prefix),
        capabil: storage.CapConditional | storage.CapVersioning |
                 storage.CapPresign | storage.CapRange,
    }
    b.presign = s3.NewPresignClient(cli)

    if !cfg.SkipValidation {
        if err := b.validateBucket(ctx); err != nil {
            return nil, err
        }
    }
    return b, nil
}

func (b *Backend) Capabilities() storage.Capability { return b.capabil }

// validateBucket calls GetBucketVersioning + GetBucketEncryption and
// fails fast when the bucket is misconfigured.
func (b *Backend) validateBucket(ctx context.Context) error {
    vrsn, err := b.cli.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: &b.bucket})
    if err != nil { return fmt.Errorf("s3 versioning probe: %w", err) }
    if vrsn.Status != types.BucketVersioningStatusEnabled {
        return fmt.Errorf("s3 bucket %q must have versioning enabled", b.bucket)
    }
    if _, err := b.cli.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: &b.bucket}); err != nil {
        // Some compat backends omit GetBucketEncryption; treat as soft fail.
        // Real AWS S3 buckets must have it.
    }
    return nil
}

func normalizePrefix(p string) string {
    p = strings.TrimSpace(p)
    p = strings.Trim(p, "/")
    if p == "" { return "" }
    return p + "/"
}

func orDefault(s, d string) string {
    if s == "" { return d }
    return s
}

// keyFor returns the full bucket-relative key (prefix + relative).
func (b *Backend) keyFor(rel string) string {
    return b.prefix + strings.TrimLeft(rel, "/")
}

// stripPrefix is the inverse of keyFor; used by List to return
// caller-relative keys.
func (b *Backend) stripPrefix(full string) string {
    if b.prefix == "" { return full }
    return strings.TrimPrefix(full, b.prefix)
}
```

Add `strings` to imports. Ensure no unused.

Commit:
```bash
git commit -m "feat(storage/s3): Backend skeleton + bucket validation"
```

---

### Task 3: Implement methods (List, Head, Get, Put, Delete, Copy)

**Files:**
- Create: `internal/storage/s3/methods.go`
- Create: `internal/storage/s3/iter.go`

`List`:

```go
// List returns an iterator over objects under prefix.
func (b *Backend) List(ctx context.Context, prefix string) (storage.Iterator, error) {
    fullPrefix := b.keyFor(prefix)
    p := s3.NewListObjectsV2Paginator(b.cli, &s3.ListObjectsV2Input{
        Bucket: &b.bucket,
        Prefix: &fullPrefix,
    })
    return &s3Iter{b: b, p: p}, nil
}
```

`Head`:

```go
func (b *Backend) Head(ctx context.Context, key string) (storage.ObjectInfo, error) {
    out, err := b.cli.HeadObject(ctx, &s3.HeadObjectInput{
        Bucket: &b.bucket,
        Key:    aws.String(b.keyFor(key)),
    })
    if err != nil {
        var nf *types.NotFound
        if errors.As(err, &nf) {
            return storage.ObjectInfo{}, errors.Join(storage.ErrNotFound, err)
        }
        return storage.ObjectInfo{}, err
    }
    return storage.ObjectInfo{
        Key:         key,
        Size:        valueOr(out.ContentLength, 0),
        ETag:        strings.Trim(strValue(out.ETag), "\""),
        ModTime:     valueOr(out.LastModified, time.Time{}),
        ContentType: strValue(out.ContentType),
    }, nil
}
```

`Get`:

```go
func (b *Backend) Get(ctx context.Context, key string, opts ...storage.GetOption) (io.ReadCloser, error) {
    o := storage.ApplyGet(opts)
    in := &s3.GetObjectInput{
        Bucket: &b.bucket,
        Key:    aws.String(b.keyFor(key)),
    }
    if o.RangeSet {
        end := o.RangeOffset + o.RangeLength - 1
        if o.RangeLength <= 0 {
            in.Range = aws.String(fmt.Sprintf("bytes=%d-", o.RangeOffset))
        } else {
            in.Range = aws.String(fmt.Sprintf("bytes=%d-%d", o.RangeOffset, end))
        }
    }
    out, err := b.cli.GetObject(ctx, in)
    if err != nil {
        var nk *types.NoSuchKey
        if errors.As(err, &nk) {
            return nil, errors.Join(storage.ErrNotFound, err)
        }
        return nil, err
    }
    return out.Body, nil
}
```

`Put` — including conditional support:

```go
func (b *Backend) Put(ctx context.Context, key string, r io.Reader, opts ...storage.PutOption) (storage.PutResult, error) {
    o := storage.ApplyPut(opts)

    // S3 PutObject requires an io.ReadSeeker for retries unless we use
    // the manager. For now, buffer the reader.
    body, err := io.ReadAll(r)
    if err != nil { return storage.PutResult{}, err }

    in := &s3.PutObjectInput{
        Bucket: &b.bucket,
        Key:    aws.String(b.keyFor(key)),
        Body:   bytes.NewReader(body),
    }
    if o.ContentType != "" { in.ContentType = aws.String(o.ContentType) }
    if o.IfMatchSet { in.IfMatch = aws.String(o.IfMatch) }
    if o.IfNoneMatchSet { in.IfNoneMatch = aws.String(o.IfNoneMatch) }

    out, err := b.cli.PutObject(ctx, in)
    if err != nil {
        // S3 returns PreconditionFailed for IfMatch mismatch.
        var pre *types.PreconditionFailed
        if errors.As(err, &pre) {
            return storage.PutResult{}, errors.Join(storage.ErrPreconditionFailed, err)
        }
        return storage.PutResult{}, err
    }
    return storage.PutResult{
        ETag:      strings.Trim(strValue(out.ETag), "\""),
        VersionID: strValue(out.VersionId),
    }, nil
}
```

`Delete` (with optional version targeting):

```go
func (b *Backend) Delete(ctx context.Context, key string, opts ...storage.DeleteOption) error {
    o := storage.ApplyDelete(opts)
    in := &s3.DeleteObjectInput{Bucket: &b.bucket, Key: aws.String(b.keyFor(key))}
    if o.VersionID != "" { in.VersionId = &o.VersionID }
    _, err := b.cli.DeleteObject(ctx, in)
    if err != nil {
        var nk *types.NoSuchKey
        if errors.As(err, &nk) { return nil }
        return err
    }
    return nil
}
```

`Copy`:

```go
func (b *Backend) Copy(ctx context.Context, src, dst string) (storage.CopyResult, error) {
    out, err := b.cli.CopyObject(ctx, &s3.CopyObjectInput{
        Bucket:     &b.bucket,
        CopySource: aws.String(fmt.Sprintf("%s/%s", b.bucket, b.keyFor(src))),
        Key:        aws.String(b.keyFor(dst)),
    })
    if err != nil {
        var nk *types.NoSuchKey
        if errors.As(err, &nk) {
            return storage.CopyResult{}, errors.Join(storage.ErrNotFound, err)
        }
        return storage.CopyResult{}, err
    }
    return storage.CopyResult{
        ETag: strings.Trim(strValue(out.CopyObjectResult.ETag), "\""),
    }, nil
}
```

`s3Iter` (`iter.go`):

```go
type s3Iter struct {
    b   *Backend
    p   *s3.ListObjectsV2Paginator
    buf []types.Object
}

func (it *s3Iter) Next(ctx context.Context) (storage.ObjectInfo, error) {
    for len(it.buf) == 0 {
        if !it.p.HasMorePages() {
            return storage.ObjectInfo{}, io.EOF
        }
        page, err := it.p.NextPage(ctx)
        if err != nil { return storage.ObjectInfo{}, err }
        it.buf = page.Contents
    }
    obj := it.buf[0]
    it.buf = it.buf[1:]
    return storage.ObjectInfo{
        Key:     it.b.stripPrefix(strValue(obj.Key)),
        Size:    valueOr(obj.Size, 0),
        ETag:    strings.Trim(strValue(obj.ETag), "\""),
        ModTime: valueOr(obj.LastModified, time.Time{}),
    }, nil
}

func (it *s3Iter) Close() error { it.buf = nil; return nil }
```

Helpers:

```go
func strValue(p *string) string { if p == nil { return "" }; return *p }
func valueOr[T any](p *T, def T) T { if p == nil { return def }; return *p }
```

Commit:
```bash
git commit -m "feat(storage/s3): List/Head/Get/Put/Delete/Copy via aws-sdk-go-v2"
```

---

### Task 4: Presign + integration tests

**Files:**
- Create: `internal/storage/s3/presign.go`
- Create: `internal/storage/s3/integration_test.go`

`presign.go`:

```go
// PresignGet returns a signed URL for direct GET access. ttl is
// clamped to [1m, 7d] (S3's hard limits).
func (b *Backend) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
    if ttl < time.Minute { ttl = time.Minute }
    if ttl > 7*24*time.Hour { ttl = 7 * 24 * time.Hour }
    req, err := b.presign.PresignGetObject(ctx, &s3.GetObjectInput{
        Bucket: &b.bucket,
        Key:    aws.String(b.keyFor(key)),
    }, s3.WithPresignExpires(ttl))
    if err != nil { return "", err }
    return req.URL, nil
}
```

Integration test (skipped when `TEST_S3_ENDPOINT` unset):

```go
//go:build s3integration

package s3_test

func TestS3Backend_Contract(t *testing.T) {
    endpoint := os.Getenv("TEST_S3_ENDPOINT")
    if endpoint == "" { t.Skip("set TEST_S3_ENDPOINT to run") }
    bucket := os.Getenv("TEST_S3_BUCKET"); if bucket == "" { bucket = "embookshelf-test" }

    storagetest.RunSuite(t, func(t *testing.T) (storage.Storage, func()) {
        b, err := s3.New(t.Context(), s3.Config{
            Endpoint:        endpoint,
            Region:          "us-east-1",
            Bucket:          bucket,
            Prefix:          fmt.Sprintf("test-%d/", time.Now().UnixNano()),
            AccessKeyID:     os.Getenv("TEST_S3_AK"),
            SecretAccessKey: os.Getenv("TEST_S3_SK"),
            ForcePathStyle:  true,
            SkipValidation:  true,
        })
        if err != nil { t.Fatal(err) }
        return b, func() { /* leaving scratch prefix; CI bucket recycles */ }
    })
}
```

Build tag `s3integration` keeps the test file out of the default `go test ./...` so missing minio doesn't break CI. Run with `go test -tags s3integration ./internal/storage/s3/`.

Commit:
```bash
git commit -m "feat(storage/s3): presign + minio integration tests (build-tagged)"
```

---

## Phase 3 — Boot Wiring

### Task 5: `LoadStorageBackends` + SQLite+S3 refusal

**Files:**
- Create: `internal/config/storage.go`
- Create: `internal/config/storage_test.go`

```go
// LoadStorageBackends reads storage_backends rows and constructs a
// Storage instance per row. Returns the map keyed by backend id plus
// the default backend (the first local one, used pre-backfill).
//
// Refuses SQLite + any S3 backend per spec §10 (multi-instance S3
// requires Postgres).
func LoadStorageBackends(ctx context.Context, repo *repo.StorageBackendRepo, dialect db.Dialect) (storage.Resolver, error) {
    rows, err := repo.List(ctx)
    if err != nil { return nil, err }

    backends := make(map[string]storage.Storage, len(rows))
    var defaultStore storage.Storage
    var hasS3 bool

    for _, row := range rows {
        switch row.Kind {
        case "local":
            root, _ := row.Config["root"].(string)
            if root == "" { return nil, fmt.Errorf("storage_backends/%s: missing config.root", row.ID) }
            ls, err := local.New(root)
            if err != nil { return nil, err }
            backends[row.ID] = ls
            if defaultStore == nil { defaultStore = ls }
        case "s3":
            hasS3 = true
            cfg, err := s3ConfigFromRow(row)
            if err != nil { return nil, err }
            sb, err := s3.New(ctx, cfg)
            if err != nil { return nil, fmt.Errorf("storage_backends/%s: %w", row.ID, err) }
            backends[row.ID] = sb
            if defaultStore == nil { defaultStore = sb }
        default:
            return nil, fmt.Errorf("storage_backends/%s: unknown kind %q", row.ID, row.Kind)
        }
    }

    if hasS3 && dialect == db.DialectSQLite {
        return nil, errors.New("storage: SQLite cannot host S3 backends — switch to Postgres (spec §10)")
    }

    if defaultStore == nil {
        // Fall back to the legacy LocalFS rooted at "/" so single-
        // library, pre-Plan-B deployments still boot.
        defaultStore, _ = local.New("/")
    }
    return &storage.MapResolver{Default: defaultStore, Backends: backends}, nil
}

func s3ConfigFromRow(row model.StorageBackend) (s3.Config, error) {
    cfg := s3.Config{}
    cfg.Bucket, _ = row.Config["bucket"].(string)
    cfg.Region, _ = row.Config["region"].(string)
    cfg.Endpoint, _ = row.Config["endpoint"].(string)
    cfg.Prefix, _ = row.Config["prefix"].(string)
    cfg.AccessKeyID, _ = row.Config["access_key_id"].(string)
    cfg.SecretAccessKey, _ = row.Config["secret_access_key"].(string)
    if v, ok := row.Config["force_path_style"].(bool); ok { cfg.ForcePathStyle = v }
    if cfg.Bucket == "" { return cfg, errors.New("missing bucket") }
    return cfg, nil
}
```

Tests:
- 0 rows → resolver returns the legacy LocalFS rooted at "/" as default.
- One local row → resolver routes its id to a LocalFS at the configured root.
- One S3 row + SQLite dialect → error mentioning "switch to Postgres".
- Bad kind → error.
- One S3 row + Postgres dialect (skipped without TEST_S3_ENDPOINT) → backend constructed.

Commit:
```bash
git commit -m "feat(config): LoadStorageBackends + SQLite+S3 refusal"
```

---

### Task 6: Wire `Resolver` through main.go and consumers

**Files modified:**
- `cmd/embookshelf/main.go`
- `internal/queue/queue.go`, `internal/queue/sqlite.go`
- `internal/task/library_scan.go`
- `internal/task/files_backfill.go`
- `internal/task/bookdrop.go`

In `main.go`, replace the `fileStorage` construction with:

```go
resolver, err := config.LoadStorageBackends(ctx, repo.NewStorageBackendRepo(dbh), dbh.Dialect)
if err != nil {
    slog.Error("storage backends", "err", err)
    os.Exit(1)
}
```

Pass `resolver` everywhere `fileStorage` is passed today:
- `queue.New(ctx, dbh, bdropSvc, libSvc, resolver, fileRepo)` — change the signature; both Postgres and SQLite queues forward.
- `task.LibraryScanDeps.Resolver = resolver` (replace `Storage`).
- `task.BookDropDeps.Resolver = resolver`.
- `task.FilesBackfillDeps.Resolver = resolver`.
- `task.RunCoversBackfill` unchanged (covers stay local).

Inside each consumer:

```go
// library_scan.go
store, err := deps.Resolver.Resolve(orZero(lib.BackendID))
if err != nil {
    slog.Warn("scan: backend resolve failed", "lib", lib.ID, "err", err)
    return legacyScan(ctx, lib, deps)
}
// … use `store` everywhere we used `deps.Storage` before
```

`orZero(p *string)` returns `*p` or `""`.

`files_backfill.go`: iterates pending files; for each, looks up the library, resolves its backend, hashes via that backend.

`bookdrop.go`: uses the bookdrop item's library_id (which the bookdrop service can carry through) to resolve. If unknown, falls back to the default resolver.

The `internal/queue/sqlite_test.go` call signature evolves; pass `nil` for the resolver. Each consumer's nil-resolver guard short-circuits as today.

Commit:
```bash
git commit -m "refactor: pass storage.Resolver through scan/bookdrop/backfill consumers"
```

---

## Phase 4 — Verification & PR

### Task 7: Final pass

- [ ] `make ci-local` — green.
- [ ] `go test -tags s3integration ./internal/storage/s3/` if TEST_S3_ENDPOINT is set.
- [ ] `git diff --stat origin/main..HEAD` confirms scope.
- [ ] Push, open PR.

---

## Self-Review

**Spec coverage:**
- §2.1 storage interface → S3 implements all six core methods + capability-gated Presign and Range.
- §2.2 S3-specific notes → bucket versioning + SSE validated at construction; one-bucket-per-environment honored via Config.Prefix.
- §3.2 S3 layout → no empty directory marker objects (List uses ListObjectsV2 native handling).
- §5.2 ETag isn't enough → ETags returned as observed; content_hash from Plan B remains authoritative.
- §6.2 conditional PUT (S3) → IfMatch/IfNoneMatch wired through; ErrPreconditionFailed surfaces.
- §10 SQLite+S3 refusal → enforced at config-load.

**Risks:**
- aws-sdk-go-v2 adds ~6 MB of compiled dep. Acceptable — every Go project that touches AWS uses it.
- `io.ReadAll` in Put buffers the whole object in memory. Books can be 100s of MB. Acceptable for now; the upload manager's streaming path is a follow-up.
- Bucket-validation HTTP calls at boot delay startup ~100ms per S3 backend. Acceptable; the fail-fast trade-off is worth it.
- Multipart-upload absence caps single-PUT at 5 GB. Documented limitation.
- The MultipartUpload manager is also what gives us streaming; without it, we buffer. Trade-off.
- LocalFS Capabilities() still returns 0 — Range/Conditional aren't supported. After Plan F lands, callers that walk capability bits per backend behave correctly: S3 supports Range, Local doesn't. Plan G adapts the handler accordingly.

**Type consistency:** `Backend`, `Config`, `New`, `Resolver`, `MapResolver`, `ConstantResolver`, `LoadStorageBackends`, `Capabilities`, `PresignGet` consistent across files.
