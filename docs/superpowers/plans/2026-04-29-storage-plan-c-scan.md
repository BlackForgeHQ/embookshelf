# Two-Phase Scan & Reconciliation — Implementation Plan (Plan C of 8)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rewrite the library scan worker as a two-phase walk + diff + ingest, using the `files` table from Plan B as authoritative state. Cheap mode skips re-hashing files whose `(size, mtime)` match the DB row; the expensive ingest path runs only for new or changed files. Missing files are soft-deleted with a 24-hour TTL. A `(library_id, content_hash)` reattach lets the scanner detect renames by hash and migrate the existing book to the new location instead of creating a duplicate.

**Architecture:** Two new packages. `internal/scan/walker.go` produces a stream of `WalkEntry{Location, Size, Mtime, ETag}` from any `storage.Storage`. `internal/scan/differ.go` consumes that stream plus a `[]model.File` snapshot from the DB and emits a `Changeset{Unchanged, Changed, New, Missing}`. The scan worker orchestrates: read DB rows → walk → diff → for `New`, enqueue bookdrop (existing pipeline); for `Changed`, re-hash and update the row in place; for `Missing`, set `missing_since=now()` so a separate sweeper purges 24h later. Hash-based reattach is a fast post-processing step inside the differ: after a Changed entry's new hash is computed, if the hash already exists on another `files` row of the same library, the entry is reclassified as a Rename (update old row's location to the new one, keep the book_id, mark the duplicate row missing).

**Tech Stack:** Go 1.25 stdlib (`io`, `sort`, `time`). Reuses Plan B's `repo.FileRepo`, `internal/hashing`, and Plan A's `storage.Storage`. No new third-party deps.

**Companion spec:** [`docs/spec/storage.spec.md`](../../spec/storage.spec.md). Sections 5.3 (two-phase scan), 5.1 (hash-based identity), and the missing-row 24h grace mentioned at the end of 5.3 drive this plan.

**Locked decisions:**
- 24-hour TTL for missing files (per spec).
- Soft-delete via `files.missing_since TIMESTAMPTZ NULL`. NULL = present, non-NULL = absent since that time.
- Periodic purge runs hourly via a goroutine timer in `main.go` — not a River job in this plan; can promote to a queue job later if it grows.
- The (size, mtime) fast-path skip is per-row on the `files` table. ETag is reserved for Plan F (S3) and ignored for local.
- Reattach is in-scope: when a file is renamed locally, the next scan reattaches the existing book row to the new location.
- The bookdrop pipeline for `New` entries is unchanged. `Changed` files do NOT go through bookdrop — they update the existing files row directly.

**Depends on:** Plan B merged. Specifically the `files` table, `FileRepo`, `BookDropRepo.SetContentHash`, `internal/hashing.HashFile`, and the `storage.Storage` walker from Plan A.

**Out of scope:**
- Sidecar files (Plan D).
- Cover storage migration (Plan E).
- S3 backend, S3 events, presigned URLs, lifecycle (Plans F–H).
- Dropping `books.path` / `libraries.path` (deferred to Plan B2 / Plan G when the handler stops reading them).
- A separate cron framework — the periodic purge is a stdlib `time.Ticker` in main.go.
- Concurrent scans across libraries — the existing River queue serializes per library; the changes here don't widen that.

---

## File Structure

### Files created

| Path | Responsibility |
|---|---|
| `internal/migrator/migrations/postgres/000026_files_missing_since.up.sql` | ADD `files.missing_since TIMESTAMPTZ`. |
| `internal/migrator/migrations/postgres/000026_files_missing_since.down.sql` | DROP the column. |
| `internal/migrator/migrations/sqlite/000026_files_missing_since.up.sql` | SQLite parallel. |
| `internal/migrator/migrations/sqlite/000026_files_missing_since.down.sql` | SQLite parallel. |
| `internal/scan/walker.go` | `WalkEntry`, `Walk(ctx, store storage.Storage, root string) (<-chan WalkEntry, <-chan error)`. |
| `internal/scan/walker_test.go` | LocalFS-backed test. |
| `internal/scan/differ.go` | `Changeset`, `Diff(walked []WalkEntry, dbFiles []model.File) Changeset`. Pure function, table-test friendly. |
| `internal/scan/differ_test.go` | Exhaustive case table. |
| `internal/scan/reattach.go` | `MaybeReattach(ctx, files repo.FileRepo, libraryID string, hash []byte, newLocation string) (bool, error)`. |
| `internal/scan/reattach_test.go` | Cover the rename detection path. |
| `internal/task/missing_purge.go` | `RunMissingPurge(ctx, files *repo.FileRepo, ttl time.Duration)` — single pass. |
| `internal/task/missing_purge_test.go` | Asserts the >TTL row is deleted, the <TTL row is kept. |

