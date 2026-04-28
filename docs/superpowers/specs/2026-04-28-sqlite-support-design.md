# SQLite Support — Design

**Status:** approved (brainstorming complete; planning next)
**Date:** 2026-04-28
**Topic:** Add SQLite as a first-class backend alongside Postgres so embookshelf can run as a true single-binary, zero-dependency service for small/single-user installs.

## 1. Goal & Non-Goals

### Goal

Run embookshelf against SQLite *by default* for small/single-user installs, while keeping Postgres a fully supported backend for larger deployments. Same binary, same UI, same feature set; the operator's only choice is `DATABASE_URL`.

### Non-Goals (v1)

- **No live data migration tool** between PG and SQLite. Pick one at install; switching means a fresh install. (Possible follow-up.)
- **No multi-writer SQLite.** No replication, no LiteFS, no horizontal scaling. SQLite mode is single-process.
- **No SQLite for the queue's Postgres installs.** River stays untouched on PG.
- **No removal of `pgxpool`.** PG still uses it under the hood; the queue layer keeps a handle for River.

## 2. Architecture Overview

```
                  ┌────────────────────────────┐
                  │  config.Load() reads        │
                  │  DATABASE_URL → dialect     │
                  └─────────────┬───────────────┘
                                │
                  ┌─────────────┴───────────────┐
                  │  db.Open(cfg) → *db.DB      │  (new package internal/db)
                  │  - PG:    pgxpool + sql.DB  │
                  │  - SQLite: sql.DB (modernc) │
                  └─────────────┬───────────────┘
                                │
        ┌───────────────────────┼───────────────────────┐
        │                       │                       │
   ┌────▼─────┐         ┌───────▼─────────┐      ┌─────▼─────┐
   │ migrator │         │   repo.*Repo    │      │ queue.Q   │
   │  pg/     │         │  one set of     │      │ interface │
   │  sqlite/ │         │  structs, two   │      ├───────────┤
   └──────────┘         │  query strings  │      │ pg → River│
                        │  per query      │      │ sqlite →  │
                        └─────────────────┘      │  homegrown│
                                                 └───────────┘
```

### Architectural Decisions

1. **New `internal/db` package** owns dialect detection and connection setup. Single source of truth for "what backend are we on."
2. **Repos take `*db.DB`** instead of `*pgxpool.Pool`. They use `db.SQL` (`*sql.DB`) for queries. The `db.PG` field is exposed only so the queue layer can hand `*pgxpool.Pool` to River.
3. **One repo struct per entity, two SQL strings per query.** No new repo-interface layer. Each method picks `qPG` or `qSQLite` based on `r.db.Dialect`.
4. **Two migration trees:** `internal/migrator/migrations/postgres/` and `internal/migrator/migrations/sqlite/`. Migrator picks the tree by dialect. SQLite tree starts as one squashed `0000_init.up.sql` reaching the current end-state schema; from version 24 onward both trees gain parallel files.
5. **Two queue implementations** behind a `queue.Queue` interface. PG path keeps River; SQLite path is a homegrown polling worker against a `jobs` table.
6. **FTS:** PG keeps existing `tsvector`. SQLite uses an FTS5 virtual table kept in sync via triggers. Search quality is preserved on both.
7. **JSONB columns** become `TEXT` with `CHECK (json_valid(...))` in SQLite, accessed via `json_extract`.
8. **UUIDs** generated app-side (`google/uuid`) for both dialects. Stored as `UUID` in PG (unchanged), `TEXT` in SQLite. Removes the `pgcrypto` extension dependency.

## 3. Data Layer

### `internal/db` package (new)

```go
type Dialect string
const (
    DialectPostgres Dialect = "postgres"
    DialectSQLite   Dialect = "sqlite"
)

type DB struct {
    SQL     *sql.DB        // shared by all repos
    Dialect Dialect
    PG      *pgxpool.Pool  // nil when Dialect == DialectSQLite; queue/River uses this
}

func Open(ctx context.Context, cfg config.Config) (*DB, error)
```

### Dialect detection

| URL pattern                          | Dialect    |
|--------------------------------------|------------|
| `postgres://…` or `postgresql://…`   | postgres   |
| `sqlite://…`, `file:…`, bare path    | sqlite     |
| anything else                        | error      |

### Connection setup per dialect

