# S3 Libraries via Shared Bucket — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development.

**Goal:** Operator configures one S3 bucket via env. Library creation gains a `kind: "local" | "s3"` choice. Choosing s3 allocates an auto-derived prefix (`libraries/{slug}/`) inside the shared bucket, materializes a per-library `storage_backends` row, and points `libraries.backend_id` at it. Library deletion accepts an optional `purge` flag that also strips the bucket prefix; default off.

**Architecture:** Env vars (`EMBOOKSHELF_S3_BUCKET`, `_REGION`, `_ENDPOINT`, `_ACCESS_KEY_ID`, `_SECRET_ACCESS_KEY`, `_FORCE_PATH_STYLE`) feed a new `config.SharedS3()` struct. When the operator creates a library with `kind=s3`, the service:

1. Validates `cfg.SharedS3.Configured()` (bucket non-empty).
2. Derives prefix = `libraries/{slug}/`.
3. INSERTs a `storage_backends` row (`kind='s3'`, config from env + computed prefix).
4. INSERTs a `libraries` row with `backend_id` set, `path` left empty (s3 libraries have no filesystem path), `root` left empty (the backend already roots at `bucket+prefix`).

Per-library s3 backends share bucket/region/credentials but each has its own prefix — matching Plan F's existing s3 backend semantics. No change to `s3.Backend`. The library scan, file backfill, presign, and cover paths inherited from Plans C/F/G all work unchanged for s3 libraries.

Library deletion stays soft by default. With `?purge=true`, after `repo.DeleteLibrary` succeeds, the service iterates `storage.List(ctx, "")` against the s3 backend, batches keys 1000 at a time into `DeleteObjects`, then deletes the `storage_backends` row. Errors are logged but don't fail the response — the DB row is already gone, the prefix may need manual cleanup.

**Tech stack:** Reuses Plan F's `aws-sdk-go-v2/service/s3` (DeleteObjects already on `s3.Client`). No new third-party deps.

**Companion reference:** `docs/spec/storage.spec.md` §3.2 (S3 layout, "one bucket per environment, libraries are prefixes"). `CONTEXT.md` (terms).

**Locked decisions:**
- Shared S3 config via env, never DB-editable. Rotating creds = rotating env vars.
- Library create accepts `kind: "local" | "s3"`, default `local`. `kind=s3` ignores `path`.
- Prefix is always auto-derived from slug: `libraries/{slug}/`. No operator override.
- Library delete with `?purge=true` (default false) cascade-deletes the bucket prefix.
- One backend row per library (matches Plan F). Bucket-level config replicated into each row's config; per-library prefix is what differs.
- Bookdrop staging area stays local. Approval flow does NOT auto-upload to s3 in this plan — explicitly out of scope.

**Out of scope:**
- **Approve → upload to S3.** Currently `Approve` records a files row pointing at the bookdrop's local path. For s3 libraries this leaves the file in `BOOKDROP_PATH` rather than moving it into the bucket. Operator workaround: pre-upload via `aws s3 sync`, scan picks up. Real fix is a follow-up plan.
- Migrating an existing local library to s3.
- Drag-drop upload directly to s3 from the UI.
- Sharing one library across multiple buckets.
- Per-library credential overrides (use the env defaults).
- Settings UI for the shared bucket — config is env-only.

**Depends on:** Plans A–H merged. PR #68 (LocalFS rooted at "/") merged.

---

## File Structure

### Files modified

