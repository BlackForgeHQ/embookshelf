# Library Creation — Feature Specification

> Create a new Library that aggregates books from one or more filesystem paths, with per-library rules for file formats, monitoring, metadata source, and organization mode.

- **Status:** Shipped
- **Scope:** `booklore-api` (Go) + `booklore-ui` (Angular)
- **Permission required:** `canManageLibrary` **or** `isAdmin`
- **Entry point:** Topbar button (`openLibraryCreatorDialog()`) → Library Creator dialog

---

## 1. Purpose

A Library is BookLore's top-level container for books. Creating a library lets an admin point the system at one or more folders on disk, pick which file formats to accept, decide whether to watch the paths for changes, and choose how files are grouped into books. On creation the backend persists the library, optionally registers a filesystem watcher, and kicks off an asynchronous initial scan (goroutine) that ingests existing files.

---

## 2. User Stories

| # | As a … | I want to … | So that … |
|---|--------|-------------|-----------|
| 1 | Admin | Create a library from one or more folders | I can point BookLore at my existing file tree |
| 2 | Admin | Pick an icon and a name | Libraries are easy to distinguish in the UI |
| 3 | Admin | Restrict accepted file formats | My "Comics" library doesn't pick up EPUBs sitting in the same tree |
| 4 | Admin | Order format priority | When multiple files represent the same book, the preferred format wins |
| 5 | Admin | Choose whether the system watches the paths | New files dropped into the folder are auto-ingested |
| 6 | Admin | Choose a metadata source (embedded vs sidecar) | I can control where book metadata comes from per library |
| 7 | Admin | Choose an organization mode (file-per-book vs folder-per-book) | Comics / audiobooks packaged as folders are grouped correctly |
| 8 | Admin | See a file count before committing | I know whether to expect a long scan |
| 9 | Admin | Be taken straight into the library after creation | I can immediately see the ingestion in progress |

---

## 3. UX Flow

### 3.1 Entry points

- **Topbar** — `openLibraryCreatorDialog()` in `app.topbar.component.ts:166`, opened via the shared dialog launcher service (`dialog-launcher.service.ts:80`).
- **Edit mode** — the same component is reused when launched with `{ mode: 'edit', libraryId }` as dialog data. Edit mode is out of scope for this spec but is called out where its behavior diverges.

### 3.2 Dialog steps

The creator is a single dialog with multiple grouped sections (no stepper). The user must satisfy two validations to enable **Create**:

1. **Library details valid** — trimmed name is non-empty and does not collide with an existing library name.
2. **Directory selection valid** — at least one folder has been added.

### 3.3 Submit sequence

1. Frontend validates name and folder list.
2. Frontend calls `POST /api/v1/libraries/scan` with the in-progress library DTO to count processable files across the selected paths.
3. If the count ≥ 500, the UI sets a "large library loading" flag so subsequent book lists buffer smoothly.
4. Frontend calls `POST /api/v1/libraries` with the library DTO.
5. On success the dialog closes and the router navigates to `/library/{id}/books`.
6. On failure the loading flag is cleared and a toast shows the error. The dialog stays open so the user can fix input.

---

## 4. Form Fields

