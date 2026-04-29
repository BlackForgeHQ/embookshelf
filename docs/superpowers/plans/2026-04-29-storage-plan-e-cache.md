# Cover Cache Reorg — Implementation Plan (Plan E of 8)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Re-key the cover store from `${root}/books/{bookID}` to `${root}/covers/<hash[0:2]>/<hash>.<ext>`, the content-hash layout the spec wants. Existing covers migrate at boot via a one-shot backfill that hashes each file and rewrites the path; the DB gains `books.cover_hash BYTEA` to point at the new entries. The `bookdrop/{id}` namespace stays as-is (pre-approval, no hash available yet); on approve, the cover moves to the hashed location and `cover_hash` is set.

**Architecture:** `internal/coverstore` adds a hash-keyed namespace alongside the existing book-id one. Saves accept (or compute) a hash and write to `covers/<hash[0:2]>/<hash>.<ext>` atomically. Reads prefer hash-keyed; fall back to id-keyed when `cover_hash` is NULL. A new `cmd/embookshelf` boot pass (sibling to `RunFilesBackfill`) walks `books/{id}` files, hashes each, copies to the new path, sets `cover_hash`, then deletes the legacy file. Idempotent; safe to re-run. The `serve` handler uses `cover_hash` if present, otherwise the legacy lookup — once backfill completes, all reads route through the new path.

**Tech Stack:** Go 1.25 stdlib (`crypto/sha256`, `io`, `os`, `path/filepath`). No new dependencies. Reuses Plan A's atomic-rename pattern from `coverstore.writeAtomic`.

**Companion spec:** [`docs/spec/storage.spec.md`](../../spec/storage.spec.md) §7 (cache layout), §3.1 (derivatives never inside library prefix).

**Locked decisions:**
- Hash algorithm: sha256 (consistent with Plan B file hashing).
- Extension is derived from the cover's MIME (`image/jpeg` → `.jpg`, `image/png` → `.png`, etc.). Books DB row already stores `cover_mime`.
- The `text/` and `thumbnails/` caches in spec §7 are **deferred** — not implemented in this plan. Plan E2 if/when text extraction or thumbnail generation lands.
- Bookdrop namespace stays book-id-keyed: pre-approval covers don't have a content_hash yet. Promote-to-book moves them to hashed layout.
- Backfill is best-effort: files that fail to hash are left in the legacy location with a warning log. Future scans retry.

**Depends on:** Plan A (storage interface), Plan B (content hashing concept and `internal/hashing`).

**Out of scope:**
- Text and thumbnail caches (Plan E2).
- Shared cache bucket for multi-instance deployments — local cache only.
- Removing `books.has_cover` / `books.cover_mime` columns.
- Changes to the public cover URL (still `/api/books/:id/cover`).

---

## File Structure

### Files created