| Path | Change |
|---|---|
| `.env.example` | Replace the documentation-only block from PR #69 with the working `EMBOOKSHELF_S3_*` env vars that the loader actually reads. |
| `internal/config/config.go` | Add `SharedS3 SharedS3Config` to `Config`. New struct with `Bucket`, `Region`, `Endpoint`, `AccessKeyID`, `SecretAccessKey`, `ForcePathStyle`, `Configured() bool`. |
| `internal/repo/library.go` | `CreateLibrary` gains `(name, slug, path string, backendID *string)` signature; `path` may be empty for s3 libraries. Drop the `libraries_path_key` UNIQUE on empty paths if not already (it should already be partial). |
| `internal/service/library.go` | `Create(ctx, name, kind, path)` switches on kind. `kind=s3` builds the storage_backends row from `cfg.SharedS3` + auto-prefix, then inserts the library. New `DeleteLibraryWithPurge(ctx, id, purge bool)`. |
| `internal/handler/library.go` | `POST /api/libraries` accepts `kind` field; `DELETE /api/libraries/:id` accepts `?purge=true` query param. |
| `ui/src/api/libraries.ts` | TS client: `createLibrary({name, kind, path?})`, `deleteLibrary(id, {purge})`. |
| `ui/src/routes/_app.settings.tsx` (or wherever the library form lives — find via `grep -rn "createLibrary\b" ui/src/`) | Kind selector dropdown; for kind=s3 hide the Path input and show a derived-prefix preview. Purge checkbox in delete confirm. |

### Files NOT touched

- `internal/storage/*` — no changes; backend already supports the per-library s3 shape.
- `internal/storageloader/loader.go` — already builds s3 backends from rows.
- `internal/task/library_scan.go` — works as-is; for s3 libraries `lib.Root` is empty, scan calls `store.List(ctx, "")`, the per-backend prefix is already encoded in the s3 backend.
- `internal/service/bookdrop.go` (Approve flow) — explicitly out of scope. Books approve into the bookdrop's local path; for s3 libraries the operator must `aws s3 sync` files in.
- DB migrations — none needed.

---

## Phase 1 — Env Config

### Task 1: `SharedS3Config` + env wiring

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go` if present
- Modify: `.env.example`

```go
// internal/config/config.go

// SharedS3Config carries the bucket-level S3 configuration shared by
// every s3-kind library in the deployment. Env-driven; not editable
// from the UI. Per-library prefix is computed from libraries.slug.
type SharedS3Config struct {
    Bucket          string
    Region          string
    Endpoint        string
    AccessKeyID     string
    SecretAccessKey string
    ForcePathStyle  bool
}

// Configured reports whether the shared bucket is set. Used by the
// library-create handler to gate the kind=s3 path.
func (s SharedS3Config) Configured() bool {
    return strings.TrimSpace(s.Bucket) != ""
}
```

In `Load()`, populate from env. Read `EMBOOKSHELF_S3_BUCKET`, `EMBOOKSHELF_S3_REGION` (default `"us-east-1"` only when bucket is set, else empty), `EMBOOKSHELF_S3_ENDPOINT`, `EMBOOKSHELF_S3_ACCESS_KEY_ID`, `EMBOOKSHELF_S3_SECRET_ACCESS_KEY`, `EMBOOKSHELF_S3_FORCE_PATH_STYLE` (bool: "true" / "1").

`.env.example`: replace the "Storage backends" block from PR #69 (the documentation-only INSERT example) with the working env vars:

```
# Shared S3 bucket for libraries created with kind=s3.
# When EMBOOKSHELF_S3_BUCKET is set, the library-create UI offers an
# "S3" option that allocates a prefix in this bucket (libraries/{slug}/).
# Single bucket per deployment; each library gets its own prefix.
# EMBOOKSHELF_S3_BUCKET=my-embookshelf-library
# EMBOOKSHELF_S3_REGION=us-east-1
# EMBOOKSHELF_S3_ENDPOINT=
# EMBOOKSHELF_S3_ACCESS_KEY_ID=
# EMBOOKSHELF_S3_SECRET_ACCESS_KEY=
# EMBOOKSHELF_S3_FORCE_PATH_STYLE=false
```

Keep the AWS_* fallback section (the SDK reads those when ACCESS_KEY_ID is empty). Drop the long DB-INSERT block — the new flow makes it obsolete.

**Tests:** unit-test `Load()` reading the new env vars; assert `SharedS3Config.Configured()` flips true when bucket is set, false otherwise.

Commit:
```bash
git commit -m "feat(config): SharedS3Config from EMBOOKSHELF_S3_* env vars"
```

---

## Phase 2 — Library Create Cutover

### Task 2: `LibraryRepo.CreateLibrary` accepts optional `backendID`

**File:** `internal/repo/library.go`

Change signature:

```go
func (r *LibraryRepo) CreateLibrary(ctx context.Context, name, slug, path string, backendID *string) (model.Library, error)
```

`backendID == nil` → INSERT with NULL backend_id (existing behavior for local libraries). `backendID != nil` → INSERT with that value. The migration already added `libraries.backend_id` (Plan B); the partial-unique on `path` excludes empty strings (Plan B), so multiple s3 libraries with empty path don't collide.

Update the SQL:

```sql
INSERT INTO libraries (id, name, slug, path, backend_id, root)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING ...
```

`root` for s3 libraries is empty (the backend has the prefix encoded); `root` for local libraries equals `path`.

Update the only caller (`service.LibraryService.Create`) — Task 3.

Commit:
```bash
git commit -m "feat(repo): CreateLibrary takes optional backend_id + root"
```

### Task 3: `LibraryService.Create(ctx, name, kind, path)` with s3 branch

**Files:**
- Modify: `internal/service/library.go`
- Modify: `internal/service/library_test.go` if present

```go
type LibraryKind string