| Field | Type | Required | Default | Notes |
|-------|------|----------|---------|-------|
| `name` (`chosenLibraryName`) | string | **Yes** | `""` | Trimmed before validation; must be unique (checked against in-memory library list). |
| `icon` / `iconType` | `{ type: 'PRIME_NG' \| 'CUSTOM_SVG', value: string } \| null` | No | `null` | Selected via icon-picker modal. |
| `paths` (`folders`) | `[]string` | **Yes** (≥1) | `[]` | Selected via directory-picker modal. Duplicates within the form are rejected at add-time. |
| `watch` | bool | No | `false` | When `true` backend registers each path with the filesystem watcher. |
| `metadataSource` | `EMBEDDED \| SIDECAR \| PREFER_SIDECAR \| PREFER_EMBEDDED \| NONE` | No | `EMBEDDED` | Per-library metadata strategy. |
| `organizationMode` | `BOOK_PER_FILE \| BOOK_PER_FOLDER \| AUTO_DETECT` | No | `BOOK_PER_FILE` (UI default) | Backend model default is `AUTO_DETECT`; UI sends `BOOK_PER_FILE`. `AUTO_DETECT` is deprecated on the backend enum. |
| `allowAllFormats` | bool | No | `true` | UI-only toggle. When `false`, `allowedFormats` drives the payload. |
| `allowedFormats` | `[]BookFileType` | No | all formats when `allowAllFormats=true` | Empty list on the backend means "accept all". |
| `formatPriority` | `[]BookFileType` | No | default order `[EPUB, PDF, CBX, MOBI, AZW3, FB2, AUDIOBOOK]` | Drag-drop reorderable (Angular CDK). Used when multiple files represent the same book. |

Reference: [library-creator.component.ts](booklore-ui/src/app/features/library-creator/library-creator.component.ts), [library-creator.component.html](booklore-ui/src/app/features/library-creator/library-creator.component.html).

---

## 5. API Surface

### 5.1 Pre-create scan

```
POST /api/v1/libraries/scan
Auth:     canManageLibrary or isAdmin
Body:     CreateLibraryRequest (without id)
Response: int64  — count of processable files across all paths
```

Implemented in [library_handler.go:130](booklore-api/internal/library/handler.go:130) → `LibraryService.ScanLibraryPaths` ([service.go:316](booklore-api/internal/library/service.go:316)). Used by the UI to decide whether to enable large-library buffering.

### 5.2 Create

```
POST /api/v1/libraries
Auth:     canManageLibrary or isAdmin
Body:     CreateLibraryRequest
Response: Library (DTO, including assigned id)
```

[library_handler.go:59](booklore-api/internal/library/handler.go:59) → `LibraryService.CreateLibrary` ([service.go:171](booklore-api/internal/library/service.go:171)).

### 5.3 DTOs

**`CreateLibraryRequest`** ([request.go](booklore-api/internal/library/dto/request.go)):

```go
type CreateLibraryRequest struct {
    Name             string                  `json:"name"             validate:"required"`
    Icon             string                  `json:"icon,omitempty"`
    IconType         *IconType               `json:"iconType,omitempty"`         // PRIME_NG | CUSTOM_SVG
    Paths            []LibraryPath           `json:"paths"            validate:"required,min=1,dive"`
    Watch            bool                    `json:"watch"`
    FormatPriority   []BookFileType          `json:"formatPriority,omitempty"`
    AllowedFormats   []BookFileType          `json:"allowedFormats,omitempty"`   // empty/nil ⇒ accept all
    MetadataSource   MetadataSource          `json:"metadataSource,omitempty"`
    OrganizationMode LibraryOrganizationMode `json:"organizationMode,omitempty"`
}
```

**`Library`** response DTO ([library.go](booklore-api/internal/library/dto/library.go)):

```go
type Library struct {
    ID                int64                   `json:"id"`
    Name              string                  `json:"name"`
    Sort              *Sort                   `json:"sort,omitempty"`
    Icon              string                  `json:"icon,omitempty"`
    IconType          *IconType               `json:"iconType,omitempty"`
    FileNamingPattern *string                 `json:"fileNamingPattern,omitempty"` // nil until set; see file-naming-patterns spec
    Watch             bool                    `json:"watch"`
    Paths             []LibraryPath           `json:"paths"`
    FormatPriority    []BookFileType          `json:"formatPriority"`
    AllowedFormats    []BookFileType          `json:"allowedFormats"`
    OrganizationMode  LibraryOrganizationMode `json:"organizationMode"`
    MetadataSource    MetadataSource          `json:"metadataSource"`
}
```

**`LibraryPath`** DTO: `{ ID int64; LibraryID int64; Path string }`.

Validation uses `github.com/go-playground/validator/v10` and is executed in the handler before invoking the service.

