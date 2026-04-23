# Library Creation — Feature Specification

> Create a new Library that aggregates books from a single filesystem root, optionally enqueues an initial scan, and gives the admin a place to configure naming-pattern overrides and future rescans.

- **Status:** Shipped
- **Scope:** `embookshelf` Go backend + React (TanStack Start) SPA
- **Permission required:** `admin` role
- **Entry point:** `/settings → Libraries` panel → **New library** button → modal dialog

---

## 1. Purpose

A Library is embookshelf's top-level container for books. Creating one points the system at a filesystem root, registers it in the `libraries` table, and (optionally) fires an asynchronous scan job that walks the tree, diffs against existing books, and feeds new files into the BookDrop queue for review.

Design choices worth flagging up front:

- **One filesystem root per library**, fixed at creation. Multiple paths would race on scans and naming collisions, so the constraint is schema-enforced (`libraries.path` is `UNIQUE` where non-empty).
- **No filesystem validation at creation**. The handler accepts any non-empty string; existence, readability, and contents are verified by the scan job.
- **Scan is always async**, whether triggered at creation or via rescan. The HTTP response returns immediately and the admin watches SSE events for progress.

---

## 2. User Stories

| # | As a … | I want to … | So that … |
|---|--------|-------------|-----------|
| 1 | Admin | Create a library from a single folder | I can point embookshelf at my existing file tree |
| 2 | Admin | See the file count before committing | I know whether to expect a long scan |
| 3 | Admin | Opt in/out of the initial scan | I can wire a library ahead of time and schedule the scan separately |
| 4 | Admin | Configure a per-library naming pattern after creation | Imported files land in the conventions my tree already follows |
| 5 | Admin | Trigger a rescan on demand | New files dropped into the folder out-of-band are picked up |
| 6 | Admin | Delete a library with a typed-name confirmation | I can remove a misconfigured library without nuking real data by accident |

---

## 3. UX Flow

### 3.1 Entry points

- **Settings → Libraries panel** ([ui/src/routes/_app.settings.tsx](ui/src/routes/_app.settings.tsx)) — the **New library** button opens the creator dialog. This is the only UI surface; there is no top-bar shortcut.

### 3.2 Dialog

A single-step modal (shadcn `Dialog`) with three inputs. Submit is disabled until both `name` and `path` are non-empty after trim and the name isn't a case-insensitive duplicate of an existing library.

| Field | Required | Default | Notes |
|-------|----------|---------|-------|
| Name | yes | `""` | Trimmed; case-insensitive client-side uniqueness check against the live list |
| Path | yes | `""` | Trimmed, trailing slashes stripped. Absolute filesystem path |
| Scan on create | no | `true` | When on, an initial scan job is enqueued immediately after insert |

### 3.3 Pre-scan preview

Before submission, the admin may click **Count files** to call `POST /api/v1/settings/libraries/scan` and see how many supported files the target path contains. The response is a count only — no library is created and no `bookdrop_items` are enqueued. Used to gut-check the path before committing.

### 3.4 Submit sequence

1. Client validates name + path (non-empty, unique name).
2. Client calls `POST /api/v1/settings/libraries` with `{ name, path, scan }`.
3. On 201, dialog closes, the libraries list refetches, and a sonner toast confirms. If `scan === true`, the library appears with `lastScannedAt = null` and starts receiving SSE scan progress events within seconds.
4. On 409, a toast surfaces the conflict (name taken or path taken). The dialog stays open.

---

## 4. API Surface

All endpoints live under `/api/v1/settings` and are admin-gated by `auth.RequireRole(model.RoleAdmin)` middleware declared in [internal/handler/router.go](internal/handler/router.go:117).

### 4.1 Pre-create file count

```
POST /api/v1/settings/libraries/scan
Auth:     admin
Body:     { "path": "<string>" }
Response: { "count": <int> }
```

Walks the path with `filepath.WalkDir` and counts files whose extension clears `fileproc.IsSupported`. No side effects; no `bookdrop_items` written.

### 4.2 Create