**Postgres:** open `pgxpool` with existing config, then `stdlib.OpenDBFromPool` for `*sql.DB`. `DatabaseMaxConns` / `DatabaseMinConns` honored as today.

**SQLite:** open `modernc.org/sqlite` via `database/sql`. Apply pragmas on every new connection (via a `sql.Register` wrapper or per-connection setup):

- `journal_mode=WAL`
- `synchronous=NORMAL`
- `foreign_keys=ON`
- `busy_timeout=5000`
- `temp_store=MEMORY`

Use **two `*sql.DB` handles internally**: one writer with `MaxOpenConns=1` (avoids `SQLITE_BUSY` storms), one reader with `MaxOpenConns=N` for concurrent reads. Both wrapped under `db.SQL` via a small router that picks the right pool by statement kind, OR — simpler — a single pool with `MaxOpenConns=1` if benchmarks show it's adequate. **Decision:** start with single-pool/MaxOpenConns=1 for simplicity; revisit if read latency suffers under load.

> **Driver constraint:** the SQLite driver MUST be pure-Go (`modernc.org/sqlite`). CGo would kill the single-binary cross-platform story. `mattn/go-sqlite3` is rejected.

### Repo refactor

Each repo today holds `pool *pgxpool.Pool`. After the change:

```go
type LibraryRepo struct { db *db.DB }

func (r *LibraryRepo) ListBooks(ctx context.Context, ...) (...) {
    q := selectQ(r.db.Dialect, qListBooksPG, qListBooksSQLite)
    rows, err := r.db.SQL.QueryContext(ctx, q, args...)
    ...
}
```

`selectQ(d, pg, sqlite)` is a tiny helper. When a query is identical across dialects, only one constant exists and the helper short-circuits.

**Argument placeholders.** PG uses `$1, $2`; SQLite uses `?`. We write **each query in its native form** rather than rewrite at runtime — honest, debuggable, no surprises.

### Phased internal migration

1. Add `internal/db` package; change repo constructors to take `*db.DB`. All existing PG queries continue working under `r.db.SQL`. Tests still pass.
2. Replace pgx-specific types (`pgconn.PgError`, `pgx.ErrNoRows`, batches) with `database/sql` equivalents (`sql.ErrNoRows`, error inspection helpers, sequential statements or transactions). Audit pgx-specific usage during plan-writing.
3. Add the second SQL string per query for SQLite.

### Errors

`ErrNotFound`, `ErrLibraryNameTaken`, `ErrLibraryPathTaken` keep their identity. Their detection moves to a `dberr` helper:

```go
func IsUniqueViolation(err error, d Dialect) bool
```

Recognizes both PG `23505` and SQLite `SQLITE_CONSTRAINT_UNIQUE` / `UNIQUE constraint failed`.

### Transactions

Existing `pool.Begin` calls move to `db.SQL.BeginTx` — semantics identical across both. SQLite serializes naturally; no behavior change.

## 4. Migrations

### Layout

```
internal/migrator/
  migrator.go
  migrations/
    postgres/
      000001_init.up.sql        ← existing 23 files moved here, unchanged
      000001_init.down.sql
      ...
      000023_user_approval_status.up.sql
      000023_user_approval_status.down.sql
    sqlite/
      0000_init.up.sql          ← squashed end-state schema, single file
      0000_init.down.sql        ← DROP TABLE IF EXISTS … for everything
```

Squashing the SQLite init is a one-time concession because no SQLite database in production today needs an incremental upgrade path. From version 24 onward, every new feature ships **both** `postgres/000024_*.{up,down}.sql` and `sqlite/000024_*.{up,down}.sql`. CI fails if either is missing (parity test, §7).

### `migrator.New` signature change

Today:

```go
func New(files embed.FS, subpath string, pool *pgxpool.Pool) (*migrate.Migrate, error)
```

After:

```go
func New(d Dialect, sqlDB *sql.DB) (*migrate.Migrate, error)
```

Subpath is derived from dialect. Driver instance is `postgres.WithInstance` (PG) or `sqlite3.WithInstance` (SQLite) — golang-migrate ships both.

### Schema differences in the squashed SQLite init

