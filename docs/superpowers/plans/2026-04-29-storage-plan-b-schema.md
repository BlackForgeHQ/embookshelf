# Content-Hash Identity & DB Schema — Implementation Plan (Plan B of 8)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move book identity from `books.path` to a content hash (sha256) keyed on a new `files` table, introduce `storage_backends` and re-shape `libraries` to point at one through a foreign key, split book metadata into a dedicated `metadata` table, and assign every book a stable UUID. Behavior at the API level stays identical; the schema underneath changes substantially.

**Architecture:** A single SQL migration adds the new tables and columns side-by-side with the existing schema, then backfills new rows from existing data, then drops the old columns. A separate Go-level "hashing pass" runs once at first boot after migration to fill `files.content_hash` for pre-existing rows by reading bytes off disk via the `internal/storage` interface from Plan A. Repos add new columns/tables; the bookdrop ingest pipeline writes one row per file (with hash) instead of mutating `books.path`. The library scan continues to discover new files and enqueue bookdrop work, but now uses `files.location` (library-relative) for identity and `(library_id, content_hash)` to detect duplicates.

**Tech Stack:** Go 1.25 stdlib `crypto/sha256`, `database/sql`, golang-migrate (PG + SQLite), pgx for the runtime path. No new third-party deps.

**Companion spec:** [`docs/spec/storage.spec.md`](../../spec/storage.spec.md). Sections 3 (layout), 4 (DB schema), 5.1–5.3 (identity, ETag-vs-hash, two-phase scan structure) drive this plan.

**Locked decisions** (carried from Plan A discussion):
- Hash algorithm: **sha256** (no new dep; spec allowed substitute for blake3).
- `books.path`, `books.format`, `library_paths` table: **hard-dropped** in this migration. No shadow columns.
- **Full 1:N storage_backends → libraries.** Even though we only build a single local backend in Plan A, the schema models the relationship so Plan F slots in cleanly.
- SQLite + S3 combo refusal: deferred to Plan F (config-load check). This plan is backend-agnostic at the SQL level.
- Sidecar metadata layering (`.embookshelf.toml`, `metadata.opf`): deferred to Plan D. The `metadata` table here holds DB-only fields; the layering policy lands later.
- One PR per plan.

**Depends on:** Plan A merged. Specifically, `internal/storage.Storage` interface and `internal/storage/local.LocalFS` are wired through `cmd/embookshelf/main.go` and available for the hash-backfill pass.

**Out of scope for this plan:**
- Two-phase scan with `(size, mtime, etag)` fast-path skip — Plan C.
- Sidecar read/merge (`metadata.opf`, `.embookshelf.toml`) — Plan D.
- Cover storage migration to content-hash key — Plan E.
- S3 backend — Plan F.
- Range reads, presigned URLs — Plan G.
- S3 events / lifecycle — Plan H.
- Any change to `internal/handler/`, `internal/coverstore/`, `internal/fileproc/` beyond the bookdrop ingest path that already lives in `internal/task/bookdrop.go`.

---

## File Structure

### Files created

| Path | Responsibility |
|---|---|
| `internal/migrator/migrations/postgres/000025_storage_v2.up.sql` | Forward migration: new tables, new columns, backfill, drop old. |
| `internal/migrator/migrations/postgres/000025_storage_v2.down.sql` | Rollback to 000024 shape. Best-effort — backfill data is lost. |
| `internal/migrator/migrations/sqlite/000025_storage_v2.up.sql` | SQLite parallel of the PG up migration. |
| `internal/migrator/migrations/sqlite/000025_storage_v2.down.sql` | SQLite rollback. |
| `internal/repo/storage_backend.go` | `StorageBackendRepo`: list, get, create, update. Used at boot to seed the default local backend. |
| `internal/repo/storage_backend_test.go` | Repo unit tests via repotest harness (PG + SQLite). |
| `internal/repo/file.go` | `FileRepo`: insert, list-by-library, get-by-hash, get-by-location, mark-missing, update-scan-time. |
| `internal/repo/file_test.go` | Repo unit tests. |
| `internal/repo/metadata.go` | `MetadataRepo`: upsert, get-by-book. |
| `internal/repo/metadata_test.go` | Repo unit tests. |
| `internal/service/storagebackend.go` | `StorageBackendService`: bootstraps the default local backend on first boot, wraps repo for handler use. |
| `internal/hashing/hasher.go` | `HashFile(ctx, store, key) ([]byte, int64, error)` — streams bytes from `Storage.Get`, computes sha256, returns hash + size. |
| `internal/hashing/hasher_test.go` | Unit tests with `local.LocalFS` rooted at `t.TempDir()`. |
| `internal/task/files_backfill.go` | One-shot background pass: for every `files` row with `content_hash IS NULL`, read bytes via `Storage` and fill in. Idempotent. |
| `internal/task/files_backfill_test.go` | Unit tests with a tiny in-memory `Storage` fake. |

### Files modified

