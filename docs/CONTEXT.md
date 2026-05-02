# CONTEXT

Domain glossary for embookshelf. Used by architecture reviews and `/grill-with-docs`-style design conversations to keep vocabulary consistent.

This file complements `docs/architecture.md` (technical layout) and `docs/spec/` (feature specs). It records the **terms** — what a thing is called and what the term means. Use these names exactly when proposing refactors or recording decisions.

---

## Storage layer (Plans A–H)

**Storage** — the `internal/storage.Storage` interface; backend-agnostic read/write of book bytes. Two adapters today: LocalFS and S3. See `docs/spec/storage.spec.md` §2.

**Backend** — a concrete `Storage` rooted somewhere (a directory, a bucket+prefix). Identified by `storage_backends.id` in the DB.

**Resolver** — `storage.Resolver`; maps a backend id (or library) to its `Storage`. Constructed at boot from `storage_backends` rows. See Plan F.

**Library** — a logical collection of books pinned to one Backend via `libraries.backend_id` and `libraries.root`. Two creation flavors:
- `kind=local` (default) — **managed**: filesystem path is auto-derived as `${DATA_PATH}/libraries/{slug}/`; the directory is created at library-create time (`os.MkdirAll`, idempotent — pre-staged folders are re-adopted). Operator does not pick the path. Existing libraries created before this convention keep their explicit paths; only new libraries are managed.
- `kind=s3` — backend-managed: prefix `libraries/{slug}/` inside the shared S3 bucket; symmetric naming with the local layout.

Library deletion: `?purge=true` query param deletes the on-disk folder (local) or the bucket prefix (s3). Default off — files preserved on row delete.

**LibraryStore** — `service.LibraryStore`; the deep seam that turns a `libraryID` into a `LibraryHandle{Library, Storage, Placer}` plus delivery glue (`BookSource`, `Open`, `Relativize`). Composes `LibraryRepo` + `Resolver` + `PlacerBuilder` behind one `For(ctx, libraryID)` method. Stateless — each call does a fresh PK lookup. Used by `BookDropService.Approve`, the file-serve handler, library scan, and files backfill. Replaces the scattered `lib, _ := libs.GetByID(); resolver.Resolve(*lib.BackendID); ...` chain that appeared at every callsite. (Bookdrop ingest still calls `Resolver` directly because it has no library_id at ingest time.)

**Source** — `storage.Source`; the random-access byte view of a single object. `io.ReaderAt + io.Closer + Size() int64`. Returned by `Storage.Open(ctx, key)`. Distinct from `storage.Get` (sequential streaming via `io.ReadCloser`) — Source is for callers that need to seek (zip directories, PDF XREF, MP4 atoms).

**Sidecar** — portable per-book metadata file at the LeafBook folder root. Two flavors:
- `metadata.opf` (Calibre) — XML, **read-only** for compat.
- `metadata.embookshelf.json` (native) — JSON, **read+write**. One file per Book, lives at `{library_root}/{Author}/{Title}/metadata.embookshelf.json` (per ADR-0003). Pre-existing per-file `<basename>.embookshelf.json` files are read once on re-scan but never written; new writes always emit the folder-root file.

The earlier `.embookshelf.toml` sidecar (Plan D pre-cutover) is **dropped** — no read, no write, no migration. Users with TOML files at upgrade lose the overlay; manual re-edit through the UI re-emits the JSON sidecar.

The JSON sidecar is **spillover-only on local-backed libraries**: holds fields the book's file format couldn't carry natively. EPUB OPF takes everything (including cover bytes) → JSON sidecar usually empty. PDF `/Info` takes only Title/Author/Description/Tags → JSON sidecar holds Subtitle, Language, Publisher, ISBN, Series, SeriesIndex, Genres, etc.

**Full-mirror sidecar** whenever the in-file write step is skipped, for any reason:
- Format has no in-file write target (CBZ/CBR/CB7, MOBI, AZW3, FB2, audio in Phase 1).
- Library is **S3-backed** (`libraries.backend_id IS NOT NULL`) — Phase 1 skips in-file write to avoid Get+Put per edit.
- In-file write attempted and failed (failure fallback so edit survives).

