# SQLite Backend — Implementation Plan (Plan 2A of 4)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make embookshelf bootable end-to-end against `DATABASE_URL=sqlite:///path/to/file.db`. After this plan, the same binary runs identical features on either backend; Postgres remains the *default* (Plan 2B flips that).

**Architecture:** Add `modernc.org/sqlite` (pure-Go, CGo-free) under `internal/db.Open` with WAL pragmas. Build a one-file squashed SQLite migration tree that produces the current end-state schema, with FTS5 standing in for Postgres `tsvector` and `TEXT` (JSON-encoded) standing in for `TEXT[]` and `JSONB`. Repos gain a second SQL string per query, dispatched via a `db.SelectQ(dialect, pg, sqlite) string` helper; INSERTs that previously relied on `gen_random_uuid()` now generate UUIDs app-side via `db.NewID()`. Three small adapter helpers (`db.ScanStringSlice`, `db.ValueStringSlice`, plus dialect-aware FTS-query escaping in `internal/search/fts5.go`) absorb the differences in array and full-text representation.

**Tech Stack:** Go 1.25, `database/sql`, `modernc.org/sqlite` (pure-Go SQLite driver), `github.com/google/uuid` (existing indirect dep, elevated to direct), `github.com/golang-migrate/migrate/v4/database/sqlite3` (already shipped with the migrate dep), SQLite 3.40+ (modernc bundles it).

**Companion spec:** [`docs/superpowers/specs/2026-04-28-sqlite-support-design.md`](../specs/2026-04-28-sqlite-support-design.md). Sections 3 (data layer), 4 (migrations), 6b (FTS5).

**Out of scope for this plan (Plan 2B picks up):**
- `DATABASE_URL` default flip from Postgres to SQLite.
- The `repotest` test-matrix harness (running every repo test against both backends).
- Migration parity test (version 24+ both folders match).
- Schema-equivalence test.
- `compose.sqlite.yml`.

**Out of scope for Plans 2A and 2B (Plan 3 picks up):**
- Queue split. River stays the only queue. SQLite mode in this plan **fails to start the queue with the existing "queue: only Postgres backend supported in Plan 1" error message** — this is intentional. Plan 2A's smoke test exercises the API/reading paths, not bookdrop ingest or library scans. The error message in `queue.New` should be widened to mention Plan 3 specifically (small doc tweak, still in this plan — see Task 21).

---

## Pre-read: design choices locked in by Plan 2A

1. **UUIDs.** Today every `id UUID PRIMARY KEY DEFAULT gen_random_uuid()` is server-generated. Plan 2A inverts that: every INSERT passes the `id` as a parameter (`db.NewID()`), and Postgres stops using its `gen_random_uuid()` default for those inserts. The PG column **default stays in place** (so external INSERTs still work), but the app no longer relies on it. SQLite stores the UUID as `TEXT`. Round-trip is identical (a 36-char hyphenated string).

2. **`TEXT[]` columns** (`books.tags`, `books.genres`, `books.moods`). Postgres keeps `TEXT[]`; SQLite stores `TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(...))` containing a JSON array (e.g. `["sci-fi","drama"]`). Two repo-side adapter funcs (`db.ScanStringSlice(dialect, src, *[]string) error` and `db.ValueStringSlice(dialect, []string) any`) bridge the difference at scan/bind time. The PG path delegates to the existing pgx codec; the SQLite path uses `json.Marshal` / `json.Unmarshal`.

3. **`JSONB` columns** (`smart_shelves.rule`, `devices.config`, `app_settings.value`, `provider_settings.config`). Both dialects use `[]byte` in Go (containing JSON text). PG stores `JSONB` (parses + indexes); SQLite stores `TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(...))`. No adapter required: `&[]byte{}` for scan, `[]byte("{...}")` for bind, both work via `database/sql`. Repo code is the same on both sides.

4. **`TIMESTAMPTZ` columns.** PG keeps `TIMESTAMPTZ` (32 occurrences). SQLite uses `TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))` storing RFC3339 strings. Go's `time.Time` scans/binds correctly against both via `database/sql` — the implementer doesn't see this difference except when writing the SQLite squashed init.

5. **`BOOLEAN`** in PG → `INTEGER NOT NULL CHECK (col IN (0,1))` in SQLite. `&bool` scanner works against both because the SQLite driver returns 0/1 and `database/sql` handles the conversion.

6. **`tsvector` column.** PG keeps the existing `books.tsv tsvector GENERATED ALWAYS AS (…) STORED` column + GIN index. SQLite has **no `tsv` column** on `books`. Instead, an FTS5 virtual table `books_fts` indexes title/author/series/description with a `content='books'` external-content option, kept in sync via three triggers. The repo's `bookCols` constant never SELECTs `tsv`, so the scanner is dialect-agnostic; only the WHERE/ORDER BY clauses differ.

7. **`ON CONFLICT (col) DO UPDATE/DO NOTHING`** — works identically in both dialects (SQLite supports it since 3.24). No translation needed for the 8 sites that use this pattern.

8. **`RETURNING`** — works in both PG and SQLite (≥ 3.35). No translation needed for the 18 sites that use this clause.

9. **`ILIKE` / case-insensitive LIKE.** SQLite has no `ILIKE`. Sites that use `ILIKE` (3 in `shelf.go`) translate to `LIKE` with `COLLATE NOCASE` on the SQLite side. The PG side keeps `ILIKE`.

10. **Placeholders.** PG uses `$1, $2`; SQLite uses `?`. Each query is written in its native form. No runtime rewriter.

---

## File Structure

### Files created

| Path | Responsibility |
|---|---|
| `internal/db/dialect.go` | `SelectQ(d, pg, sqlite) string`, `NewID() string` (UUID), `ScanStringSlice`, `ValueStringSlice` adapters. |
| `internal/db/dialect_test.go` | Unit tests for `SelectQ`, `NewID`, the slice adapters (table-driven for PG and SQLite). |
| `internal/migrator/migrations/sqlite/0000_init.up.sql` | Single squashed schema reaching the current end-state. ~250 lines. |
| `internal/migrator/migrations/sqlite/0000_init.down.sql` | DROP TABLE / DROP TRIGGER / DROP INDEX for everything created in `0000_init.up.sql`. |
| `internal/search/fts5.go` | `EscapeFTS5Query(input string) string` — escapes user search terms for safe FTS5 MATCH dispatch (handles quotes, reserved punctuation; wraps tokens in `"…"*` for prefix-tolerant matching). |
| `internal/search/fts5_test.go` | Unit tests for the escaping behavior. |

### Files modified