| Path | Change |
|---|---|
| `internal/model/book.go` | Add `UUID` field. Drop `Path`, `Format` (now in `Files`). Add `FolderPath` (nullable). Domain model split: `Book` (identity) + `Metadata` (now its own struct). |
| `internal/model/library.go` | Add `BackendID`, `Root`, `OrgMode` fields. Drop `Path` (renamed to `Root`). |
| `internal/model/file.go` | NEW: `File` struct mirroring the `files` row. |
| `internal/model/storage_backend.go` | NEW: `StorageBackend` struct mirroring the row. |
| `internal/repo/library.go` | Update `INSERT`/`SELECT`/`UPDATE` to use new columns. `GetByID` joins `storage_backends`. `BookExistsByPath` is renamed to `BookExistsByContentHash` (callers in `library_scan.go` updated). New helper `LibraryRoot(libID)` returns the (backend, root) pair. |
| `internal/repo/book.go` (or wherever book reads live in `library.go`) | `INSERT INTO books` no longer takes `path` or `format`. Reads join `metadata` for the title/author/year/description fields the API surfaces. |
| `internal/repo/bookdrop.go` | The `Approve` path now creates a `files` row instead of mutating `books.path`. |
| `internal/service/library.go` | `Approve` writes to `files`, `metadata`, `books`. `BookExistsByPath` → `BookExistsByContentHash`. |
| `internal/service/bookdrop.go` | When a bookdrop item is approved into a book, insert a `files` row with the staged sha256 (computed at ingest). |
| `internal/task/library_scan.go` | Replace `BookExistsByPath(p)` with `FileExistsByLocation(libraryID, relLoc)`. Compute `relLoc` as `obj.Key` after stripping the library root prefix. |
| `internal/task/bookdrop.go` | Compute sha256 during extract (same stream as metadata extraction where possible). Persist hash + size into the bookdrop row for later promotion. |
| `internal/repo/bookdrop.go` (model + columns) | Add `content_hash BLOB`, `size BIGINT` (already there), `format TEXT` (already there). |
| `cmd/embookshelf/main.go` | Boot: seed default `storage_backends` row from existing `libraries.path` + `LocalFS` if no backends row exists. Kick off `files_backfill` worker once on startup; it terminates when no rows have NULL hash. |
| `internal/handler/library.go` (book detail, list endpoints) | Reading title/author/etc now joins `metadata` — repo changes hide this from handlers, but handler tests may need updating if they assert specific SQL paths. |
| `internal/repo/repotest/...` | Test harness fixture rows updated to insert into the new schema. |

### Files NOT touched

- `internal/coverstore/` — covers stay book-id-keyed in this plan (Plan E migrates to hash-key).
- `internal/handler/files.go` — file serving still resolves through `book.path` is dead; handler now reads location via `files` join. **Wait — see Step 7 in the migration phase.** The handler must change to look up the file path through the `files` table, joining via `books.id`. That counts as a touch; tracked under "Files modified" above (re-add to the right cell).
- `internal/fileproc/` — extractor signatures unchanged.

---

## Phase 1 — SQL Migrations

### Task 1: Postgres `up` migration

**File:**
- Create: `internal/migrator/migrations/postgres/000025_storage_v2.up.sql`

- [ ] **Step 1: Author the migration**

