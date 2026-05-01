# Edit-side Metadata Write-back — Feature Specification

> When a user edits book metadata (manually or by applying an enrichment match), embookshelf writes the change to three artifacts in fixed order: **DB → JSON sidecar → file embedded**. The DB is canonical; the file and sidecar are export artifacts so metadata travels with the book when copied to a Kobo, scanned by Calibre, or rsynced to a fresh install.

- **Status:** Draft
- **Scope:** `embookshelf` Go backend (`internal/sidecar`, `internal/fileproc`, `internal/service/{enrichment,book}`, `internal/task`, `internal/repo/book`) + React UI edit-metadata panel (no UX changes; existing UI already issues the trigger).
- **Permission required:** any user with edit-metadata access on the book (existing handler gate).
- **Entry points:**
  - `PATCH /api/books/:id` (manual edit) → triggers full pipeline.
  - `POST /api/books/:id/enrich/apply` (apply match) → triggers full pipeline.
- **Companion artifacts:** `docs/CONTEXT.md` (Sidecar entry), `docs/adr/0001-edit-side-metadata-write-back.md` (decisions + reasons).

---

## 1. Purpose

Embookshelf's books carry metadata in three places: the DB (`books` table), the file's embedded metadata (EPUB OPF, PDF `/Info`, ID3, etc.), and an optional sidecar file (`<basename>.embookshelf.json`). Today (Plan D landed) the sidecar is *read-only* — bookdrop ingest overlays it onto extracted metadata. There is **no path that writes the user's edits back out** to the file or the sidecar; every edit is DB-only.

That's a problem in three deployment shapes:

- **External readers (Kobo, Boox, Calibre):** the user opens the EPUB in another tool and sees the *original* (often Amazon-default) metadata, not the curated set they edited in embookshelf.
- **Backup/restore drills:** a user `rsync`s their library directory to a fresh install. Books re-ingest from file embedded only; tags, genres, series, custom descriptions are gone unless they manually copied a DB dump too.
- **Cross-device portability:** a book file moved to a new machine should carry its own metadata. Today the metadata stranded itself in the DB.

This spec adds the missing direction: **DB → file → sidecar on every explicit edit**, with a single rule for what spills to the sidecar.

---

## 2. User stories

1. **Curated library traveling to Kobo.** A reader edits an EPUB's title in the embookshelf UI, plugs in their Kobo, copies the file. The Kobo home screen shows the curated title, not the publisher's "Untitled-final-final-v3.epub" garbage.

2. **Apply enrichment, ship the file.** A user matches a book against Google Books, clicks Apply. The DB updates with the new description, ISBN, series. Next time they hand the EPUB to a friend, the OPF inside the EPUB carries the same description.

3. **Backup drill.** Admin tarballs `/library` to S3, restores to a fresh embookshelf instance with no DB. Bookdrop re-ingests every file. Each book recovers its full metadata: tags, genres, custom titles — read from the JSON sidecar that embookshelf wrote earlier.

4. **External tool round-trip.** User opens the EPUB in Sigil to fix a typo in chapter 3, saves, drops back into the library folder. Library scan detects the file changed → re-ingests → applies the new metadata to DB **except** for fields the user has locked (those stay).

5. **PDF with tags.** PDFs can hold Title/Author in `/Info` but have no native slot for series or genres. User edits a PDF's series in embookshelf. The file gets `/Info` updates for what fits; the sidecar JSON next to it carries series + genres + ISBN.

---

## 3. Domain

See `docs/CONTEXT.md` for the canonical glossary. The terms this spec relies on:

- **Sidecar** — `<basename>.embookshelf.json`. Paired filename next to the book file. Spillover-only when the in-file write succeeds; full mirror otherwise.
- **In-file write** — direct modification of the book file's embedded metadata. EPUB: rezip with new OPF + cover bytes. PDF: in-place patch of `/Info` dict.
- **Trigger** — the upstream action that fires the write pipeline. Only `manual edit` and `apply enrichment` qualify; everything else (auto-enrichment, scan, approve) skips file/sidecar.
- **Lock** — `books.<field>_locked = TRUE` shields that field from every automatic write path: enrichment auto-apply, scan re-ingest, sidecar overlay merge.
- **Hash-stamp** — `files.content_hash` recorded after every in-file write. Library scan compares the file's actual hash against this stamp; mismatch = re-extract triggers, match = no-op.

---

## 4. Sidecar schema

