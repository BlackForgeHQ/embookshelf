# Library Layout — Feature Specification

> Every Book in a Library lives in its own folder under the library root, named `{Author}/{Title}/{filename}`. Scanner treats one folder = one Book; multi-format siblings (epub + mp3 + cover.jpg + sidecar) share that folder and produce one `books` row with multiple `files` rows. Edit-time renames keep the folder name aligned with the canonical DB values for user-driven edits only.

- **Status:** Draft
- **Scope:** `embookshelf` Go backend (`internal/service/{placer,bookdrop,metadata_writer}`, `internal/task/{library_scan,scan_import}`, `internal/layout`, `internal/ingest`, `internal/sidecar`, `internal/repo/{book,library}`, migration 000030).
- **Permission required:** library-create — `admin`. Edits — any user with edit-metadata access on the book.
- **Companion artifacts:** `docs/adr/0003-book-per-folder-library-layout.md` (decisions + reasons), `docs/adr/0004-scan-auto-imports-bookdrop-uploads-only.md` (scan↔bookdrop seam), `docs/spec/sidecar-write.spec.md` (write pipeline).

---

## 1. Purpose

Embookshelf libraries today have a contradictory layout story: the schema records `org_mode = book_per_folder` while the runtime flat-drops every file at the library root and never groups files into Books. ADR-0003 resolves this by hardcoding `book_per_folder` semantics across the whole stack and dropping the `org_mode` knob.

This spec defines the user-visible behavior of that layout: where files land, how scanner finds Books on disk, when folders get renamed, what happens to multi-format siblings and missing files, and how the change interacts with existing libraries that have a different shape today.

---

## 2. Domain

See `CONTEXT.md` for the canonical glossary. Key terms this spec uses:

- **LeafBook** — a directory under the library root that contains ≥1 supported file. Scanner treats it as a single Book.
- **Container** — a directory under the library root that contains only subdirectories (no supported files at this level). Scanner recurses into each subdir as a `LeafBook` candidate.
- **Primary file** — the highest-priority supported file inside a LeafBook (EPUB > PDF > CBZ > AZW3 > MOBI > FB2 > M4B > MP3). Drives `books.format` and is the source for in-file metadata writes.
- **Companion file** — any other supported file in the same LeafBook.
- **Folder rename** — `os.Rename(oldDir, newDir)` invoked when `Author` or `Title` changes via a `manual_edit` or `apply_enrichment` trigger. Subject to `decideEffects` matrix.
- **Sentinel folder** — `Unknown Author` and `Untitled` are reserved literal strings used when the corresponding fields are empty.

---

## 3. Path layout

### 3.1 New approves and uploads

Files placed by `LocalPlacer` or `BackendPlacer` go to:

```
{library_root}/{Author}/{Title}/{filename}
```