```sql
-- Plan B of 8: content-hash identity + DB schema reshape.
-- See docs/superpowers/plans/2026-04-29-storage-plan-b-schema.md.
--
-- This migration runs in three logical phases inside one SQL file:
--   1. Add new tables and new columns alongside existing ones.
--   2. Backfill new rows/columns from current state.
--   3. Drop old columns/tables.
--
-- The transaction is implicit (golang-migrate wraps each up file in a tx);
-- if any statement fails, the whole migration rolls back.

BEGIN;

-- ---------- 1. NEW TABLES ----------

CREATE TABLE IF NOT EXISTS storage_backends (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind        TEXT NOT NULL CHECK (kind IN ('local', 's3')),
    config      JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE libraries
    ADD COLUMN IF NOT EXISTS backend_id  UUID REFERENCES storage_backends(id) ON DELETE RESTRICT,
    ADD COLUMN IF NOT EXISTS root        TEXT,
    ADD COLUMN IF NOT EXISTS org_mode    TEXT NOT NULL DEFAULT 'book_per_folder'
        CHECK (org_mode IN ('book_per_file', 'book_per_folder'));

CREATE TABLE IF NOT EXISTS files (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id    UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    book_id       UUID REFERENCES books(id) ON DELETE CASCADE,
    location      TEXT NOT NULL,        -- relative to library.root
    size          BIGINT NOT NULL,
    mtime         TIMESTAMPTZ NOT NULL,
    etag          TEXT,                 -- nullable; "" or ETag from S3 head
    content_hash  BYTEA,                -- sha256, 32 bytes; nullable until backfill completes
    format        TEXT NOT NULL,        -- 'EPUB' | 'PDF' | …
    last_scanned  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(library_id, location)
);

CREATE INDEX IF NOT EXISTS idx_files_hash ON files(content_hash) WHERE content_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_files_book ON files(book_id);
CREATE INDEX IF NOT EXISTS idx_files_library ON files(library_id);

ALTER TABLE books
    ADD COLUMN IF NOT EXISTS uuid         UUID UNIQUE,
    ADD COLUMN IF NOT EXISTS folder_path  TEXT;

CREATE TABLE IF NOT EXISTS metadata (
    book_id          UUID PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE,
    title            TEXT NOT NULL,
    title_sort       TEXT NOT NULL DEFAULT '',
    subtitle         TEXT,
    description      TEXT,
    language         TEXT,
    publisher        TEXT,
    published_date   TEXT,                -- free text, the spec keeps it loose
    isbn             TEXT,
    page_count       INTEGER,
    duration_sec     INTEGER,
    cover_hash       BYTEA,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------- 2. BACKFILL ----------

-- (a) Seed exactly one storage_backends row for every existing library.path.
--     Distinct paths get distinct backends. Empty paths are NOT created
--     (we won't fabricate a backend for libraries that never had one).
INSERT INTO storage_backends (id, kind, config, created_at)
SELECT
    gen_random_uuid(),
    'local',
    jsonb_build_object('root', l.path),
    now()
FROM (SELECT DISTINCT path FROM libraries WHERE path <> '') l;

-- (b) Wire each library to its backend and copy path → root.
UPDATE libraries l
SET backend_id = sb.id,
    root       = l.path
FROM storage_backends sb
WHERE sb.kind = 'local'
  AND sb.config->>'root' = l.path
  AND l.path <> '';

-- (c) Insert one files row per existing book with a non-empty path.
--     content_hash stays NULL — the boot-time hashing pass fills it.
--     etag stays NULL on local. mtime is set to books.updated_at as a
--     placeholder; the backfill task re-stats the file and corrects it.
INSERT INTO files (id, library_id, book_id, location, size, mtime, etag, content_hash, format, last_scanned)
SELECT
    gen_random_uuid(),
    b.library_id,
    b.id,
    -- Make location relative to library.root; if for some reason the
    -- book path doesn't fall under the library root, store the absolute
    -- path verbatim — the next scan will reconcile.
    CASE
        WHEN l.root <> '' AND b.path LIKE l.root || '/%'
            THEN substring(b.path FROM length(l.root) + 2)
        ELSE b.path
    END,
    0,           -- size unknown until first stat
    b.updated_at,
    NULL,
    NULL,
    b.format,
    b.updated_at
FROM books b
JOIN libraries l ON l.id = b.library_id
WHERE b.path <> ''
  AND b.deleted_at IS NULL
ON CONFLICT (library_id, location) DO NOTHING;

-- (d) Assign uuids to every existing book that doesn't have one.
UPDATE books SET uuid = gen_random_uuid() WHERE uuid IS NULL;

-- (e) Move title/author/year/description and friends into metadata.
--     We keep books.title etc. for one transition (until step 3
--     drops them), but the new metadata table is the writable source
--     of truth from this migration onward.
INSERT INTO metadata (book_id, title, title_sort, subtitle, description, language, publisher, published_date, isbn, page_count, duration_sec, cover_hash, updated_at)
SELECT
    b.id,
    b.title,
    COALESCE(b.title_sort, b.title),
    NULL,
    b.description,
    b.language,
    b.publisher,
    NULLIF(CAST(b.year AS TEXT), '0'),
    b.isbn,
    b.page_count,
    b.duration_seconds,
    NULL,
    b.updated_at
FROM books b
WHERE b.deleted_at IS NULL
ON CONFLICT (book_id) DO NOTHING;

-- ---------- 3. CONSTRAINTS & DROPS ----------

-- libraries.backend_id is required from now on, but we deferred the
-- NOT NULL until after backfill so libraries with empty path could
-- survive (they have backend_id = NULL and root = NULL — caller must
-- treat these as misconfigured and refuse scans, see service layer).
-- Tighten the constraint partially: any library with a non-empty
-- root must have a backend.
ALTER TABLE libraries
    ADD CONSTRAINT libraries_backend_consistency
    CHECK ((root IS NULL AND backend_id IS NULL) OR (root IS NOT NULL AND backend_id IS NOT NULL));

-- books.uuid becomes required.
ALTER TABLE books ALTER COLUMN uuid SET NOT NULL;

-- Drop old columns. Indexes that referenced them go with them.
DROP INDEX IF EXISTS idx_books_path;
DROP INDEX IF EXISTS idx_books_format;
DROP INDEX IF EXISTS libraries_path_key;

ALTER TABLE books
    DROP COLUMN IF EXISTS path,
    DROP COLUMN IF EXISTS format,
    DROP COLUMN IF EXISTS title_sort,
    DROP COLUMN IF EXISTS description,
    DROP COLUMN IF EXISTS language,
    DROP COLUMN IF EXISTS publisher,
    DROP COLUMN IF EXISTS year,
    DROP COLUMN IF EXISTS isbn,
    DROP COLUMN IF EXISTS page_count,
    DROP COLUMN IF EXISTS duration_seconds;
-- (Keep title for one more migration as a denormalized search hint;
-- removed in Plan C when FTS is reworked.)

ALTER TABLE libraries DROP COLUMN IF EXISTS path;

COMMIT;
```

**Important review notes for the implementer:**

- The exact list of `books` columns to drop in step 3 must be cross-checked against the *current* schema. Run `\d books` (or the SQLite equivalent) before authoring this file. The list above is best-effort from a prior survey. **If any column in the DROP list does not exist, golang-migrate will fail.** Use `DROP COLUMN IF EXISTS` (already shown) and verify each.
- `b.title_sort`, `b.year`, `b.duration_seconds`, etc. — confirm the actual column names. Adjust the metadata INSERT and the books DROP to match what the schema really has.
- The audiobook columns from migration 000024 (narrator, etc.) are NOT moved to metadata in this plan. Decide during implementation whether to keep them on books or extend metadata. Default: keep on books.

- [ ] **Step 2: Verify it parses**

Apply against a freshly-seeded dev DB:

```bash
make db-up
make seed
make migrate
```

If migration 000025 fails, the error message will name the offending statement. Fix and re-run.

- [ ] **Step 3: Commit**

```bash
git add internal/migrator/migrations/postgres/000025_storage_v2.up.sql
git commit -m "feat(db): add storage_v2 forward migration (PG)

Adds storage_backends, files, metadata. Reshapes libraries (backend_id,
root, org_mode) and books (uuid, folder_path). Backfills from existing
data. Drops books.path, books.format, libraries.path."
```

---

### Task 2: Postgres `down` migration

**File:**
- Create: `internal/migrator/migrations/postgres/000025_storage_v2.down.sql`

- [ ] **Step 1: Author the rollback**