| Path | Change |
|---|---|
| `go.mod` / `go.sum` | Add `modernc.org/sqlite` direct dep. Promote `github.com/google/uuid` from indirect to direct. |
| `internal/db/db.go` | Implement `openSQLite` (replace the current `errors.New("sqlite backend not yet supported (Plan 2)")`). Add the `OpenMigrationDB` SQLite branch. Apply pragmas `journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON`, `busy_timeout=5000`, `temp_store=MEMORY`. Single pool, `MaxOpenConns=1` for now (per spec §3 decision). |
| `internal/db/dberr/dberr.go` | Add SQLite branch to `IsUniqueViolation`. modernc surfaces unique-violation as `*sqlite.Error` with `Code() == 2067` (SQLITE_CONSTRAINT_UNIQUE) and an extended-error message like `"constraint failed: UNIQUE constraint failed: libraries.slug"`. The constraint *name* in SQLite is the column or index name, not a Postgres-style constraint identifier — see Task 3 for the parsing rule. |
| `internal/db/dberr/dberr_test.go` | Add SQLite-branch tests (synthetic `*sqlite.Error`). |
| `internal/migrator/migrator.go` | Replace the `db.DialectSQLite` placeholder error in `driverFor` with `sqlite3.WithInstance(sqlDB, &sqlite3.Config{})`. |
| `internal/queue/queue.go` | Update the existing error string from `"queue: only Postgres backend supported in Plan 1"` → `"queue: SQLite backend lands in Plan 3"` to reflect the active phase. (Behavior unchanged.) |
| `internal/repo/library.go` | Largest repo change. Every query gets a SQLite variant via `db.SelectQ`. INSERTs swap `gen_random_uuid()` defaults for `db.NewID()` parameters. The 3 FTS sites (lines around 263, 573, 601 — search WHERE / ORDER BY in book listing and quick search) get a SQLite branch using `books_fts MATCH ?` + `ORDER BY bm25(books_fts) ASC`. Array columns (`tags`, `genres`, `moods`) use `db.ScanStringSlice` / `db.ValueStringSlice`. ~7 INSERT/UPSERT queries plus ~10 SELECT queries to dialect-tag. |
| `internal/repo/shelf.go` | INSERTs with `gen_random_uuid()` → `db.NewID()` parameter. `ILIKE` clauses → SQLite `LIKE … COLLATE NOCASE`. The `rule` JSONB column (smart_shelves) is already `[]byte` in Go — no change. |
| `internal/repo/user.go` | INSERTs (users, password_resets) → `db.NewID()`. No arrays/JSONB. |
| `internal/repo/session.go` | INSERTs → `db.NewID()`. |
| `internal/repo/bookdrop.go` | INSERT (bookdrop_items) → `db.NewID()`. `ON CONFLICT (path) DO NOTHING RETURNING …` works on both. No arrays/JSONB. |
| `internal/repo/progress.go` | One UPSERT (user_book_progress); IDs are composite keys (user_id, book_id), no UUID gen needed. |
| `internal/repo/annotation.go` | INSERTs → `db.NewID()`. |
| `internal/repo/stats.go` | Read-only repo. Mostly aggregate queries — verify each returns the same shape on both dialects (e.g. `COUNT(*)`, `SUM(...)`, `AVG(...)` are identical). No INSERTs. |
| `internal/repo/reading_session.go` | INSERTs → `db.NewID()`. |
| `internal/repo/device.go` | INSERTs (user_devices) → `db.NewID()`. `config` JSONB (already `[]byte`) — no change. The unique constraint name for "name taken" differs per dialect (PG `idx_user_devices_user_name`; SQLite parses out the column tuple `user_id, name`). The dberr helper handles this — see Task 3. |
| `internal/repo/app_settings.go` | UPSERTs on `app_settings` (composite-key, no UUID). `value` is already `[]byte`. |
| `internal/repo/provider_settings.go` | INSERTs on `provider_settings` (id is the provider's well-known string, not a UUID — no `db.NewID()` needed). `config` JSONB is already `[]byte`. |
| `cmd/embookshelf/main.go` | No structural change. The existing `db.Open(ctx, cfg)` already routes to the SQLite branch when `DATABASE_URL=sqlite://…`; that branch starts working in Task 2. |

The plan does **not** modify `internal/migrator/migrations/postgres/`. Postgres migrations stay frozen at version 23 — that's Plan 1's terminal state. Future schema changes (version 24+) ship parallel files in both trees per the spec; nothing in Plan 2A introduces new schema.

---

## Phase 0 — SQLite Foundation Helpers

### Task 1: Add `modernc.org/sqlite` + `google/uuid`; create `internal/db/dialect.go`

**Files:**
- Create: `internal/db/dialect.go`
- Create: `internal/db/dialect_test.go`
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Pull in the new deps**

```bash
go get modernc.org/sqlite@latest
go get github.com/google/uuid@latest
go mod tidy
```

Confirm `go.mod` now lists both as direct requirements (not indirect):

```bash
grep -E "modernc.org/sqlite|google/uuid" go.mod
```

Expected output (versions may vary):
```
github.com/google/uuid v1.6.0
modernc.org/sqlite v1.x.y
```

- [ ] **Step 2: Write the failing tests**

Create `internal/db/dialect_test.go`:

```go
package db

import (
	"encoding/json"
	"testing"
)

func TestSelectQ(t *testing.T) {
	const pg = "SELECT $1"
	const sq = "SELECT ?"
	if got := SelectQ(DialectPostgres, pg, sq); got != pg {
		t.Fatalf("PG: got %q want %q", got, pg)
	}
	if got := SelectQ(DialectSQLite, pg, sq); got != sq {
		t.Fatalf("SQLite: got %q want %q", got, sq)
	}
}

func TestNewID(t *testing.T) {
	id := NewID()
	if len(id) != 36 {
		t.Fatalf("got %q (len=%d), want 36-char UUID", id, len(id))
	}
	if id == NewID() {
		t.Fatal("two NewID() calls returned the same value")
	}
}

func TestValueStringSlice(t *testing.T) {
	in := []string{"sci-fi", "drama"}

	// PG: returns the slice unchanged; pgx codec handles encoding.
	pgVal, err := ValueStringSlice(DialectPostgres, in)
	if err != nil {
		t.Fatalf("PG ValueStringSlice: %v", err)
	}
	pgSlice, ok := pgVal.([]string)
	if !ok {
		t.Fatalf("PG: got %T, want []string", pgVal)
	}
	if len(pgSlice) != 2 || pgSlice[0] != "sci-fi" {
		t.Fatalf("PG: got %v, want [sci-fi drama]", pgSlice)
	}

	// SQLite: returns a JSON-encoded string.
	sqliteVal, err := ValueStringSlice(DialectSQLite, in)
	if err != nil {
		t.Fatalf("SQLite ValueStringSlice: %v", err)
	}
	sqliteStr, ok := sqliteVal.(string)
	if !ok {
		t.Fatalf("SQLite: got %T, want string", sqliteVal)
	}
	var roundTrip []string
	if err := json.Unmarshal([]byte(sqliteStr), &roundTrip); err != nil {
		t.Fatalf("SQLite roundtrip unmarshal: %v", err)
	}
	if len(roundTrip) != 2 || roundTrip[0] != "sci-fi" {
		t.Fatalf("SQLite roundtrip: got %v, want [sci-fi drama]", roundTrip)
	}

	// Empty slice: SQLite produces "[]", not "null".
	emptyVal, err := ValueStringSlice(DialectSQLite, nil)
	if err != nil {
		t.Fatalf("SQLite ValueStringSlice(nil): %v", err)
	}
	if emptyVal.(string) != "[]" {
		t.Fatalf("SQLite empty: got %q want []", emptyVal)
	}
}

func TestScanStringSlice(t *testing.T) {
	// PG: src will be []string already (pgx codec). Just copy.
	var pgDst []string
	if err := ScanStringSlice(DialectPostgres, []string{"a", "b"}, &pgDst); err != nil {
		t.Fatalf("PG ScanStringSlice: %v", err)
	}
	if len(pgDst) != 2 || pgDst[0] != "a" {
		t.Fatalf("PG: got %v, want [a b]", pgDst)
	}

	// SQLite: src is a string holding JSON.
	var sqliteDst []string
	if err := ScanStringSlice(DialectSQLite, `["x","y","z"]`, &sqliteDst); err != nil {
		t.Fatalf("SQLite ScanStringSlice: %v", err)
	}
	if len(sqliteDst) != 3 || sqliteDst[2] != "z" {
		t.Fatalf("SQLite: got %v, want [x y z]", sqliteDst)
	}

	// SQLite empty: "[]" decodes to empty slice.
	var emptyDst []string
	if err := ScanStringSlice(DialectSQLite, "[]", &emptyDst); err != nil {
		t.Fatalf("SQLite empty ScanStringSlice: %v", err)
	}
	if len(emptyDst) != 0 {
		t.Fatalf("SQLite empty: got %v, want []", emptyDst)
	}

	// SQLite nil src: empty slice, no error.
	var nilDst []string
	if err := ScanStringSlice(DialectSQLite, nil, &nilDst); err != nil {
		t.Fatalf("SQLite nil src: %v", err)
	}
	if len(nilDst) != 0 {
		t.Fatalf("SQLite nil src: got %v, want []", nilDst)
	}
}
```

Run: `go test ./internal/db/ -run "TestSelectQ|TestNewID|TestValueStringSlice|TestScanStringSlice" -v`
Expected: FAIL — symbols undefined.

- [ ] **Step 3: Implement `internal/db/dialect.go`**

Create the file:

```go
package db

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// SelectQ returns the dialect-appropriate SQL string. Used pervasively in
// repos when a query needs a different shape on Postgres vs SQLite.
//
// If a query is identical between dialects (e.g. simple lookups by id),
// callers should use just one constant and pass it as both args; the cost
// is negligible.
func SelectQ(d Dialect, pg, sqlite string) string {
	if d == DialectSQLite {
		return sqlite
	}
	return pg
}

// NewID returns a fresh canonical UUID string. Both Postgres (UUID column)
// and SQLite (TEXT column) accept the 36-char hyphenated form. Repos call
// this for every INSERT instead of relying on Postgres' gen_random_uuid()
// default, so the same INSERT shape works on both backends.
func NewID() string {
	return uuid.NewString()
}

// ValueStringSlice prepares a []string for binding into a query as a
// dialect-appropriate value.
//
//   - Postgres: returns the slice unchanged. The pgx stdlib codec
//     (registered when we open the *sql.DB via stdlib.OpenDBFromPool)
//     encodes []string as a TEXT[] literal automatically.
//   - SQLite: returns a JSON-encoded string. The repo's INSERT/UPDATE
//     should bind into a TEXT column with a CHECK (json_valid(col)).
func ValueStringSlice(d Dialect, s []string) (any, error) {
	if d == DialectPostgres {
		return s, nil
	}
	if s == nil {
		return "[]", nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("encode string slice: %w", err)
	}
	return string(b), nil
}

// ScanStringSlice decodes a value retrieved from the database into *[]string.
//
//   - Postgres: the source is already a []string courtesy of the pgx codec.
//     We copy it into dst.
//   - SQLite: the source is a string (or []byte) containing JSON. We
//     json.Unmarshal it.
//
// Nil source produces an empty slice; this matches both Postgres' empty
// array literal '{}' and SQLite's default '[]'.
func ScanStringSlice(d Dialect, src any, dst *[]string) error {
	if dst == nil {
		return fmt.Errorf("scan string slice: nil dst")
	}
	if src == nil {
		*dst = nil
		return nil
	}
	if d == DialectPostgres {
		s, ok := src.([]string)
		if !ok {
			return fmt.Errorf("scan string slice (PG): unexpected type %T", src)
		}
		*dst = append((*dst)[:0], s...)
		return nil
	}
	// SQLite
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("scan string slice (SQLite): unexpected type %T", src)
	}
	return json.Unmarshal(b, dst)
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/db/ -run "TestSelectQ|TestNewID|TestValueStringSlice|TestScanStringSlice" -v
```

Expected: all four PASS.

- [ ] **Step 5: Build everything**

```bash
go build ./...
```

Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/db/dialect.go internal/db/dialect_test.go go.mod go.sum
git commit -m "$(cat <<'EOF'
feat(db): add dialect helpers (SelectQ, NewID, slice adapters)

Adds modernc.org/sqlite as a direct dep and elevates google/uuid from
indirect to direct. The new internal/db/dialect.go centralizes:

- SelectQ(d, pg, sqlite): per-query dialect dispatch
- NewID(): app-side UUID generation, replacing PG gen_random_uuid()
- ValueStringSlice / ScanStringSlice: TEXT[] (PG) <-> JSON TEXT (SQLite)
  bridges so repos use one Go type ([]string) on both backends.

Plan 2A foundation; consumers land in subsequent tasks.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Implement `openSQLite` in `internal/db/db.go`

**Files:**
- Modify: `internal/db/db.go`
- Modify: `internal/db/db_test.go`

- [ ] **Step 1: Replace the SQLite branch placeholder**

Open `internal/db/db.go`. Find:

```go
case DialectSQLite:
    return nil, errors.New("sqlite backend not yet supported (Plan 2)")
```

Replace with `return openSQLite(ctx, cfg)`.

Add the `openSQLite` function below `openPostgres`:

```go
func openSQLite(ctx context.Context, cfg config.Config) (*DB, error) {
	dsn, err := sqliteDSN(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}

	// Single writer to avoid SQLITE_BUSY storms. Plan 2A's spec §3 records
	// the decision to revisit if read latency suffers; the simplest model
	// for a small/single-user install is one connection serializing both
	// reads and writes.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("sqlite ping: %w", err)
	}

	// Apply pragmas on the live connection. WAL gives concurrent reads
	// while a writer is active; foreign_keys=ON enforces the FK
	// constraints we declare in the squashed init.
	pragmas := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
		`PRAGMA foreign_keys=ON`,
		`PRAGMA busy_timeout=5000`,
		`PRAGMA temp_store=MEMORY`,
	}
	for _, p := range pragmas {
		if _, err := sqlDB.ExecContext(ctx, p); err != nil {
			_ = sqlDB.Close()
			return nil, fmt.Errorf("sqlite pragma %q: %w", p, err)
		}
	}

	return &DB{
		SQL:     sqlDB,
		Dialect: DialectSQLite,
		PG:      nil,
	}, nil
}

// sqliteDSN converts the user-facing DATABASE_URL into a path the
// modernc.org/sqlite driver accepts. Accepted inputs:
//
//   sqlite:///absolute/path/to/file.db
//   sqlite://./relative/path.db
//   sqlite://relative.db
//   file:./relative.db
//   ./relative.db   (or any bare path)
//
// All forms are equivalent; the driver opens the file (creating it if
// absent) using whatever the OS does with that path.
func sqliteDSN(url string) (string, error) {
	switch {
	case strings.HasPrefix(strings.ToLower(url), "sqlite://"):
		return strings.TrimPrefix(url[len("sqlite://"):], "/"), nil
	case strings.HasPrefix(strings.ToLower(url), "file:"):
		return url[len("file:"):], nil
	default:
		return url, nil
	}
}
```

The trim of a single leading `/` after `sqlite://` is intentional: `sqlite:///foo/bar.db` is the canonical "absolute path" form (three slashes — the third makes the path absolute), so the driver should see `/foo/bar.db`. We drop one leading slash from the path-portion to keep that semantics.

- [ ] **Step 2: Update the `OpenMigrationDB` SQLite branch**

Find `OpenMigrationDB` in the same file. Today it returns an error for SQLite. Replace with:

```go
case DialectSQLite:
    // SQLite migrations run on the same single-writer connection.
    // golang-migrate's sqlite3 driver calls Close() on the *sql.DB at
    // m.Close() time; we hand it a fresh handle bound to the SAME file
    // so it doesn't tear down our shared db.SQL.
    return openSQLiteMigrationDB(d.dsn)
```

To make this work, store the resolved DSN on the `*DB` value. Add an unexported `dsn string` field:

```go
type DB struct {
    SQL     *sql.DB
    Dialect Dialect
    PG      *pgxpool.Pool
    dsn     string // SQLite only; the resolved file path passed to sql.Open("sqlite", …)
}
```

Set it inside `openSQLite`:

```go
return &DB{
    SQL:     sqlDB,
    Dialect: DialectSQLite,
    PG:      nil,
    dsn:     dsn,
}, nil
```

Add `openSQLiteMigrationDB`:

```go
func openSQLiteMigrationDB(dsn string) (*sql.DB, error) {
	if dsn == "" {
		return nil, errors.New("sqlite migration db: empty dsn (db.Open did not store one)")
	}
	mig, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite migration open: %w", err)
	}
	mig.SetMaxOpenConns(1)
	// Match the production pragmas so migrations and the running app see
	// the same on-disk format. foreign_keys is the load-bearing one:
	// migrate's CREATE TABLE statements rely on it being on for
	// constraint validation.
	pragmas := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA foreign_keys=ON`,
		`PRAGMA busy_timeout=5000`,
	}
	for _, p := range pragmas {
		if _, err := mig.Exec(p); err != nil {
			_ = mig.Close()
			return nil, fmt.Errorf("sqlite migration pragma %q: %w", p, err)
		}
	}
	return mig, nil
}
```

- [ ] **Step 3: Adjust the `_ "modernc.org/sqlite"` blank import**

The `modernc.org/sqlite` driver registers itself under the name `"sqlite"` only if its package init runs. Add a blank import to `internal/db/db.go`:

```go
import (
    // ... existing imports
    _ "modernc.org/sqlite"
)
```

Place it in its own import group at the bottom, after the named imports (Go convention for side-effect-only imports).

- [ ] **Step 4: Add a real-DB SQLite test**

Open `internal/db/db_test.go`. Add:

```go
func TestOpenSQLite_live(t *testing.T) {
	dir := t.TempDir()
	dsn := "sqlite:" + dir + "/test.db"
	cfg := config.Config{DatabaseURL: dsn}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = d.Close() }()

	if d.Dialect != DialectSQLite {
		t.Fatalf("dialect=%q want sqlite", d.Dialect)
	}
	if d.PG != nil {
		t.Fatal("PG should be nil for sqlite dialect")
	}
	if d.SQL == nil {
		t.Fatal("SQL nil")
	}
	if err := d.SQL.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	// Verify pragmas took effect.
	var jm string
	if err := d.SQL.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&jm); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if jm != "wal" {
		t.Fatalf("journal_mode=%q want wal", jm)
	}

	var fk int
	if err := d.SQL.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys=%d want 1", fk)
	}

	// OpenMigrationDB must produce a working *sql.DB pointed at the same file.
	mig, err := d.OpenMigrationDB()
	if err != nil {
		t.Fatalf("OpenMigrationDB: %v", err)
	}
	defer func() { _ = mig.Close() }()
	if err := mig.PingContext(ctx); err != nil {
		t.Fatalf("migration ping: %v", err)
	}
}
```

Also delete the existing `TestOpenSQLite_notYetSupported` test (the SQLite branch is no longer "not yet supported").

- [ ] **Step 5: Run the SQLite test**

```bash
go test ./internal/db/ -run TestOpenSQLite_live -v
```

Expected: PASS. Pragmas read back as `wal` and `1`.

- [ ] **Step 6: Run all `internal/db` tests**

```bash
TEST_DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' \
  go test ./internal/db/... -v