Single rule: `inFileWritten == false → sidecar = full mirror`. `inFileWritten == true → sidecar = spillover for that format`. Triggered by **manual edit** or **apply-enrichment** only — auto-enrichment, scan re-ingest, and approve do not write file/sidecar.

Read path (ingest): file embedded → OPF (if present) → JSON, each layer overlays the previous, **lock-aware** (per-field `*_locked` flags shield DB values from re-extract). Write path (user edits): DB (canonical) → JSON sidecar → file embedded (EPUB cover+text rezip; PDF `/Info` text only; audio deferred). Each step is sequenced and atomic; scan skips re-extract when `files.content_hash` matches our recorded write (hash-stamp guard).

**Bookdrop** — pre-approval staging area; files land here before being approved into a Library. Each file becomes a row in `bookdrop_items` with extracted metadata + cover. Approving creates the `books` row and the `files` row.

**Files row** — one entry in the `files` table per physical artifact tied to a `book_id`. Carries `location` (relative to `library.root`), `size`, `mtime`, `etag`, `content_hash` (sha256), `format`. Plan B.

**Tier** — hot / warm / cold; assigned by `internal/tagging.Classify` based on last-read time. Drives S3 lifecycle transitions via `tag:tier=...` on the object. Plan H.

---

## Identity

**Content hash** — sha256 of a book file's bytes. Authoritative identity in the new schema. Plan B.

**ETag** — opaque change token from a backend (S3 returns one; LocalFS leaves it empty). Use to *detect* whether a single object changed since last observation. **Never** use to compare two objects for equality — multipart uploads make ETag input-dependent.

---

## Workers

**Library scan** — `task.LibraryScan`; the periodic walk-then-diff-then-act over a Library. Plan C.

**Walker** — `internal/scan.Walk`; streams `WalkEntry{Location, Size, Mtime, ETag}` from a Storage. Plan C.

**Differ** — `internal/scan.Diff`; pure function classifying walk × DB rows into `Changeset{Unchanged, Changed, New, Missing}`.

**Reattach** — `internal/scan.MaybeReattach`; on a Changed file's hash matching another row in the same Library, treats as a rename and preserves `book_id` continuity.

**Drainer** — `task.Drain[T]`; the loop shape used by boot-time backfills that read pending rows from a predicate query, do per-item work that may fail per item, and exit when the predicate is empty or no item in a batch made progress. Owns logging + the in-run skip set so closures stay focused on the work itself. Used by Files backfill (sha256 fill) and Covers backfill (legacy → hash-keyed). Distinct from a schema-bootstrap backfill (`migrator.BackfillStorageV2`), which runs once after `migrate.Up`, sentinel-gated, DB-only.

**Files backfill** — `task.RunFilesBackfill`; one-shot at-boot worker that fills `files.content_hash` for rows backfilled by the migration with NULL hashes.

**Covers backfill** — `task.RunCoversBackfill`; one-shot at-boot worker that re-keys legacy book-id-keyed cover files to the hash-keyed layout. Plan E.

**Missing purge** — `task.LoopMissingPurge`; hourly sweeper that deletes `files` rows whose `missing_since` is older than 24h. Plan C.

**S3 event loop** — `task.RunS3EventLoop`; opt-in SQS-poll worker that reconciles S3 `ObjectCreated`/`ObjectRemoved` events into the `files` table without waiting for the periodic walk. Plan H.

---

## Service layer

**Approve** — `BookDropService.Approve`; the orchestration that turns a `bookdrop_items` row into a `books` row + `files` row + cover. Five side effects in sequence.

**Bookdrop ingest** — the worker pipeline that takes a staged file path, computes its hash, dispatches a Processor by extension, extracts metadata + cover, persists to the bookdrop row.