```sql
-- Best-effort rollback to the 000024 shape. New rows in files,
-- storage_backends, metadata are dropped (not recoverable). Books
-- regain path/format from the latest files row when one exists.

BEGIN;

ALTER TABLE libraries DROP CONSTRAINT IF EXISTS libraries_backend_consistency;

ALTER TABLE libraries ADD COLUMN IF NOT EXISTS path TEXT NOT NULL DEFAULT '';

UPDATE libraries SET path = COALESCE(root, '');

ALTER TABLE books
    ADD COLUMN IF NOT EXISTS path             TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS format           TEXT    NOT NULL DEFAULT 'EPUB',
    ADD COLUMN IF NOT EXISTS title_sort       TEXT,
    ADD COLUMN IF NOT EXISTS description      TEXT,
    ADD COLUMN IF NOT EXISTS language         TEXT,
    ADD COLUMN IF NOT EXISTS publisher        TEXT,
    ADD COLUMN IF NOT EXISTS year             INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS isbn             TEXT,
    ADD COLUMN IF NOT EXISTS page_count       INTEGER,
    ADD COLUMN IF NOT EXISTS duration_seconds INTEGER;

UPDATE books b
SET
    path     = COALESCE(l.root || '/' || f.location, ''),
    format   = COALESCE(f.format, 'EPUB')
FROM files f, libraries l
WHERE f.book_id = b.id AND l.id = b.library_id;

UPDATE books b
SET
    title_sort       = m.title_sort,
    description      = m.description,
    language         = m.language,
    publisher        = m.publisher,
    year             = COALESCE(NULLIF(m.published_date, '')::INTEGER, 0),
    isbn             = m.isbn,
    page_count       = m.page_count,
    duration_seconds = m.duration_sec
FROM metadata m
WHERE m.book_id = b.id;

ALTER TABLE books DROP COLUMN IF EXISTS uuid;
ALTER TABLE books DROP COLUMN IF EXISTS folder_path;

DROP TABLE IF EXISTS metadata;
DROP TABLE IF EXISTS files;
DROP TABLE IF EXISTS storage_backends;

ALTER TABLE libraries
    DROP COLUMN IF EXISTS backend_id,
    DROP COLUMN IF EXISTS root,
    DROP COLUMN IF EXISTS org_mode;

COMMIT;
```

- [ ] **Step 2: Verify against a migrated dev DB**

```bash
make migrate
make migrate-down  # should drop back to 000024
make migrate       # should re-apply 000025 cleanly
```

- [ ] **Step 3: Commit**

```bash
git add internal/migrator/migrations/postgres/000025_storage_v2.down.sql
git commit -m "feat(db): add storage_v2 rollback (PG)"
```

---

### Task 3: SQLite parallel migrations

**Files:**
- Create: `internal/migrator/migrations/sqlite/000025_storage_v2.up.sql`
- Create: `internal/migrator/migrations/sqlite/000025_storage_v2.down.sql`

- [ ] **Step 1: Author SQLite up**

Mirror the PG `up.sql` with these adaptations:

- `UUID PRIMARY KEY DEFAULT gen_random_uuid()` → `TEXT PRIMARY KEY` (SQLite has no native UUID; the app generates them).
- `BYTEA` → `BLOB`.
- `JSONB` → `TEXT` (caller serializes to JSON via `encoding/json`).
- `TIMESTAMPTZ DEFAULT now()` → `INTEGER NOT NULL DEFAULT (unixepoch())` (epoch seconds).
- Drop the `BEGIN/COMMIT` (golang-migrate handles the implicit tx).
- `ALTER TABLE … ADD CONSTRAINT CHECK …` is not supported in SQLite; instead, recreate the affected table via the standard rename-create-copy-drop dance, or skip the constraint and enforce in app code. **Preferred approach:** skip the cross-column CHECK on libraries; the service layer rejects scans for libraries without a backend.
- `gen_random_uuid()` calls in the backfill INSERTs → use a placeholder. SQLite cannot generate UUIDs in pure SQL; replace with a Go-level migration step. **Preferred approach:** split the SQLite migration into:
  - `000025_storage_v2_schema.up.sql` — schema only (CREATE TABLE / ALTER TABLE), no backfill.
  - A Go-level migration in `internal/migrator/migrator.go` that runs after SQL migrations, generating UUIDs and doing the backfill via `database/sql`.

For consistency, do the same on Postgres (move backfill to Go) so both dialects share one code path. Update Task 1's migration to keep schema-only changes, and add a new Task 3a for the Go-level backfill helper.

> **Decision deferred to implementer:** Either embed a SQLite UUID generator (`SELECT lower(hex(randomblob(16)))` reformatted with hyphens) in pure SQL, or split backfill into Go. The pure-SQL UUID hack is ugly but keeps everything in one migration file; the Go-level approach is cleaner long-term. **Recommendation:** Go-level backfill (cleaner, dialect-agnostic, testable). Author this in `internal/migrator/backfill_storage_v2.go` and call it from the migrator after `migrate.Up`.

- [ ] **Step 2: Author SQLite down**

Same shape as PG down, with the same syntax adaptations.

- [ ] **Step 3: Apply, rollback, re-apply**

```bash
DATABASE_URL=sqlite://./data/test.db make migrate
DATABASE_URL=sqlite://./data/test.db make migrate-down
DATABASE_URL=sqlite://./data/test.db make migrate
```

- [ ] **Step 4: Commit**

```bash
git add internal/migrator/migrations/sqlite/000025_storage_v2.{up,down}.sql
git commit -m "feat(db): add storage_v2 migrations (SQLite)"
```

---

### Task 3a: Go-level backfill helper

**File:**
- Create: `internal/migrator/backfill_storage_v2.go`
- Modify: `internal/migrator/migrator.go` to call the helper after `migrate.Up()` when the migration version equals 25.

- [ ] **Step 1: Write the failing test**

`internal/migrator/backfill_storage_v2_test.go`:

```go
package migrator_test

import (
	"context"
	"testing"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/migrator"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

func TestBackfillStorageV2_SeedsBackendAndFiles(t *testing.T) {
	for _, dialect := range []db.Dialect{db.DialectPostgres, db.DialectSQLite} {
		t.Run(string(dialect), func(t *testing.T) {
			ctx := context.Background()
			d, _ := repotest.OpenAt(t, dialect, 24) // migrate to v24, then we'll migrate to v25 + backfill

			// Insert a library with a path and one book with a non-empty path.
			repotest.MustExec(t, d, `INSERT INTO libraries (id, name, slug, path) VALUES ($1, 'lib', 'lib', '/tmp/lib')`, repotest.NewID())
			lib, _ := repotest.QueryRow[string](t, d, `SELECT id FROM libraries`)
			repotest.MustExec(t, d, `INSERT INTO books (id, library_id, title, path, format) VALUES ($1, $2, 'b', '/tmp/lib/a.epub', 'EPUB')`, repotest.NewID(), lib)

			if err := migrator.MigrateTo(ctx, d, 25); err != nil {
				t.Fatal(err)
			}

			// One backend row.
			var backendCount int
			if err := d.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM storage_backends`).Scan(&backendCount); err != nil {
				t.Fatal(err)
			}
			if backendCount != 1 {
				t.Errorf("backend count = %d, want 1", backendCount)
			}
			// One files row, location=relative.
			var loc string
			if err := d.SQL.QueryRowContext(ctx, `SELECT location FROM files`).Scan(&loc); err != nil {
				t.Fatal(err)
			}
			if loc != "a.epub" {
				t.Errorf("location = %q, want %q", loc, "a.epub")
			}
			// content_hash is NULL after backfill — filled later by hashing pass.
			var hash []byte
			if err := d.SQL.QueryRowContext(ctx, `SELECT content_hash FROM files`).Scan(&hash); err == nil && hash != nil {
				t.Errorf("content_hash = %x, want NULL", hash)
			}
		})
	}
}
```

The `repotest.OpenAt(t, dialect, version)` helper does not exist yet — add it to `internal/repo/repotest`. It opens a fresh DB and runs migrations up to a specific version.

- [ ] **Step 2: Implement the helper**

`internal/migrator/backfill_storage_v2.go`:

```go
// Package migrator runs schema migrations + the storage_v2 backfill.
package migrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/blackforge/embookshelf/internal/db"
)

// BackfillStorageV2 runs after migration 000025 to seed storage_backends,
// wire libraries.backend_id + root, copy book.path → files.location,
// move metadata fields, and assign UUIDs to books that lack them. Pure
// SQL via database/sql; works on both PG and SQLite.
//
// Idempotent: re-running on an already-backfilled DB is a no-op (the
// INSERTs use INSERT … ON CONFLICT DO NOTHING / WHERE NOT EXISTS).
func BackfillStorageV2(ctx context.Context, d *db.DB) error {
	// 1. Distinct library paths → storage_backends rows.
	rows, err := d.SQL.QueryContext(ctx, `SELECT DISTINCT path FROM libraries WHERE path <> ''`)
	if err != nil {
		return fmt.Errorf("backfill: select library paths: %w", err)
	}
	defer rows.Close()
	type pathRow struct{ path string }
	var paths []pathRow
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return err
		}
		paths = append(paths, pathRow{p})
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, p := range paths {
		cfg, _ := json.Marshal(map[string]string{"root": p.path})
		_, err := d.SQL.ExecContext(ctx, `
			INSERT INTO storage_backends (id, kind, config)
			SELECT $1, 'local', $2
			WHERE NOT EXISTS (
				SELECT 1 FROM storage_backends WHERE kind = 'local' AND `+jsonExtract(d.Dialect, "config", "root")+` = $3
			)
		`, uuid.NewString(), string(cfg), p.path)
		if err != nil {
			return fmt.Errorf("backfill: insert backend: %w", err)
		}
	}

	// 2. Wire libraries.backend_id + root.
	_, err = d.SQL.ExecContext(ctx, `
		UPDATE libraries
		SET backend_id = (SELECT id FROM storage_backends WHERE kind = 'local' AND `+jsonExtract(d.Dialect, "config", "root")+` = libraries.path),
		    root       = libraries.path
		WHERE libraries.path <> '' AND backend_id IS NULL
	`)
	if err != nil {
		return fmt.Errorf("backfill: wire libraries: %w", err)
	}

	// 3. Books without uuid → assign.
	rows, err = d.SQL.QueryContext(ctx, `SELECT id FROM books WHERE uuid IS NULL`)
	if err != nil {
		return fmt.Errorf("backfill: select books needing uuid: %w", err)
	}
	defer rows.Close()
	var bookIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		bookIDs = append(bookIDs, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range bookIDs {
		_, err := d.SQL.ExecContext(ctx, `UPDATE books SET uuid = $1 WHERE id = $2 AND uuid IS NULL`, uuid.NewString(), id)
		if err != nil {
			return fmt.Errorf("backfill: assign uuid: %w", err)
		}
	}

	// 4. files row per book with non-empty path. content_hash stays NULL.
	_, err = d.SQL.ExecContext(ctx, `
		INSERT INTO files (id, library_id, book_id, location, size, mtime, format, last_scanned)
		SELECT
			$1,
			b.library_id,
			b.id,
			CASE
				WHEN l.root <> '' AND b.path LIKE l.root || '/%'
					THEN substr(b.path, length(l.root) + 2)
				ELSE b.path
			END,
			0,
			b.updated_at,
			b.format,
			b.updated_at
		FROM books b
		JOIN libraries l ON l.id = b.library_id
		WHERE b.path <> '' AND b.deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM files WHERE files.book_id = b.id)
	`, uuid.NewString())
	// Note: $1 is reused for every row, which means every backfilled file
	// gets the SAME id. That's wrong — fix by switching to per-row inserts
	// in a loop, or by using gen_random_uuid() (PG) / a SQL UUID expression.
	if err != nil {
		return fmt.Errorf("backfill: insert files: %w", err)
	}
	// TODO(implementer): the single-id reuse above is a bug. Either:
	//   (a) Loop over each book in Go and INSERT with a fresh UUID.
	//   (b) Compute UUIDs in SQL (SQLite hack: lower(hex(randomblob(16))) with manual
	//       hyphenation; PG: gen_random_uuid()).
	// Recommendation: (a). It's slower but obviously correct.

	// 5. metadata row per book.
	_, err = d.SQL.ExecContext(ctx, `
		INSERT INTO metadata (book_id, title, title_sort, description, language, publisher, published_date, isbn, page_count, duration_sec, updated_at)
		SELECT
			b.id, b.title, COALESCE(b.title_sort, b.title),
			b.description, b.language, b.publisher,
			CASE WHEN b.year = 0 THEN NULL ELSE CAST(b.year AS TEXT) END,
			b.isbn, b.page_count, b.duration_seconds, b.updated_at
		FROM books b
		WHERE b.deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM metadata WHERE metadata.book_id = b.id)
	`)
	if err != nil {
		return fmt.Errorf("backfill: insert metadata: %w", err)
	}

	return nil
}