```
POST /api/v1/settings/libraries
Auth:     admin
Body:     CreateLibraryRequest
Response: 201 Created → { "library": SettingsLibraryDTO }
Errors:   400 if name or path empty; 409 on ErrLibraryNameTaken / ErrLibraryPathTaken
```

See [internal/handler/settings.go](internal/handler/settings.go) (`SettingsLibraryCreate`).

### 4.3 Related endpoints (same panel)

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/settings/libraries` | List all libraries with scan stats + book counts |
| `POST` | `/settings/libraries/:id/rescan` | Enqueue a rescan job (202 Accepted) |
| `DELETE` | `/settings/libraries/:id` | Hard-delete library + cascade book rows (204 No Content) |
| `POST` | `/settings/libraries/pattern/preview` | Render a naming pattern against a sample row |
| `GET` | `/settings/libraries/pattern/default` | Fetch instance-wide default naming pattern |
| `PUT` | `/settings/libraries/pattern/default` | Set instance-wide default naming pattern |
| `PATCH` | `/settings/libraries/:id/file-naming-pattern` | Per-library pattern override |

### 4.4 DTOs

**`createLibraryReq`** ([internal/handler/settings.go](internal/handler/settings.go)):

```go
type createLibraryReq struct {
    Name string `json:"name"`
    Path string `json:"path"`
    Scan bool   `json:"scan"`
}
```

Validation is hand-rolled (no `validator/v10` tags): both `Name` and `Path` are trimmed, and `Path` has trailing `/` stripped before the non-empty check. A missing or empty value returns `400 Bad Request`.

**`SettingsLibraryDTO`** response shape:

```json
{
  "library": {
    "id": "<uuid>",
    "name": "string",
    "slug": "string",
    "path": "string",
    "lastScannedAt": null,
    "fileCount": 0,
    "discoveredCount": 0,
    "fileNamingPattern": null,
    "bookCount": 0,
    "createdAt": "<RFC3339>"
  }
}
```

---

## 5. Backend Logic

### 5.1 Handler sequence

`SettingsLibraryCreate` ([internal/handler/settings.go](internal/handler/settings.go)):

1. Bind JSON body; reject malformed payloads with `400`.
2. Validate `name` and `path` are non-empty after trim / slash-strip; `400` otherwise.
3. Delegate to `lib.Create(ctx, name, path)` — the service layer.
4. Map `ErrLibraryNameTaken` → `409`, `ErrLibraryPathTaken` → `409`, other errors → `500`.
5. When `body.Scan == true` and a queue is available, enqueue a River `LibraryScan` job via `h.queue.EnqueueLibraryScan(ctx, lib.ID)`. Failures here are logged as warnings but do **not** fail the response — the library already exists and the admin can rescan manually.
6. Respond `201 Created` with the `SettingsLibraryDTO`.

### 5.2 Service layer

[internal/service/library.go](internal/service/library.go):

```go
func (s *LibraryService) Create(ctx context.Context, name, path string) (model.Library, error) {
    name = strings.TrimSpace(name)
    path = strings.TrimRight(strings.TrimSpace(path), "/")
    return s.repo.CreateLibrary(ctx, name, slugify(name), path)
}
```

The service is a thin pass-through: derive a URL-safe slug from the name, then hand off to the repo. No filesystem validation, no watcher registration (we don't run `fsnotify` — the scan job is manual-trigger or on-create).

### 5.3 Repository layer

[internal/repo/library.go](internal/repo/library.go):

```sql
INSERT INTO libraries (name, slug, path)
VALUES ($1, $2, $3)
RETURNING
    id, name, slug, path,
    last_scanned_at, file_count, discovered_count,
    file_naming_pattern, created_at,
    0 AS book_count