### Files modified

| Path | Change |
|---|---|
| `internal/repo/file.go` | Add `MarkMissing(fileID, when)`, `ClearMissing(fileID)`, `DeleteMissingOlderThan(ctx, ttl) (int64, error)`, `ListByLibrary(ctx, libraryID) ([]model.File, error)`, `UpdateLocation(ctx, fileID, location string)`. Update `bdCols`-equivalent if needed. |
| `internal/model/file.go` | Add `MissingSince *time.Time`. |
| `internal/task/library_scan.go` | Replace the body with: load `dbFiles := files.ListByLibrary(libID)`, `walked := walker.Walk(...)`, `cs := differ.Diff(walked, dbFiles)`, then act per category. Hash-based reattach lives in the Changed branch via `reattach.MaybeReattach`. |
| `cmd/embookshelf/main.go` | Launch `task.RunMissingPurge` in a goroutine on a 1-hour ticker after queue setup. |
| `internal/queue/queue.go`, `internal/queue/sqlite.go` | If `LibraryScanDeps` adds new fields, plumb them through. (Likely just `Hashing` if the scan worker needs to compute hashes inline; alternatively the scan worker calls `hashing.HashFile` directly with the storage already in deps.) |

### Files NOT touched

- `internal/handler/files.go` — still reads `book.path`. Plan B2 / G when API consumers cut over.
- `internal/coverstore/` — Plan E.
- `internal/fileproc/` — extractor signatures unchanged.
- `internal/service/bookdrop.go` Approve path — still creates the books + files rows for genuinely new imports.

---

## Phase 1 — Schema

### Task 1: `files.missing_since` migration (PG + SQLite)

**Files:**
- Create: `internal/migrator/migrations/postgres/000026_files_missing_since.{up,down}.sql`
- Create: `internal/migrator/migrations/sqlite/000026_files_missing_since.{up,down}.sql`

PG up:

```sql
ALTER TABLE files ADD COLUMN IF NOT EXISTS missing_since TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_files_missing_since ON files(missing_since) WHERE missing_since IS NOT NULL;
```

PG down:

```sql
DROP INDEX IF EXISTS idx_files_missing_since;
ALTER TABLE files DROP COLUMN IF EXISTS missing_since;
```

SQLite up:

```sql
ALTER TABLE files ADD COLUMN missing_since TEXT;
CREATE INDEX IF NOT EXISTS idx_files_missing_since ON files(missing_since) WHERE missing_since IS NOT NULL;
```

(SQLite has no `IF NOT EXISTS` on `ADD COLUMN`. modernc.org/sqlite errors on that syntax — this was hit in Plan B Task 1.)

SQLite down:

```sql
DROP INDEX IF EXISTS idx_files_missing_since;
-- SQLite cannot DROP COLUMN before 3.35.0; modernc.org/sqlite supports it.
ALTER TABLE files DROP COLUMN missing_since;
```

If the modernc driver doesn't support `DROP COLUMN`, fall back to the rename-create-copy-drop dance. Verify by reading the driver version and a recent SQLite migration that drops a column (none in this codebase yet — modernc.org/sqlite has supported DROP COLUMN since 1.20.0; the project pins a newer version).

- [ ] Commit:

```bash
git add internal/migrator/migrations/postgres/000026_files_missing_since.{up,down}.sql internal/migrator/migrations/sqlite/000026_files_missing_since.{up,down}.sql
git commit -m "feat(db): add files.missing_since for scan reconciliation"
```

---

## Phase 2 — Repo Extensions

### Task 2: FileRepo extensions

**Files:**
- Modify: `internal/repo/file.go`
- Modify: `internal/repo/file_test.go`
- Modify: `internal/model/file.go` (add `MissingSince *time.Time`)

Add `missing_since` to the `fileCols`-equivalent constant and to the `scanFile` helper.

New methods:

```go
// MarkMissing records that the file is no longer present in storage.
// Idempotent: calling twice with the same `when` is a no-op.
func (r *FileRepo) MarkMissing(ctx context.Context, fileID string, when time.Time) error

// ClearMissing flips missing_since back to NULL when a previously
// missing file reappears.
func (r *FileRepo) ClearMissing(ctx context.Context, fileID string) error

// DeleteMissingOlderThan purges rows whose missing_since is more
// than ttl ago. Returns the count deleted.
func (r *FileRepo) DeleteMissingOlderThan(ctx context.Context, ttl time.Duration) (int64, error)

// ListByLibrary returns every files row for libraryID (including
// missing). Used by the scan worker to diff against the live walk.
func (r *FileRepo) ListByLibrary(ctx context.Context, libraryID string) ([]model.File, error)

// UpdateLocation moves a row to a new (library_id, location). Used
// by hash-based reattach when a file is renamed within the same
// library. Returns ErrFileLocationTaken on conflict.
func (r *FileRepo) UpdateLocation(ctx context.Context, fileID, newLocation string) error
```

Tests cover: each method's happy path, NULL handling on read, conflict on UpdateLocation, batch correctness on DeleteMissingOlderThan (rows ≤ ttl preserved).

- [ ] Commit:

```bash
git commit -m "feat(repo): file missing-since lifecycle + ListByLibrary"
```

---

## Phase 3 — Walker + Differ

### Task 3: `internal/scan/walker.go`

**Files:**
- Create: `internal/scan/walker.go`
- Create: `internal/scan/walker_test.go`

```go
// Package scan orchestrates the two-phase library scan: walk, diff,
// then act on the changeset. The walker yields entries via a
// channel so the iterator API of storage.Storage doesn't leak into
// the differ.
package scan

import (
    "context"
    "errors"
    "io"
    "time"

    "github.com/blackforge/embookshelf/internal/storage"
)

// WalkEntry is one observation from a storage backend during the
// cheap walk phase. Hashes are NOT computed here.
type WalkEntry struct {
    Location string
    Size     int64
    Mtime    time.Time
    ETag     string
}

// Walk lists every object under root in store and forwards each as a
// WalkEntry. Errors during iteration go to errc; the caller MUST
// consume both channels to completion.
func Walk(ctx context.Context, store storage.Storage, root string) (<-chan WalkEntry, <-chan error) {
    out := make(chan WalkEntry, 64)
    errc := make(chan error, 1)
    go func() {
        defer close(out)
        defer close(errc)
        it, err := store.List(ctx, root)
        if err != nil {
            errc <- err
            return
        }
        defer func() { _ = it.Close() }()
        for {
            obj, err := it.Next(ctx)
            if errors.Is(err, io.EOF) {
                return
            }
            if err != nil {
                errc <- err
                return
            }
            entry := WalkEntry{
                Location: obj.Key,
                Size:     obj.Size,
                Mtime:    obj.ModTime,
                ETag:     obj.ETag,
            }
            select {
            case out <- entry:
            case <-ctx.Done():
                errc <- ctx.Err()
                return
            }
        }
    }()
    return out, errc
}
```

Tests with `local.LocalFS` rooted at `t.TempDir()`:
- Empty dir → no entries.
- 3 files → 3 entries with non-zero sizes and mtimes.
- Cancellation: cancel ctx mid-walk; expect ctx.Err() on errc.

- [ ] Commit:

```bash
git commit -m "feat(scan): walker streams WalkEntry from storage.Storage"
```

---

### Task 4: `internal/scan/differ.go`

**Files:**
- Create: `internal/scan/differ.go`
- Create: `internal/scan/differ_test.go`