---

## 6. Backend Logic

### 6.1 `CreateLibrary` sequence

1. Resolve the authenticated user from the request context (`ctx.Value(authContextKey)`).
2. Build `LibraryModel` from the request — name, icon/iconType, watch, formatPriority, allowedFormats, metadataSource, organizationMode.
3. Build `LibraryPathModel` slice from `paths` and attach them (GORM associations with `FullSaveAssociations`).
4. Associate the library with the current user (many-to-many `users` join table).
5. Persist inside a `gorm.DB.Transaction(...)` so path inserts roll back with the library row.
6. If `Watch=true`, for each path call `libraryWatchService.RegisterPath(ctx, library, path)`.
7. Launch the initial scan in a goroutine, propagating a detached context built from the user's auth claims (`authctx.Detach(ctx)`) so request cancellation doesn't abort the scan. Track the library id in `scanningLibraries` (a `sync.Map`) to prevent duplicate concurrent scans; delete on completion.
8. Audit: `auditService.Log(ctx, audit.LibraryCreated, "Library", libraryID, fmt.Sprintf("Created library: %s", name))`.
9. Return the persisted `Library` DTO to the handler (response is immediate; scan runs async).

### 6.2 Initial scan

Async entry point: `LibraryProcessingService.ProcessLibrary(ctx, libraryID)` ([processing.go:51](booklore-api/internal/library/processing.go:51)).

Pipeline:
1. Load `LibraryModel`.
2. Emit a "scan started" notification to connected clients (via the SSE/WebSocket hub).
3. Walk each `LibraryPathModel` via `LibraryFileHelper.GetLibraryFiles` (uses `filepath.WalkDir`).
4. Diff discovered files against existing `BookModel` rows.
5. Group files according to `OrganizationMode` via `BookGroupingService.GroupForInitialScan`.
6. Process groups via `FileAsBookProcessor.ProcessLibraryFilesGrouped`.
7. Emit a "scan complete" notification. Errors are logged (`slog.Error`), not propagated.

The goroutine recovers from panics with `defer func() { if r := recover(); r != nil { ... } }()` so a single bad file cannot crash the process.

### 6.3 Path validation

- `ScanLibraryPaths` checks each path with `os.Stat`; missing paths are logged as warnings but do not fail the scan call.
- Recursive rescans of an existing library return `apierr.LibraryPathNotAccessible` when the library has books but the scan now finds zero processable files (suggests the mount dropped).
- No duplicate-path check within a single library — the form prevents it at add-time but the backend accepts duplicates.

### 6.4 Processable-file filter

`isProcessableFile` ([service.go:355](booklore-api/internal/library/service.go:355)):
- Extension (lowercased via `strings.ToLower(filepath.Ext(name))`) is matched against `BookFileType` (PDF, EPUB, CBX `.cbz/.cbr/.cb7`, FB2, MOBI, AZW3 `.azw3/.azw`, AUDIOBOOK `.m4b/.m4a/.mp3/.opus`).
- If `AllowedFormats` is non-empty, the extension must match one of the allowed types.

---

## 7. Data Model

### 7.1 `library` table

Base from `0001_create_tables.up.sql` (golang-migrate):

```sql
CREATE TABLE library (
    id     BIGSERIAL PRIMARY KEY,
    name   VARCHAR(255) UNIQUE NOT NULL,
    sort   VARCHAR(255) NULL,
    icon   VARCHAR(64)  NOT NULL,
    watch  BOOLEAN NOT NULL DEFAULT FALSE
);
```

Additive columns (through later migrations):

| Column | Type | Default | Migration |
|--------|------|---------|-----------|
| `file_naming_pattern` | `VARCHAR(1000)` | `NULL` | 0043 |
| `format_priority` | `JSONB` | `NULL` → empty list | — |
| `allowed_formats` | `JSONB` | `NULL` → empty list | — |
| `organization_mode` | `VARCHAR(50)` | `'AUTO_DETECT'` | 0103 |
| `metadata_source` | `VARCHAR(50)` | — (app default `EMBEDDED`) | — |
| `icon_type` | `VARCHAR(50)` | `NULL` | — |