JSON, UTF-8, atomic write-temp-then-rename via `storage.Put` (existing `internal/sidecar/Writer`).

### 4.1 Shape

```json
{
  "version": 1,
  "format": "EPUB",
  "mode": "spillover",
  "fields": {
    "title": "Frankenstein",
    "subtitle": "or, The Modern Prometheus",
    "author": "Mary Shelley",
    "description": "...",
    "language": "en",
    "publisher": "Lackington, Hughes...",
    "published_date": "1818",
    "isbn": "978-0-486-28211-4",
    "series": "Penguin Classics",
    "series_index": 12,
    "tags": ["gothic", "monster"],
    "genres": ["fiction", "horror"]
  },
  "written_at": "2026-04-30T14:22:00Z",
  "writer": "embookshelf/0.18.3"
}
```

- **`version`** — schema version. Start at `1`; bump on incompatible changes.
- **`format`** — book format the sidecar was written next to. Used as a sanity check at read-time (warn on mismatch, don't fail).
- **`mode`** — `"spillover"` (only fields the file format couldn't carry) or `"full"` (mirror; in-file write was skipped or failed).
- **`fields`** — the metadata payload. Field set always uses the same JSON keys regardless of mode; readers don't need to know which mode the writer used.
- **`written_at`** — RFC 3339 UTC. Helps the operator debug sync issues.
- **`writer`** — `"embookshelf/<version>"`. Other tools writing this format can use a different writer string.

### 4.2 Read-time tolerance

- Unknown top-level keys → ignored.
- `version > 1` → log warning, attempt to parse `fields` with current schema, ignore unknown nested keys.
- Missing `mode` → treat as `"spillover"`.
- Empty `fields` → equivalent to no sidecar present (overlay is a no-op).
- Malformed JSON → log warning, treat as no sidecar.

---

## 5. Per-format mapping

The book's format determines which fields land in the file vs. spill to the sidecar.

### 5.1 EPUB (OPF 3.0 inside the .epub zip)

All fields land in the OPF. Sidecar is empty under normal write success.

| Field          | OPF target                                                       |
|----------------|------------------------------------------------------------------|
| Title          | `<dc:title>`                                                     |
| Subtitle       | `<dc:title>` with `<meta property="title-type">subtitle</meta>`  |
| Author         | `<dc:creator opf:role="aut">`                                    |
| Description    | `<dc:description>`                                               |
| Language       | `<dc:language>`                                                  |
| Publisher      | `<dc:publisher>`                                                 |
| Published Date | `<dc:date>`                                                      |
| ISBN           | `<dc:identifier opf:scheme="ISBN">`                              |
| Series         | `<meta property="belongs-to-collection">` + `<calibre:series>` (compat) |
| Series Index   | `<meta property="group-position">` + `<calibre:series_index>`    |
| Tags           | `<meta property="embookshelf:tag">` × N + `<dc:subject>` × N (Calibre compat) |
| Genres         | `<meta property="embookshelf:genre">` × N + `<dc:subject>` × N (Calibre compat) |
| Cover bytes    | manifest item `properties="cover-image"`; replace bytes inline   |

**Tags vs Genres:** dual write per ADR 0001. embookshelf's `<meta property="embookshelf:*">` is the lossless source on read; `<dc:subject>` mirrors both as a flat list so Calibre/Kobo see populated subjects. On read-back, prefer `<meta property="embookshelf:*">`; if absent, fall through to flat `<dc:subject>` and assign all entries to **Tags** (Genres stays empty until the user re-buckets in UI).

### 5.2 PDF (`/Info` dict)

PDF holds the editorial four; everything else spills.

| Field          | `/Info` target                  | Spillover? |
|----------------|---------------------------------|------------|
| Title          | `/Title`                        | no         |
| Author         | `/Author`                       | no         |
| Description    | `/Subject`                      | no         |
| Tags           | `/Keywords` (comma-joined)      | no         |
| Genres         | `/Keywords` (comma-joined w/ Tags) | no      |
| Subtitle       | —                               | **yes**    |
| Language       | —                               | **yes**    |
| Publisher      | —                               | **yes**    |
| Published Date | — (not `/CreationDate`)         | **yes**    |
| ISBN           | —                               | **yes**    |
| Series         | —                               | **yes**    |
| Series Index   | —                               | **yes**    |
| Cover bytes    | — (PDFs rarely embed)           | n/a        |

**Tags + Genres into `/Keywords`:** inline prefix scheme. `tag:fantasy, tag:adventure, genre:fiction`. Read-back regex `^(tag|genre):(.+)$` per comma-separated entry; unprefixed entries → Tags by default.

**`/CreationDate`:** **never overwritten.** `/CreationDate` is the file-creation timestamp by PDF convention; mutating it confuses every other tool that reads it. Published-date always goes to sidecar for PDFs.

**No XMP packet write in this phase.** Calibre populates an XMP RDF packet for richer PDF metadata; embookshelf does not (heavy lift, no Go lib in good shape). XMP is a Phase 2 candidate.

### 5.3 Other formats (CBZ, CBR, CB7, MOBI, AZW3, FB2, MP3, M4B, M4A)

**No in-file write in Phase 1.** Sidecar carries the **full mirror** (`mode: "full"`).

Audio formats (MP3, M4B, M4A) are explicit deferral candidates for Phase 2 (ID3v2 frames + MP4 atoms via the existing tag-reader libraries that already extract metadata at ingest).

---

## 6. Triggers and write order

### 6.1 Trigger contract

| Trigger                    | DB | File rewrite | Sidecar rewrite |
|----------------------------|----|--------------|-----------------|
| Manual edit                | ✓  | ✓ (local) / skip (S3) | ✓ |
| Apply enrichment match     | ✓  | ✓ (local) / skip (S3) | ✓ |
| Auto-enrichment (bg)       | ✓  | —            | ✓               |
| Library scan re-ingest     | ✓  | —            | — (file/sidecar are inputs) |
| Bookdrop approve           | ✓  | —            | — (file is the source) |

**Local backend** = `libraries.backend_id IS NULL`.
**S3 backend** = `libraries.backend_id IS NOT NULL` → in-file write skipped Phase 1; sidecar `mode: "full"`.

### 6.2 Write sequence

A successful manual edit (or apply-enrichment) executes in order:

1. **DB write** — transactional, via `BookRepo.UpdateMetadata` (or `UpdateAudio` for audio fields). User-facing response returns after this step succeeds. All subsequent steps are post-commit and best-effort.

2. **Sidecar write** — `internal/sidecar.Writer.Write(ctx, store, sidecarKey, payload)` w/ `application/json` content-type. Writer's per-key mutex (existing) serializes concurrent edits to the same book.

3. **In-file write** — gated on `local backend && format ∈ {EPUB, PDF}`:
   - **EPUB:** open object via `handle.Storage.Open` → unzip in memory → modify OPF (and cover-image manifest entry) → rezip → `Put` to a temp key → atomic rename to original key (existing storage primitive). After Put, compute sha256 of new bytes, update `files.content_hash`.
   - **PDF:** open object → patch `/Info` dict in place → write to temp key → atomic rename → update `files.content_hash`.

4. **Hash-stamp** — `files.content_hash` set to the new file's sha256. The library scan compares this against the file's actual hash; match = no re-extract.

### 6.3 Failure handling

- **Step 1 fails:** error returned to caller. UI shows error. No file/sidecar touched.
- **Step 2 fails:** logged via `slog.Warn`. Step 3 still runs. Next manual edit retries. Background "sidecar repair" worker (Phase 2 candidate) sweeps DB rows whose sidecar mtime is older than `books.updated_at`.
- **Step 3 fails:** logged via `slog.Warn`. **Sidecar is rewritten in `mode: "full"`** so that on re-ingest the metadata still recovers without the file's contribution. `files.content_hash` left at its pre-attempt value (scan will see the file as unchanged → no re-extract loop).

A spec invariant: **the user's edit is durable after step 1**. Steps 2 and 3 are observable side effects; their failure is logged, not surfaced.

---

## 7. Lock-aware re-extract

Library scan (Plan C) detects file changes via `etag` / `mtime` / sha256. When the scan re-extracts, the merge into DB is **lock-aware**:

```
new_value(field) =
  DB[field]                if books.<field>_locked == TRUE
  extracted[field]         otherwise
```

Locked fields are shielded from external file edits, JSON sidecar overlay, and any other automatic source. The user's UI edit (manual edit) bypasses the lock — locks shield against *automatic* writes only.

**Hash-stamp short-circuit:** before re-extracting, scan compares the file's current sha256 against `files.content_hash`. Match → skip re-extract entirely (covers the "scan saw our own write" case). The hash-stamp is updated in step 4 of every successful in-file write, so the scan never re-ingests a file that embookshelf just rewrote.

External edit semantics:
1. External tool (Sigil, Calibre) modifies the EPUB. File hash changes.
2. Scan detects mismatch with `files.content_hash`.
3. Re-extract metadata (file embedded → sidecar overlay).
4. Apply lock-aware merge to DB.
5. **Update `files.content_hash`** to match the externally-edited file. The scan stops re-firing; the next embookshelf edit will rewrite the file and update the stamp again.

---

## 8. Sidecar location on disk

Paired filename next to the book file: `<basename>.embookshelf.json`.

- `library/folder/harry-potter.epub` → `library/folder/harry-potter.embookshelf.json`
- `library/audiobooks/dune/disc-1.m4b` → `library/audiobooks/dune/disc-1.embookshelf.json`

Same rule for both `org_mode = book_per_file` and `org_mode = book_per_folder`. The sidecar's path is computed as `filepath.Join(filepath.Dir(book.Path), strings.TrimSuffix(filepath.Base(book.Path), filepath.Ext(book.Path)) + ".embookshelf.json")`.

When a book file is **moved** (rename detection in scan, Plan C's `MaybeReattach`), the sidecar must be moved alongside it. Concrete: scan's reattach path moves the book file, then attempts to move the sidecar at the old paired path to the new paired path. Sidecar move failure is logged but non-fatal.

---

## 9. TOML cutover (no migration)

Per ADR 0001: `.embookshelf.toml` is dropped entirely. No read, no write, no rename. The existing `internal/sidecar` package is refactored:

- `EncodeTOML`, `DecodeTOML` removed.
- `Writer.Write` uses `EncodeJSON` and `application/json` content-type.
- `Read` looks for `<basename>.embookshelf.json` only.
- TOML field tags on `Sidecar` struct (`toml:"..."`) replaced with `json:"..."`.

Existing TOML files on disk become orphans: they sit there, embookshelf doesn't read them, the user can delete them at leisure. The bookdrop ingest pipeline will not crash on their presence (the JSON-only read path simply doesn't consult them).

**Migration tooling: none.** Users who need to recover TOML overlay metadata can decode the TOML by hand and re-enter the fields through the UI; the next save emits the JSON sidecar.

---

## 10. Out of scope (Phase 2 candidates)

- **In-file write for audio** (MP3 ID3v2, M4B/M4A MP4 atoms). Tag libraries already exist in the project; deferred for time, not capability.
- **In-file write for FB2.** XML edit is straightforward but no demand signal yet.
- **XMP packet writes for PDF.** Calibre uses XMP for richer PDF metadata; would let PDF carry series/genres natively. No Go XMP-write lib in good shape.
- **In-file write for S3-backed libraries.** Costs Get + Put per edit. Phase 2 candidate: queued/deduplicated background River job that batches multiple edits to one rezip per book per N minutes.
- **Multi-instance write coordination.** The existing `Writer` per-key mutex is in-process. Multi-replica deployments racing edits on the same book would need conditional Put (S3 If-Match etag, FS lockfile). Plan F+ topic.
- **Sidecar repair worker.** Periodic sweep that detects "DB.updated_at > sidecar.mtime" and rewrites the sidecar from DB. Useful for self-healing after step-2 failures.
- **TOML migration.** Decided against in ADR 0001; included here for completeness.
- **Schema versioning of in-file metadata.** OPF version bumps, PDF version bumps. Currently embookshelf reads what it gets and writes what it writes; no schema negotiation.

---

## 11. Interfaces (Go)

### 11.1 `internal/sidecar/writer.go` changes

```go
// Write encodes s as JSON and stores it at key. Per-key serialized.
// Existing signature unchanged; payload format flips.
func (w *Writer) Write(ctx context.Context, store storage.Storage, key string, s Sidecar) error
```

`Sidecar` struct gains `json:"..."` tags (replace `toml:"..."`); EncodeJSON wraps it in the v1 envelope (`version`, `format`, `mode`, `fields`, `written_at`, `writer`).

### 11.2 `internal/fileproc` — new write path

New file-format-specific write op. Mirrors the existing `Processor` (read) interface:

```go
// Embedder writes a Metadata snapshot back into the file's embedded
// metadata. One impl per format. ErrUnsupported when the format
// doesn't expose embedded metadata.
type Embedder interface {
    Embed(ctx context.Context, src storage.Source, dest io.Writer, m Metadata, cover []byte) error
}

// DispatchEmbedder picks the right embedder for a format.
func DispatchEmbedder(format string) (Embedder, error)
```

Phase 1 implementations: `EPUBEmbedder` (rezip), `PDFEmbedder` (`/Info` patch). Other formats return `ErrUnsupported` from `DispatchEmbedder`.

### 11.3 New service: `service.MetadataWriter`

Coordinates the three-step pipeline. Constructed at boot with `BookRepo`, `LibraryStore`, `Writer`, `coverstore.Store`, `EmbedderRegistry`.

```go
// Write persists the book's edited metadata: DB → sidecar → file embedded.
// Trigger drives the trigger contract from §6.1. Returns nil after step 1
// (DB) succeeds; steps 2-3 are post-commit best-effort.
func (s *MetadataWriter) Write(ctx context.Context, bookID string, fields Metadata, trigger Trigger) error

type Trigger string

const (
    TriggerManualEdit       Trigger = "manual_edit"
    TriggerApplyEnrichment  Trigger = "apply_enrichment"
    TriggerAutoEnrichment   Trigger = "auto_enrichment"   // bg, no file/sidecar
)
```

Manual-edit and apply-enrichment HTTP handlers call `MetadataWriter.Write(ctx, id, fields, TriggerManualEdit/Apply)`. Background auto-enrichment uses `TriggerAutoEnrichment` to short-circuit file/sidecar steps.

### 11.4 `LibraryHandle` extensions

`LibraryStore.For(ctx, libID)` returns a handle whose existing `Storage` field powers the in-file write. New helpers on the handle:

```go
// SidecarKey returns the storage key for a book's sidecar, paired with
// the book's location.
func (h *LibraryHandle) SidecarKey(bookLocation string) string

// CanWriteInFile reports whether the handle's backend supports in-file
// writes (Phase 1: only local-backed libraries).
func (h *LibraryHandle) CanWriteInFile() bool
```

`MetadataWriter` consults `CanWriteInFile()` to gate step 3.

---

## 12. Test surface

### 12.1 Unit (per package)

- `internal/sidecar` — JSON encode/decode round-trip; v1 envelope shape; concurrent-Write per-key serialization; Read tolerance to malformed input.
- `internal/fileproc/epub_embed_test.go` — round-trip a known EPUB through `EPUBEmbedder.Embed` + re-extract; assert all fields preserved; assert cover bytes replaced; assert dual-write on dc:subject + embookshelf:tag.
- `internal/fileproc/pdf_embed_test.go` — round-trip Title/Author/Description/Tags through `/Info`; assert `/CreationDate` not overwritten; assert `/Keywords` prefix scheme.

### 12.2 Service (`internal/service`)

- `MetadataWriter_Write_localEPUB` — DB + sidecar (empty) + file rewritten; `files.content_hash` updated.
- `MetadataWriter_Write_localPDF` — DB + sidecar (spillover non-empty) + file `/Info` patched.
- `MetadataWriter_Write_S3backend` — DB + sidecar (full mirror) + file untouched.
- `MetadataWriter_Write_unsupportedFormat` — DB + sidecar (full mirror) + file untouched.
- `MetadataWriter_Write_inFileFails` — DB ✓; file write returns error; sidecar rewritten in `full` mode; `files.content_hash` not advanced.
- `MetadataWriter_Write_TriggerAutoEnrichment` — DB only; no sidecar/file touched.

### 12.3 Integration (library scan)

- `LibraryScan_lockShieldsExternalEdit` — set `title_locked=true`; externally rewrite the EPUB's title; run scan; assert DB title unchanged, `files.content_hash` advanced to match new file (so scan stops re-firing).
- `LibraryScan_hashStampSkipsRefire` — call `MetadataWriter.Write` to rewrite a file; run scan immediately after; assert no re-extract triggered (hash matches stamp).
- `LibraryScan_externalEditClearsToDB` — externally rewrite EPUB title (no locks); scan picks up the new title in DB.

### 12.4 End-to-end

Playwright test: edit a book's title in the UI → assert DB updated → assert sidecar JSON contains the new title (or assert OPF inside the EPUB, depending on format).

---

## 13. Open questions deferred to follow-up

- **Sidecar repair worker** — does drift accumulate enough in practice to justify a Drainer-shaped worker that rewrites sidecars from DB? Decide after observing field reports.
- **Conditional Put for multi-instance setups** — needed if/when embookshelf supports multi-replica HA; not before.
- **EPUB rezip performance for large files (50MB+)** — measure on representative library; budget a streaming rezip if memory pressure shows up.