| Path | Responsibility |
|---|---|
| `internal/migrator/migrations/postgres/000027_books_cover_hash.up.sql` | ADD `books.cover_hash BYTEA`. |
| `internal/migrator/migrations/postgres/000027_books_cover_hash.down.sql` | DROP it. |
| `internal/migrator/migrations/sqlite/000027_books_cover_hash.up.sql` | SQLite parallel. |
| `internal/migrator/migrations/sqlite/000027_books_cover_hash.down.sql` | SQLite parallel. |
| `internal/coverstore/store_test.go` | Unit tests for the new hash-keyed save/load (file likely doesn't exist yet — create). |
| `internal/task/covers_backfill.go` | One-shot at-boot worker that migrates legacy book-id covers to hash-keyed paths. |
| `internal/task/covers_backfill_test.go` | Unit tests with `t.TempDir()`. |

### Files modified

| Path | Change |
|---|---|
| `internal/coverstore/store.go` | Add `SaveBookHashed(hash []byte, mime string, data []byte) (string, error)`, `OpenBookHashed(hash []byte, mime string) (io.ReadCloser, error)`, `DeleteBookHashed(hash []byte, mime string) error`. Update `PromoteBookDropToBook` to take a hash + mime, hash and rename to the new path, return the hash and the canonical path. |
| `internal/repo/library.go` | Read `books.cover_hash` in `bookCols` + `scanBook`. Add `SetCoverHash(ctx, bookID, hash) error`. |
| `internal/model/book.go` | Add `CoverHash []byte` to `model.Book`. |
| `internal/handler/cover.go` (or wherever covers are served — likely `library.go` or a `cover` handler — find via `grep cover_mime internal/handler`) | Use `book.CoverHash` to resolve the disk path; fall back to the legacy `BookPath(book.ID)` only when `CoverHash` is nil. |
| `internal/service/bookdrop.go` | In `Approve`, after `PromoteBookDropToBook` returns the new hash, call `libRepo.SetCoverHash(ctx, bookID, hash)`. |
| `cmd/embookshelf/main.go` | Launch `task.RunCoversBackfill` once at boot, after `RunFilesBackfill`. |

---

## Phase 1 — Schema

### Task 1: `books.cover_hash` migration

**Files:**
- Create: `internal/migrator/migrations/postgres/000027_books_cover_hash.{up,down}.sql`
- Create: `internal/migrator/migrations/sqlite/000027_books_cover_hash.{up,down}.sql`

PG up:

```sql
ALTER TABLE books ADD COLUMN IF NOT EXISTS cover_hash BYTEA;
CREATE INDEX IF NOT EXISTS idx_books_cover_hash ON books(cover_hash) WHERE cover_hash IS NOT NULL;
```

PG down:

```sql
DROP INDEX IF EXISTS idx_books_cover_hash;
ALTER TABLE books DROP COLUMN IF EXISTS cover_hash;
```

SQLite up — modernc doesn't accept `IF NOT EXISTS` on ADD COLUMN:

```sql
ALTER TABLE books ADD COLUMN cover_hash BLOB;
CREATE INDEX IF NOT EXISTS idx_books_cover_hash ON books(cover_hash) WHERE cover_hash IS NOT NULL;
```

SQLite down:

```sql
DROP INDEX IF EXISTS idx_books_cover_hash;
ALTER TABLE books DROP COLUMN cover_hash;
```

Commit:

```bash
git commit -m "feat(db): books.cover_hash for hash-keyed cover store"
```

---

## Phase 2 — Coverstore Layout

### Task 2: Add hash-keyed save / open / delete + extension helper

**File to modify:** `internal/coverstore/store.go`

Add a small extension helper:

```go
// extForMIME maps a cover's MIME type to a filename suffix. Unknown
// MIMEs default to ".bin" — the cover still serves correctly because
// the response Content-Type comes from the DB row, not the path.
func extForMIME(mime string) string {
    switch strings.ToLower(strings.TrimSpace(mime)) {
    case "image/jpeg", "image/jpg":
        return ".jpg"
    case "image/png":
        return ".png"
    case "image/webp":
        return ".webp"
    case "image/gif":
        return ".gif"
    case "image/avif":
        return ".avif"
    default:
        return ".bin"
    }
}
```

Add hash-keyed methods alongside the existing id-keyed ones:

```go
// hashedDir returns ${root}/covers (the new hash-keyed namespace).
func (s *Store) hashedDir() string { return filepath.Join(s.root, "covers") }

// HashedPath returns the disk path for a cover keyed by content hash.
// Layout: covers/<hash[0:2]>/<hash><ext>. Hash is hex-encoded.
func (s *Store) HashedPath(hash []byte, mime string) string {
    if len(hash) == 0 {
        return ""
    }
    hex := fmt.Sprintf("%x", hash)
    bucket := hex[:2]
    return filepath.Join(s.hashedDir(), bucket, hex+extForMIME(mime))
}

// SaveBookHashed writes data atomically to the hash-keyed path.
// Idempotent: re-saving identical bytes is a no-op (Stat skip).
func (s *Store) SaveBookHashed(hash []byte, mime string, data []byte) error {
    p := s.HashedPath(hash, mime)
    if p == "" {
        return errors.New("coverstore: empty hash")
    }
    if _, err := os.Stat(p); err == nil {
        return nil // already there
    }
    return writeAtomic(p, data)
}

// OpenBookHashed returns a reader for the cover at the hashed path.
func (s *Store) OpenBookHashed(hash []byte, mime string) (io.ReadCloser, error) {
    p := s.HashedPath(hash, mime)
    if p == "" {
        return nil, os.ErrNotExist
    }
    return os.Open(p)
}

// DeleteBookHashed removes the cover at the hashed path. Missing is
// not an error.
func (s *Store) DeleteBookHashed(hash []byte, mime string) error {
    p := s.HashedPath(hash, mime)
    if p == "" {
        return nil
    }
    return removeIfExists(p)
}
```

Tests in `internal/coverstore/store_test.go`:

- `SaveBookHashed` writes to expected path, sha256 round-trip works.
- `OpenBookHashed` reads back the same bytes.
- Two distinct hashes write to distinct files.
- Same hash + different MIME write to distinct files (.jpg vs .png). Defensible: the MIME is part of the path so a malicious/erroneous Save with a wrong MIME doesn't clobber.
- Missing file returns ErrNotExist on Open.
- Idempotent re-save: second SaveBookHashed with same hash+mime+bytes is a no-op (no temp file leakage, no error).

Commit:

```bash
git commit -m "feat(coverstore): hash-keyed save/open/delete (covers/<hash>)"
```

---

## Phase 3 — DB & Service Wiring

### Task 3: `LibraryRepo.SetCoverHash` + `model.Book.CoverHash`

**Files to modify:**
- `internal/model/book.go` — add `CoverHash []byte` field with doc comment.
- `internal/repo/library.go` — append `cover_hash` to `bookCols` and `bookColsReturning` (search the file). Update `scanBook` to scan into the new field. Add:

```go
// SetCoverHash records the sha256 of the cover image. NULL means
// "not yet hashed" (covers backfill will set it).
func (r *LibraryRepo) SetCoverHash(ctx context.Context, bookID string, hash []byte) error {
    const qPG = `UPDATE books SET cover_hash = $1 WHERE id = $2`
    const qSQLite = `UPDATE books SET cover_hash = ?1 WHERE id = ?2`
    _, err := r.db.SQL.ExecContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), hash, bookID)
    return err
}
```

Tests: append a small `TestLibraryRepo_setCoverHash` to `internal/repo/library_test.go` if it exists, or add a minimal one.

Commit:

```bash
git commit -m "feat(repo): SetCoverHash + Book.CoverHash"
```

---

### Task 4: `Approve` writes cover hash; `serveCover` handler reads it

**Files to modify:**
- `internal/service/bookdrop.go` — In `Approve`, change the cover-promotion block to:
  1. Read the bookdrop's cover bytes via `s.covers.OpenBookDrop(item.ID)` if `item.HasCover`.
  2. sha256 the bytes.
  3. `s.covers.SaveBookHashed(hash, item.CoverMime, bytes)`.
  4. `s.libs.SetCoverHash(ctx, created.ID, hash)`.
  5. Best-effort `s.covers.DeleteBookDrop(item.ID)`.

  The existing `PromoteBookDropToBook` (book-id-keyed) is no longer called for the new flow but **don't delete it yet** — the legacy serve fallback uses `OpenBook(bookID)` for un-backfilled covers. Mark it with a `// Deprecated:` doc comment instead.

- `internal/handler/<cover-handler>.go` — find the cover endpoint via `grep -n "cover_mime\|covers.OpenBook" internal/handler/`. Replace the body to:

```go
if len(book.CoverHash) > 0 {
    rc, err := h.covers.OpenBookHashed(book.CoverHash, book.CoverMime)
    if err == nil {
        defer func() { _ = rc.Close() }()
        c.Header("Content-Type", book.CoverMime)
        // … existing cache headers …
        _, _ = io.Copy(c.Writer, rc)
        return
    }
    if !errors.Is(err, os.ErrNotExist) {
        // log + 500
    }
}
// Legacy fallback (un-backfilled).
rc, err := h.covers.OpenBook(book.ID)
// … existing logic …
```

Update handler tests if they assert specific paths.

Commit:

```bash
git commit -m "feat(service+handler): Approve and cover-serve use hash-keyed store"
```

---

## Phase 4 — Boot-Time Backfill

### Task 5: `RunCoversBackfill`

**Files to create:**
- `internal/task/covers_backfill.go`
- `internal/task/covers_backfill_test.go`

**File to modify:** `cmd/embookshelf/main.go`

```go
package task

import (
    "context"
    "crypto/sha256"
    "io"
    "log/slog"
    "os"
    "time"

    "github.com/blackforge/embookshelf/internal/coverstore"
    "github.com/blackforge/embookshelf/internal/repo"
)

// CoversBackfillDeps groups the dependencies the cover migration needs.
type CoversBackfillDeps struct {
    Library *repo.LibraryRepo
    Covers  *coverstore.Store
    Sleep   time.Duration // pause between books; 0 → no pause
}

// RunCoversBackfill walks every book whose CoverHash is NULL and has
// HasCover=true. For each: read the legacy book-id-keyed file, hash
// it, save under the hash path, write CoverHash to the DB, delete
// the legacy file.
//
// Idempotent. Errors per-book are logged and skipped; the next boot
// retries.
func RunCoversBackfill(ctx context.Context, deps CoversBackfillDeps) error {
    if deps.Library == nil || deps.Covers == nil {
        return nil
    }
    books, err := deps.Library.ListBooksMissingCoverHash(ctx) // new repo method
    if err != nil {
        return err
    }
    migrated := 0
    for _, b := range books {
        if err := ctx.Err(); err != nil {
            return err
        }
        legacy := deps.Covers.BookPath(b.ID)
        f, err := os.Open(legacy)
        if err != nil {
            slog.Warn("covers backfill: open legacy", "book_id", b.ID, "err", err)
            continue
        }
        h := sha256.New()
        if _, err := io.Copy(h, f); err != nil {
            _ = f.Close()
            slog.Warn("covers backfill: hash", "book_id", b.ID, "err", err)
            continue
        }
        _ = f.Close()
        sum := h.Sum(nil)

        // Re-open to write to the hashed path.
        data, err := os.ReadFile(legacy)
        if err != nil {
            slog.Warn("covers backfill: re-read", "book_id", b.ID, "err", err)
            continue
        }
        if err := deps.Covers.SaveBookHashed(sum, b.CoverMime, data); err != nil {
            slog.Warn("covers backfill: save hashed", "book_id", b.ID, "err", err)
            continue
        }
        if err := deps.Library.SetCoverHash(ctx, b.ID, sum); err != nil {
            slog.Warn("covers backfill: set hash", "book_id", b.ID, "err", err)
            continue
        }
        if err := deps.Covers.DeleteBook(b.ID); err != nil {
            slog.Warn("covers backfill: delete legacy", "book_id", b.ID, "err", err)
            // non-fatal: the file still serves through legacy fallback
        }
        migrated++
        if deps.Sleep > 0 {
            select {
            case <-time.After(deps.Sleep):
            case <-ctx.Done():
                return ctx.Err()
            }
        }
    }
    if migrated > 0 {
        slog.Info("covers backfill done", "migrated", migrated)
    }
    return nil
}
```

`LibraryRepo.ListBooksMissingCoverHash(ctx) ([]model.Book, error)`:

```go
const qPG = `SELECT ` + bookCols + ` ` + bookFromPG + `
    WHERE b.has_cover = TRUE AND b.cover_hash IS NULL AND b.deleted_at IS NULL
    LIMIT 500`
const qSQLite = ... -- mirror with ? placeholders
```

The query passes user_id=NULL or empty; the cover backfill doesn't care about user-progress rows, but the bookCols join needs the placeholder. Safe value: `''` for user_id (matches no rows in user_book_progress).

`main.go` change: launch `RunCoversBackfill` in a goroutine after `RunFilesBackfill`:

```go
go func() {
    backfillCtx, cancel := context.WithTimeout(context.Background(), 1*time.Hour)
    defer cancel()
    if err := task.RunCoversBackfill(backfillCtx, task.CoversBackfillDeps{
        Library: libRepo,
        Covers:  covers,
    }); err != nil {
        slog.Warn("covers backfill", "err", err)
    }
}()
```

Tests:
- 0 books missing hash → no-op, returns nil.
- 1 book missing hash, legacy file exists → hash computed, saved at hashed path, DB updated, legacy file removed.
- 1 book missing hash but legacy file missing → skipped with warning, no DB change.
- Re-run after success → no-op (the LIMIT-500 query returns no rows).

Commit:

```bash
git commit -m "feat(task): boot-time covers backfill (book-id → hash-keyed)"
```

---

## Phase 5 — Verification

### Task 6: Verify and PR

- [ ] `make ci-local` green.
- [ ] Existing tests pass.
- [ ] `git diff --stat origin/main..HEAD` reflects the planned scope.
- [ ] Push, open PR.

---

## Self-Review

**Spec coverage:**
- §7 cache layout: covers/<hash[0:2]>/<hash>.<ext> → covered. text/ and thumbnails/ deferred to Plan E2.
- §3.1 derivatives never inside library prefix → respected: covers stay under DataPath, never under library.root.

**Risks:**
- The legacy fallback in the handler keeps un-backfilled covers serving until the boot worker catches up. After backfill completes, `cover_hash IS NOT NULL` for every cover; the fallback path becomes dead code and can be removed in a follow-up.
- Concurrent reads during the migration: when the worker has copied bytes to the new path but hasn't yet set `cover_hash`, the handler still serves from the legacy path. Once the DB row updates, subsequent reads route to the new path. Window is sub-second per cover.
- Different MIMEs for the same hash produce different filenames. If a cover is later promoted with a corrected MIME, the old file leaks. Plan E2 (or a periodic GC) can sweep orphaned `covers/` files.

**Type consistency:** `SaveBookHashed`, `OpenBookHashed`, `DeleteBookHashed`, `HashedPath`, `SetCoverHash`, `ListBooksMissingCoverHash`, `RunCoversBackfill`, `CoversBackfillDeps`, `model.Book.CoverHash` consistent across tasks.