```go
package scan

import (
    "github.com/blackforge/embookshelf/internal/model"
)

// Changeset is the output of Diff. Each slice holds the WalkEntry +
// the matching DB row (for Changed and Missing) or just the
// WalkEntry (for New) / just the model.File (for Missing).
type Changeset struct {
    // Unchanged: row exists, size+mtime match. No work needed.
    Unchanged []model.File
    // Changed: row exists but size or mtime differ — re-hash + update.
    Changed []ChangedEntry
    // New: walked but not in DB — enqueue ingest.
    New []WalkEntry
    // Missing: in DB but not walked — mark missing_since.
    Missing []model.File
}

// ChangedEntry pairs the live observation with the stale DB row.
type ChangedEntry struct {
    Walk WalkEntry
    DB   model.File
}

// Diff computes the changeset comparing walked entries against the
// current DB rows for one library. Both slices may be in any order;
// Diff sorts internally by Location.
func Diff(walked []WalkEntry, dbFiles []model.File) Changeset {
    // Build a map of DB rows by Location for O(1) lookup.
    byLoc := make(map[string]model.File, len(dbFiles))
    for _, f := range dbFiles {
        byLoc[f.Location] = f
    }

    var cs Changeset
    seen := make(map[string]bool, len(walked))
    for _, w := range walked {
        seen[w.Location] = true
        f, ok := byLoc[w.Location]
        if !ok {
            cs.New = append(cs.New, w)
            continue
        }
        // Compare size + mtime (truncated to whole seconds — local FS
        // and S3 both report at ≥1s resolution). Float ETags would
        // fail equality; we strip subsecond precision deliberately.
        if w.Size == f.Size && sameSecond(w.Mtime, f.Mtime) {
            cs.Unchanged = append(cs.Unchanged, f)
            continue
        }
        cs.Changed = append(cs.Changed, ChangedEntry{Walk: w, DB: f})
    }
    for _, f := range dbFiles {
        if !seen[f.Location] {
            cs.Missing = append(cs.Missing, f)
        }
    }
    return cs
}

func sameSecond(a, b time.Time) bool {
    return a.Truncate(time.Second).Equal(b.Truncate(time.Second))
}
```

Tests cover all 4 categories:
- All unchanged
- One new
- One changed (size differs / mtime differs)
- One missing
- Mixed: 2 unchanged + 1 changed + 1 new + 1 missing
- Empty dbFiles + walked → all New
- Empty walked + dbFiles → all Missing
- Walked entry where DB row has missing_since set: still classified as Unchanged or Changed by size+mtime; the caller is responsible for ClearMissing.

- [ ] Commit:

```bash
git commit -m "feat(scan): differ classifies walk vs DB into Unchanged/Changed/New/Missing"
```

---

### Task 5: `internal/scan/reattach.go`

**Files:**
- Create: `internal/scan/reattach.go`
- Create: `internal/scan/reattach_test.go`

```go
// MaybeReattach handles the rename case. After a Changed entry has
// been re-hashed, if the new hash matches another files row in the
// same library, the file was probably renamed: update that row's
// location instead of orphaning the old row. Returns (true, nil)
// when a reattach happened.
//
// The caller passes:
//   - the freshly computed hash for the moved file,
//   - the new location it now lives at,
//   - the OLD row's id (the row that was at the previous location).
//
// On reattach: the OTHER row (the one whose hash matches) takes
// over the new location, and the OLD row is marked missing so the
// purge sweeper deletes it after the TTL. This preserves book_id
// continuity when the user renames a file outside the app.
func MaybeReattach(ctx context.Context, files *repo.FileRepo, libraryID string, hash []byte, newLocation string, oldRowID string) (bool, error) {
    if len(hash) == 0 { return false, nil }
    matches, err := files.GetByContentHash(ctx, hash)
    if err != nil { return false, err }
    for _, m := range matches {
        if m.LibraryID != libraryID { continue }
        if m.ID == oldRowID { continue }
        // Reattach: m takes over newLocation; old row goes missing.
        if err := files.UpdateLocation(ctx, m.ID, newLocation); err != nil {
            return false, err
        }
        if err := files.MarkMissing(ctx, oldRowID, time.Now()); err != nil {
            return false, err
        }
        return true, nil
    }
    return false, nil
}
```

Tests: classic rename scenario — file A.epub renamed to B.epub locally; the row's hash already exists; reattach swaps location and marks the stale row missing.

- [ ] Commit:

```bash
git commit -m "feat(scan): hash-based rename reattach"
```

---

## Phase 4 — Library Scan Worker Rewrite

### Task 6: Rewrite `internal/task/library_scan.go`

**Files:**
- Modify: `internal/task/library_scan.go`

Replace the body of `LibraryScan` with the orchestration logic:

```go
func LibraryScan(ctx context.Context, args LibraryScanArgs, deps LibraryScanDeps) error {
    lib, err := deps.Lib.GetByID(ctx, args.LibraryID)
    if err != nil { return err }
    root := lib.Path
    if lib.Root != nil && *lib.Root != "" { root = *lib.Root }
    if root == "" {
        slog.Warn("library scan: empty root, skipping", "library_id", lib.ID)
        return nil
    }
    if deps.Storage == nil || deps.Files == nil {
        slog.Warn("library scan: missing deps, falling back", "library_id", lib.ID)
        return legacyScan(ctx, lib, deps) // keeps the old BookExistsByPath path
    }

    dbFiles, err := deps.Files.ListByLibrary(ctx, lib.ID)
    if err != nil {
        return fmt.Errorf("list db files: %w", err)
    }

    // Phase 1: walk
    var walked []scan.WalkEntry
    entries, errc := scan.Walk(ctx, deps.Storage, root)
    for e := range entries {
        // Convert from absolute key (LocalFS rooted at /) to library-
        // relative location consistent with the DB.
        e.Location = relativizeLocation(lib, "/"+e.Location)
        walked = append(walked, e)
    }
    if err := <-errc; err != nil && !errors.Is(err, context.Canceled) {
        slog.Warn("library scan: walk error", "library_id", lib.ID, "err", err)
    }

    cs := scan.Diff(walked, dbFiles)

    // Phase 2: act
    fileCount := len(walked)
    discovered := 0

    for _, f := range cs.Unchanged {
        if f.MissingSince != nil {
            // File reappeared after going missing; clear the flag.
            _ = deps.Files.ClearMissing(ctx, f.ID)
        }
    }

    for _, ce := range cs.Changed {
        // Re-hash + update the row in place.
        key := joinKey(root, ce.Walk.Location)
        hash, size, herr := hashing.HashFile(ctx, deps.Storage, key)
        if herr != nil {
            slog.Warn("library scan: rehash failed", "loc", ce.Walk.Location, "err", herr)
            continue
        }
        // Reattach: if the new hash matches a different row in this
        // library, swap their locations.
        if reattached, rerr := scan.MaybeReattach(ctx, deps.Files, lib.ID, hash, ce.Walk.Location, ce.DB.ID); rerr != nil {
            slog.Warn("library scan: reattach failed", "loc", ce.Walk.Location, "err", rerr)
        } else if reattached {
            continue
        }
        if err := deps.Files.SetContentHash(ctx, ce.DB.ID, hash, size, ce.Walk.Mtime); err != nil {
            slog.Warn("library scan: update changed row", "id", ce.DB.ID, "err", err)
        }
    }

    for _, w := range cs.New {
        if !fileproc.IsSupported("/" + w.Location) { continue }
        format := fileproc.FormatForExt(filepath.Ext(w.Location))
        absPath := joinKey(root, w.Location) // bookdrop still wants the absolute path
        item, created, err := deps.BookDrop.Enqueue(ctx, "/"+absPath, format, w.Size)
        if err != nil {
            slog.Warn("library scan: enqueue", "path", absPath, "err", err)
            continue
        }
        if !created { continue }
        if deps.Queue != nil {
            if err := deps.Queue.EnqueueBookDrop(ctx, item.ID); err != nil {
                slog.Warn("library scan: enqueue queue job", "id", item.ID, "err", err)
            }
        }
        discovered++
    }

    for _, f := range cs.Missing {
        if f.MissingSince != nil { continue } // already marked
        if err := deps.Files.MarkMissing(ctx, f.ID, time.Now()); err != nil {
            slog.Warn("library scan: mark missing", "id", f.ID, "err", err)
        }
    }

    if err := deps.Lib.TouchScan(ctx, lib.ID, fileCount, discovered); err != nil {
        slog.Warn("library scan: touch", "id", lib.ID, "err", err)
    }
    slog.Info("library scan done",
        "library", lib.ID, "root", root,
        "files", fileCount, "discovered", discovered,
        "changed", len(cs.Changed), "missing", len(cs.Missing),
    )
    return nil
}

// joinKey is the same helper as in task/files_backfill.go; consider
// promoting to internal/scan or a shared util.
func joinKey(root, loc string) string {
    root = strings.TrimRight(root, "/")
    loc = strings.TrimLeft(loc, "/")
    if root == "" { return loc }
    if loc == "" { return root }
    return root + "/" + loc
}
```

`legacyScan` is the previous implementation kept verbatim as a fallback when `deps.Files` or `deps.Storage` is nil. This protects the SQLite-test code paths that pass nil today.

- [ ] Commit:

```bash
git commit -m "refactor(task): library scan = walk + diff + ingest

Replaces the linear walk-then-check loop with a structured
two-phase pipeline. Files with matching size+mtime skip the
re-hash entirely; changed files re-hash and update in place;
missing files are soft-flagged for the 24h purge sweeper.
Hash-based reattach keeps book_id continuity across renames."
```

---

## Phase 5 — Missing Purge Sweeper

### Task 7: `internal/task/missing_purge.go`

**Files:**
- Create: `internal/task/missing_purge.go`
- Create: `internal/task/missing_purge_test.go`