- `library_root` — `${DATA_PATH}/libraries/{slug}/` for managed local (per ADR-0002), the operator-supplied path for legacy local libraries, or `libraries/{slug}/` (object key prefix) for S3.
- `Author` — `book.Author` after sanitization (§3.3). Empty → `Unknown Author`.
- `Title` — `book.Title` after sanitization (§3.3). Empty → `Untitled`.
- `filename` — `filepath.Base(item.Path)` for bookdrop approves (preserves the upload's original name); for direct scan-imports, the existing on-disk filename.

Folder collisions (an existing folder at the same `{Author}/{Title}/` path) are resolved with the same `uniqueDestination` logic that `LocalPlacer` uses for files today: append ` (2)`, ` (3)`, … to the title segment until free.

### 3.2 S3 backends

`BackendPlacer.Place` writes the object key as `{Author}/{Title}/{filename}` so S3 libraries are layout-symmetric with local at place-time. No edit-time copy+delete on S3 (per ADR-0003 §7).

### 3.3 Sanitization

Implemented in `internal/layout/sanitize.go`. Applied to each path segment (Author, Title) independently:

| Rule | Action |
|---|---|
| Replace `/ \ : * ? " < > \|` | with `_` |
| Replace control chars `\x00-\x1f` | with `_` |
| Strip leading/trailing dots | `..foo` → `foo`, `foo.` → `foo` |
| Strip leading/trailing whitespace | trim |
| NTFS reserved names (`CON`, `PRN`, `AUX`, `NUL`, `COM1-9`, `LPT1-9`) | append `_` |
| Unicode | NFC-normalize |
| Length | cap at 200 bytes UTF-8 (truncate at code-point boundary) |
| Empty after sanitize | fall back to `Untitled` / `Unknown Author` |

Multi-author authors stay one string (`"A & B"`, `"A, B, C"`) — sanitized as a single segment.

---

## 4. Scanner behavior

### 4.1 Two-phase classification

`task.LibraryScan` walks the filesystem (or backend keyspace) and classifies entries:

| Entry | Classification |
|---|---|
| File at library root depth 0 | Legacy single-file Book (one Book per file). Today's behavior; lazy migration phases out organically. |
| Directory at any depth | One of: `Container`, `LeafBook`, or `Mixed`. |

For a directory:

- **`Container`** — contains only subdirectories (no supported files at this level). Recurse into each subdirectory.
- **`LeafBook`** — contains ≥1 supported file. Treat as a single Book; collect every supported file at any depth inside.
- **`Mixed`** — contains both supported files at this level AND subdirectories that themselves hold supported files. Treat the top level as a `LeafBook`, but limit the depth-1 sweep to files at this level only (do not recurse into the subdirs); log a warning; surface in admin scan-failures UI.

Tie-breakers:
- Directory holds `metadata.embookshelf.json` AND a supported file → `LeafBook` (sidecar pins identity).
- Directory holds `metadata.embookshelf.json` AND no supported files → ignore (orphan sidecar; log).

### 4.2 Reattach via content hash

For each `LeafBook` candidate, the scan-import worker hashes (or reads cached `content_hash` per Plan B's hash-stamp guard) every file in the folder. If any file's hash matches an existing `files` row in the same library:

- Fold the LeafBook into that row's `book_id`.
- Update `books.folder_path` to the candidate's path (handles external `mv`).
- Insert any new files in the LeafBook as additional `files` rows under the same `book_id`.
- Mark any DB `files` rows that no longer exist on disk as `missing_since = now()`.

Otherwise, treat as a new Book: insert `books` + N `files` rows + cover + sidecar overlay in one DB tx.

### 4.3 Idempotency

The scan-import worker is idempotent. Re-running on an unchanged LeafBook is a no-op. Hash-stamp guard short-circuits re-extraction; reattach short-circuits row inserts.

---

## 5. Book composition

### 5.1 `books.format` (primary)

Set to the format of the LeafBook's primary file. Priority: `EPUB > PDF > CBZ > AZW3 > MOBI > FB2 > M4B > MP3`. Recomputed on every `files` insert/delete that targets the Book.

### 5.2 `files` rows

One per supported file in the LeafBook. `files.location` is library-relative (or backend-key for S3), e.g. `Tolkien/The Hobbit/hobbit.epub`. Identity per Plan B: `(library_id, content_hash)`.

### 5.3 Cover precedence

Highest-wins:

1. `books.cover_locked = true` → keep the current cover.
2. `cover.{jpg,jpeg,png,webp}` at folder root, alphabetical-by-extension-priority (jpg first).
3. Embedded in primary file.
4. Embedded in companion file (priority order over siblings).
5. Sidecar JSON `cover_b64` (full-mirror sidecars only).
6. None — UI placeholder.

Cover is stored in `coverstore` keyed by content hash (existing layout). `books.has_cover` and `books.cover_mime` reflect the chosen source.

### 5.4 Sidecar

`metadata.embookshelf.json` at LeafBook folder root. Single sidecar per Book. Pre-existing per-file `<basename>.embookshelf.json` files are read on first re-scan but never written; new writes always emit the folder-root file. See `docs/spec/sidecar-write.spec.md` for the spillover/full-mirror rule.

---

## 6. Edit-time folder rename

### 6.1 Trigger gate

Folder rename runs only on `manual_edit` and `apply_enrichment` triggers. `auto_enrichment`, scan re-extract, and bookdrop approve do **not** rename. Same trigger gate as ADR-0001's file/sidecar writes.

### 6.2 Field gate

Only `Author` and `Title` changes trigger rename. `Series`, `Year`, `Description`, etc. stay DB-only.

### 6.3 Backend gate

`library.backend_id != nil` (S3) → no rename. `decideEffects` returns `FolderRename = false`.

### 6.4 Effects matrix

`MetadataWriter.decideEffects` returns:

```go
type Effects struct {
    DB           bool
    InFileFormat string // "" = skip in-file embed
    Sidecar      string // "spillover" | "full-mirror"
    FolderRename bool
}
```

`FolderRename` is `true` iff: trigger ∈ {manual_edit, apply_enrichment} AND backend == local AND (`book.Author` or `book.Title` changed in this edit).

### 6.5 Order inside `Write`

1. DB commit (canonical).
2. Sidecar write at OLD basename location (atomic temp+rename within old folder).
3. In-file embed at OLD path (atomic per ADR-0001).
4. `files.content_hash` stamp.
5. If `Effects.FolderRename`: compute `newDir = filepath.Join(library.Path, sanitize(newAuthor), sanitize(newTitle))`, run `os.Rename(oldDir, newDir)` (with `uniqueDestination` collision handling), then update `files.location` + `books.folder_path` for every `files` row of the Book in a single DB tx.
6. Rename failure: log, leave DB-committed state, retry via River. File on disk is correct, just at the old folder name.

Concurrent reads holding open file descriptors during rename survive via Linux/APFS inode-based FD semantics.

---

## 7. Lazy migration

Existing libraries keep their current layout. No boot-time relocation worker.

- Files at library root depth 0 stay there until the corresponding Book is touched by a user edit (then §6.5 step 5 moves the file into the new layout).
- Existing folders that already match `{author}/{title}/` shape (Calibre imports) are picked up by §4.1's classifier and treated as native LeafBooks.
- Existing folders with a different shape (e.g., `Sci-Fi/Books/Tolkien/Hobbit/file.epub`) are still scanned correctly via two-phase classification — the deepest `LeafBook` wins. They drift into our preferred shape only on edit.

Mixed-layout libraries are expected during the transition tail; all library code paths read `files.location` and `books.folder_path` from DB as the source of truth, never from disk-walk inference.

---

## 8. Deletion

- File missing for ≥24h → purge that `files` row (existing `task.LoopMissingPurge`).
- Last `files` row of a Book purged → cascade-delete the Book row (cover, sidecar metadata, progress all FK-cascade as today).
- Folder is left on disk after the cascade. User may have side artifacts (notes, third-party sidecars, cover backups) inside; loud-removing their directory is rude.
- Cross-library moves (folder externally moved from lib A to lib B): out of scope. Lib B sees a new Book; lib A's Book purges naturally on missing-files cascade.

---

## 9. Bookdrop interaction (ADR-0004)

Scan-discovered files **do not** route through `bookdrop_items`. Scanner detects a `LeafBook` → enqueues a `ScanImportJob` River task that calls `internal/ingest/extract.Extract` and inserts directly.

Bookdrop scope is uploads only:
- Files arriving via the watched `bookdrop/` folder.
- Files uploaded via the web UI.

These still go through `bookdrop_items` → user clicks Approve → `BookDropService.Approve` places them at `{Author}/{Title}/{filename}` per §3.

---

## 10. Test plan

| Scenario | Expected |
|---|---|
| Approve a single-file bookdrop with Author + Title | File lands at `{library}/Tolkien/The Hobbit/hobbit.epub`. `books.folder_path = "Tolkien/The Hobbit"`. `files.location = "Tolkien/The Hobbit/hobbit.epub"`. |
| Approve with empty Author | Folder = `Unknown Author/Title/`. |
| Approve with empty Title | Folder = `Author/Untitled/`. |
| Approve with Title containing `/`, `:`, `*` | Sanitized to `_`. |
| Approve when target folder exists | Title segment suffixed ` (2)`. |
| User edits Title via PATCH | Folder renames, `files.location` + `books.folder_path` updated, content hash unchanged. |
| User edits Author via apply-enrichment | Folder renames. |
| Auto-enrichment changes Title | DB updates; folder does NOT rename. |
| User edits Description | DB updates; no rename. |
| Edit on S3-backed library | DB updates; no key rename. |
| Edit fails mid-rename (`os.Rename` returns error) | DB committed, log warning, retry-able. Disk holds book at old folder; UI shows new title. |
| Scan finds a Calibre-shape import (`Author/Title/file.epub`) | Classified as LeafBook via two-phase walk. New Book inserted. |
| Scan finds a Mixed dir (files + subdirs with files) | Top-level treated as LeafBook with depth-1 sweep; warning logged. |
| Scan re-runs on unchanged library | No-op (hash-stamp + reattach). |
| Operator `mv`s a LeafBook folder externally | Reattach: `books.folder_path` updates; no duplicate Book. |
| LeafBook holds epub + mp3 | One Book row, two files rows. `books.format = "EPUB"` (primary). |
| Last file in a LeafBook deleted on disk | After 24h, files row purges → Book row cascade-deletes → folder remains on disk. |
| `cover.jpg` at folder root | Used in preference to in-file cover when `cover_locked=false`. |
| Pre-existing `<basename>.embookshelf.json` next to a file | Read overlays metadata on next re-scan; new writes go to `metadata.embookshelf.json` at folder root. |

---

## 11. Open questions

- Admin UI for the scan-failures view (§4.1 Mixed warnings, §9 ScanImportJob failures) is out of scope for this spec; tracked separately.
- Cross-library move detection (§8) deferred until there's a concrete user demand.
- A future "restructure now" admin button (eager migration on demand) is **not** part of this spec; if needed, layered on later as a Drainer.