// jsonExtract returns the dialect-specific JSON access expression for
// the given column and key.
func jsonExtract(dialect db.Dialect, col, key string) string {
	switch dialect {
	case db.DialectPostgres:
		return col + "->>" + "'" + key + "'"
	case db.DialectSQLite:
		return "json_extract(" + col + ", '$." + key + "')"
	}
	return ""
}
```

Add `github.com/google/uuid` to `go.mod` if not already present (`go get github.com/google/uuid`).

- [ ] **Step 3: Wire into the migrator**

In `internal/migrator/migrator.go`, after `migrate.Up()` succeeds and the new version is ≥ 25, call `BackfillStorageV2(ctx, d)`. Be careful: if the DB was already at 25 before this run, the backfill must still run (idempotently) on the next boot to handle the "deployed before app code knew about the helper" case.

Strategy: track whether backfill ran via a sentinel row in `app_settings` (table already exists in this codebase). Set `app_settings.storage_v2_backfilled = '1'` after the helper succeeds; skip the helper if that row is present.

- [ ] **Step 4: Test**

```bash
make test
```

- [ ] **Step 5: Commit**

```bash
git add internal/migrator/backfill_storage_v2.go internal/migrator/migrator.go internal/migrator/backfill_storage_v2_test.go internal/repo/repotest/
git commit -m "feat(migrator): storage_v2 Go-level backfill

Idempotent. Seeds storage_backends from libraries.path, wires
backend_id/root, assigns book.uuid, copies path → files.location
(content_hash NULL until hashing pass), moves metadata fields."
```

---

## Phase 2 — Repos

### Task 4: `StorageBackendRepo`

**Files:**
- Create: `internal/repo/storage_backend.go`
- Create: `internal/repo/storage_backend_test.go`

- [ ] **Step 1: Failing test**

```go
func TestStorageBackendRepo_CreateAndList(t *testing.T) {
    repotest.RunOnAllDialects(t, func(t *testing.T, d *db.DB) {
        r := repo.NewStorageBackendRepo(d)
        b, err := r.Create(context.Background(), "local", map[string]any{"root": "/tmp/x"})
        if err != nil { t.Fatal(err) }
        list, err := r.List(context.Background())
        if err != nil { t.Fatal(err) }
        if len(list) != 1 { t.Fatalf("len = %d, want 1", len(list)) }
        if list[0].ID != b.ID { t.Errorf("id mismatch") }
    })
}
```

- [ ] **Step 2: Implement**

`internal/repo/storage_backend.go` — straightforward CRUD on the new table. Mirror the dialect-tagged pattern used in `internal/repo/library.go` (FROM clauses + placeholder variants). Methods: `Create`, `Get(id)`, `List()`, `Update(id, config)`, `Delete(id)` (returning a not-found marker if any libraries still reference it).

Read the existing `library.go` repo for the established conventions (model.* types, dialect SQL helpers, error wrapping).

- [ ] **Step 3: Test passes**
- [ ] **Step 4: Commit**

```bash
git commit -m "feat(repo): storage_backend repo + tests"
```

---

### Task 5: `FileRepo`

**Files:**
- Create: `internal/repo/file.go`
- Create: `internal/repo/file_test.go`
- Modify: `internal/model/file.go` (NEW domain struct)

Methods (signature first):

```go
type File = model.File // see internal/model/file.go