| Postgres feature                                  | SQLite equivalent                                                           |
|---------------------------------------------------|-----------------------------------------------------------------------------|
| `UUID PRIMARY KEY DEFAULT gen_random_uuid()`      | `TEXT PRIMARY KEY NOT NULL` (app-generated UUID)                            |
| `TIMESTAMPTZ` / `now()`                           | `TEXT` storing RFC3339 / `datetime('now')`                                  |
| `JSONB NOT NULL DEFAULT '{}'::jsonb`              | `TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(value))`                      |
| `BOOLEAN`                                         | `INTEGER` with `CHECK (col IN (0,1))`                                       |
| `INTEGER GENERATED ALWAYS AS IDENTITY`            | `INTEGER PRIMARY KEY AUTOINCREMENT`                                         |
| Partial indexes `WHERE deleted_at IS NULL`        | unchanged — SQLite supports them                                            |
| Foreign keys with `ON DELETE CASCADE`             | unchanged — relies on `PRAGMA foreign_keys=ON` (set by connector)           |
| `CREATE EXTENSION pgcrypto`                       | removed (UUIDs are app-generated)                                           |

### FTS

PG tree: existing `tsvector` column, GIN index, and trigger remain unchanged.

SQLite tree: add an FTS5 virtual table:

```sql
CREATE VIRTUAL TABLE books_fts USING fts5(
    title, subtitle, author, description, series, isbn, isbn10,
    content='books', content_rowid='rowid'
);
-- Triggers AFTER INSERT, AFTER UPDATE, AFTER DELETE on books to keep books_fts aligned.
```

Repo query branches in `internal/repo/library.go` (today: lines 263, 573, 601):

- PG path: `b.tsv @@ websearch_to_tsquery('english', $N)`, ORDER BY `ts_rank(b.tsv, …)` DESC.
- SQLite path: `books_fts MATCH ?`, ORDER BY `bm25(books_fts)` ASC (lower is better in bm25).

### Down migrations on SQLite

Single `0000_init.down.sql` drops everything. From version 24, each parallel `_down.sql` follows its dialect's grammar.

### Migration runner on boot

`runAppMigrations` in `cmd/embookshelf/main.go` becomes dialect-agnostic — it calls `migrator.New(db.Dialect, db.SQL)`.

### River's own migrations

River runs them itself inside `queue.New` against `db.PG`. On SQLite that path is skipped entirely; the homegrown queue manages its own `jobs` table via the app's migrator (it lives in the SQLite squashed init).

## 5. Queue Split

### Interface (`internal/queue`)

```go
type Queue interface {
    EnqueueBookDropIngest(ctx context.Context, args BookDropIngestArgs) error
    EnqueueLibraryScan(ctx context.Context, args LibraryScanArgs) error
    Stop(ctx context.Context) error
}

func New(
    ctx context.Context,
    db *db.DB,
    bdrop *service.BookDropService,
    lib   *service.LibraryService,
) (Queue, error)
```

`New` dispatches on `db.Dialect`. Callers (`main.go`, handlers) see only the interface.

### Postgres path: unchanged

Existing `internal/queue/queue.go` becomes `queue_pg.go`. River + `riverpgxv5` continue as today, including River's own migrations on boot.

### SQLite path: homegrown polling worker

#### Schema (added to the SQLite migration tree)

```sql
CREATE TABLE jobs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    kind          TEXT NOT NULL,                   -- 'bookdrop_ingest', 'library_scan'
    args          TEXT NOT NULL,                   -- JSON payload
    state         TEXT NOT NULL DEFAULT 'pending', -- pending|running|completed|failed
    attempts      INTEGER NOT NULL DEFAULT 0,
    max_attempts  INTEGER NOT NULL DEFAULT 5,
    last_error    TEXT,
    scheduled_at  TEXT NOT NULL DEFAULT (datetime('now')),
    started_at    TEXT,
    finished_at   TEXT,
    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_jobs_pending ON jobs(state, scheduled_at) WHERE state = 'pending';
```

#### Worker loop