```

Two unique indexes trip the same Postgres `23505` code:

- `libraries_slug_key` → `ErrLibraryNameTaken`
- `libraries_path_key` → `ErrLibraryPathTaken`

The handler maps both to `409 Conflict` with distinct messages.

> **PG quirk**: the `RETURNING` clause pulls every column `scanLibrary` expects, with a literal `0 AS book_count`. A brand-new library has no books, and a modifying CTE (e.g. `WITH ins AS (INSERT ...) SELECT ... FROM libraries l JOIN ins ...`) wouldn't see its own insert — per the PG manual, both share a snapshot. Single-statement `INSERT … RETURNING` is the only shape that works cleanly.

### 5.4 Scan pipeline

Initial scan is a River background job ([internal/task/library_scan.go](internal/task/library_scan.go)):

1. Load the `libraries` row by ID; read `path`.
2. `filepath.WalkDir` the tree.
3. For each file:
   - Skip if `repo.BookExistsByPath(path)` returns true — prevents re-ingesting the same file on rescan.
   - Skip if `fileproc.IsSupported(path)` returns false — extension filter (EPUB / PDF / CBZ / MOBI / FB2 / TXT today).
   - Otherwise enqueue a row in `bookdrop_items` via `BookDropService.Enqueue`; fire a River `bookdrop` job for the file processor.
   - Increment `fileCount` (supported files seen) and `discovered` (new bookdrop items created).
4. Call `LibraryRepo.TouchScan(ctx, libID, fileCount, discovered)` to stamp `last_scanned_at = now()` and the two counters on the row.
5. Broadcast SSE events (`library.scan-started`, `library.scan-completed`) so the UI can refresh without polling.

Errors from the walk are logged (`slog.Warn`) and don't abort the job — a partial scan still stamps whatever was counted. No retry / outbox / rescue — if the River worker crashes mid-walk, River retries the job from the start and dedup is handled by `BookExistsByPath`.

### 5.5 File naming patterns (post-creation)

The create endpoint does **not** accept a `fileNamingPattern` field. The column defaults to `NULL`, which means "fall back to the instance-wide default" (or to "keep the original filename" if that's blank too). Pattern configuration happens separately on the same Settings panel via:

- `PUT /settings/libraries/pattern/default` — instance default
- `PATCH /settings/libraries/:id/file-naming-pattern` — per-library override (send `null` or `""` to clear)

See [spec/file-naming-patterns.spec.md](spec/file-naming-patterns.spec.md) if present.

### 5.6 Delete

`DELETE /settings/libraries/:id` hands off to `LibraryRepo.DeleteLibrary` which runs a transactional delete:

1. Select dependent book IDs (so the handler can clean up cover files).
2. Delete the library row — FK cascades take care of `books`, `shelf_books`, `annotations`, `reading_sessions`, `user_book_progress`.
3. Handler loops over the returned book IDs and calls `coverstore.DeleteBook(id)` for each — covers are on disk, not in the DB, so the cascade can't reach them.
4. **Source files on disk are intentionally left alone.** Library paths point at user-managed filesystem roots; "unregister this library" is not the same as "wipe the bytes."

The UI guards this with a typed-name confirmation: the admin must type the library name exactly before the Delete button enables.

### 5.7 Rescan

`POST /settings/libraries/:id/rescan` just enqueues the same River job the create path uses. Returns `202 Accepted` immediately; progress is tracked via SSE. Returns `503 Service Unavailable` if the queue isn't initialized (pre-migration boot states).

---

## 6. Data Model

### 6.1 `libraries` table

Schema built up across three migrations:

**[000001_init.up.sql](internal/migrator/migrations/000001_init.up.sql):**

```sql
CREATE TABLE libraries (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT        NOT NULL,
    slug       TEXT        NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**[000016_library_naming_pattern.up.sql](internal/migrator/migrations/000016_library_naming_pattern.up.sql):** adds per-library override.

```sql
ALTER TABLE libraries ADD COLUMN file_naming_pattern TEXT;
```

**[000018_library_single_path.up.sql](internal/migrator/migrations/000018_library_single_path.up.sql):** collapses the multi-path model and adds scan-state columns.

```sql
ALTER TABLE libraries
    ADD COLUMN path             TEXT        NOT NULL DEFAULT '',
    ADD COLUMN last_scanned_at  TIMESTAMPTZ,
    ADD COLUMN file_count       INTEGER     NOT NULL DEFAULT 0,
    ADD COLUMN discovered_count INTEGER     NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX libraries_path_key
    ON libraries (path)
    WHERE path <> '';
```

The `WHERE path <> ''` partial predicate is what lets us have the column `NOT NULL DEFAULT ''` while still rejecting duplicate real paths — seed / pre-migration rows with an empty path don't trip the index.

### 6.2 Final column set

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `id` | UUID | PK, default `gen_random_uuid()` | Surface key |
| `name` | TEXT | NOT NULL | Display string; not unique at the DB level — uniqueness is via `slug` |
| `slug` | TEXT | UNIQUE NOT NULL | URL-safe derivative of `name`; collisions surface as `ErrLibraryNameTaken` |
| `path` | TEXT | NOT NULL DEFAULT `''`, partial UNIQUE | Single filesystem root, immutable post-create |
| `last_scanned_at` | TIMESTAMPTZ | nullable | NULL until first successful scan completes |
| `file_count` | INTEGER | NOT NULL DEFAULT 0 | Supported files seen on last scan |
| `discovered_count` | INTEGER | NOT NULL DEFAULT 0 | New `bookdrop_items` produced on last scan |
| `file_naming_pattern` | TEXT | nullable | Per-library override; NULL ⇒ use instance default |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT now() | Insert timestamp |

### 6.3 Model struct

[internal/model/library.go](internal/model/library.go):

```go
type Library struct {
    ID              string
    Name            string
    Slug            string
    Path            string      // immutable post-create
    LastScannedAt   *time.Time
    FileCount       int
    DiscoveredCount int
    BookCount       int         // computed via subquery in SELECTs
    CreatedAt       time.Time
    FileNamingPattern *string   // nil == "use instance default"
}
```

`BookCount` is never stored — the `libCols` SQL block carries a correlated subquery against `books WHERE library_id = l.id AND deleted_at IS NULL`. Keeps the row small; the subquery is cheap on the sizes we target.

---

## 7. Edge Cases

| Case | Outcome |
|---|---|
| Empty `name` | `400 Bad Request`; dialog keeps state |
| Empty `path` | `400 Bad Request`; dialog keeps state |
| Path with trailing slashes (`/srv/books///`) | Stripped by the service layer before insert; normalized stored path is `/srv/books` |
| Name collision (case-insensitive, in-memory) | UI blocks submission client-side |
| Name collision (server-side, via slug) | `409 Conflict` with `ErrLibraryNameTaken`; possible when two admins race, or the client bypasses the UI check |
| Path collision | `409 Conflict` with `ErrLibraryPathTaken` — two libraries cannot share one root |
| Path that doesn't exist on disk | Library row is created. Scan job runs, `filepath.WalkDir` returns immediately, `file_count = 0`, `discovered = 0`. No error surfaced to the admin beyond the empty counts on the row |
| Path becomes unreadable between create and scan | Walk errors are logged; row still gets stamped with whatever counts were reached |
| `scan = false` on create | Library row persisted; no River job enqueued. `last_scanned_at` stays `NULL` until a manual rescan |
| Queue unavailable (River not up) | Library row still created; scan-enqueue logs a warning. No response error — `lastScannedAt` stays null and the admin sees "never scanned" in the list |
| Concurrent scans on the same library | Not explicitly guarded at the service level. Each scan job calls `BookExistsByPath` per file, so a double-scan at worst re-examines files that are already imported — it won't produce duplicate `books` or `bookdrop_items` |
| Delete on a library with active scan | The DB delete cascades; the in-flight scan job will log errors on subsequent `TouchScan` / `BookDropService.Enqueue` calls and the River worker treats the job as failed (retried once or twice, then dropped) |
| Non-admin user | `403 Forbidden` from the admin middleware on the `/settings` group |

---

## 8. Validation Summary

| Layer | Rule |
|---|---|
| UI | Both fields non-empty after trim; name not a case-insensitive duplicate in the live list |
| Handler | Both fields non-empty after trim + path slash-strip; malformed JSON → 400 |
| Service | Trims inputs and generates `slug` |
| Repository | Two unique indexes (`slug`, `path`) enforce uniqueness at the DB level |
| Auth | Admin-only; enforced by `auth.RequireRole(model.RoleAdmin)` on the `/settings` group |

---

## 9. Security Considerations

- The admin supplies absolute filesystem paths; there is no sandbox. Deployments rely on the container / systemd unit's filesystem boundary to restrict reachable roots. Paths are trimmed but not normalized against a chroot.
- Admin role is required to create libraries; non-admins cannot point embookshelf at arbitrary paths.
- The pre-create count endpoint reads directory listings but does not open file bytes — the same walk function is shared with the real scan job.
- Path traversal via `..` inside the stored path is not actively blocked; the walk would resolve it but still stay under whatever the process can read. Deployments that care should run the binary under a user with a narrow home / bind-mount.
- No `fsnotify` watcher is registered, so a malicious library path can't be used to pin watchers on unrelated directories.

---

## 10. Cross-feature Interactions

- **BookDrop** — the scan job's discovered files land in `bookdrop_items`, not directly in `books`. An admin reviews and approves each item, which then creates the `books` row. See [spec/bookdrop.spec.md](spec/bookdrop.spec.md) if present.
- **File Naming Patterns** — per-library pattern lives on the library row; applied on BookDrop approval (file move + rename).
- **Cover storage** — covers live on disk under `DATA_PATH/covers/books/{bookID}` and are not owned by the `libraries` row. Delete cleans them up per-book in the handler loop.
- **SSE** — scan job broadcasts `library.scan-started` / `library.scan-completed` events that the UI listens to for cache invalidation.

---

## 11. Open / Future Work

1. **Surface scan progress** — today SSE fires start / complete only. A `{ filesScanned, total }` tick would let the UI render a real progress bar instead of an indeterminate spinner.
2. **Up-front path existence check** — creating a library pointed at a non-existent path silently produces an empty scan. A 400-level response or an inline warning on the library row would match admin expectations.
3. **`fsnotify` auto-ingest** — "drop a file, it appears in BookDrop within seconds" would be a natural extension of the scan job. The `watch` field exists in the spec world but not in ours.
4. **Concurrent-scan guard** — a `sync.Map`-backed set per library ID would short-circuit a double-rescan instead of relying on per-file dedup.
5. **Path editing** — currently immutable. Adding an edit path requires rewriting every `books.path` that pointed under the old root, which is why it's punted.
6. **Per-library allowed-formats filter** — today the scan job runs one extension set shared across all libraries (`fileproc.IsSupported`). A per-library whitelist would let a "Comics" library skip EPUBs in the same tree.

---

## 12. Key References

- Handler: [internal/handler/settings.go](internal/handler/settings.go) — `SettingsLibraryCreate`, `SettingsLibraryScan`, `SettingsLibraryRescan`, `SettingsLibraryDelete`
- Router: [internal/handler/router.go](internal/handler/router.go) (lines 117–129)
- Service: [internal/service/library.go](internal/service/library.go)
- Repository: [internal/repo/library.go](internal/repo/library.go) — `CreateLibrary`, `DeleteLibrary`, `TouchScan`
- Scan worker: [internal/task/library_scan.go](internal/task/library_scan.go)
- Model: [internal/model/library.go](internal/model/library.go)
- Migrations: [000001](internal/migrator/migrations/000001_init.up.sql), [000016](internal/migrator/migrations/000016_library_naming_pattern.up.sql), [000018](internal/migrator/migrations/000018_library_single_path.up.sql)
- UI panel: [ui/src/routes/_app.settings.tsx](ui/src/routes/_app.settings.tsx) — Libraries section + creator dialog + delete confirmation
- UI API client: [ui/src/api/settings.ts](ui/src/api/settings.ts) — `createLibrary`, `prescanLibraryPaths`, `rescanLibrary`, `deleteLibrary`