**Processor** — `fileproc.Processor`; per-format metadata extractor. `Extract(ctx, src Source) (Metadata, error)`. One implementation per format (EPUB, PDF, CBZ, MP3, M4B).

**BookSource** — `service.BookSource`; a *delivery decision* for the file-serve handler: `{Kind: "local", Path}` (stream via `c.File()`) or `{Kind: "presign", URL, TTL}` (302 redirect). Plan G. Built by `LibraryHandle.BookSource(ctx, book)`. **Distinct from `storage.Source`** — that's a byte-access primitive; this is a routing answer.

**Placer** — `service.Placer`; the seam Approve uses to materialize a bookdrop file at its final library location. Two adapters: `LocalPlacer` (filesystem rename + collision-suffix under the library root) and `BackendPlacer` (stream-upload to a `Storage` then drop the local source). Returns `PlaceResult{Location, Size, Mtime}` — the values the `files` row needs. The `PlacerBuilder` factory injected at boot picks the adapter from `Library.BackendID`. Approve never branches on local-vs-S3.

**MetadataWriter** — `service.MetadataWriter`; the **edit-side write pipeline** module. Owns the `DB → JSON sidecar → file embedded → folder rename` sequence for user-driven edits only. Three triggers in scope: `manual_edit`, `apply_enrichment`, `auto_enrichment`. The other ADR-0001 §3 rows (`bookdrop approve`, `library scan re-ingest`) deliberately route around this module — for those, the file *is* the source, so a writer that rewrites the file would loop. Single entry point: `Write(ctx, book, trigger) (Outcome, error)`. Decision lives in `decideEffects` (pure); execution is a flat orchestration of four private steps (DB, sidecar, in-file embed, folder rename). Stamps `files.content_hash` after a successful in-file write so the next library scan recognises its own write and skips re-extract.

**Effects** — `service.Effects{DB, InFileFormat, Sidecar, FolderRename}`; the plan returned by `decideEffects(trigger, handle, format, fieldChanges)`. Encodes the trigger × backend × field-changed matrix from ADR-0001 §3 + ADR-0003 §6. `FolderRename = true` iff trigger ∈ {manual_edit, apply_enrichment} AND backend == local AND `Author` or `Title` changed. Pure function — no I/O — so combinations are scenario-tested without standing up storage or repos.

**Outcome** — `service.Outcome{InFileWritten, SidecarMode, FolderRenamed}`; what the executor returns alongside `error`. `SidecarMode` is decided post-hoc from `InFileWritten` per ADR-0001's `inFileWritten == false → sidecar = full mirror` rule. `FolderRenamed` is set when ADR-0003 §6 fired. Outcome is consumed by tests, optionally by SSE telemetry/audit; callers that don't care can discard.

---

## Library layout (ADR-0003)

**Folder layout** — every Book lives in its own folder named `{Author}/{Title}/{filename}` under the library root. Sentinels: empty Author → `Unknown Author`, empty Title → `Untitled`. Path segments sanitized by `internal/layout/sanitize.go` (replace `/ \ : * ? " < > |`, NFC-normalize, cap 200 bytes). Multi-author authors stay one string. Collisions resolved by `uniqueDestination` ` (2)`, ` (3)` suffix on the title segment.

**LeafBook** — a directory under the library root that holds ≥1 supported file. Scanner treats it as one Book; all supported files inside (any depth, recursive when no nested LeafBook descendants) become `files` rows under one `book_id`. Sidecar at folder root.

**Container** — a directory under the library root that holds only subdirectories (no supported files at this level). Scanner recurses into each subdir as a `LeafBook` candidate. Handles the `Author/` layer of `Author/Title/file.epub`.

**Classify** — `scan.Classify(entries, isSupported)`; pure function partitioning a `[]WalkEntry` into `Flat` (root-depth files, legacy single-file Books) and `LeafBooks` (folder-as-Book groupings, including the `Mixed` flag for dirs with both direct files and subdir-LeafBooks). Building block for the scan rewrite (ADR-0004); not yet wired into `task.LibraryScan`.