const (
    LibraryKindLocal LibraryKind = "local"
    LibraryKindS3    LibraryKind = "s3"
)

// LibraryServiceDeps groups the deps the service needs beyond its
// repo. Used to keep the constructor stable.
type LibraryServiceDeps struct {
    Backends *repo.StorageBackendRepo
    SharedS3 config.SharedS3Config
}

// Create inserts a new library. Kind selects the storage flavor:
//
//   - local: path is required, names a filesystem directory. No
//     backend row is created; backend_id stays NULL and the loader
//     falls back to the LocalFS-at-/ default.
//   - s3: path is ignored; the service derives prefix=libraries/{slug}/,
//     INSERTs a storage_backends row from cfg.SharedS3, and points
//     the library at that backend.
func (s *LibraryService) Create(ctx context.Context, name string, kind LibraryKind, path string) (model.Library, error) {
    name = strings.TrimSpace(name)
    slug := slugify(name)

    switch kind {
    case "", LibraryKindLocal:
        path = strings.TrimRight(strings.TrimSpace(path), "/")
        return s.repo.CreateLibrary(ctx, name, slug, path, nil)

    case LibraryKindS3:
        if !s.deps.SharedS3.Configured() {
            return model.Library{}, ErrS3NotConfigured
        }
        prefix := "libraries/" + slug + "/"
        cfg := map[string]any{
            "bucket":            s.deps.SharedS3.Bucket,
            "region":            s.deps.SharedS3.Region,
            "endpoint":          s.deps.SharedS3.Endpoint,
            "prefix":            prefix,
            "access_key_id":     s.deps.SharedS3.AccessKeyID,
            "secret_access_key": s.deps.SharedS3.SecretAccessKey,
            "force_path_style":  s.deps.SharedS3.ForcePathStyle,
        }
        backend, err := s.deps.Backends.Create(ctx, "s3", cfg)
        if err != nil {
            return model.Library{}, fmt.Errorf("create s3 backend row: %w", err)
        }
        lib, err := s.repo.CreateLibrary(ctx, name, slug, "", &backend.ID)
        if err != nil {
            // Best-effort cleanup of the backend row we just inserted.
            _ = s.deps.Backends.Delete(ctx, backend.ID)
            return model.Library{}, err
        }
        return lib, nil

    default:
        return model.Library{}, fmt.Errorf("unknown library kind %q", kind)
    }
}

