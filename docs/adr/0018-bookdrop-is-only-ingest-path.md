# Bookdrop is the only ingest path; library scan shrinks to drift detection

## Status

accepted (2026-05-08); supersedes ADR-0004

## Context

ADR-0004 (2026-05-02) chose scan auto-import: `task.LibraryScan` walks a library's storage, classifies LeafBooks, and materialises `books` rows directly via `service.ScanImport`. The premise was that operators would place files into the library tree and expect them to appear without an approval gate.

Operational reality is the opposite. Every file that enters a managed embookshelf instance arrives via:

1. The `bookdrop/` watched folder, or
2. The web upload UI (`POST /api/bookdrop/upload`).

No one places files directly under `${library_root}/Author/Title/`. No one runs `aws s3 cp` against an embookshelf S3 prefix. The library directory is owned by the app — `BookDropService.Approve` writes there, `MetadataWriter` writes there, nothing else.

Under that premise, scan-as-ingest is dead weight. It also reintroduces a class of edit-time hazards: re-extract on Changed re-races the `MetadataWriter` hash-stamp guard, the `cls.Flat` branch keeps a legacy bookdrop-staging path alive, and `ScanImport`'s reattach exists to defend against external renames that don't happen.

## Decision

### 1. Bookdrop is the only path that creates a `books` row.

`BookDropService.Approve` is the sole writer. `service.ScanImport`, `task.ScanImportArgs`, `task.ScanImportWorker`, and `queue.Client.EnqueueScanImport` are removed. `scan.Classify`, `scan.PickCover`, and `scan.MaybeReattach` are removed.

A pre-populated library directory or S3 prefix is **not visible** to the app until each file is re-fed through bookdrop. Cold-start migrations are the operator's job; the app does not pretend to discover anything.

### 2. Library scan shrinks to drift detection.

`task.LibraryScan` keeps Walk + Diff and acts on two of the four diff buckets:

- **New** — for each previously-unseen entry, hash bytes and look up `Files.GetByContentHash` in the same library. A match means the file was renamed externally; update the existing row's `location`. No match means an externally-placed file appeared; **ignore** (no `books` row, no `bookdrop_items` row, no log line at warn).
- **Missing** — soft-flag with `MarkMissing(now)`. The existing 24h `task.LoopMissingPurge` sweeper deletes `files` rows whose `missing_since` is older than 24h. This is the canary for "the bytes are really gone" — failed disk, accidental `rm`, S3 lifecycle expiration.
- **Changed** — no-op (logged at debug). Re-extract + lock-merge is removed. The hash-stamp guard against `MetadataWriter` writes goes away with it. External edits drift silently; bookdrop / the app's edit pipeline are the supported edit paths.
- **Unchanged** — clear `missing_since` if the file reappeared. Same as today.

### 3. Cadence: on-demand only.

The periodic `task.LibraryScan` timer is removed. Scan runs only when an admin invokes it from the UI (or via the equivalent API). Periodic scan exists in other tools to catch external drift; under decision (1) external drift is not part of the supported model, so the timer is doing nothing useful and burning storage I/O on every tick.

### 4. ADR-0004 is superseded.

ADR-0004 stays in the repo with status `superseded by 0018` so the rationale trail survives. The "Scan auto-import" CONTEXT.md term is removed.

## Considered alternatives

- **(b2) Auto-import for cold start only**, gated on `libraries.first_scan_done`. Rejected: a flag that exists to be flipped once is operationally fragile; the next migration always finds the flag in the wrong state.
- **(b3) Auto-import behind admin toggle.** Rejected by ADR-0004 §"Considered alternatives" already; reinstating the knob brings back the same "everyone enables it" failure mode.
- **(c1) Drop New handling entirely.** Rejected: external `mv` would silently age the file row out via Missing within 24h. (c2)'s hash-lookup is cheap and preserves rename continuity without materialising new books.
- **(d3) Keep Changed re-extract.** Rejected: under decision (1) Changed only fires on external interference, which is out-of-scope. Keeping the re-extract path means continuing to maintain the hash-stamp race against `MetadataWriter`.

## Consequences

**Positive:**

- One ingest seam, one mental model. "How does a book get in?" has one answer.
- `internal/scan/` collapses to walker + differ + a small relocate helper. `service.ScanImport` and the `cls.Flat` legacy bookdrop-staging branch disappear.
- `MetadataWriter`'s hash-stamp guard against scan re-extract becomes unnecessary.
- No periodic I/O against library storage.

**Negative / surprising:**

- Pre-populated libraries are unsupported. Operators migrating from Calibre / NAS / S3 must feed files through bookdrop. Document loudly.
- External edits to library files (Calibre saves over an EPUB in place) drift silently from DB. Under decision (1) this is not supposed to happen, but it will happen at least once and the user will be confused.
- External deletion still gets caught (Missing → 24h purge) but the user has no DB-level signal that an external rename was *not* caught — the hash-lookup in §2 succeeds silently. Operators rebooting from a half-migrated state may need a one-shot reconcile script outside the app.

## Implementation notes

- Delete: `internal/task/scan_import.go`, `task.ScanImportArgs`, `task.ScanImportWorker`, `task.ScanImportFile`, `queue.Client.EnqueueScanImport`, `service.ScanImport`, `scan.Classify`, `scan.PickCover`, `scan.MaybeReattach`, the `cls.Flat` and `cls.LeafBooks` branches in `task.LibraryScan`, `reExtractAndMerge` and its `Books`/`LockMerger` deps, the periodic-scan timer in `cmd/embookshelf/main.go`.
- Replace: `MaybeReattach` (Changed-shaped) with a `RelocateByHash` (New-shaped) helper that hashes one file and updates `files.location` on a same-library hash hit.
- Keep: `scan.Walk`, `scan.Diff`, `task.LoopMissingPurge`, `Files.MarkMissing`, `Files.ClearMissing`, `Files.GetByContentHash`.
- CONTEXT.md: remove "Reattach", "Classify", "PickCover", "Scan auto-import", "LeafBook", "Container", "Primary file / primary format" (last only if no other consumer); rewrite "Library scan" to drift-detection scope.