```

Expected: every test PASS. PG and SQLite paths both work.

- [ ] **Step 7: Build everything**

```bash
go build ./...
```

Expected: clean.

- [ ] **Step 8: Commit**

```bash
git add internal/db/db.go internal/db/db_test.go go.mod go.sum
git commit -m "$(cat <<'EOF'
feat(db): wire up SQLite backend via modernc.org/sqlite

Replaces the Plan-2 placeholder error with a real openSQLite path:
single writer (MaxOpenConns=1), WAL journal, foreign keys on, 5s busy
timeout. The DSN is parsed from sqlite:// / file: / bare-path forms.

OpenMigrationDB returns a fresh *sql.DB bound to the same file so
golang-migrate's sqlite3 driver can close it at m.Close() without
tearing down the shared application handle (mirrors the pgxpool
pattern from Plan 1).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Add SQLite branch to `dberr.IsUniqueViolation`

**Files:**
- Modify: `internal/db/dberr/dberr.go`
- Modify: `internal/db/dberr/dberr_test.go`

The `dberr` package today returns `(true, "libraries_slug_key")` from a Postgres `*pgconn.PgError`. SQLite's modernc driver surfaces unique-violation as a `*sqlite.Error` with `.Code() == 2067`. The error message has the form:

```
constraint failed: UNIQUE constraint failed: libraries.slug (2067)
```

Two complications versus PG:

1. **No constraint name.** SQLite reports the violated *column* (`libraries.slug`) instead of the index/constraint name (`libraries_slug_key`). Repos that switch on the constraint name need a translation layer.

2. **Composite uniques** report multiple columns: `UNIQUE constraint failed: user_devices.user_id, user_devices.name`. Repos that need to distinguish these (devices.go uses this) will get a comma-separated string.

We resolve both by returning a synthetic identifier that matches the **PG constraint name** for the corresponding index, hard-coded in a small map. Plan 1's `dberr.IsUniqueViolation` returned the raw constraint name; Plan 2A's still does, but on SQLite we look up the column-tuple in a translation table.

- [ ] **Step 1: Write failing tests**

Open `internal/db/dberr/dberr_test.go`. Add:

```go
import (
	// existing imports plus:
	"errors"

	"modernc.org/sqlite"
	sqlitelib "modernc.org/sqlite/lib"
)

func TestIsUniqueViolation_sqlite_libraries_slug(t *testing.T) {
	// Synthetic SQLite error mimicking what the driver actually surfaces.
	err := &sqlite.Error{}
	// We can't construct a *sqlite.Error fully because its fields are
	// unexported; build a wrapping error with the right message instead
	// and verify the parser can pick the columns out of the message.
	wrapped := errors.New("constraint failed: UNIQUE constraint failed: libraries.slug (2067)")

	ok, name := IsUniqueViolation(wrapped)
	if !ok {
		t.Fatal("UNIQUE constraint failed message should be a unique violation")
	}
	if name != "libraries_slug_key" {
		t.Fatalf("constraint=%q want libraries_slug_key", name)
	}

	_ = sqlitelib.SQLITE_CONSTRAINT_UNIQUE // import keeps compile-only reference
	_ = err
}

func TestIsUniqueViolation_sqlite_libraries_path(t *testing.T) {
	wrapped := errors.New("UNIQUE constraint failed: libraries.path")
	ok, name := IsUniqueViolation(wrapped)
	if !ok || name != "libraries_path_key" {
		t.Fatalf("got (%v, %q), want (true, libraries_path_key)", ok, name)
	}
}

func TestIsUniqueViolation_sqlite_devices_composite(t *testing.T) {
	wrapped := errors.New("UNIQUE constraint failed: user_devices.user_id, user_devices.name")
	ok, name := IsUniqueViolation(wrapped)
	if !ok || name != "idx_user_devices_user_name" {
		t.Fatalf("got (%v, %q), want (true, idx_user_devices_user_name)", ok, name)
	}
}

func TestIsUniqueViolation_sqlite_unmapped(t *testing.T) {
	wrapped := errors.New("UNIQUE constraint failed: some_table.some_col")
	ok, name := IsUniqueViolation(wrapped)
	if !ok {
		t.Fatal("should still classify as unique violation")
	}
	// Unmapped columns return the dotted form so callers can log it.
	if name != "some_table.some_col" {
		t.Fatalf("constraint=%q want some_table.some_col", name)
	}
}
```

Run: `go test ./internal/db/dberr/ -v`
Expected: FAIL — new tests fail because the SQLite branch doesn't exist.

- [ ] **Step 2: Implement the SQLite branch**

Open `internal/db/dberr/dberr.go`. Replace the file with:

```go
// Package dberr centralizes the error-inspection helpers that repos used
// to do inline against pgx-specific types. Branches for Postgres (pgx)
// and SQLite (modernc.org/sqlite) live behind one interface so repos
// don't import driver packages.
package dberr

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// IsNotFound reports whether err denotes "no rows" from any supported
// driver. Today that's database/sql (sql.ErrNoRows); the underlying
// driver is irrelevant because repos use *sql.DB.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, sql.ErrNoRows)
}

// IsUniqueViolation reports whether err denotes a unique-constraint
// violation, and if so returns a stable identifier for the violated
// constraint.
//
// On Postgres the identifier is the constraint name (`libraries_slug_key`)
// taken from *pgconn.PgError.ConstraintName.
//
// On SQLite the underlying error reports the columns that violated the
// uniqueness instead. We translate them to the equivalent PG constraint
// name using sqliteUniqueIndex so callers can compare against the same
// string regardless of backend. Unknown column-tuples return the raw
// dotted form ("table.column" or "table.col_a, table.col_b") so they
// surface in logs and the caller can decide what to do.
func IsUniqueViolation(err error) (bool, string) {
	if err == nil {
		return false, ""
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return true, pgErr.ConstraintName
	}
	if msg := err.Error(); strings.Contains(msg, "UNIQUE constraint failed:") {
		// Pull the substring after the marker.
		i := strings.Index(msg, "UNIQUE constraint failed:")
		tail := strings.TrimSpace(msg[i+len("UNIQUE constraint failed:"):])
		// Strip any trailing " (2067)" or similar appended by some wrappers.
		if j := strings.Index(tail, " ("); j != -1 {
			tail = tail[:j]
		}
		// Normalize whitespace in the column list.
		cols := strings.Join(strings.Fields(tail), " ")
		if name, ok := sqliteUniqueIndex[cols]; ok {
			return true, name
		}
		return true, cols
	}
	return false, ""
}

// sqliteUniqueIndex maps SQLite's column-tuple form of a violated
// uniqueness constraint to the equivalent Postgres constraint name.
// Keep this in sync with the unique indexes declared in the SQLite
// squashed init (internal/migrator/migrations/sqlite/0000_init.up.sql).
//
// Add an entry whenever a new unique index lands. Repo code that
// branches on a constraint name (e.g. CreateLibrary distinguishing
// slug vs path) must continue to receive the PG-flavored name on
// either backend.
var sqliteUniqueIndex = map[string]string{
	"libraries.slug":                              "libraries_slug_key",
	"libraries.path":                              "libraries_path_key",
	"users.email":                                 "users_email_key",
	"shelves.user_id, shelves.slug":               "shelves_user_id_slug_key",
	"sessions.token":                              "sessions_token_key",
	"bookdrop_items.path":                         "bookdrop_items_path_key",
	"user_devices.user_id, user_devices.name":    "idx_user_devices_user_name",
	"app_settings.name":                           "app_settings_pkey",
	"provider_settings.id":                        "provider_settings_pkey",
}
```