### 7.2 `library_path` table

```sql
CREATE TABLE library_path (
    id         BIGSERIAL PRIMARY KEY,
    path       TEXT,
    library_id BIGINT,
    CONSTRAINT fk_library_path FOREIGN KEY (library_id)
        REFERENCES library(id) ON DELETE CASCADE
);
```

Cascade delete was tightened in `0116_add_cascade_delete_to_library_path.up.sql`.

### 7.3 GORM

- `LibraryModel.LibraryPaths` — `gorm:"foreignKey:LibraryID;constraint:OnDelete:CASCADE"`, loaded with `.Preload("LibraryPaths")`.
- `LibraryModel.Books` — `gorm:"foreignKey:LibraryID"` with orphan cleanup handled by service deletes.
- `LibraryModel.Users` — `gorm:"many2many:user_libraries;"`.
- `format_priority` / `allowed_formats` use a custom `Scan` / `Value` pair (`pgtype.JSONB` or `json.RawMessage` + `[]BookFileType`).
- `sort` is serialized with a custom `driver.Valuer` / `sql.Scanner` implementation on the `Sort` struct.

---

## 8. Enums

Go enums are modeled as named string types with package-level constants and a `values()` helper for validation.

| Enum | Values | Source |
|------|--------|--------|
| `BookFileType` | `PDF, EPUB, CBX, FB2, MOBI, AZW3, AUDIOBOOK` | [book_file_type.go](booklore-api/internal/library/enum/book_file_type.go) |
| `MetadataSource` | `EMBEDDED, SIDECAR, PREFER_SIDECAR, PREFER_EMBEDDED, NONE` | [metadata_source.go](booklore-api/internal/library/enum/metadata_source.go) |
| `LibraryOrganizationMode` | `BOOK_PER_FILE, BOOK_PER_FOLDER, AUTO_DETECT` *(deprecated)* | [organization_mode.go](booklore-api/internal/library/enum/organization_mode.go) |
| `IconType` | `PRIME_NG, CUSTOM_SVG` | [icon_type.go](booklore-api/internal/library/enum/icon_type.go) |

Each enum implements `encoding.TextMarshaler` / `TextUnmarshaler` so it round-trips through JSON and the DB driver as a string.

---

## 9. Edge Cases

| Case | Outcome |
|------|---------|
| Empty `name` | UI disables Create; backend rejects with validator `required` tag → 400. |
| Duplicate `name` (in-memory match) | UI blocks submission with a validation error. No backend uniqueness check beyond the `UNIQUE` DB constraint, which would surface as a 500 via the `pgconn.PgError` `23505` path if bypassed. |
| No paths selected | UI disables Create; backend rejects with validator `min=1` → 400. |
| Path does not exist at scan time | `os.Stat` returns `ErrNotExist`; logged as a warning; creation still succeeds; scan reports zero files for that path. |
| Path exists but has no processable files | Library is created; initial scan yields zero books. Subsequent rescans on an already-populated library would return `LibraryPathNotAccessible`. |
| Large folder (≥ 500 processable files) | UI enables a "large library loading" flag so list rendering buffers; backend scan is always async (goroutine). |
| `watch=true` but path is not watchable (e.g. remote mount) | Registration is attempted per path; `fsnotify.Watcher.Add` errors are logged by the watch service. Library creation is not rolled back. |
| `AllowedFormats` empty or nil | Treated as "accept all formats". |
| `FormatPriority` nil | Persisted as empty JSON array. |
| Concurrent scans on the same library | Prevented by a `sync.Map`-backed `scanningLibraries` set guarding `ProcessLibrary`. |
| User lacks permission | `403 Forbidden` from the `RequireLibraryManageOrAdmin` middleware. |

---

## 10. Cross-feature Interactions