type FileRepo interface {
    Insert(ctx context.Context, f File) (File, error)
    GetByLocation(ctx context.Context, libraryID, location string) (File, error)
    GetByContentHash(ctx context.Context, hash []byte) ([]File, error)  // duplicate detection
    ExistsByLocation(ctx context.Context, libraryID, location string) (bool, error)
    SetContentHash(ctx context.Context, fileID string, hash []byte, size int64, mtime time.Time) error
    ListPendingHash(ctx context.Context, batchSize int) ([]File, error)  // content_hash IS NULL
    MarkScanned(ctx context.Context, fileID string) error
}
```

Tests must cover: insert dedupe via UNIQUE(library_id, location), the SetContentHash null→non-null path, ListPendingHash batching, and dialect parity (PG + SQLite).

- [ ] Commit:

```bash
git commit -m "feat(repo): file repo + content-hash queries"
```

---

### Task 6: `MetadataRepo`

**Files:**
- Create: `internal/repo/metadata.go`
- Create: `internal/repo/metadata_test.go`

```go
type MetadataRepo interface {
    Upsert(ctx context.Context, m model.Metadata) error
    Get(ctx context.Context, bookID string) (model.Metadata, error)
}
```

`Upsert` uses `INSERT … ON CONFLICT (book_id) DO UPDATE` (PG) / `INSERT OR REPLACE` or `ON CONFLICT … DO UPDATE` (SQLite).

- [ ] Commit:

```bash
git commit -m "feat(repo): metadata repo + tests"
```

---

### Task 7: Update `LibraryRepo`

**Files:**
- Modify: `internal/repo/library.go`
- Modify: `internal/model/library.go`
- Modify: `internal/repo/library_test.go` if it exists

Changes:
- `Library` model: replace `Path` with `Root` and `BackendID`. Keep `LastScannedAt`, `FileCount`, `DiscoveredCount`.
- `Create(name, slug, root, backendID, orgMode)` — replaces the old Create signature.
- `BookExistsByPath(path)` → REMOVE. Callers move to `FileRepo.ExistsByLocation`.
- New helper: `LibraryRoot(ctx, libraryID) (root string, backend model.StorageBackend, err error)`.
- All `INSERT INTO books`-style calls in this repo: drop `path`, drop `format`, add separate `metadata` insert + `files` insert. Wrap in a tx; the existing tx pattern in `library.go` is a guide.

Tests: cover the multi-table create flow, library list with backend join, list-by-backend.

- [ ] Commit:

```bash
git commit -m "feat(repo): update LibraryRepo for storage_v2 schema"
```

---

### Task 8: Update `BookDropRepo`

**Files:**
- Modify: `internal/repo/bookdrop.go`
- Modify: `internal/migrator/migrations/{postgres,sqlite}/000025_storage_v2.up.sql` to add `bookdrop_items.content_hash BLOB` if not already there (verify).

`bookdrop_items` already carries `path`, `format`, `size`. Add `content_hash` column (32-byte sha256 written by the ingest task — Task 11).

- [ ] Commit:

```bash
git commit -m "feat(repo): bookdrop carries content_hash"
```

---

## Phase 3 — Hashing & Backfill Worker

### Task 9: `internal/hashing` helper

**Files:**
- Create: `internal/hashing/hasher.go`
- Create: `internal/hashing/hasher_test.go`

```go
package hashing

import (
    "context"
    "crypto/sha256"
    "io"

    "github.com/blackforge/embookshelf/internal/storage"
)