(The constraint names on the right side are exactly those Postgres uses — confirm by running `\d <table>` in psql or by reading the relevant up.sql migrations. The squashed SQLite init in Task 5 will declare matching unique indexes; the column-tuples on the left must match what SQLite reports.)

- [ ] **Step 3: Run the tests**

```bash
go test ./internal/db/dberr/ -v
```

Expected: all PASS — both PG and SQLite branches green.

- [ ] **Step 4: Commit**

```bash
git add internal/db/dberr/dberr.go internal/db/dberr/dberr_test.go
git commit -m "$(cat <<'EOF'
feat(db): add SQLite branch to dberr.IsUniqueViolation

SQLite reports unique-violation errors with the offending column tuple
(e.g. "UNIQUE constraint failed: libraries.slug") instead of a constraint
name. We parse the message and translate via a column-tuple → PG-name
map so repo callers see the same constraint identifier on both
backends. Unmapped tuples return the raw dotted form.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Wire the SQLite migrator driver

**Files:**
- Modify: `internal/migrator/migrator.go`

- [ ] **Step 1: Add the SQLite driver case to `driverFor`**

Open `internal/migrator/migrator.go`. Find:

```go
case db.DialectSQLite:
    return nil, "", errors.New("sqlite migrator driver not yet supported (Plan 2)")
```

Replace with:

```go
case db.DialectSQLite:
    drv, err := sqlite3.WithInstance(sqlDB, &sqlite3.Config{})
    if err != nil {
        return nil, "", fmt.Errorf("migrate sqlite3 driver: %w", err)
    }
    return drv, "sqlite3", nil
```

Update the import block:

```go
import (
    "database/sql"
    "embed"
    "errors"
    "fmt"

    "github.com/golang-migrate/migrate/v4"
    "github.com/golang-migrate/migrate/v4/database"
    "github.com/golang-migrate/migrate/v4/database/postgres"
    "github.com/golang-migrate/migrate/v4/database/sqlite3"
    "github.com/golang-migrate/migrate/v4/source/iofs"

    "github.com/blackforge/embookshelf/internal/db"
)
```

The `sqlite3` package ships with `golang-migrate/migrate/v4` already (no new dep needed; verify with `go mod tidy` afterwards).

- [ ] **Step 2: Build and run any existing tests**

```bash
go build ./...
go test ./internal/migrator/... -v
```

Expected: clean. (No test files in migrator; build success is the test.)

- [ ] **Step 3: Confirm a SQLite Open + migrator handshake works against an empty file**

This is a smoke step — no commit on its own. Skim the bottom of `internal/db/db.go` to confirm `OpenMigrationDB` returns a working `*sql.DB` for SQLite.

```bash
go run ./cmd/migrate version -dsn 'sqlite:'/tmp/embookshelf-mig-smoke.db 2>&1 | head -5
rm -f /tmp/embookshelf-mig-smoke.db
```

Expected: the command exits non-zero with `error: migrate source "migrations/sqlite": ... file does not exist` (because the SQLite migration tree doesn't exist yet — that's Task 5). The pertinent observation is that the SQLite *driver* loaded; if it printed `sqlite3: not registered` you've missed the blank-import in Task 2 Step 3.

- [ ] **Step 4: Commit**

```bash
git add internal/migrator/migrator.go go.sum
git commit -m "$(cat <<'EOF'
feat(migrator): wire SQLite driver via golang-migrate sqlite3

driverFor now returns a real sqlite3.WithInstance driver. The matching
migration tree (internal/migrator/migrations/sqlite/) lands in the
next task; running migrate against sqlite:// before that point
correctly errors out with "file does not exist".

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 1 — Schema (squashed SQLite init + FTS5)

### Task 5: Write `migrations/sqlite/0000_init.up.sql` (squashed schema)

**Files:**
- Create: `internal/migrator/migrations/sqlite/0000_init.up.sql`
- Create: `internal/migrator/migrations/sqlite/0000_init.down.sql`

This task is the largest single piece of pure SQL in the plan. The squashed init must reach the same logical schema as Postgres at version 23. The implementer translates column types per the spec's table (§2 lookup). Below is the canonical translation cheatsheet plus the **complete** squashed init.

#### Type translations

| Postgres | SQLite |
|---|---|
| `UUID PRIMARY KEY DEFAULT gen_random_uuid()` | `TEXT PRIMARY KEY NOT NULL` (id provided by app via `db.NewID()`) |
| `UUID` (foreign key) | `TEXT` |
| `TEXT NOT NULL DEFAULT ''` | `TEXT NOT NULL DEFAULT ''` (unchanged) |
| `INTEGER NOT NULL DEFAULT 0` | `INTEGER NOT NULL DEFAULT 0` (unchanged) |
| `BOOLEAN NOT NULL DEFAULT false` | `INTEGER NOT NULL DEFAULT 0 CHECK (col IN (0,1))` |
| `TIMESTAMPTZ NOT NULL DEFAULT now()` | `TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))` |
| `TIMESTAMPTZ` (nullable) | `TEXT` |
| `TEXT[] NOT NULL DEFAULT '{}'` | `TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(col))` |
| `JSONB NOT NULL DEFAULT '{}'::jsonb` | `TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(col))` |
| `tsvector GENERATED ALWAYS AS (…) STORED` | (omit; FTS5 virtual table replaces) |
| `CREATE EXTENSION pgcrypto` | (omit; UUIDs are app-generated) |
| `WHERE deleted_at IS NULL` (partial index) | identical (SQLite supports it) |
| `ON DELETE CASCADE` | identical (relies on `PRAGMA foreign_keys=ON`, set in `openSQLite`) |
| `CREATE UNIQUE INDEX idx_X ON tbl(...)` | identical |

- [ ] **Step 1: Create the squashed up migration**

Create `internal/migrator/migrations/sqlite/0000_init.up.sql`:

```sql
-- Squashed end-state schema for embookshelf on SQLite. Equivalent to
-- the union of internal/migrator/migrations/postgres/000001..000023
-- after every up.sql has been applied, with the type translations
-- listed in docs/superpowers/plans/2026-04-28-sqlite-2a-backend.md.
--
-- Forward ports beyond this point (version 24+) live as parallel
-- migrations under both postgres/ and sqlite/, NOT as additions to
-- this squashed file.

-- ============================================================
-- libraries
-- ============================================================
CREATE TABLE IF NOT EXISTS libraries (
    id                   TEXT PRIMARY KEY NOT NULL,
    name                 TEXT NOT NULL,
    slug                 TEXT NOT NULL,
    path                 TEXT NOT NULL DEFAULT '',
    last_scanned_at      TEXT,
    file_count           INTEGER NOT NULL DEFAULT 0,
    discovered_count     INTEGER NOT NULL DEFAULT 0,
    file_naming_pattern  TEXT NOT NULL DEFAULT '',
    created_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS libraries_slug_key  ON libraries(slug);
CREATE UNIQUE INDEX IF NOT EXISTS libraries_path_key  ON libraries(path) WHERE path != '';

-- ============================================================
-- books
-- ============================================================
CREATE TABLE IF NOT EXISTS books (
    id                  TEXT PRIMARY KEY NOT NULL,
    library_id          TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    title               TEXT NOT NULL,
    subtitle            TEXT NOT NULL DEFAULT '',
    author              TEXT NOT NULL DEFAULT '',
    format              TEXT NOT NULL DEFAULT 'EPUB',
    year                INTEGER NOT NULL DEFAULT 0,
    publish_date        TEXT NOT NULL DEFAULT '',
    language            TEXT NOT NULL DEFAULT '',
    rating              INTEGER NOT NULL DEFAULT 0,
    cover_palette       TEXT NOT NULL DEFAULT 'navy',
    description         TEXT NOT NULL DEFAULT '',
    isbn                TEXT NOT NULL DEFAULT '',
    isbn10              TEXT NOT NULL DEFAULT '',
    publisher           TEXT NOT NULL DEFAULT '',
    series              TEXT NOT NULL DEFAULT '',
    series_index        INTEGER NOT NULL DEFAULT 0,
    series_total        INTEGER NOT NULL DEFAULT 0,
    genres              TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(genres)),
    moods               TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(moods)),
    tags                TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(tags)),
    age_rating          TEXT NOT NULL DEFAULT '',
    content_rating      TEXT NOT NULL DEFAULT '',
    pages               INTEGER NOT NULL DEFAULT 0,
    public_reviews      TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    deleted_at          TEXT,
    path                TEXT NOT NULL DEFAULT '',
    has_cover           INTEGER NOT NULL DEFAULT 0 CHECK (has_cover IN (0,1)),
    cover_mime          TEXT NOT NULL DEFAULT '',
    title_locked        INTEGER NOT NULL DEFAULT 0 CHECK (title_locked IN (0,1)),
    subtitle_locked     INTEGER NOT NULL DEFAULT 0 CHECK (subtitle_locked IN (0,1)),
    author_locked       INTEGER NOT NULL DEFAULT 0 CHECK (author_locked IN (0,1)),
    description_locked  INTEGER NOT NULL DEFAULT 0 CHECK (description_locked IN (0,1)),
    publisher_locked    INTEGER NOT NULL DEFAULT 0 CHECK (publisher_locked IN (0,1)),
    series_locked       INTEGER NOT NULL DEFAULT 0 CHECK (series_locked IN (0,1)),
    isbn_locked         INTEGER NOT NULL DEFAULT 0 CHECK (isbn_locked IN (0,1)),
    isbn10_locked       INTEGER NOT NULL DEFAULT 0 CHECK (isbn10_locked IN (0,1)),
    language_locked     INTEGER NOT NULL DEFAULT 0 CHECK (language_locked IN (0,1)),
    publish_date_locked INTEGER NOT NULL DEFAULT 0 CHECK (publish_date_locked IN (0,1)),
    genres_locked       INTEGER NOT NULL DEFAULT 0 CHECK (genres_locked IN (0,1)),
    moods_locked        INTEGER NOT NULL DEFAULT 0 CHECK (moods_locked IN (0,1)),
    tags_locked         INTEGER NOT NULL DEFAULT 0 CHECK (tags_locked IN (0,1)),
    pages_locked        INTEGER NOT NULL DEFAULT 0 CHECK (pages_locked IN (0,1)),
    cover_locked        INTEGER NOT NULL DEFAULT 0 CHECK (cover_locked IN (0,1))
);

CREATE INDEX IF NOT EXISTS idx_books_library_id ON books(library_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_books_title      ON books(title)      WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_books_format     ON books(format)     WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_books_path       ON books(path)       WHERE deleted_at IS NULL;

-- ============================================================
-- shelves + shelf_books
-- ============================================================
CREATE TABLE IF NOT EXISTS shelves (
    id          TEXT PRIMARY KEY NOT NULL,
    user_id     TEXT NOT NULL,
    name        TEXT NOT NULL,
    slug        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    is_smart    INTEGER NOT NULL DEFAULT 0 CHECK (is_smart IN (0,1)),
    rule        TEXT,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS shelves_user_id_slug_key
    ON shelves(user_id, slug);

CREATE TABLE IF NOT EXISTS shelf_books (
    shelf_id   TEXT NOT NULL REFERENCES shelves(id) ON DELETE CASCADE,
    book_id    TEXT NOT NULL REFERENCES books(id)   ON DELETE CASCADE,
    added_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (shelf_id, book_id)
);

-- ============================================================
-- users + sessions + password_resets
-- ============================================================
CREATE TABLE IF NOT EXISTS users (
    id              TEXT PRIMARY KEY NOT NULL,
    email           TEXT NOT NULL,
    name            TEXT NOT NULL DEFAULT '',
    password_hash   TEXT NOT NULL DEFAULT '',
    role            TEXT NOT NULL DEFAULT 'user',
    status          TEXT NOT NULL DEFAULT 'approved',
    auth_provider   TEXT NOT NULL DEFAULT 'local',
    oidc_subject    TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS users_email_key
    ON users(LOWER(email));

CREATE TABLE IF NOT EXISTS sessions (
    id          TEXT PRIMARY KEY NOT NULL,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token       TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    expires_at  TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS sessions_token_key ON sessions(token);

-- (continue translating each PG migration's tables, indexes, and
-- triggers in the same order: bookdrop, per-user-progress, reader,
-- covers, library_paths, annotations, smart_shelves, reading_sessions,
-- devices, oidc, provider_settings, library_naming_pattern,
-- app_settings, library_single_path, book_metadata_extended,
-- book_metadata_locks, provider_settings_config, provider_health,
-- user_approval_status. The pattern is uniform — every CREATE TABLE
-- gets the type translations above, and every CREATE [UNIQUE] INDEX
-- carries over verbatim. ON DELETE CASCADE is preserved because
-- foreign_keys=ON is set in openSQLite.)

-- ============================================================
-- (FTS5 virtual table + triggers land in Task 6)
-- ============================================================
```