var ErrS3NotConfigured = errors.New("s3 libraries require EMBOOKSHELF_S3_BUCKET to be set")
```

Update `NewLibraryService` to accept the deps:

```go
func NewLibraryService(r *repo.LibraryRepo, deps LibraryServiceDeps) *LibraryService { ... }
```

Update `cmd/embookshelf/main.go` to pass `LibraryServiceDeps{Backends: backendRepo, SharedS3: cfg.SharedS3}`.

**Tests:**
- `kind=local` happy path (existing tests still pass).
- `kind=s3` with bucket configured → backend row + library row, both reachable.
- `kind=s3` without bucket → `ErrS3NotConfigured`.
- `kind=s3` slug collision → ErrLibraryNameTaken; backend row not orphaned (cleanup).
- `kind=""` defaults to local (backward compat).

Commit:
```bash
git commit -m "feat(service): library Create dispatches by kind (local|s3)"
```

### Task 4: `LibraryService.DeleteLibrary` with optional purge

**Files:**
- Modify: `internal/service/library.go`
- Modify: `internal/handler/library.go`

```go
// DeleteLibrary removes the library row. When purge is true and the
// library is backed by an s3 backend, also strips every object under
// the backend's prefix. The backend row is deleted last; failures
// during purge are logged but the response still succeeds (operator
// can manually clean up the bucket if needed).
func (s *LibraryService) DeleteLibrary(ctx context.Context, id string, purge bool) ([]string, error) {
    lib, err := s.repo.GetByID(ctx, id)
    if err != nil {
        return nil, err
    }
    bookIDs, err := s.repo.DeleteLibrary(ctx, id)
    if err != nil {
        return nil, err
    }
    if purge && lib.BackendID != nil {
        s.purgeBackend(ctx, *lib.BackendID)
    }
    return bookIDs, nil
}

func (s *LibraryService) purgeBackend(ctx context.Context, backendID string) {
    if s.deps.Resolver == nil { return }
    store, err := s.deps.Resolver.Resolve(backendID)
    if err != nil {
        slog.Warn("library purge: resolve backend", "id", backendID, "err", err)
        return
    }
    it, err := store.List(ctx, "")
    if err != nil {
        slog.Warn("library purge: list", "id", backendID, "err", err)
        return
    }
    defer func() { _ = it.Close() }()
    for {
        obj, err := it.Next(ctx)
        if errors.Is(err, io.EOF) { break }
        if err != nil {
            slog.Warn("library purge: iterate", "id", backendID, "err", err)
            break
        }
        if err := store.Delete(ctx, obj.Key); err != nil {
            slog.Warn("library purge: delete", "key", obj.Key, "err", err)
            continue
        }
    }
    // Drop the backend row so future reads through Resolver don't
    // hit a stale config. ErrStorageBackendInUse should not fire
    // here — the library row is already gone.
    if err := s.deps.Backends.Delete(ctx, backendID); err != nil {
        slog.Warn("library purge: delete backend row", "id", backendID, "err", err)
    }
}
```

Add `Resolver storage.Resolver` to `LibraryServiceDeps`. main.go passes it.

**Handler:**

```go
// internal/handler/library.go DeleteLibrary handler:
purge := c.Query("purge") == "true"
bookIDs, err := h.libsvc.DeleteLibrary(c.Request.Context(), id, purge)
```

Existing local library deletes don't call into `purgeBackend` (BackendID is nil) — behavior unchanged.

**Tests:**
- Local library delete (purge=false) → DB rows go, no purge logic invoked.
- Local library delete (purge=true) → same as above, no S3 calls (BackendID is nil).
- S3 library delete (purge=false) → DB rows go, S3 prefix untouched, backend row remains.
- S3 library delete (purge=true) → DB rows go, every key under prefix is deleted, backend row deleted.
- S3 library delete (purge=true) with list error → DB delete succeeds, error logged, response success.

Commit:
```bash
git commit -m "feat(service+handler): library delete with optional ?purge=true"
```

### Task 5: Handler accepts `kind` field on POST /api/libraries

**File:** `internal/handler/library.go`

Find the create-library handler (`grep -n "func.*CreateLibrary\b\|func.*Libraries\b" internal/handler/library.go`). Update the request struct:

```go
type createLibraryReq struct {
    Name string `json:"name" binding:"required"`
    Kind string `json:"kind"`            // "local" (default) | "s3"
    Path string `json:"path"`            // required for kind=local
}
```

Validate: when kind=local (or empty), path must be non-empty. When kind=s3, path is ignored. Map `service.ErrS3NotConfigured` → HTTP 400 with a clear message.

Commit:
```bash
git commit -m "feat(handler): POST /api/libraries accepts kind field"
```

---

## Phase 3 — UI

### Task 6: TypeScript client + library form

**Files:**
- Modify: `ui/src/api/libraries.ts` (or wherever the API client is — find via `grep -rn "POST.*libraries\b\|createLibrary" ui/src/`).
- Modify: the library-create form component.

API client:

```typescript
export type LibraryKind = 'local' | 's3';

