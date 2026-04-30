# CONTEXT

Domain glossary for embookshelf. Used by architecture reviews and `/grill-with-docs`-style design conversations to keep vocabulary consistent.

This file complements `docs/architecture.md` (technical layout) and `docs/spec/` (feature specs). It records the **terms** — what a thing is called and what the term means. Use these names exactly when proposing refactors or recording decisions.

---

## Storage layer (Plans A–H)

**Storage** — the `internal/storage.Storage` interface; backend-agnostic read/write of book bytes. Two adapters today: LocalFS and S3. See `docs/spec/storage.spec.md` §2.

**Backend** — a concrete `Storage` rooted somewhere (a directory, a bucket+prefix). Identified by `storage_backends.id` in the DB.

**Resolver** — `storage.Resolver`; maps a backend id (or library) to its `Storage`. Constructed at boot from `storage_backends` rows. See Plan F.

**Library** — a logical collection of books pinned to one Backend via `libraries.backend_id` and `libraries.root`.

**Source** — `storage.Source`; the random-access byte view of a single object. `io.ReaderAt + io.Closer + Size() int64`. Returned by `Storage.Open(ctx, key)`. Distinct from `storage.Get` (sequential streaming via `io.ReadCloser`) — Source is for callers that need to seek (zip directories, PDF XREF, MP4 atoms).

**Sidecar** — portable, user-editable metadata file next to a book on disk: `metadata.opf` (Calibre, read-only) or `.embookshelf.toml` (native). Plan D.

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

**BookSource** — `handler.BookSource`; a *delivery decision* for the file-serve handler: `{Kind: "local", Path}` (stream via `c.File()`) or `{Kind: "presign", URL, TTL}` (302 redirect). Plan G. **Distinct from `storage.Source`** — that's a byte-access primitive; this is a routing answer.

**Placer** — `service.Placer`; the seam Approve uses to materialize a bookdrop file at its final library location. Two adapters: `LocalPlacer` (filesystem rename + collision-suffix under the library root) and `BackendPlacer` (stream-upload to a `Storage` then drop the local source). Returns `PlaceResult{Location, Size, Mtime}` — the values the `files` row needs. The `PlacerBuilder` factory injected at boot picks the adapter from `Library.BackendID`. Approve never branches on local-vs-S3.

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