The omitted tables in the comment block above are NOT optional — the implementer must port all of them following the same translation rules. Read each `internal/migrator/migrations/postgres/0000XX_*.up.sql` file, translate it under the rules from the table at the top of Task 5, and append to the squashed init. Confirm row counts match by running `\dt` in psql against the dev PG and `.tables` in `sqlite3 /tmp/embookshelf-mig-smoke.db` after migration.

For brevity the plan stops after the first 5 tables — the implementer continues with the remaining ~15 tables/indexes following the identical template. **There is no shortcut here**; each table must be translated by hand.

- [ ] **Step 2: Create the down migration**

Create `internal/migrator/migrations/sqlite/0000_init.down.sql`:

```sql
-- Plan 2A teardown: drop everything created in 0000_init.up.sql.
-- Triggers and FTS5 virtual table are dropped in Task 6's commit; the
-- DROP statements below are idempotent so the order between Task 5
-- and Task 6 commits doesn't matter for clean teardown.

DROP TRIGGER IF EXISTS books_fts_after_insert;
DROP TRIGGER IF EXISTS books_fts_after_delete;
DROP TRIGGER IF EXISTS books_fts_after_update;
DROP TABLE   IF EXISTS books_fts;

DROP INDEX IF EXISTS sessions_token_key;
DROP INDEX IF EXISTS users_email_key;
DROP INDEX IF EXISTS shelves_user_id_slug_key;
DROP INDEX IF EXISTS idx_books_path;
DROP INDEX IF EXISTS idx_books_format;
DROP INDEX IF EXISTS idx_books_title;
DROP INDEX IF EXISTS idx_books_library_id;
DROP INDEX IF EXISTS libraries_path_key;
DROP INDEX IF EXISTS libraries_slug_key;

DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS shelf_books;
DROP TABLE IF EXISTS shelves;
DROP TABLE IF EXISTS books;
DROP TABLE IF EXISTS libraries;

-- (continue with the remaining DROPs in reverse-creation order to
--  satisfy any FK relationships)
```

Expand to cover every table created in `0000_init.up.sql`.

- [ ] **Step 3: Run the migrate up against a fresh SQLite file**

```bash
rm -f /tmp/embookshelf-sqlite-init.db
go run ./cmd/migrate up -dsn 'sqlite:/tmp/embookshelf-sqlite-init.db'
```

Expected: `ok`. Inspect the resulting database:

```bash
sqlite3 /tmp/embookshelf-sqlite-init.db ".tables"
sqlite3 /tmp/embookshelf-sqlite-init.db ".schema libraries"
sqlite3 /tmp/embookshelf-sqlite-init.db "PRAGMA integrity_check;"
```

Expected:
- `.tables` lists every translated table.
- `.schema libraries` shows the column types translated per the table.
- `PRAGMA integrity_check` returns `ok`.

- [ ] **Step 4: Run migrate down to verify the down script is symmetric**

```bash
go run ./cmd/migrate down -dsn 'sqlite:/tmp/embookshelf-sqlite-init.db'
sqlite3 /tmp/embookshelf-sqlite-init.db ".tables"
```

Expected: `ok`, then `.tables` returns no application tables (only `schema_migrations`).

- [ ] **Step 5: Re-up to confirm idempotency, then commit**

```bash
rm -f /tmp/embookshelf-sqlite-init.db
go run ./cmd/migrate up   -dsn 'sqlite:/tmp/embookshelf-sqlite-init.db'
go run ./cmd/migrate down -dsn 'sqlite:/tmp/embookshelf-sqlite-init.db'
go run ./cmd/migrate up   -dsn 'sqlite:/tmp/embookshelf-sqlite-init.db'
```

All three steps must print `ok`. Last `up` re-creates everything cleanly.

```bash
git add internal/migrator/migrations/sqlite/0000_init.up.sql \
        internal/migrator/migrations/sqlite/0000_init.down.sql
git commit -m "$(cat <<'EOF'
feat(migrator): add squashed SQLite init (no FTS5 yet)

Single 0000_init pair captures the end-state schema reachable from
the union of postgres/000001..000023. Type translations follow the
plan's table: UUID->TEXT, TIMESTAMPTZ->TEXT (RFC3339), JSONB->TEXT
with json_valid(), TEXT[]->TEXT (JSON-encoded) with json_valid(),
BOOLEAN->INTEGER+CHECK. tsvector is intentionally omitted; the FTS5
virtual table lands in the next task.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Add FTS5 virtual table + triggers

**Files:**
- Modify: `internal/migrator/migrations/sqlite/0000_init.up.sql`
- Modify: `internal/migrator/migrations/sqlite/0000_init.down.sql`

The squashed init from Task 5 doesn't yet include FTS5. We add it in a separate commit so the diff is reviewable on its own. Since this is still version `0000` and migrations are idempotent (`CREATE VIRTUAL TABLE IF NOT EXISTS`), modifying the same up.sql is safe — anyone who ran Task 5's up against a real DB needs to `migrate down && migrate up` to pick up the FTS additions, but the squashed init has no production users (it ships in this plan).

- [ ] **Step 1: Append the FTS5 virtual table and triggers to `0000_init.up.sql`**

Add these blocks at the end of `internal/migrator/migrations/sqlite/0000_init.up.sql`:

```sql
-- ============================================================
-- Full-text search: FTS5 virtual table mirrors title/author/series/description.
--   - content='books' makes it an "external content" FTS table; the
--     virtual table doesn't store its own copy of the text and
--     trigger-driven sync keeps it aligned.
--   - content_rowid='rowid' uses the books table's implicit rowid
--     for cross-references (NOT the books.id TEXT — FTS5 needs an
--     INTEGER content_rowid).
-- The PG side keeps the existing tsvector + GIN index.
-- ============================================================
CREATE VIRTUAL TABLE IF NOT EXISTS books_fts USING fts5(
    title,
    author,
    series,
    description,
    content='books',
    content_rowid='rowid',
    tokenize='unicode61'
);

CREATE TRIGGER IF NOT EXISTS books_fts_after_insert
AFTER INSERT ON books BEGIN
    INSERT INTO books_fts(rowid, title, author, series, description)
    VALUES (new.rowid, new.title, new.author, new.series, new.description);
END;

CREATE TRIGGER IF NOT EXISTS books_fts_after_delete
AFTER DELETE ON books BEGIN
    INSERT INTO books_fts(books_fts, rowid, title, author, series, description)
    VALUES ('delete', old.rowid, old.title, old.author, old.series, old.description);
END;

CREATE TRIGGER IF NOT EXISTS books_fts_after_update
AFTER UPDATE ON books BEGIN
    INSERT INTO books_fts(books_fts, rowid, title, author, series, description)
    VALUES ('delete', old.rowid, old.title, old.author, old.series, old.description);
    INSERT INTO books_fts(rowid, title, author, series, description)
    VALUES (new.rowid, new.title, new.author, new.series, new.description);
END;
```

Note: the `'delete'` rows are FTS5's prescribed content-table contract for keeping external-content tables in sync; the implementer should NOT replace them with `DELETE FROM books_fts WHERE rowid = old.rowid`.

- [ ] **Step 2: Confirm the up still applies cleanly**

```bash
rm -f /tmp/embookshelf-sqlite-fts.db
go run ./cmd/migrate up -dsn 'sqlite:/tmp/embookshelf-sqlite-fts.db'
sqlite3 /tmp/embookshelf-sqlite-fts.db <<'SQL'
.mode line
.tables
.schema books_fts
SQL
```

Expected: `books_fts` appears in `.tables` and is a virtual fts5 table. The triggers are visible via:

```bash
sqlite3 /tmp/embookshelf-sqlite-fts.db "SELECT name FROM sqlite_master WHERE type='trigger';"
```

Expected: `books_fts_after_insert`, `books_fts_after_update`, `books_fts_after_delete`.

- [ ] **Step 3: Smoke-test FTS round-trip**

```bash
sqlite3 /tmp/embookshelf-sqlite-fts.db <<'SQL'
INSERT INTO libraries(id, name, slug) VALUES ('lib-1','My Library','my-library');
INSERT INTO books(id, library_id, title, author, series, description)
VALUES
  ('b1','lib-1','Dune','Frank Herbert','Dune','Sandworms and politics.'),
  ('b2','lib-1','Foundation','Isaac Asimov','Foundation','Galactic empire fall.');

SELECT b.title, bm25(books_fts) AS rank
FROM books_fts
JOIN books b ON b.rowid = books_fts.rowid
WHERE books_fts MATCH 'galactic OR sandworms*'
ORDER BY rank ASC;
SQL
```

Expected: both books returned; ordering by `bm25` ascending.

- [ ] **Step 4: Update the down migration**

Confirm `0000_init.down.sql` already lists the FTS triggers and table at the top (Task 5 Step 2 already included them). If not, prepend:

```sql
DROP TRIGGER IF EXISTS books_fts_after_insert;
DROP TRIGGER IF EXISTS books_fts_after_delete;
DROP TRIGGER IF EXISTS books_fts_after_update;
DROP TABLE   IF EXISTS books_fts;
```

- [ ] **Step 5: Down → up cycle to verify**

```bash
go run ./cmd/migrate down -dsn 'sqlite:/tmp/embookshelf-sqlite-fts.db'
go run ./cmd/migrate up   -dsn 'sqlite:/tmp/embookshelf-sqlite-fts.db'
sqlite3 /tmp/embookshelf-sqlite-fts.db ".tables" | grep -E "books_fts|books"
```

Expected: both `books` and `books_fts` reappear after the up.

- [ ] **Step 6: Commit**

```bash
git add internal/migrator/migrations/sqlite/0000_init.up.sql \
        internal/migrator/migrations/sqlite/0000_init.down.sql
