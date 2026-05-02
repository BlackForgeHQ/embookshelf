# Book-per-folder library layout: `{author}/{title}/{filename}`

Every Book in a Library lives in its own folder under the library root, named `{Author}/{Title}/{filename}`. The folder is the Book's identity on disk: scanner treats one folder = one Book; multi-format siblings (epub + mp3 + cover.jpg + sidecar) share that folder and produce one `books` row with multiple `files` rows. The dormant `libraries.org_mode` column is dropped — there is no per-library switch.

## Status

accepted (2026-05-02)

## Context

Today's library physical layout is contradictory:

- DB schema has `libraries.org_mode TEXT NOT NULL DEFAULT 'book_per_folder' CHECK ('book_per_file', 'book_per_folder')` (migration 000025).
- `model.Library.OrgMode` exists.
- **Nothing reads it.** Scanner does not group, placer flat-drops at library root (`internal/service/placer.go:88` — `filepath.Join(root, filepath.Base(src.Path))`), `MetadataWriter.decideEffects` does not branch on it.
- `docs/spec/sidecar-write.spec.md:231` literally says "Same rule for both `org_mode = book_per_file` and `org_mode = book_per_folder`."
- UI has zero references; operators cannot change it.

So the schema records `book_per_folder` while the runtime behaves `book_per_file` flat. The knob is a lie.

Separately, real demands push toward folder-per-book:
- Calibre/Plex/Audiobookshelf convention is folder-per-book — users importing existing libraries arrive with that shape and expect it preserved.
- Audiobook + companion ebook (one Book, multiple files) has no clean home with a flat layout.
- ADR-0001 (sidecar write-back) needs a stable place to drop sidecars; per-file basename pairing fragments across multi-format siblings.
- ADR-0002 made local libraries managed under `${DATA_PATH}/libraries/{slug}/` — symmetry with S3, scriptable backups. A consistent in-library layout extends that story.

## Decision

### 1. Hardcode book-per-folder semantics. Drop `org_mode`.

Migration 000030 drops `libraries.org_mode`. `model.Library.OrgMode` and all repo references go with it. The column added zero behavior in 6 months; we delete the false promise instead of growing two code paths.

### 2. New approves and uploads place files at `{library_root}/{Author}/{Title}/{filename}`.

`Author` and `Title` come from extracted metadata (manual upload) or the Book row (any other path). Sentinels for missing values:

- Empty Author → folder literal `Unknown Author`.
- Empty Title → folder literal `Untitled` (matches existing fallback in `BookDropService.Approve`).

Path segments are sanitized by a shared util `internal/layout/sanitize.go`:
- Replace `/ \ : * ? " < > |`, control chars `\x00-\x1f`, leading/trailing dots and spaces with `_`.
- Reject NTFS reserved names (`CON`, `PRN`, `AUX`, `NUL`, `COM1-9`, `LPT1-9`) by suffixing `_`.
- NFC-normalize unicode.
- Cap each segment at 200 bytes UTF-8 (preserves headroom under the 255-byte ext4/APFS limit).
- Empty after sanitize → fall back to `Untitled` / `Unknown Author`.

Multi-author authors stay one string (`"A & B"`, `"A, B, C"`) — no list semantics introduced here.

### 3. Folder = Book at scan time.

Scanner classifies directories under library root in two phases:

- Files at library root depth 0 (no enclosing folder under root) → **legacy single-file Books**, one Book per file. Today's behavior; phased out organically by §5 lazy migration.
- Each subdirectory → either:
  - `Container` — holds only subdirs (no supported files at this level). Recurse into each subdir as a candidate. Handles the `Author/` layer of `Author/Title/file.epub` (Calibre, our own output).
  - `LeafBook` — holds ≥1 supported file. All supported files inside (depth N, recursive) become `files` rows of one Book. Handles multi-chapter audiobooks.

Mixed case (a directory holds both supported files AND subdirs that hold supported files): treat as `LeafBook` at the top level with depth-1 supported-file scan; warn in scan log; surface in admin UI later.

### 4. Bookdrop scope shrinks to upload staging only.

Scan-discovered files no longer route through `bookdrop_items`. Scanner detects a `LeafBook` → enqueues a `ScanImportJob` River task → worker calls a refactored `internal/ingest/extract.Extract(ctx, primaryPath)` and inserts `books` row + N `files` rows + cover + sidecar overlay in a single DB tx. Bookdrop ingest reuses the same `extract.Extract` for manual uploads. See ADR-0004.

### 5. Lazy migration — never auto-relocate.

Existing libraries keep their current layout. Place-time uses the new layout for new approves. Edit-time folder rename (per §6) lazily moves actively-curated Books into the new shape. Inactive flat-layout Books stay flat indefinitely. Mirrors ADR-0002's "no automatic move" principle.

### 6. Folder rename on user-driven edits only.

When `manual_edit` or `apply_enrichment` mutates `books.author` or `books.title`, `MetadataWriter` renames the Book's folder from `{old_author}/{old_title}/` to `{new_author}/{new_title}/` after the existing DB → sidecar → in-file pipeline succeeds. Auto-enrichment, scan re-extract, and bookdrop approve do **not** rename — same trigger gate as ADR-0001's file/sidecar writes.

`MetadataWriter.decideEffects` returns a 4-tuple `Effects{DB, InFileFormat, Sidecar, FolderRename}`. The pure decision function encodes the trigger × backend × field-changed matrix.