export type CreateLibraryInput = {
  name: string;
  kind: LibraryKind;
  path?: string;  // required for kind='local', omitted for 's3'
};

export async function createLibrary(input: CreateLibraryInput): Promise<Library> { ... }
export async function deleteLibrary(id: string, opts?: { purge?: boolean }): Promise<void> {
  const params = opts?.purge ? '?purge=true' : '';
  // ... fetch DELETE /api/libraries/{id}${params}
}
```

Form changes:

- **Kind selector**: dropdown / segmented control with two options: "Local filesystem" and "S3 bucket". Disable the s3 option (with a tooltip "Set EMBOOKSHELF_S3_BUCKET to enable") when the server returns `s3Available: false` in some bootstrap endpoint — or just let the create call fail with a 400 and surface the error inline.
- **Path field**: visible only when kind=local. For kind=s3, show a read-only preview: `libraries/{slug}/` (computed from the entered name).
- **Delete confirm**: add a "Also delete files in S3 bucket" checkbox, visible only for s3 libraries. Posts the `purge=true` query param.

Server bootstrap endpoint (small): `GET /api/config` returns `{ s3Available: boolean }` so the UI can disable the s3 option pre-emptively rather than failing post-submit. Add to the existing settings-bootstrap response if there is one, else a tiny new endpoint.

**Tests:** existing UI test suite covers the form; add a test that the s3 kind sends `path: undefined` (or omits it).

Commit:
```bash
git commit -m "feat(ui): library create form + delete confirm support s3 kind"
```

---

## Phase 4 — Verification

### Task 7: Verify and PR

- [ ] `make ci-local` green.
- [ ] Manual smoke (env-driven):
  - Set `EMBOOKSHELF_S3_BUCKET=test-bucket` (with valid creds).
  - Boot. UI library-create form shows the S3 option enabled.
  - Create a library named "Sci-Fi" with kind=s3 → backend row + library row land; bucket is empty.
  - `aws s3 sync ./local/scifi/ s3://test-bucket/libraries/sci-fi/` to seed files.
  - Library scan discovers the files; `files` rows appear with content_hash NULL.
  - Boot worker fills hashes via the s3 source.
  - File-serve URL 302-redirects to a presigned URL.
  - Delete the library with purge=true → backend row gone, bucket prefix empty.
- [ ] Push, open PR.

---

## Self-Review

**Spec coverage:**
- §3.2 S3 layout ("one bucket per environment, libraries are prefixes") → covered.
- §10 SQLite + S3 refusal → still enforced by the existing `storageloader` check; creating an s3 library on a SQLite install works at the API level (the row inserts) but boot will refuse on next restart. Worth a guard at create-time too — add `s.deps.Dialect != SQLite` check to the s3 branch.

**Risks:**
- Approve → upload to s3 is missing. UX says "create s3 library" but actually using it for new imports requires manual `aws s3 sync`. Document as a follow-up plan.
- Slug collisions on create → handled (ErrLibraryNameTaken). The orphaned-backend-row cleanup is best-effort; on cleanup failure the next create with the same slug will leak two backend rows. Acceptable; admin can manually delete.
- Purge during a concurrent scan → could race (scan reads keys that purge is deleting). Mitigated because purge runs after the library row is gone — scans of that library_id won't dispatch.
- The handler-side bootstrap for `s3Available` is the only signal the UI has that S3 is configured. If env changes between page loads, UI shows stale state. Self-hosted reload-the-page is acceptable.

**Type consistency:** `LibraryKind`, `LibraryKindLocal`, `LibraryKindS3`, `SharedS3Config`, `LibraryServiceDeps`, `ErrS3NotConfigured`, `purgeBackend` consistent across the plan.