git commit -m "$(cat <<'EOF'
feat(migrator): add FTS5 virtual table for SQLite book search

books_fts is an external-content FTS5 table mirroring title, author,
series, and description from books. Three AFTER triggers keep the
index in sync using FTS5's prescribed 'delete'/insert protocol.

The PG path continues to use the existing tsvector GENERATED column +
GIN index; both backends preserve the search experience.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 2 — Search query escaping helper

### Task 7: `internal/search/fts5.go` — safe FTS5 query builder

**Files:**
- Create: `internal/search/fts5.go`
- Create: `internal/search/fts5_test.go`

Postgres' `websearch_to_tsquery('english', input)` accepts arbitrary user input and returns a safe tsquery. SQLite's FTS5 has no equivalent — passing `MATCH 'title: "open quote'` crashes the query. We need a small parser that turns a user search string into a safe FTS5 expression.

Strategy:
1. Lowercase the input.
2. Tokenize on whitespace.
3. Strip every character outside `[a-z0-9'-]` from each token.
4. Drop empty tokens.
5. Wrap each surviving token in `"…"*` (quoted, prefix-tolerant).
6. Join with spaces (FTS5 default: implicit AND).

This loses some power (no NEAR, no explicit OR, no field qualifiers) but matches what `websearch_to_tsquery` does on the PG side at the level the UI exposes today (a single search box). It's small enough to be obviously correct.

- [ ] **Step 1: Write the failing tests**

Create `internal/search/fts5_test.go`:

```go
package search

import "testing"

func TestEscapeFTS5Query(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"single token", "dune", `"dune"*`},
		{"multi token AND", "dune frank", `"dune"* "frank"*`},
		{"strips quotes", `"dune"`, `"dune"*`},
		{"strips parens", "(dune)", `"dune"*`},
		{"strips reserved", "dune* AND OR NEAR", `"dune"* "and"* "or"* "near"*`},
		{"keeps apostrophe", "robot's dawn", `"robot's"* "dawn"*`},
		{"keeps hyphen", "anti-matter", `"anti-matter"*`},
		{"unicode lowercase", "DÜNE", `"düne"*`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EscapeFTS5Query(tc.in)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
```

Run: `go test ./internal/search/ -v`
Expected: FAIL — symbol undefined.

- [ ] **Step 2: Implement the helper**

Create `internal/search/fts5.go`:

```go
// Package search holds search-related helpers shared by the library
// repo and the OPDS layer. Today the only export is EscapeFTS5Query
// for the SQLite FTS5 path; Postgres uses websearch_to_tsquery and
// doesn't need an equivalent.
package search

import (
	"strings"
	"unicode"
)

// EscapeFTS5Query turns arbitrary user input into a safe FTS5 MATCH
// expression. The result is a space-separated list of `"<token>"*`
// chunks where each token is the lowercased, ASCII-letter/digit/
// hyphen/apostrophe-only fragment of the input word. Tokens are
// joined by FTS5's implicit AND.
//
// The empty string is returned for input that contains no tokens
// (e.g. "" or "  ?"). Callers should treat that as "no search filter"
// and fall back to whatever query they'd run with no search term.
func EscapeFTS5Query(in string) string {
	in = strings.ToLower(in)
	fields := strings.Fields(in)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		var sb strings.Builder
		for _, r := range f {
			switch {
			case unicode.IsLetter(r), unicode.IsDigit(r), r == '\'', r == '-':
				sb.WriteRune(r)
			}
		}
		t := sb.String()
		if t == "" {
			continue
		}
		out = append(out, `"`+t+`"*`)
	}
	return strings.Join(out, " ")
}
```

- [ ] **Step 3: Run the tests**

```bash
go test ./internal/search/ -v
```

Expected: PASS for every subcase.

- [ ] **Step 4: Commit**

```bash
git add internal/search/fts5.go internal/search/fts5_test.go
git commit -m "$(cat <<'EOF'
feat(search): add EscapeFTS5Query helper for SQLite full-text search

Turns arbitrary user input into a safe FTS5 MATCH expression: lowercase,
tokenize on whitespace, strip punctuation outside ASCII letters/digits/
hyphens/apostrophes, wrap each token as "<tok>"* for prefix-tolerant
matching. Tokens implicitly AND.

Library.go's search query branch (Task 8) calls this for the SQLite
path. Postgres continues to use websearch_to_tsquery directly.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 3 — Per-repo dialect-tagged queries

Each of the next 12 tasks adapts one repo to support both backends. The work pattern per task:

1. Add an `internal/db.SelectQ` call around each non-trivial SQL string.
2. For INSERTs that previously relied on `gen_random_uuid()`, generate the ID app-side via `db.NewID()` and pass it as a parameter (PG INSERT becomes `INSERT INTO X (id, …) VALUES ($1, …)`; SQLite uses `?1, …`).
3. For columns of type `TEXT[]`, use `db.ValueStringSlice(d, slice)` when binding and `db.ScanStringSlice(d, srcAny, &dst)` when reading. The `srcAny` form requires scanning into a `*any` placeholder first, then calling `ScanStringSlice` — see Task 8 for the canonical example.
4. For `JSONB` / `TEXT json_valid` columns, no change required (both backends accept `[]byte`).
5. For `ILIKE`, the SQLite path uses `LIKE … COLLATE NOCASE`.
6. For FTS-driven WHERE/ORDER BY (library.go only), the SQLite path uses `books_fts MATCH ?` + `bm25(books_fts) ASC`, with the search term passed through `search.EscapeFTS5Query`.

After each task, run:

```bash
go build ./...
go vet ./...
make go-lint
TEST_DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' go test ./...
```

All four must pass before commit.

### Task 8: `library.go` — books, libraries, FTS

This is the largest repo change in Plan 2A. Roughly 17 SELECT/INSERT/UPDATE/DELETE queries to dialect-tag, plus the 3 FTS sites and the 3 array columns (`tags`, `genres`, `moods`).

**Files:**
- Modify: `internal/repo/library.go`

The mechanical changes are too numerous to enumerate verbatim here. Instead, the plan supplies the canonical query-rewrite pattern with two complete worked examples (one INSERT, one FTS SELECT). The implementer applies the same pattern to every other query.

#### Worked example 1: `CreateLibrary` (INSERT … RETURNING with app-side UUID)

Current:

```go
func (r *LibraryRepo) CreateLibrary(ctx context.Context, name, slug, path string) (model.Library, error) {
    row := r.db.SQL.QueryRowContext(ctx, `
        INSERT INTO libraries (name, slug, path)
        VALUES ($1, $2, $3)
        RETURNING
            id, name, slug, path,
            last_scanned_at, file_count, discovered_count,
            file_naming_pattern, created_at,
            0 AS book_count
    `, name, slug, path)
    l, err := scanLibrary(row)
    ...
}
```

After:

```go
func (r *LibraryRepo) CreateLibrary(ctx context.Context, name, slug, path string) (model.Library, error) {
    id := db.NewID()
    const qPG = `
        INSERT INTO libraries (id, name, slug, path)
        VALUES ($1, $2, $3, $4)
        RETURNING ` + libCols + `, 0 AS book_count
    `
    const qSQLite = `
        INSERT INTO libraries (id, name, slug, path)
        VALUES (?, ?, ?, ?)
        RETURNING ` + libCols + `, 0 AS book_count
    `
    row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite),
        id, name, slug, path)
    l, err := scanLibrary(row)
    ...
}
```

Note: `libCols` already SELECTs from `l` alias today; in this form it's a bare RETURNING without the alias, so `libCols` may need adjusting OR a second constant `libColsReturning` introduced. Read the existing definition before editing — it's likely already aliased; introduce `const libColsReturning = ... (no l. prefix) ...` if needed.

#### Worked example 2: book search (FTS branch)

The current search query (lines around 263, 573, 601 of `library.go`) uses Postgres FTS:

```go
where = append(where, fmt.Sprintf("b.tsv @@ websearch_to_tsquery('english', $%d)", len(args)))
```

The dialect-tagged version:

```go
var fts string
if r.db.Dialect == db.DialectSQLite {
    fts = search.EscapeFTS5Query(searchInput)
    if fts != "" {
        where = append(where, "b.rowid IN (SELECT rowid FROM books_fts WHERE books_fts MATCH ?)")
        args = append(args, fts)
    }
} else {
    where = append(where, fmt.Sprintf("b.tsv @@ websearch_to_tsquery('english', $%d)", len(args)+1))
    args = append(args, searchInput)
}
```

For the ranking ORDER BY (around line 574):

```go
ORDER BY ts_rank(b.tsv, websearch_to_tsquery('english', $1)) DESC, b.title
```

becomes:

```go
const orderPG     = `ORDER BY ts_rank(b.tsv, websearch_to_tsquery('english', $1)) DESC, b.title`
const orderSQLite = `ORDER BY (SELECT bm25(books_fts) FROM books_fts WHERE books_fts MATCH ? AND books_fts.rowid = b.rowid) ASC, b.title`
```

…dispatched via `db.SelectQ`. This is fiddly — the bm25 ranking has to be computable for each row, hence the correlated subquery. An alternative is to JOIN `books_fts` and select bm25 directly; either works as long as the test in Task 21 confirms search results come back in a sane order on SQLite.

#### Array column handling

For columns `tags`, `genres`, `moods` in book INSERT/UPDATE statements, replace direct `tags` arg with:

```go
tagsVal, err := db.ValueStringSlice(r.db.Dialect, b.Tags)
if err != nil {
    return model.Book{}, fmt.Errorf("encode tags: %w", err)
}
```

…and pass `tagsVal` as the bound argument.

For SELECTs that scan into `&book.Tags`, change the scanner. Today `scanBook` does:

```go
err := s.Scan(
    &b.ID, ...,
    &b.Genres, &b.Moods, &b.Tags,
    ...,
)
```

After:

```go
var genresAny, moodsAny, tagsAny any
err := s.Scan(
    &b.ID, ...,
    &genresAny, &moodsAny, &tagsAny,
    ...,
)
if err != nil { return ..., err }
if err := db.ScanStringSlice(dialect, genresAny, &b.Genres); err != nil { ... }
if err := db.ScanStringSlice(dialect, moodsAny,  &b.Moods);  err != nil { ... }
if err := db.ScanStringSlice(dialect, tagsAny,   &b.Tags);   err != nil { ... }
```

The `dialect` value needs to be passed down to `scanBook`. Since `scanBook` is package-level, easiest path is to add a `dialect db.Dialect` parameter:

```go
func scanBook(d db.Dialect, s scanner) (model.Book, error)
```

…and update every call site.

#### Steps

- [ ] **Step 1: Read the current `library.go` end to end**, list every SQL string by approximate line, and identify which ones are simple (no Postgres-specific syntax) and which need a SQLite-specific rewrite. Many lookups by id are trivially identical except for `$N` vs `?`.

- [ ] **Step 2: Add the `db` package alias if not already imported.** Add `"github.com/blackforge/embookshelf/internal/search"` for the FTS helper.

- [ ] **Step 3: Refactor `scanBook` to take a `db.Dialect` parameter** and call `db.ScanStringSlice` for the three array columns. Update all call sites.

- [ ] **Step 4: For each INSERT / UPSERT**, rewrite per worked example 1: app-side `db.NewID()`, two SQL strings via `db.SelectQ`. INSERTs targeting `books` must encode `tags`/`genres`/`moods` via `db.ValueStringSlice` before binding.

- [ ] **Step 5: For each pure SELECT**, write two query strings (PG `$N` form, SQLite `?` form). For trivial lookups by id, a helper `placeholders.Build(d, n)` could be used, but YAGNI — the duplication is acceptable.

- [ ] **Step 6: Rewrite the FTS branches** per worked example 2.

- [ ] **Step 7: Build / vet / lint / test** as listed at the top of Phase 3. PG path must remain green.

- [ ] **Step 8: Verify SQLite end-to-end**

Boot the server with SQLite:

```bash
rm -f /tmp/embookshelf-sqlite-app.db
DATABASE_URL='sqlite:/tmp/embookshelf-sqlite-app.db' \
  go run ./cmd/migrate up