Order inside `Write`:
1. DB commit (canonical, per ADR-0001).
2. Sidecar write at OLD basename (atomic temp+rename).
3. In-file embed at OLD path (atomic per ADR-0001).
4. `files.content_hash` stamp.
5. If `Effects.FolderRename`: `os.Rename(oldDir, newDir)` + DB tx update of `files.location` + `books.folder_path`.
6. Rename failure: log, leave DB-committed state, retry-able. File on disk is correct, just at the old folder name.

### 7. S3 backends: layout at place-time, never on edit.

`BackendPlacer` writes keys `{author}/{title}/{filename}` so freshly-populated S3 libraries are layout-symmetric with local. No edit-time copy+delete — `decideEffects` returns `FolderRename = false` whenever `library.backend_id` is set, paralleling the existing "S3 skips in-file write" rule.

### 8. Sidecar relocates to folder root.

Sidecar filename changes from `<basename>.embookshelf.json` (next to each file) to `metadata.embookshelf.json` (at folder root). Single sidecar per Book, not per file. Spillover-vs-full-mirror rule unchanged.

Pre-existing per-file sidecars are read but never written. New writes always emit the folder-root file. See `docs/spec/sidecar-write.spec.md` for full pipeline.

### 9. Cover precedence.

Highest-wins:
1. `books.cover_locked = true` → keep current.
2. `cover.{jpg,jpeg,png,webp}` at folder root (alphabetical-by-extension-priority).
3. Embedded in primary file (highest format priority among siblings).
4. Embedded in companion file (other siblings).
5. Sidecar JSON `cover_b64` (full-mirror sidecars only).
6. None — UI placeholder.

### 10. Identity via content hash.

Same identity rule as Plan B: a Book is reattached on re-scan when **any** file in the candidate `LeafBook` has a `content_hash` matching an existing `files` row in the same library. Folder renames done outside our control (`mv Tolkien/Hobbit Tolkien/The\ Hobbit`) reattach for free — primary's hash matches → `books.folder_path` updates → no duplicate Book.

### 11. `books.format` becomes "primary format".

Single-string column stays. Picked by format priority (EPUB > PDF > CBZ > AZW3 > MOBI > FB2 > M4B > MP3) over the Book's `files` rows. UI derives the full format set via join when needed. Re-evaluated on every files insert/delete.

### 12. Deletion semantics.

- File missing for 24h → purge that `files` row (existing `task.LoopMissingPurge`).
- Last `files` row purged → cascade-delete the Book row. Folder is left on disk (user may have side artifacts).

## Considered alternatives

- **Keep `org_mode` and finally implement both branches.** Two grouping algorithms, two placer adapters, more code paths — without a real user demand for `book_per_file`. Rejected: a folder-per-book convention covers single-file Books too (a 1-file folder).
- **Drop `org_mode`, keep flat layout.** Easy. Rejected: leaves audiobook+ebook unsolvable, fragments sidecars across siblings, breaks Calibre import expectations.
- **Eager migration: boot worker relocates everything.** Massive at-boot I/O, $$$ on S3, partial-failure mid-run is messy. Rejected on the same reasoning ADR-0002 used for paths.
- **Use `books.folder_path` as the only canonical layout source — never compute from author/title.** Place at `{uuid}/{filename}`. Stable, ugly, no rename-on-edit churn. Rejected: loses the "browse Finder, find your book" property; also makes `rsync`-restore-into-fresh-instance produce nameless-feeling shelves.
- **Folder name = original basename stem (`harry-potter/harry-potter.epub`).** No rename on edit. Rejected: doesn't deduplicate multi-file Books, doesn't help users who imported with Calibre layout.

## Consequences

**Positive:**
- One layout convention across local + S3 (at place-time).
- Calibre-imported libraries Just Work.
- Audiobook+ebook unification path is now obvious — `files` rows already support it.
- Sidecar lives in one place per Book.
- Drops a dormant column + 6 months of misleading glossary.

**Negative / surprising:**
- Title/Author edits move bytes on local libraries. `rsync --delete` next backup churns. Mitigation: trigger gate is narrow (manual + apply_enrichment only).
- Kobo-style external readers that bookmark by file path break on edit-rename. Mitigation: same scope as today's hash-stamp invalidation; users learn this once.
- S3 + edits drift: keys reflect approve-time titles forever. Same trade as ADR-0001's "S3 skips in-file write" — tolerable, fully captured in sidecar full-mirror.
- Mixed-layout libraries during the lazy-migration tail: some Books flat at root, some in folders. Tooling that assumes a uniform layout breaks until each Book is touched.
- Two-phase scanner classification has edge cases (`LeafBook` directly under another `LeafBook`, dir with both files and supported-file subdirs). Loud-warn + pick-`LeafBook`-when-ambiguous behaviour documented in spec; surfaces in admin UI.
- `{author}/{title}/` segments collide if two real Books share both. Resolved by `uniqueDestination` suffix (` (2)`, ` (3)`) at the folder name level.

## Implementation phasing

This ADR covers the design. Implementation lands across multiple PRs:

1. Migration 000030 + repo/model cleanup: drop `org_mode`. No behavior change.
2. `internal/layout/sanitize.go` shared util.
3. `Placer` adapter rewrite: emits folder layout + populates `books.folder_path`.
4. `MetadataWriter.decideEffects` adds `FolderRename`; `Write` adds rename step.
5. Scanner two-phase walk + `ScanImportJob` worker; bookdrop scope shrink (ADR-0004).
6. Sidecar relocation (`metadata.embookshelf.json` at folder root).
7. Cover precedence + folder-root cover detection.

Each phase ships independently; lazy migration means partial rollouts are safe.