```go
package task

import (
    "context"
    "log/slog"
    "time"

    "github.com/blackforge/embookshelf/internal/repo"
)

// MissingTTL is the grace period before missing files are purged.
// Spec §5.3 sets this at 24h to ride out unmounted drives, S3 region
// blips, and IAM hiccups.
const MissingTTL = 24 * time.Hour

// RunMissingPurge deletes files rows whose missing_since is older
// than MissingTTL. Returns the count purged.
func RunMissingPurge(ctx context.Context, files *repo.FileRepo) (int64, error) {
    return files.DeleteMissingOlderThan(ctx, MissingTTL)
}

// LoopMissingPurge runs RunMissingPurge on a ticker until ctx is
// cancelled. Errors are logged but do not stop the loop.
func LoopMissingPurge(ctx context.Context, files *repo.FileRepo, every time.Duration) {
    if every <= 0 { every = time.Hour }
    t := time.NewTicker(every)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-t.C:
            n, err := RunMissingPurge(ctx, files)
            if err != nil {
                slog.Warn("missing purge", "err", err)
                continue
            }
            if n > 0 {
                slog.Info("missing purge", "deleted", n)
            }
        }
    }
}
```

Tests:
- 0 missing rows → returns (0, nil).
- 1 missing row, missing_since < TTL → not deleted.
- 1 missing row, missing_since > TTL → deleted, returns 1.
- Loop test with a tight ticker + cancellable ctx exits cleanly.

`main.go` change:

```go
go task.LoopMissingPurge(ctx, fileRepo, time.Hour)
```

Place after the existing `RunFilesBackfill` goroutine.

- [ ] Commit:

```bash
git commit -m "feat(task): missing-files purge sweeper (24h TTL)"
```

---

## Phase 6 — Verification & PR

### Task 8: Final pass

- [ ] `make ci-local` green.
- [ ] `git diff --stat origin/main..HEAD` confirms only the listed files changed.
- [ ] Migration roundtrip if dev DB available.
- [ ] Manual smoke if Docker available: rename a file in a library; trigger a scan; verify the renamed row keeps its book_id (look at `files.book_id` before/after).
- [ ] Push, open PR.

```bash
gh pr create --base main --title "feat(scan): two-phase scan + reconciliation (Plan C of 8)" --body-file <(cat <<'EOF'
## Summary
- Library scan rewritten as walk → diff → ingest (Plan C of 8).
- Files with matching size + mtime skip re-hash entirely.
- Missing files are soft-deleted with a 24-hour TTL via files.missing_since.
- Hash-based reattach keeps book_id continuity when files are renamed locally.
- New packages: internal/scan (walker, differ, reattach) and internal/task/missing_purge.

## Plan
docs/superpowers/plans/2026-04-29-storage-plan-c-scan.md.
Spec: docs/spec/storage.spec.md §5.3.

## Test plan
- [x] make ci-local green.
- [x] go test ./internal/scan/... ./internal/repo/... ./internal/task/...
- [ ] Manual smoke: rename a file in a library; next scan reattaches the row.
EOF
)
```

---

## Self-Review

**Spec coverage:**
- §5.3 two-phase scan → covered (walk/diff/act).
- §5.3 missing 24h TTL → covered (missing_since + purge sweeper).
- §5.1 hash-based identity → reinforced by reattach.
- §5.2 ETag advisory → ETag plumbed through WalkEntry; ignored on local; useful in Plan F.
- §5.4 change notification (inotify, S3 events) → still deferred; the periodic full walk every 6h is implicit in the scan worker's existing schedule.

**Risks:**
- Differ comparing mtime at 1-second resolution: filesystem write within the same second of a previous scan + size unchanged would be misclassified Unchanged. Acceptable trade-off; the next scan catches it once mtime advances.
- Walking very large libraries: the channel buffer (64) plus ListByLibrary's full-table read are O(n) memory. For a library with millions of files we'd switch to a streaming differ. Today's deployments are well below that.
- Reattach across libraries is intentionally NOT supported: a file moved between libraries gets a fresh row in the destination and the old row goes missing.
- Backward compat: `legacyScan` fallback keeps existing scan tests green when deps.Files is nil.

**Type consistency:** `WalkEntry`, `Changeset{Unchanged, Changed, New, Missing}`, `ChangedEntry`, `MaybeReattach`, `LoopMissingPurge`, `RunMissingPurge`, `MissingTTL` referenced consistently across tasks.