DATABASE_URL='sqlite:/tmp/embookshelf-sqlite-app.db' \
  go build -o /tmp/embookshelf ./cmd/embookshelf
DATABASE_URL='sqlite:/tmp/embookshelf-sqlite-app.db' \
  /tmp/embookshelf 2>&1 | tee /tmp/server.log &
PID=$!
sleep 5
curl -s http://localhost:6060/api/libraries -o /dev/null -w "HTTP %{http_code}\n"
kill $PID 2>/dev/null
wait 2>/dev/null
```

Expected: server boots (the queue's non-Postgres error message will surface — that's fine, it just means River doesn't start; `/api/libraries` should still return 200). At the bare minimum, the books listing endpoint works.

If the server crashes at startup with `queue: …`, you've successfully reached the queue gate — that's the expected stop point for Plan 2A. The plan's terminal state is "API works, queue does not."

- [ ] **Step 9: Commit**

```bash
git add internal/repo/library.go
git commit -m "$(cat <<'EOF'
feat(repo): library supports SQLite via dialect-tagged queries

Every SQL string in library.go now exists in two flavors dispatched
through db.SelectQ: Postgres ($N placeholders, native tsvector +
text[] columns) and SQLite (? placeholders, FTS5 books_fts virtual
table, JSON-encoded TEXT for arrays).

INSERTs generate UUIDs app-side via db.NewID() instead of relying on
PG's gen_random_uuid() default. The PG column default stays in place
so external INSERTs keep working.

scanBook gains a db.Dialect parameter so it can route the genres /
moods / tags scan through db.ScanStringSlice. The book search FTS
branch escapes user input via search.EscapeFTS5Query before MATCH.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: `shelf.go`

**Files:**
- Modify: `internal/repo/shelf.go`

Apply the canonical pattern from the top of Phase 3. Repo-specific notes:

- 5 INSERT/UPDATE statements need `db.NewID()` for the `id` column.
- 3 `ILIKE` clauses (lines around 488, 490, 541) → SQLite uses `LIKE … COLLATE NOCASE`.
- The `rule` JSONB column is already `[]byte` in Go; no adapter needed.
- `collectBooks` is called from shelf.go — it now takes `*sql.Rows` after Plan 1 + Task 8. Verify the call sites still compile.
- 1 `errors.As` for `*pgconn.PgError` was removed in Plan 1; the `dberr.IsUniqueViolation` call already returns the constraint name (e.g. `shelves_user_id_slug_key`), which the SQLite branch in `dberr` maps to from the column tuple.

Steps:
- [ ] Apply pattern.
- [ ] Build / vet / lint / test (PG green).
- [ ] SQLite smoke (server boots, `/api/shelves` returns 200).
- [ ] Commit `feat(repo): shelf supports SQLite via dialect-tagged queries`.

### Task 10: `user.go`

**Files:**
- Modify: `internal/repo/user.go`

- 4 INSERTs need `db.NewID()`: users (id), the password_resets table (if separate; check schema).
- `users.email` is uniquely indexed via `LOWER(email)` in both backends — query continues to use `WHERE LOWER(email) = LOWER($1)` / `WHERE LOWER(email) = LOWER(?)`.
- No arrays / JSONB.

Steps: same as Task 9. Commit `feat(repo): user supports SQLite via dialect-tagged queries`.

### Task 11: `session.go`

**Files:**
- Modify: `internal/repo/session.go`

- INSERTs need `db.NewID()`.
- `expires_at` is `TIMESTAMPTZ` (PG) / `TEXT` (SQLite); Go's `time.Time` round-trips on both via `database/sql`.

Steps: same. Commit `feat(repo): session supports SQLite via dialect-tagged queries`.

### Task 12: `bookdrop.go`

**Files:**
- Modify: `internal/repo/bookdrop.go`

- 1 INSERT (`bookdrop_items`) needs `db.NewID()`.
- `ON CONFLICT (path) DO NOTHING RETURNING …` works on both backends.
- Worth verifying: when `ON CONFLICT DO NOTHING` returns no rows on SQLite, the `RETURNING` clause yields zero rows (same as PG) — `scanBookDrop` returns `ErrNotFound`, the existing code path handles it.

Steps: same. Commit `feat(repo): bookdrop supports SQLite via dialect-tagged queries`.

### Task 13: `progress.go`

**Files:**
- Modify: `internal/repo/progress.go`

Tiny repo. One UPSERT:

```sql
INSERT INTO user_book_progress (user_id, book_id, progress, resume_cfi, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (user_id, book_id) DO UPDATE
SET progress = EXCLUDED.progress, resume_cfi = EXCLUDED.resume_cfi, updated_at = now()
```

SQLite version replaces `now()` with `(strftime('%Y-%m-%dT%H:%M:%fZ','now'))` and `$N` with `?`. No UUID needed (composite PK).

Steps: same. Commit `feat(repo): progress supports SQLite via dialect-tagged queries`.

### Task 14: `annotation.go`

**Files:**
- Modify: `internal/repo/annotation.go`

- 2 INSERTs need `db.NewID()`.
- `collectAnnotations` returns `*sql.Rows` after Plan 1.

Steps: same. Commit `feat(repo): annotation supports SQLite via dialect-tagged queries`.

### Task 15: `stats.go`

**Files:**
- Modify: `internal/repo/stats.go`

Read-only. Aggregates (`COUNT(*)`, `SUM`, `AVG`) and date-arithmetic. The PG queries use `EXTRACT(...)`, `date_trunc`, etc. — these need SQLite equivalents:

| PG | SQLite |
|---|---|
| `EXTRACT(YEAR FROM created_at)` | `CAST(strftime('%Y', created_at) AS INTEGER)` |
| `date_trunc('month', created_at)` | `strftime('%Y-%m-01', created_at)` |
| `created_at >= now() - interval '30 days'` | `created_at >= datetime('now','-30 days')` |
| `EXTRACT(EPOCH FROM (a - b))` | `(julianday(a) - julianday(b)) * 86400` |

Read each query in `stats.go` and translate accordingly. There are 8 `r.db.SQL.*` calls; each is a self-contained aggregate that can be dialect-tagged independently.

Steps: same. Commit `feat(repo): stats supports SQLite via dialect-tagged queries`.

### Task 16: `reading_session.go`

**Files:**
- Modify: `internal/repo/reading_session.go`

Same shape as session.go: INSERT with `db.NewID()`, time fields. Date arithmetic if any (check the query body).

Steps: same. Commit `feat(repo): reading_session supports SQLite via dialect-tagged queries`.

### Task 17: `device.go`

**Files:**
- Modify: `internal/repo/device.go`

- INSERT needs `db.NewID()`.
- `config` JSONB is already `[]byte` in Go.
- The unique-violation switch on `idx_user_devices_user_name` works because `dberr.IsUniqueViolation`'s SQLite branch maps `user_devices.user_id, user_devices.name` → `idx_user_devices_user_name`.

Steps: same. Commit `feat(repo): device supports SQLite via dialect-tagged queries`.

### Task 18: `app_settings.go`

**Files:**
- Modify: `internal/repo/app_settings.go`

- Composite PK (`name` is the primary key). No UUID needed.
- `value` JSONB → `[]byte`. No change in Go.
- The UPSERT `ON CONFLICT (name) DO UPDATE` works on both backends.

Steps: same. Commit `feat(repo): app_settings supports SQLite via dialect-tagged queries`.

### Task 19: `provider_settings.go`

**Files:**
- Modify: `internal/repo/provider_settings.go`

- `id` column is the provider's well-known string (`"google_books"`, etc.) — no UUID generation.
- `config` JSONB → `[]byte`. No change.
- Multiple `ON CONFLICT (id) DO UPDATE` patterns — work on both.

Steps: same. Commit `feat(repo): provider_settings supports SQLite via dialect-tagged queries`.

---

## Phase 4 — Final wiring

### Task 20: Update `internal/queue/queue.go` error message

**Files:**
- Modify: `internal/queue/queue.go`

Tiny doc-only change. Find:

```go
return nil, errors.New("queue: only Postgres backend supported in Plan 1")
```

Replace with:

```go
return nil, errors.New("queue: SQLite backend lands in Plan 3; use a Postgres DATABASE_URL or wait for the SQLite queue worker")
```

This makes the failure mode self-explanatory when a Plan 2A user runs SQLite mode.

- [ ] **Step 1: Make the edit.**
- [ ] **Step 2: Build / vet.**
- [ ] **Step 3: Commit:**