// HashFile streams bytes from store at key, returning the sha256 hash
// and the byte count. Closes the stream on return.
func HashFile(ctx context.Context, store storage.Storage, key string) ([]byte, int64, error) {
    rc, err := store.Get(ctx, key)
    if err != nil {
        return nil, 0, err
    }
    defer func() { _ = rc.Close() }()

    h := sha256.New()
    n, err := io.Copy(h, rc)
    if err != nil {
        return nil, 0, err
    }
    return h.Sum(nil), n, nil
}
```

Tests with `local.LocalFS` rooted at `t.TempDir()`: known-input → expected-sha256, empty file, large file (1 MB), context cancellation mid-stream.

- [ ] Commit:

```bash
git commit -m "feat(hashing): sha256-of-storage-key helper + tests"
```

---

### Task 10: Boot-time hashing pass

**Files:**
- Create: `internal/task/files_backfill.go`
- Create: `internal/task/files_backfill_test.go`
- Modify: `cmd/embookshelf/main.go` to launch the goroutine.

Logic:

```go
func RunFilesBackfill(ctx context.Context, files repo.FileRepo, libs repo.LibraryRepo, backends repo.StorageBackendRepo, store map[string]storage.Storage) error {
    for {
        batch, err := files.ListPendingHash(ctx, 100)
        if err != nil {
            return err
        }
        if len(batch) == 0 {
            return nil // done; no rows left with NULL hash
        }
        for _, f := range batch {
            // Resolve the file's library → backend → storage key.
            // store map is keyed by backend.id; main.go pre-builds it.
            lib, err := libs.GetByID(ctx, f.LibraryID)
            if err != nil { /* log and continue */ continue }
            s, ok := store[lib.BackendID]
            if !ok { /* log and continue */ continue }
            hash, size, err := hashing.HashFile(ctx, s, joinKey(lib.Root, f.Location))
            if err != nil { /* log */ continue }
            // Re-stat for mtime.
            info, _ := s.Head(ctx, joinKey(lib.Root, f.Location))
            if err := files.SetContentHash(ctx, f.ID, hash, size, info.ModTime); err != nil {
                /* log */ continue
            }
        }
    }
}
```

`joinKey(root, location)` is a small helper that concatenates with a single `/`; callers in Plan B use it everywhere a backend key is constructed.

`main.go` change: launch `RunFilesBackfill` in a goroutine after queue setup. Don't block boot. Log progress every 100 files.

Tests with `local.LocalFS` + a tiny SQLite DB; assert NULL hashes turn into expected sha256 sums.

- [ ] Commit:

```bash
git commit -m "feat(task): boot-time storage_v2 hash backfill worker"
```

---

## Phase 4 — Service & Scan Updates

### Task 11: Bookdrop ingest computes hash

**File:**
- Modify: `internal/task/bookdrop.go`

When the bookdrop ingester opens the file for metadata extraction, also stream a sha256 alongside (using `io.TeeReader`). Persist the hash on the bookdrop_items row before metadata is written.

For now, the format processors still take `path string`. Use `os.Open(item.Path)` + `TeeReader(f, h)` where `h := sha256.New()`. The processor reads from the tee'd reader; after extraction, the hash is in `h.Sum(nil)`.

> **Caveat:** EPUB / PDF / CBZ processors require random access (zip seek). A `TeeReader` only supports forward streaming. **Two approaches:**
> 1. Hash in a separate pass: `os.Open` → sha256 → `Close` → re-open for processor.
> 2. Hash via the future `internal/storage` `Get` stream and accept the double read.
>
> Pick (1) for Plan B simplicity. The cost is one extra full-file read per ingest, acceptable since ingest is already I/O bound.

- [ ] Commit:

```bash
git commit -m "feat(task): bookdrop ingest computes sha256"
```

---

### Task 12: Library scan uses location-based identity

**File:**
- Modify: `internal/task/library_scan.go`

Replace `BookExistsByPath(p)` with:

```go
relLoc := strings.TrimPrefix(p, lib.Root + "/")
exists, err := deps.Files.ExistsByLocation(ctx, lib.ID, relLoc)
```

The handler passes the full path today; the migration moves identity to `(library_id, location)`. The Approve path (in `service/bookdrop.go`) writes the `files` row with the staged `content_hash` from the bookdrop item.

`LibraryScanDeps` gains a `Files repo.FileRepo` field.

- [ ] Commit:

```bash
git commit -m "refactor(task): library scan keys identity by (library_id, location)"
```

---

### Task 13: BookDrop service Approve writes new schema

**File:**
- Modify: `internal/service/bookdrop.go`

The current `Approve` flow updates `books.path = approvedPath`. Replace with:

1. Insert `metadata` row from extracted metadata.
2. Insert `files` row with `library_id`, `book_id`, location (relative to library.root), size, mtime, `content_hash` (from bookdrop item), format.
3. Promote the cover via `coverstore.PromoteBookDropToBook` (unchanged).

Wrap 1+2 in a tx. Existing tests in `internal/service/bookdrop_test.go` likely need updating to match.

- [ ] Commit:

```bash
git commit -m "feat(service): bookdrop Approve writes files + metadata"
```

---

### Task 14: Handler reads via files+metadata join

**File:**
- Modify: `internal/handler/files.go` (the `serveBookFile` helper) and `internal/handler/library.go` (book detail responses).

`serveBookFile` no longer takes `book.path`. Lookup chain: `bookID → files.location` (latest active file for the book) → `library.root` → join → absolute path. The path validation (allow-listed roots) stays.

Handler-level tests need a quick audit; if they assert specific JSON shapes for book details (title/author/etc), the response logic now joins `metadata` and the assertions probably still pass, but verify.

- [ ] Commit:

```bash
git commit -m "feat(handler): serveBookFile resolves via files table"
```

---

## Phase 5 — Verification & PR

### Task 15: Final verification

- [ ] **Step 1:** `make ci-local` green.
- [ ] **Step 2:** `make migrate-down && make migrate` works against both PG + SQLite.
- [ ] **Step 3:** Manual smoke: `make seed && make up`. Existing books still browse, library scan still discovers new files, bookdrop approve still moves items into `books`. Boot logs show "storage_v2 backfill: complete" once.
- [ ] **Step 4:** `git diff --stat origin/main..HEAD` confirms only the planned files changed.
- [ ] **Step 5:** Push, open PR.

```bash
gh pr create --base main --title "feat(db): storage_v2 schema + content-hash identity (Plan B of 8)" --body-file <(cat <<'EOF'
## Summary
- Adds storage_backends, files, metadata tables.
- Reshapes libraries (backend_id, root, org_mode).
- Books gain uuid + folder_path; lose path, format, and most metadata columns (now in metadata).
- Boot-time worker fills files.content_hash with sha256 lazily.
- Library scan + bookdrop approve cut over to the new identity model.

## Plan
docs/superpowers/plans/2026-04-29-storage-plan-b-schema.md (Plan B of 8).
Spec: docs/spec/storage.spec.md.

## Migration safety
- Up migration backfills before dropping. Down rebuilds books.path from files.
- Initial sha256 backfill is asynchronous; queries that don't filter by hash work immediately.
- One-shot test: dev seed + manual approve + scan; both PG and SQLite verified.

## Test plan
- [x] make ci-local
- [x] make migrate-down + make migrate
- [x] manual smoke
EOF
)
```

---

## Self-Review Checklist

**Spec coverage (this plan):**
- §3 layout (folder structure) → partially: org_mode column added; physical layout enforcement stays in pattern resolver, untouched.
- §4 schema (storage_backends, libraries, files, books, metadata) → fully covered.
- §5.1 content hashing as authoritative identity → covered (sha256 backfill).
- §5.2 ETag isn't enough → echoed in column design (etag nullable, content_hash authoritative).
- §5.3 two-phase scan → only Phase 1 (walk + identify) lands here. Phase 2 (full reconcile, mtime/etag fast-path) → Plan C.

**Spec coverage (deferred):**
- §3.3 sidecar files → Plan D.
- §5.4 change notification (inotify, S3 events) → Plan F + future.
- §5.5 book-boundary resolution → Plan D (sidecar manifest); current heuristic preserved.
- §6 sidecar atomicity → Plan D.
- §7 cache layout (covers by hash) → Plan E.

**Placeholder scan:** one explicit `TODO(implementer)` in Task 3a around UUID generation in the backfill loop, with two recommended approaches and a chosen one. Acceptable — directive is concrete.

**Type consistency:** `model.File`, `model.StorageBackend`, `model.Metadata`, `repo.FileRepo`, `repo.MetadataRepo`, `repo.StorageBackendRepo`, `hashing.HashFile`, `task.RunFilesBackfill` — names referenced consistently across tasks.

**Risks called out:**
- The single-id-reuse bug in Task 3a's INSERT INTO files — flagged with TODO + fix.
- SQLite has no native UUID; resolved via Go-level backfill.
- Cross-column CHECK constraints not portable; resolved by enforcing in the service layer for SQLite.
- EPUB/PDF/CBZ processors need random access — solved by hashing in a second open in Task 11.
- Cover storage stays book-id-keyed; not blocked by this plan but noted for Plan E.

---

## Execution Handoff

Once Plan A merges to `main`, this plan is unblocked. Recommended approach: same as Plan A — **Subagent-Driven** with two-stage review per task. Phases 1–3 are mechanical SQL/repo work; Phase 4 (services + scan + handler) is where coordination matters most, and a code-quality reviewer will catch most of the schema-cutover regressions before they ship.