One goroutine, shared across all kinds (single-user SQLite installs don't benefit from parallelism, and parallelism would magnify write contention).

```
loop:
  - SELECT id, kind, args FROM jobs
    WHERE state='pending' AND scheduled_at <= datetime('now')
    ORDER BY id LIMIT 1;
  - If none: sleep poll_interval (default 1s), continue.
  - UPDATE jobs SET state='running', started_at=datetime('now'),
      attempts=attempts+1
    WHERE id=? AND state='pending';   -- atomic claim; rows-affected==0 ⇒ lost the race
  - dispatch by kind → call into the shared task function
  - on success: state='completed', finished_at=now
  - on error: if attempts < max_attempts → state='pending',
      scheduled_at=now + backoff(attempts);  else state='failed', last_error=err.
```

Backoff: exponential with jitter, capped at 5 minutes. Single-process, no leader election. No periodic-job support in v1.

### Shared task implementations

Today's `internal/task/` workers are River-typed. Refactor each into a plain function:

```go
func BookDropIngest(ctx context.Context, args BookDropIngestArgs, deps Deps) error
func LibraryScan(ctx context.Context, args LibraryScanArgs, deps Deps) error
```

Two thin adapters wrap them: a River worker on PG, a kind-dispatch entry on SQLite. **No business logic duplication.**

### Startup recovery

River requeues mid-flight jobs automatically on restart. The SQLite worker does the same on boot:

```sql
UPDATE jobs SET state='pending', started_at=NULL WHERE state='running';
```

Existing `ingest.DiscoverOnStartup` keeps working through the new interface.

### What the SQLite queue gives up vs River

- No web dashboard (River has one; we don't expose it today anyway).
- No cross-process leadership / horizontal scaling. Acceptable — SQLite mode is single-process.
- No unique-job constraints. Bookdrop ingest already keys by file path/hash in `bookdrop_files`, so duplicate prevention exists at the caller layer.

## 6. Configuration

### `config.Config` changes

```go
DatabaseURL      string  // existing — drives everything
DatabaseMaxConns int32   // PG only; ignored on SQLite (always 1 writer)
```

Default flips:

```go
DatabaseURL: envStr("DATABASE_URL", "sqlite://./data/embookshelf.db"),
```

### SQLite path resolution

`sqlite://./data/embookshelf.db` resolves against `cfg.DataPath` so `DATA_PATH=/var/lib/embookshelf` Just Works. Documented in the env table.

### Compose

- `compose.dev.yml`: keeps Postgres (developers want PG locally for parity with prod-PG users).
- `compose.sqlite.yml`: new file for testing/demoing the SQLite path.
- Production `Dockerfile`: unchanged — the binary doesn't care.

### Removed/changed env vars

None removed. `DATABASE_MAX_CONNS` / `DATABASE_MIN_CONNS` become no-ops on SQLite with a one-line warning if explicitly set.

## 7. Testing Strategy

- **Unit tests for repos run against both dialects.** A `testdb` helper provisions a temp DB per test (file-based SQLite for correctness, PG via the existing dev container). Repo tests are wrapped in `t.Run("postgres" / "sqlite", …)`.
- **Migration parity test.** `TestParity` enumerates `migrations/postgres/*.up.sql` and `migrations/sqlite/*.up.sql` from version 24 onward; asserts every PG version has a SQLite sibling.
- **Schema-equivalence test.** After end-to-end migration of each tree, compares table names + column names (not types — those legitimately diverge). Catches "added a table to PG, forgot SQLite."
- **CI matrix.** GitHub Actions adds a `test-sqlite` lane alongside the existing `test-postgres` lane. Both run `make test`. E2E (Playwright) gains a SQLite run alongside the existing PG run.
- **Queue tests.** River tests stay as today. SQLite worker tests cover claim atomicity (two simulated workers, only one wins), backoff + retry, restart recovery (`running → pending` on boot).

## 8. Rollout & Docs

- **`docs/architecture.md`** gets a new "Database Backends" section covering the dialect split.
- **`docs/prd.md` / `README`** quickstart leads with the SQLite path ("just run the binary"), Postgres second ("for multi-user installs").
- **CHANGELOG / release-please breaking-change footer** flags the default-DB flip explicitly. Anyone with `DATABASE_URL` already set is unaffected; anyone relying on the bare default is told to set it explicitly.
- **Migration tool from PG → SQLite (and vice versa):** out of scope for this spec. Tracked as a follow-up.

## 9. Open Questions / Follow-ups

- **Read pool split on SQLite** — start with single-pool `MaxOpenConns=1`; revisit if read latency degrades.
- **PG → SQLite (or reverse) data migration tool** — deferred; needs its own spec.
- **Periodic jobs on SQLite** — not in v1; River has the feature unused today on PG too.
- **Audit of pgx-specific call sites** (batches, custom types, `pgconn.PgError` inspection) happens during plan-writing so the refactor scope in §3 is fully bounded.