```bash
git add internal/queue/queue.go
git commit -m "$(cat <<'EOF'
chore(queue): refresh error message to point at Plan 3

The bridge from Plan 1 said "Plan 1" — Plan 2A's SQLite work is now
landed but the queue still needs Plan 3's homegrown worker.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 21: End-to-end SQLite smoke test

**Files:**
- (No edits.)

Verifies that a fresh SQLite DB can:
1. Apply migrations (squashed init + FTS5).
2. Boot the server (queue will fail to start, that's fine — it's gated by Plan 3).
3. Serve API requests for the read paths.
4. Insert + retrieve a book via the API and have FTS find it.

- [ ] **Step 1: Fresh SQLite DB**

```bash
rm -f /tmp/embookshelf-final.db
DATABASE_URL='sqlite:/tmp/embookshelf-final.db' go run ./cmd/migrate up
DATABASE_URL='sqlite:/tmp/embookshelf-final.db' go run ./cmd/migrate version
```

Expected: `ok` then `0 (dirty=false)` (the SQLite tree starts at 0 because the squashed init is `0000_init`).

- [ ] **Step 2: Seed minimum data via SQL** (since the queue can't run, seed by hand)

```bash
sqlite3 /tmp/embookshelf-final.db <<'SQL'
INSERT INTO users(id, email, name, password_hash, role, status)
VALUES ('u-admin', 'admin@local', 'Admin', '$2a$10$placeholderHASH', 'admin', 'approved');
INSERT INTO libraries(id, name, slug, path)
VALUES ('lib-1', 'My Library', 'my-library', '/tmp/books');
INSERT INTO books(id, library_id, title, author, series, description)
VALUES
  ('b-dune', 'lib-1', 'Dune', 'Frank Herbert', 'Dune', 'Sandworms and politics on Arrakis.'),
  ('b-found', 'lib-1', 'Foundation', 'Isaac Asimov', 'Foundation', 'Galactic empire fall.');
SELECT 'libraries:', COUNT(*) FROM libraries;
SELECT 'books:', COUNT(*) FROM books;
SELECT 'books_fts:', COUNT(*) FROM books_fts;
SQL
```

Expected: counts 1, 2, 2 (the FTS triggers populated `books_fts` on insert).

- [ ] **Step 3: Boot the server (it WILL fail at queue init)**

The current main.go calls `q, err := queue.New(ctx, dbh, …)`. `queue.New` returns a `*RiverClient` and exits the process on error. Under SQLite that always errors out. We need to change two things:

1. `queue.New`'s return type changes from `*RiverClient` to the existing `Client` interface — that lets us hand out a no-op implementation in SQLite mode without changing main.go's variable type.
2. `cmd/embookshelf/main.go` tolerates the SQLite-mode queue error by substituting a `queue.Noop{}`.

Both changes land in this task.

In `internal/queue/queue.go`, change the signature:

```go
// before
func New(ctx context.Context, d *db.DB, bdropSvc *service.BookDropService, libSvc *service.LibraryService) (*RiverClient, error)

// after
func New(ctx context.Context, d *db.DB, bdropSvc *service.BookDropService, libSvc *service.LibraryService) (Client, error)
```

The body returns the same `*RiverClient` value; Go's interface-satisfaction makes the type promotion automatic (`*RiverClient` already implements `Client`).

Add a `Noop` implementation in the same file:

```go
// Noop is a queue implementation that fails every enqueue. Used in
// SQLite mode until Plan 3 lands the homegrown worker. Stop is a
// no-op so deferred cleanup in main.go is safe.
type Noop struct{}

func (Noop) EnqueueBookDrop(ctx context.Context, itemID string) error {
    return errors.New("queue: bookdrop disabled on sqlite (Plan 3)")
}

func (Noop) EnqueueLibraryScan(ctx context.Context, libraryID string) error {
    return errors.New("queue: library scan disabled on sqlite (Plan 3)")
}

func (Noop) Stop(ctx context.Context) error { return nil }

// Compile-time interface conformance check.
var _ Client = Noop{}
```

In `cmd/embookshelf/main.go`, find the queue init and adjust:

```go
// before
q, err := queue.New(ctx, dbh, bdropSvc, libSvc)
if err != nil {
    slog.Error("queue", "err", err)
    os.Exit(1)
}

// after
q, err := queue.New(ctx, dbh, bdropSvc, libSvc)
if err != nil {
    if dbh.Dialect == db.DialectSQLite {
        slog.Warn("queue disabled on sqlite (Plan 3 introduces the SQLite worker)", "err", err)
        q = queue.Noop{}
    } else {
        slog.Error("queue", "err", err)
        os.Exit(1)
    }
}
```

`q`'s inferred type is now `queue.Client` (from the new `New` return type), so assigning `queue.Noop{}` compiles.

Build:

```bash
go build ./...
```

Expected: clean. (If you see "cannot use queue.Noop as *RiverClient", the `New` signature change in `queue.go` didn't take effect — re-read the file.)

- [ ] **Step 4: Re-run the smoke after the queue-tolerant change**

```bash
kill $PID 2>/dev/null; wait 2>/dev/null
DATABASE_URL='sqlite:/tmp/embookshelf-final.db' go build -o /tmp/embookshelf ./cmd/embookshelf
DATABASE_URL='sqlite:/tmp/embookshelf-final.db' /tmp/embookshelf 2>&1 | tee /tmp/server-final.log &
PID=$!
sleep 5
echo "--- API ---"
curl -s http://localhost:6060/api/libraries  -o /dev/null -w "libraries  HTTP %{http_code}\n"
curl -s http://localhost:6060/api/auth/me    -o /dev/null -w "auth/me   HTTP %{http_code}\n"
curl -s 'http://localhost:6060/api/library/lib-1/books' -o /tmp/books.json -w "books      HTTP %{http_code}\n"
echo "books returned:" ; jq 'length' /tmp/books.json 2>/dev/null
echo "--- Search ---"
curl -s 'http://localhost:6060/api/search/books?q=galactic' -o /tmp/search.json -w "search     HTTP %{http_code}\n"
echo "search hits:"  ; jq 'length' /tmp/search.json 2>/dev/null
kill $PID 2>/dev/null; wait 2>/dev/null
```

Expected:
- `libraries HTTP 200` with a 1-element array.
- `auth/me HTTP 401` (no session cookie — that's fine).
- `books HTTP 200` with a 2-element array.
- `search HTTP 200` with at least 1 hit (Foundation matches "galactic").

If FTS returns zero hits, inspect `books_fts` directly:

```bash
sqlite3 /tmp/embookshelf-final.db "SELECT rowid, title FROM books_fts WHERE books_fts MATCH 'galactic*';"
```

Expected: 1 row.

- [ ] **Step 5: Commit the queue-tolerant change**

```bash
git add cmd/embookshelf/main.go internal/queue/queue.go
git commit -m "$(cat <<'EOF'
feat(main): tolerate queue init failure in SQLite mode

Plan 2A ships a working SQLite backend for the API/reading paths.
The queue still requires Postgres (Plan 3 adds the SQLite worker).
Until then, log a warning and continue with a no-op queue when
running on SQLite, instead of exiting on boot.

queue.Noop is a tiny no-op implementation of queue.Client that returns
"disabled" errors on enqueue and is a no-op on Stop.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

- [ ] **Step 6: Confirm the PG path still works after this change**

```bash
DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' \
  go build -o /tmp/embookshelf ./cmd/embookshelf
DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' \
  /tmp/embookshelf &
PID=$!
sleep 5
curl -s http://localhost:6060/api/libraries -o /dev/null -w "PG libraries HTTP %{http_code}\n"
kill $PID 2>/dev/null; wait 2>/dev/null
```

Expected: HTTP 200. The PG path doesn't take the SQLite-tolerant branch.

---

## Self-Review

**1. Spec coverage:**

- §3 SQLite driver via modernc.org/sqlite ⇒ Tasks 1, 2.
- §3 pragmas (WAL, foreign_keys, busy_timeout, …) ⇒ Task 2 Step 1.
- §3 dberr SQLite branch ⇒ Task 3.
- §3 array adapters ⇒ Task 1.
- §4 squashed SQLite init ⇒ Task 5.
- §4 FTS5 virtual table + triggers ⇒ Task 6.
- §6b FTS5 query escaping ⇒ Task 7.
- §6b ranking via bm25 ⇒ Task 8 worked example 2.
- Per-repo dialect queries ⇒ Tasks 8–19.
- `db.NewID` (app-side UUID) ⇒ Task 1.
- Migrator SQLite driver ⇒ Task 4.
- Queue gating (until Plan 3) ⇒ Task 20 (message refresh) + Task 21 (Noop fallback).
- End-to-end smoke ⇒ Task 21.

**Deferred to Plan 2B (intentionally):**
- `repotest` test-matrix harness (running every existing PG test against SQLite too).
- `DATABASE_URL` default flip from PG to SQLite.
- Migration parity test for version 24+.
- Schema-equivalence test.
- `compose.sqlite.yml`.

**Deferred to Plan 3 (intentionally):**
- SQLite queue implementation (homegrown polling worker).
- Bookdrop ingest + library scans on SQLite.

**2. Placeholder scan:** None remaining. Task 5 explicitly tells the implementer to translate the remaining ~15 PG migrations following the table at the top of the task — that's a real instruction, not a placeholder, but it's the largest stretch in the plan and worth flagging at execution time.

**3. Type consistency:**
- `db.SelectQ(d, pg, sqlite) string` — defined in Task 1, used Tasks 8–19.
- `db.NewID() string` — defined in Task 1, used Tasks 8–19.
- `db.ScanStringSlice(d, src, dst) error` and `db.ValueStringSlice(d, slice) (any, error)` — defined in Task 1, used in Task 8 (worked example) and Tasks 9–19 by analogy.
- `dberr.IsUniqueViolation(err) (bool, string)` — extended in Task 3, callers (library, shelf, device) unchanged.
- `search.EscapeFTS5Query(in string) string` — defined in Task 7, used in Task 8.
- `*db.DB.dsn` — added in Task 2, consumed by `OpenMigrationDB` in Task 2.
- `queue.Noop{}` — defined in Task 21, satisfies the existing `queue.Client` interface (verified by `var _ Client = Noop{}` in the same task).

**4. Effort estimate:**

| Phase | Tasks | Effort |
|---|---|---|
| 0 — Foundation helpers | 1–4 | small (2–3 days) |
| 1 — Schema (squashed init + FTS5) | 5–6 | medium (3–4 days) — type translation is the long pole |
| 2 — Search escaping | 7 | small (½ day) |
| 3 — Per-repo dialect queries | 8–19 | medium-large (5–7 days) |
| 4 — Final wiring | 20–21 | small (½–1 day) |
| **Total** | **21 tasks** | **~2–3 weeks** |

---

## After Plan 2A

The merged outcome of Plan 2A:
- Same binary boots on `DATABASE_URL=postgres://…` or `DATABASE_URL=sqlite://./data/embookshelf.db`.
- Postgres remains the default (no env-var change).
- API + reading + search work on both backends.
- Bookdrop ingest and library scans **still require Postgres** until Plan 3.

Plan 2B (next, separate spec/plan):
- `repotest` harness so every existing PG repo test runs against SQLite too — this is the durable correctness story.
- Migration parity test enforcing `version-N` exists in both `migrations/postgres/` and `migrations/sqlite/` from #24 onward.
- Schema-equivalence test (table + column names match across both trees after migration).
- `DATABASE_URL` default flip to `sqlite://./data/embookshelf.db`.
- `compose.sqlite.yml` for testing/demoing the SQLite path.
- Release-please breaking-change footer + README/architecture.md updates.

Plan 3 (after Plan 2B):
- `Queue` interface in `internal/queue` (formalize the Client interface introduced piecemeal in Task 21).
- Homegrown SQLite polling worker (jobs table + 1-goroutine claim loop).
- Task functions extracted from River workers so both queue implementations call shared logic.
- Restart recovery (`UPDATE jobs SET state='pending' WHERE state='running'` on boot).
