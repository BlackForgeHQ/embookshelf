# Scan auto-imports; Bookdrop scope shrinks to upload staging

Library scan creates `books` rows directly when it detects a `LeafBook` it has not seen before. It no longer enqueues `bookdrop_items` rows for scan-discovered files. Bookdrop becomes the staging area exclusively for files that arrive via the upload landing pad (`bookdrop/` watched folder + manual web upload).

## Status

superseded by ADR-0018 (2026-05-08)

Original status: accepted (2026-05-02). Reversed when operational reality showed every file enters via bookdrop or web upload — no one places files directly under the library tree, so scan-as-ingest is dead weight. See ADR-0018.

## Context

`bookdrop_items` today serves two purposes:

1. **Upload staging.** A user drops `book.epub` into the watched `bookdrop/` folder or uses the upload UI. The watcher creates a `bookdrop_items` row, ingest extracts metadata + cover, the row sits in a "ready for approve" tray. The user clicks Approve → `BookDropService.Approve` builds a `books` row + `files` row and moves the file into the library.
2. **Scan-discovered curation queue.** `task.LibraryScan` walks a library's filesystem root, classifies entries as `New`, and enqueues each one through the same bookdrop pipeline. The user must Approve each one to actually import it.

The second role is friction without payoff:

- The file is *already in the user's library directory*. They put it there. Why is approval needed?
- Calibre, Plex, Audiobookshelf, Booksonic — every comparable tool auto-imports scan finds.
- ADR-0003 decisions (folder = Book, multi-format siblings, two-phase classification) compound this: a single `LeafBook` produces N `bookdrop_items` rows that the user has to approve as a batch and that lose their grouping at the bookdrop layer.
- Edit-after-import already covers the "I want to fix metadata" use case. Curation pre-import is a UX speedbump.

ADR-0003 §4 takes the decision implicitly. This ADR makes it explicit and documents the seam.

## Decision

### 1. Scan creates `books` rows directly.

`task.LibraryScan` walks the filesystem, classifies subdirectories per ADR-0003 §3 (`Container` vs `LeafBook`), and for each unseen `LeafBook` enqueues a `ScanImportJob` River task. The worker:

1. Calls `internal/ingest/extract.Extract(ctx, primaryPath)` to get `ExtractResult{Metadata, Cover, Audio}`.
2. Runs reattach per ADR-0003 §10 — if any file's `content_hash` matches an existing `files` row in the same library, fold into that Book's `book_id` instead of inserting.
3. Otherwise insert `books` row + N `files` rows + cover + sidecar overlay in one DB tx.
4. Surfaces as an SSE `book_added` / `book_changed` event for the UI.

No `bookdrop_items` row is created. No approval gate.

### 2. Refactor extraction into `internal/ingest/extract.go`.

The extraction logic currently embedded in the bookdrop ingest pipeline (`fileproc.Dispatch`, cover decode, audio metadata pull, sidecar overlay) moves into a single pure function:

```go
type ExtractResult struct {
    Metadata fileproc.Metadata
    Cover    []byte
    CoverMime string
    Audio    *AudioMetadata
}

func Extract(ctx context.Context, src storage.Source, format string) (ExtractResult, error)
```

Both bookdrop ingest and `ScanImportJob` call `Extract`. Result feeds either a `bookdrop_items` row (uploads) or a direct `books` insert (scan).

### 3. Bookdrop scope: uploads only.

`bookdrop_items` exists if and only if a file landed via:
- `bookdrop/` watched folder (operator drag-and-drop).
- Web upload UI (`POST /api/bookdrop/upload`).

`BookDropService.Approve` stays — it still moves the staged file into the library at the `{author}/{title}/{filename}` path (ADR-0003 §2). The scan ingest path goes around it.

### 4. Failure semantics.

A scan-import job fails (extraction error, DB conflict, missing field). Options considered:

- **Skip and log** — file stays on disk, scan picks it up next cycle, retried via React queue's retry policy.
- **Park in bookdrop** — re-route failed scan imports into `bookdrop_items` so the user can review/fix.

Decision: **skip and log**. River's retry handles transient errors. Persistent errors (corrupt file, unsupported format) surface in the admin's scan-failures view (separate UI work). Bookdrop stays clean — its rows are always upload-origin.

## Considered alternatives

- **Keep dual role; just decorate scan-created bookdrop rows so they group as folders.** Schema change to `bookdrop_items` (folder-item type vs file-item type), still requires the user to approve scan finds. Rejected: the approval step provides no value for files the operator put in their own library.
- **Merge bookdrop into scan path entirely; uploads go through a virtual "upload" library.** Rejected: uploads inherently lack a target library at write-time (user picks at approve). Bookdrop-as-staging is real and earns its keep.
- **Auto-import scan finds, but require an admin toggle.** Rejected: yet another knob; everyone enables it.

## Consequences

**Positive:**
- `BookDropService` shrinks; one less branching purpose.
- Scan path is self-contained — walk → classify → import — no cross-module dance.
- Single extraction implementation across both ingest paths (DRY win).
- "I dropped 200 files into my library, now I have to click Approve 200 times" disappears.

**Negative / surprising:**
- Loses pre-import metadata curation for scan-discovered files. Mitigation: edit after import is the same UI; nothing was actually different about that step.
- A malformed file in a library directory becomes a malformed Book row instead of a "failed bookdrop item the user can ignore". Skip-and-log is the safety net.
- Existing `task.LibraryScan` + `BookDropService.Stage` integration disappears; tests covering the seam need rewriting.
- Operators who built workflows on top of "scan stages, I approve" lose that gate. Documented in release notes.

## Implementation notes

- `internal/ingest/extract.go` is the new home for the extraction function. `internal/ingest/watcher.go` keeps file-watch only.
- `task.ScanImportJob` is the new River worker. `internal/task/scan_import.go`.
- `BookDropService.Stage` (or whichever method the watcher calls) keeps the upload-origin path; the scan-origin entry point gets removed.