- **File Naming Patterns** — `FileNamingPattern` is not set during creation; it stays `nil` and falls back to the system default. See [file-naming-patterns.spec.md](specs/file-naming-patterns.spec.md).
- **File watcher** — `LibraryWatchService` (wrapping `fsnotify.Watcher`) is called from both create (`watch=true`) and update flows. The watcher is also paused around bulk moves (see File Naming Patterns spec §5.2).
- **Metadata persistence** — library metadata source does not inherit from global `MetadataPersistenceSettings`; it is stored per library.
- **App settings** — no settings are copied from global defaults on creation.

---

## 11. Audit

Every successful create writes an audit row:

```
audit.LibraryCreated
entity   = "Library"
entityID = <new library id>
details  = "Created library: <name>"
```

Related actions: `LibraryUpdated`, `LibraryDeleted`, `LibraryScanned`, `NamingPatternChanged`.

---

## 12. Validation Summary

| Layer | Rule |
|-------|------|
| UI | Trimmed name non-empty; name not already in in-memory library list; ≥ 1 folder; no duplicate folder added to the form. |
| DTO | `validate:"required"` on `Name`; `validate:"required,min=1,dive"` on `Paths`. |
| Database | `library.name` `UNIQUE NOT NULL`. |
| Auth | `canManageLibrary` or `isAdmin` middleware; edit/delete additionally require the `CheckLibraryAccess` middleware. |

---

## 13. Security Considerations

- The user supplies absolute filesystem paths; there is no sandbox to a root directory. Deployments should rely on the container/host filesystem boundary to restrict reachable paths. Paths are cleaned with `filepath.Clean` but not resolved against a chroot.
- Permission is required to create libraries; non-admin users cannot point BookLore at arbitrary paths.
- The pre-create scan reads directory listings (`os.ReadDir`) but does not open file contents; the async initial scan is what actually reads files.
- Path inputs that contain null bytes or traversal sequences (`..`) are rejected before `os.Stat` is called.

---

## 14. Open / Future Work

1. **Backend duplicate-path check** — reject adding the same path to a library (UI already does, DB does not).
2. **Path reachability check up-front** — currently a missing path silently scans zero files; a warning in the create response would be clearer.
3. **Inheritance from app settings** — opt-in defaults for `MetadataSource`, `OrganizationMode`, and `FileNamingPattern` at creation time.
4. **Finish deprecating `AUTO_DETECT`** — the enum value is marked deprecated but remains the column default.
5. **Atomic rollback when watcher registration fails** — today the library is created even if watch registration errors. Consider wrapping watcher registration in the same transaction via an outbox or a post-commit hook that can mark the library as `watchErrored`.
6. **Context-aware cancellation of initial scan** — expose an admin-facing cancel endpoint that closes the scan's context.

---

## 15. Key References

- Handler: [handler.go](booklore-api/internal/library/handler.go)
- Service: [service.go](booklore-api/internal/library/service.go)
- Processor: [processing.go](booklore-api/internal/library/processing.go)
- Watcher: [watch.go](booklore-api/internal/library/watch.go) (`LibraryWatchService`)
- Request DTO: [request.go](booklore-api/internal/library/dto/request.go)
- Response DTO: [library.go](booklore-api/internal/library/dto/library.go)
- Models: [library_model.go](booklore-api/internal/library/model/library_model.go), [library_path_model.go](booklore-api/internal/library/model/library_path_model.go)
- UI: [library-creator.component.ts](booklore-ui/src/app/features/library-creator/library-creator.component.ts), [library-creator.component.html](booklore-ui/src/app/features/library-creator/library-creator.component.html)
- UI service: [library.service.ts](booklore-ui/src/app/features/book/service/library.service.ts)
- Topbar entry: [app.topbar.component.ts:166](booklore-ui/src/app/shared/layout/component/layout-topbar/app.topbar.component.ts:166)
- Dialog launcher: [dialog-launcher.service.ts:80](booklore-ui/src/app/shared/services/dialog-launcher.service.ts:80)