**PickCover** — `scan.PickCover(CoverInputs) CoverChoice`; pure function encoding ADR-0003 §9 cover precedence (`Locked > FolderImage > EmbeddedPrimary > EmbeddedCompanion > SidecarJSON > None`). Companion to `Classify`; the caller (eventual `ScanImportJob`) reads bytes from whichever source the result names. Not yet wired.

**Primary file / primary format** — the highest-priority supported file inside a LeafBook (EPUB > PDF > CBZ > AZW3 > MOBI > FB2 > M4B > MP3). Drives `books.format` (single string, denormalized cache) and is the source for in-file metadata writes. UI derives the full format set from `files` rows when needed.

**Sentinel folder** — `Unknown Author` and `Untitled` are reserved literal strings used when the corresponding Book fields are empty. Predictable depth: every Book sits at depth 2 under the library root.

**Folder rename** — `os.Rename(oldDir, newDir)` invoked by `MetadataWriter` after the DB → sidecar → in-file pipeline succeeds, when `Author` or `Title` change via a `manual_edit` or `apply_enrichment` trigger on a local-backed library. S3 backends never rename. `auto_enrichment` and scan re-extract never rename — DB drifts from disk, accepted.

**Lazy layout migration** — existing libraries keep their current shape. Place-time uses the new layout for new approves; edit-time rename lazily moves actively-curated Books into the new shape. No boot-time relocation. Inactive flat-layout Books stay flat indefinitely. Mirrors ADR-0002's "no automatic move" principle. Mixed-layout libraries are expected during the transition tail; all reads use `files.location` + `books.folder_path` from DB, not disk-walk inference.

**Scan auto-import** (ADR-0004) — `service.ScanImport(ctx, deps, libraryID, lb)`; service-layer function that materializes one `scan.LeafBook` into a Book row + N files rows + cover + audio metadata. Idempotent via `Files.ExistsByLocation`; returns `service.ErrAlreadyImported` for noops. Performs content-hash reattach (ADR-0003 §10): on import, hashes the LeafBook's primary file and looks up `Files.GetByContentHash`; a same-library match folds into the existing `book_id`, inserts any new sibling files, and updates `books.folder_path` so externally-renamed folders don't duplicate. Wrapped by `task.ScanImportArgs` + `task.ScanImportWorker` (kind `scan.import`) and reachable via `queue.Client.EnqueueScanImport`. `task.LibraryScan` runs `scan.Classify` over its `New` set: LeafBooks dispatch through `ScanImport`; root-depth flat files keep the legacy bookdrop staging path until the lazy ADR-0003 §5 migration eventually moves them into folders.

**Extract** — `extractor.Extract(ctx, store, src, format, key) ExtractResult`; shared extraction primitive returning format metadata + cover bytes + audio fields + sidecar overlay. Single contract between the bookdrop ingest path (already cut over) and the future `ScanImportJob`. Lives in `internal/extractor/` to avoid the import cycle with `internal/ingest/`'s bookdrop watcher (which imports `queue`, which imports `task`).

---

## Vocabulary discipline

Avoid these substitutes — they drift the meaning:
- "component" / "service" / "package" → say **module** when discussing depth/seam, **adapter** when discussing implementations of an interface
- "API" / "signature" → say **interface** (includes invariants, error modes, ordering, not just types)
- "boundary" → say **seam** (boundary is overloaded with DDD bounded contexts)
- "Storage source" / "BookSource" used interchangeably → no. `storage.Source` = bytes; `handler.BookSource` = delivery target.

---

## Architecture skill conventions

When proposing a deepening, use the LANGUAGE.md vocabulary: **module**, **interface**, **implementation**, **depth**, **seam**, **adapter**, **leverage**, **locality**, plus **deletion test** and **one-adapter-is-hypothetical-two-is-real**.

ADRs (when added) live under `docs/adr/NNNN-title.md`. Format: see `~/.claude/skills/grill-with-docs/ADR-FORMAT.md`.
