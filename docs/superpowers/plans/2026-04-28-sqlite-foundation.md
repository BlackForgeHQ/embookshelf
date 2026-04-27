# SQLite Foundation — Implementation Plan (Plan 1 of 4)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce a `*db.DB` abstraction that wraps `database/sql` (and exposes a `*pgxpool.Pool` for River), refactor all 14 repos and the migrator to use it, and replace pgx-specific error inspection with a dialect-agnostic helper. Postgres remains the only supported backend at the end of this plan; SQLite work is layered on in Plans 2–4.

**Architecture:** A new `internal/db` package owns connection setup and dialect identity. Repos hold `*db.DB` instead of `*pgxpool.Pool` and run queries via `db.SQL` (a `*sql.DB`). The PG path keeps `pgxpool.Pool` under the hood (exposed as `db.PG`) so River keeps its native driver. The migrator gains a `Dialect` parameter; the `cmd/migrate` CLI and `runAppMigrations` in `cmd/embookshelf/main.go` call into the new signature. Errors switch from `pgx.ErrNoRows` / `pgconn.PgError` inspection to `database/sql` equivalents, with a `dberr` helper for unique-violation detection.

**Tech Stack:** Go 1.25, `database/sql`, `github.com/jackc/pgx/v5/stdlib` (for `*sql.DB` from a pgxpool), `github.com/jackc/pgx/v5/pgxpool` (kept for River), `github.com/golang-migrate/migrate/v4`.

**Companion spec:** [`docs/superpowers/specs/2026-04-28-sqlite-support-design.md`](../specs/2026-04-28-sqlite-support-design.md). Sections 2–3 (architecture, data layer) cover the abstraction; this plan is the §3 "Phased internal migration" step 1, plus the prerequisite scaffolding.

**Out of scope for this plan:**
- Any SQLite code, driver, or migrations.
- The `Queue` interface split / SQLite worker (Plan 3).
- Dialect-tagged query strings (Plan 2 — only the abstraction lands here).
- Default `DATABASE_URL` flip (Plan 2).

---

## File Structure

### Files created

| Path | Responsibility |
|---|---|
| `internal/db/db.go` | `Dialect` enum, `DB` struct, `Open(ctx, cfg) (*DB, error)`. PG-only in this plan. |
| `internal/db/db_test.go` | Unit tests for `Open` (URL parsing, dialect detection). |
| `internal/db/dberr/dberr.go` | `IsUniqueViolation(err) bool` and `IsNotFound(err) bool` helpers. PG only in this plan; SQLite branch added in Plan 2. |
| `internal/db/dberr/dberr_test.go` | Unit tests against synthetic and real PG errors. |

### Files modified

| Path | Change |
|---|---|
| `internal/migrator/migrator.go` | Signature: `New(d db.Dialect, sqlDB *sql.DB) (*migrate.Migrate, error)`. Subpath derived from dialect. |
| `cmd/migrate/main.go` | Open `*db.DB` via `db.Open`; pass `db.Dialect` and `db.SQL` to migrator. |
| `cmd/embookshelf/main.go` | Replace `newPool` with `db.Open`; pass `*db.DB` to all repo constructors and to the queue. |
| `internal/queue/queue.go` | `New(ctx, *db.DB, …)`. Internally use `db.PG` (the `*pgxpool.Pool`) for River. |
| `internal/repo/library.go` | Constructor `NewLibraryRepo(*db.DB)`; struct field `db *db.DB`; swap `r.pool.*` calls to `r.db.SQL.*Context`; replace `pgx.Rows`/`pgx.Row` with `*sql.Rows`/`*sql.Row`; replace `pgx.ErrNoRows` with `sql.ErrNoRows`; replace the inline `pgconn.PgError` check at line 99 with `dberr.IsUniqueViolation` + a constraint-name read. |
| `internal/repo/shelf.go` | Same pattern as library.go. |
| `internal/repo/user.go` | Same pattern. |
| `internal/repo/session.go` | Same pattern. |
| `internal/repo/bookdrop.go` | Same pattern; convert `collectBookDrop(pgx.Rows)` → `collectBookDrop(*sql.Rows)`. |
| `internal/repo/progress.go` | Same pattern. |
| `internal/repo/annotation.go` | Same pattern; convert `collectAnnotations(pgx.Rows)` → `collectAnnotations(*sql.Rows)`. |
| `internal/repo/stats.go` | Same pattern. |
| `internal/repo/reading_session.go` | Same pattern. |
| `internal/repo/device.go` | Same pattern. |
| `internal/repo/app_settings.go` | Same pattern. |
| `internal/repo/provider_settings.go` | Same pattern. |
| `internal/repo/library.go` (scanners) | `scanner` interface remains unchanged (already abstract — `Scan(...) error` works for both `*sql.Row` and `pgx.Row`). Just update the parameter types of helpers like `collectBooks` from `pgx.Rows` to `*sql.Rows`. |

No tests under `internal/repo/` exist today; this plan does not add per-repo unit tests (Plan 2 introduces the dialect matrix and the `repotest` harness). The safety net in Plan 1 is `go build ./...`, `go vet ./...`, `make go-lint`, `make test` (which runs the existing handler/service/crypto/provider/pattern tests through the new abstraction), and a manual smoke run of `make up`.

---

## Phase 1 — `internal/db` Package Skeleton

### Task 1: Create the `db` package with the `Dialect` type

**Files:**
- Create: `internal/db/db.go`
- Create: `internal/db/db_test.go`

- [ ] **Step 1: Write the failing test for dialect detection**

Create `internal/db/db_test.go`:

```go
package db

import "testing"

func TestDetectDialect(t *testing.T) {
	cases := []struct {
		url  string
		want Dialect
		err  bool
	}{
		{"postgres://u:p@host/db", DialectPostgres, false},
		{"postgresql://u:p@host/db", DialectPostgres, false},
		{"sqlite:///var/lib/app.db", DialectSQLite, false},
		{"file:./data.db", DialectSQLite, false},
		{"./data.db", DialectSQLite, false},
		{"mysql://u:p@host/db", "", true},
		{"", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			got, err := DetectDialect(tc.url)
			if (err != nil) != tc.err {
				t.Fatalf("err=%v want_err=%v", err, tc.err)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/db/ -run TestDetectDialect -v`
Expected: FAIL — `package internal/db` doesn't compile (no `Dialect`, no `DetectDialect`).

- [ ] **Step 3: Implement the package skeleton**

Create `internal/db/db.go`:

```go
// Package db owns database connection setup and dialect identity for the
// app. It exposes a single `*DB` value that wraps a `database/sql.DB` for
// repo queries plus a `pgxpool.Pool` for the queue's River driver. The
// SQLite branch is wired in Plan 2; today this package only supports
// Postgres URLs but already returns a typed Dialect so callers can branch.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/blackforge/embookshelf/internal/config"
)

type Dialect string

const (
	DialectPostgres Dialect = "postgres"
	DialectSQLite   Dialect = "sqlite"
)

// DB is the single handle every repo and the migrator hold. The SQL field
// is the canonical entry point; PG is non-nil only when Dialect == DialectPostgres
// and is reserved for the queue layer's River driver.
type DB struct {
	SQL     *sql.DB
	Dialect Dialect
	PG      *pgxpool.Pool // nil when Dialect == DialectSQLite
}

// DetectDialect parses a DSN-or-path string and returns the dialect it
// describes. Schemes are matched case-insensitively. Bare paths (no
// scheme) are treated as SQLite filenames.
func DetectDialect(url string) (Dialect, error) {
	if url == "" {
		return "", errors.New("empty database URL")
	}
	low := strings.ToLower(url)
	switch {
	case strings.HasPrefix(low, "postgres://"), strings.HasPrefix(low, "postgresql://"):
		return DialectPostgres, nil
	case strings.HasPrefix(low, "sqlite://"), strings.HasPrefix(low, "file:"):
		return DialectSQLite, nil
	case strings.Contains(low, "://"):
		return "", fmt.Errorf("unsupported database URL scheme: %q", url)
	default:
		// Bare path → SQLite
		return DialectSQLite, nil
	}
}

// Open builds a *DB from configuration. In this plan only DialectPostgres
// is implemented; SQLite returns an explicit error so anyone running on
// SQLite during Plan 1 fails loudly. Plan 2 adds the SQLite branch.
func Open(ctx context.Context, cfg config.Config) (*DB, error) {
	d, err := DetectDialect(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	switch d {
	case DialectPostgres:
		return openPostgres(ctx, cfg)
	case DialectSQLite:
		return nil, errors.New("sqlite backend not yet supported (Plan 2)")
	default:
		return nil, fmt.Errorf("unknown dialect: %q", d)
	}
}

func openPostgres(ctx context.Context, cfg config.Config) (*DB, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse pg config: %w", err)
	}
	poolCfg.MaxConns = cfg.DatabaseMaxConns
	poolCfg.MinConns = cfg.DatabaseMinConns

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("pgxpool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pg ping: %w", err)
	}

	return &DB{
		SQL:     stdlib.OpenDBFromPool(pool),
		Dialect: DialectPostgres,
		PG:      pool,
	}, nil
}

// Close releases all underlying handles. Safe to call multiple times.
func (db *DB) Close() error {
	if db == nil {
		return nil
	}
	var firstErr error
	if db.SQL != nil {
		if err := db.SQL.Close(); err != nil {
			firstErr = err
		}
	}
	if db.PG != nil {
		db.PG.Close()
	}
	return firstErr
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/db/ -run TestDetectDialect -v`
Expected: PASS — all subcases green.

- [ ] **Step 5: Build the rest of the module to confirm no breakage**

Run: `go build ./...`
Expected: existing build succeeds (the new package is unused so far).

- [ ] **Step 6: Commit**

```bash
git add internal/db/db.go internal/db/db_test.go
git commit -m "feat(db): add dialect-aware DB abstraction (PG only)"
```

---

### Task 2: Test `Open` against a live Postgres

**Files:**
- Modify: `internal/db/db_test.go`

This task uses the existing dev Postgres (the same `make db-up` instance the project uses for `make test`). The test is gated by an env var so CI without PG can skip it.

- [ ] **Step 1: Add the test**

Append to `internal/db/db_test.go`:

```go
import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/config"
)

func TestOpenPostgres_live(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live PG test")
	}
	cfg := config.Config{
		DatabaseURL:      dsn,
		DatabaseMaxConns: 4,
		DatabaseMinConns: 1,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if db.Dialect != DialectPostgres {
		t.Fatalf("dialect=%q want postgres", db.Dialect)
	}
	if db.PG == nil {
		t.Fatal("PG handle nil for postgres dialect")
	}
	if db.SQL == nil {
		t.Fatal("SQL handle nil")
	}
	if err := db.SQL.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestOpenSQLite_notYetSupported(t *testing.T) {
	cfg := config.Config{DatabaseURL: "file:./does-not-matter.db"}
	if _, err := Open(context.Background(), cfg); err == nil {
		t.Fatal("expected error for sqlite dialect in Plan 1")
	}
}
```

Be careful with the import block — Go merges duplicates. If the file already imports `testing`, fold the new imports in.

- [ ] **Step 2: Run the tests**

```bash
make db-up
TEST_DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' \
  go test ./internal/db/ -v
```

Expected: both tests PASS. `TestDetectDialect` continues to pass.

- [ ] **Step 3: Commit**

```bash
git add internal/db/db_test.go
git commit -m "test(db): cover Open against live Postgres and SQLite-not-supported guard"
```

---

## Phase 2 — `dberr` Helper

### Task 3: Create `dberr.IsUniqueViolation` and `IsNotFound`

**Files:**
- Create: `internal/db/dberr/dberr.go`
- Create: `internal/db/dberr/dberr_test.go`

The current single user of `pgconn.PgError` is `internal/repo/library.go:99`, which checks `pgErr.Code == "23505"` and dispatches on `pgErr.ConstraintName` (`libraries_slug_key`, `libraries_path_key`). The helper preserves both pieces of information.

- [ ] **Step 1: Write the failing tests**

Create `internal/db/dberr/dberr_test.go`:

```go
package dberr

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsNotFound(t *testing.T) {
	if !IsNotFound(sql.ErrNoRows) {
		t.Fatal("sql.ErrNoRows should be NotFound")
	}
	if IsNotFound(errors.New("nope")) {
		t.Fatal("plain error should not be NotFound")
	}
	if IsNotFound(nil) {
		t.Fatal("nil should not be NotFound")
	}
}

func TestIsUniqueViolation_pg(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "libraries_slug_key"}
	ok, name := IsUniqueViolation(pgErr)
	if !ok {
		t.Fatal("23505 should be a unique violation")
	}
	if name != "libraries_slug_key" {
		t.Fatalf("constraint=%q want libraries_slug_key", name)
	}
}

func TestIsUniqueViolation_pg_otherCode(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23503"} // foreign key violation
	if ok, _ := IsUniqueViolation(pgErr); ok {
		t.Fatal("23503 should not be a unique violation")
	}
}

func TestIsUniqueViolation_nil(t *testing.T) {
	if ok, _ := IsUniqueViolation(nil); ok {
		t.Fatal("nil should not be a unique violation")
	}
}

func TestIsUniqueViolation_plain(t *testing.T) {
	if ok, _ := IsUniqueViolation(errors.New("nope")); ok {
		t.Fatal("plain error should not be a unique violation")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/db/dberr/ -v`
Expected: FAIL — package does not compile.

- [ ] **Step 3: Implement the helper**

Create `internal/db/dberr/dberr.go`:

```go
// Package dberr centralizes the error-inspection helpers that repos used
// to do inline against pgx-specific types. Today only the Postgres branch
// is implemented; the SQLite branch lands in Plan 2 alongside the SQLite
// driver wiring.
package dberr

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// IsNotFound reports whether err denotes "no rows" from any supported
// driver. Today that's database/sql; pgx-native errors are no longer
// returned to callers because repos use *sql.DB.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, sql.ErrNoRows)
}

// IsUniqueViolation reports whether err denotes a unique-constraint
// violation, and if so returns the violated constraint's name (or "" if
// the driver doesn't expose it). Callers use the constraint name to
// distinguish e.g. ErrLibraryNameTaken from ErrLibraryPathTaken.
func IsUniqueViolation(err error) (bool, string) {
	if err == nil {
		return false, ""
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return true, pgErr.ConstraintName
	}
	return false, ""
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/db/dberr/ -v`
Expected: all four tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/db/dberr/dberr.go internal/db/dberr/dberr_test.go
git commit -m "feat(db): add dberr helpers for not-found and unique-violation detection"
```

---

## Phase 3 — Migrator Dialect Parameter

### Task 4: Change `migrator.New` signature

**Files:**
- Modify: `internal/migrator/migrator.go`

The current signature ties the migrator to `pgxpool.Pool`. We change it to take `db.Dialect` + `*sql.DB`. The PG driver is selected from the dialect; subpath is derived. SQLite returns an error in this plan (the migration tree doesn't exist yet — that's Plan 2).

- [ ] **Step 1: Replace the implementation**

Open `internal/migrator/migrator.go` and replace its contents with:

```go
// Package migrator wraps golang-migrate/migrate with the embedded
// migrations and a dialect-aware driver. Callers (the migrate CLI and
// the server's runAppMigrations) supply a db.Dialect and *sql.DB; the
// migrator picks the right migration subpath and driver instance.
package migrator

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/blackforge/embookshelf/internal/db"
)

//go:embed all:migrations
var FS embed.FS

// New builds a *migrate.Migrate bound to the embedded migrations for the
// given dialect and the provided *sql.DB. The caller owns sqlDB's
// lifecycle. m.Close() does NOT close sqlDB — that's the caller's job
// (closing the *db.DB closes both).
func New(d db.Dialect, sqlDB *sql.DB) (*migrate.Migrate, error) {
	subpath, err := subpathFor(d)
	if err != nil {
		return nil, err
	}

	src, err := iofs.New(FS, subpath)
	if err != nil {
		return nil, fmt.Errorf("migrate source %q: %w", subpath, err)
	}

	driver, dbName, err := driverFor(d, sqlDB)
	if err != nil {
		return nil, err
	}

	m, err := migrate.NewWithInstance("iofs", src, dbName, driver)
	if err != nil {
		return nil, fmt.Errorf("migrate instance: %w", err)
	}
	return m, nil
}

// Up applies all pending migrations. migrate.ErrNoChange is not an error.
func Up(m *migrate.Migrate) error {
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

func subpathFor(d db.Dialect) (string, error) {
	switch d {
	case db.DialectPostgres:
		return "migrations/postgres", nil
	case db.DialectSQLite:
		return "migrations/sqlite", nil
	default:
		return "", fmt.Errorf("migrator: unknown dialect %q", d)
	}
}

func driverFor(d db.Dialect, sqlDB *sql.DB) (database.Driver, string, error) {
	switch d {
	case db.DialectPostgres:
		drv, err := postgres.WithInstance(sqlDB, &postgres.Config{})
		if err != nil {
			return nil, "", fmt.Errorf("migrate postgres driver: %w", err)
		}
		return drv, "postgres", nil
	case db.DialectSQLite:
		return nil, "", errors.New("sqlite migrator driver not yet supported (Plan 2)")
	default:
		return nil, "", fmt.Errorf("migrator: unknown dialect %q", d)
	}
}
```

The `database.Driver` interface is what `migrate.NewWithInstance` accepts; both the postgres driver (used today) and the sqlite3 driver (used in Plan 2) implement it. The previous `migrator.Subpath` constant is gone — subpath is derived from dialect inside the package.

- [ ] **Step 2: Move existing migrations into `postgres/` subdirectory**

```bash
mkdir -p internal/migrator/migrations/postgres
git mv internal/migrator/migrations/000001_init.up.sql       internal/migrator/migrations/postgres/
git mv internal/migrator/migrations/000001_init.down.sql     internal/migrator/migrations/postgres/
git mv internal/migrator/migrations/000002_book_details.up.sql   internal/migrator/migrations/postgres/
git mv internal/migrator/migrations/000002_book_details.down.sql internal/migrator/migrations/postgres/
# … repeat for all 23 migrations (000001 through 000023, both .up.sql and .down.sql)
```

To save typing, run this once-and-done shell loop (paste into your shell, not into the plan):

```bash
for i in $(seq -f "%06g" 1 23); do
  for f in internal/migrator/migrations/${i}_*.sql; do
    [ -f "$f" ] && git mv "$f" internal/migrator/migrations/postgres/
  done
done
```

- [ ] **Step 3: Verify the embed still picks them up**

Run: `go build ./internal/migrator/`
Expected: no error.

The `//go:embed all:migrations` directive in the new `migrator.go` walks the `migrations/` tree recursively, so the `postgres/` subdirectory is included automatically.

- [ ] **Step 4: Update `cmd/embookshelf/main.go` call site**

Open `cmd/embookshelf/main.go`. Find `runAppMigrations`:

```go
func runAppMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	m, err := migrator.New(migrator.FS, migrator.Subpath, pool)
```

Change the function to take `*db.DB` and call `migrator.New(d.Dialect, d.SQL)`:

```go
func runAppMigrations(ctx context.Context, d *db.DB) error {
	m, err := migrator.New(d.Dialect, d.SQL)
	if err != nil {
		return err
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			slog.Warn("migrate source close", "err", srcErr)
		}
		if dbErr != nil {
			slog.Warn("migrate db close", "err", dbErr)
		}
	}()
	v, dirty, _ := m.Version()
	if err := migrator.Up(m); err != nil {
		return err
	}
	newV, _, _ := m.Version()
	if newV != v {
		slog.Info("migrations applied", "from", v, "to", newV, "dirty", dirty)
	}
	_ = ctx
	return nil
}
```

Add `"github.com/blackforge/embookshelf/internal/db"` to the imports. The `migrator.FS` and `migrator.Subpath` symbols are no longer referenced from main.go (FS is private to migrator now; Subpath is removed entirely — derived from dialect). Remove `migrator.Subpath` from anywhere else it's referenced via `grep -rn "migrator.Subpath" .` and ensure none remain.

- [ ] **Step 5: Update `cmd/migrate/main.go`**

Replace the migrator call sites in `cmd/migrate/main.go`:

```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/joho/godotenv"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/migrator"
)

func main() {
	_ = godotenv.Load()

	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "database URL (defaults to $DATABASE_URL)")
	flag.Parse()

	if *dsn == "" {
		fatal("no DSN: set DATABASE_URL or pass -dsn")
	}

	cmd := flag.Arg(0)
	if cmd == "" {
		cmd = "up"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg := config.Config{
		DatabaseURL:      *dsn,
		DatabaseMaxConns: 2,
		DatabaseMinConns: 1,
	}
	d, err := db.Open(ctx, cfg)
	if err != nil {
		fatal("db open: %v", err)
	}
	defer d.Close()

	m, err := migrator.New(d.Dialect, d.SQL)
	if err != nil {
		fatal("migrator: %v", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			slog.Warn("migrate source close", "err", srcErr)
		}
		if dbErr != nil {
			slog.Warn("migrate db close", "err", dbErr)
		}
	}()

	switch cmd {
	case "up":
		if err := migrator.Up(m); err != nil {
			fatal("up: %v", err)
		}
		fmt.Println("ok")
	case "down":
		if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			fatal("down: %v", err)
		}
		fmt.Println("ok")
	case "force":
		if flag.NArg() < 2 {
			fatal("force requires a version argument")
		}
		v, err := strconv.Atoi(flag.Arg(1))
		if err != nil {
			fatal("force version: %v", err)
		}
		if err := m.Force(v); err != nil {
			fatal("force: %v", err)
		}
		fmt.Printf("forced version %d\n", v)
	case "version":
		v, dirty, err := m.Version()
		if errors.Is(err, migrate.ErrNilVersion) {
			fmt.Println("none")
			return
		}
		if err != nil {
			fatal("version: %v", err)
		}
		fmt.Printf("%d (dirty=%t)\n", v, dirty)
	default:
		fatal("unknown command: %q", cmd)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
```

The `pgxpool` import disappears — `cmd/migrate` no longer touches pgx directly.

- [ ] **Step 6: Build and run migrate to verify**

```bash
go build ./...
make db-up
DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' \
  go run ./cmd/migrate version
```

Expected: prints the current migration version (e.g. `23 (dirty=false)`). If the DB is fresh, `up` first.

- [ ] **Step 7: Commit**

```bash
git add internal/migrator/migrator.go internal/migrator/migrations/ cmd/migrate/main.go cmd/embookshelf/main.go
git commit -m "refactor(migrator): take dialect + *sql.DB, move PG migrations into postgres/"
```

---

## Phase 4 — Repo Refactor (14 repos)

Every repo gets the same mechanical transformation. Tasks 5–18 each cover **one repo file**. The pattern below is the canonical recipe; each task lists the file-specific deltas (line numbers, scanner helpers, error sites) and runs the same verification sequence at the end.

### The Canonical Repo Refactor Pattern

For every repo file, perform these substitutions:

| Old | New |
|---|---|
| `import "github.com/jackc/pgx/v5"` | `import "database/sql"` (drop pgx unless still needed for the `Tx` type — it isn't, after this change) |
| `import "github.com/jackc/pgx/v5/pgxpool"` | drop |
| `import "github.com/jackc/pgx/v5/pgconn"` | drop (replaced by `dberr`) |
| `pool *pgxpool.Pool` (struct field) | `db *db.DB` (rename and retype) |
| `func NewXRepo(pool *pgxpool.Pool) *XRepo { return &XRepo{pool: pool} }` | `func NewXRepo(d *db.DB) *XRepo { return &XRepo{db: d} }` |
| `r.pool.Query(ctx, …)` | `r.db.SQL.QueryContext(ctx, …)` |
| `r.pool.QueryRow(ctx, …)` | `r.db.SQL.QueryRowContext(ctx, …)` |
| `r.pool.Exec(ctx, …)` | `r.db.SQL.ExecContext(ctx, …)` |
| `r.pool.Begin(ctx)` | `r.db.SQL.BeginTx(ctx, nil)` |
| `tx.Query(ctx, …)`, `tx.QueryRow(ctx, …)`, `tx.Exec(ctx, …)` | `tx.QueryContext(ctx, …)`, `tx.QueryRowContext(ctx, …)`, `tx.ExecContext(ctx, …)` |
| `tx.Rollback(ctx)` | `tx.Rollback()` (sql.Tx takes no ctx) |
| `tx.Commit(ctx)` | `tx.Commit()` |
| `tag.RowsAffected() == 0` (pgx CommandTag) | `n, err := res.RowsAffected(); if err != nil { return …err… }; if n == 0 { … }` — `database/sql.Result.RowsAffected()` returns `(int64, error)`; never discard the error |
| `errors.Is(err, pgx.ErrNoRows)` | `dberr.IsNotFound(err)` |
| `var pgErr *pgconn.PgError; if errors.As(err, &pgErr) && pgErr.Code == "23505"` | `if ok, name := dberr.IsUniqueViolation(err); ok` (use `name` where `pgErr.ConstraintName` was used) |
| Type `pgx.Rows` (in helper signatures) | `*sql.Rows` |
| Type `pgx.Row` (rare) | `*sql.Row` |

After every repo refactor:

1. `go build ./...` must succeed.
2. `go vet ./...` must be clean.
3. `make test` must still pass (existing tests only — no new ones in Plan 1).

> **Notes on subtle differences:**
> - `database/sql.Tx.Rollback()` is **idempotent** — calling it after `Commit()` is a no-op. pgx's was the same. The `defer tx.Rollback()` pattern works identically.
> - `database/sql.Rows` exposes `Next()`, `Scan(...)`, `Err()`, `Close()` — same surface as `pgx.Rows`. Existing scanner helpers that take an interface (`type scanner interface { Scan(...) error }`) work unchanged for both `*sql.Row` and `*sql.Rows`.
> - `database/sql` does **not** support `[]string`/`[]int` Postgres array scanning natively; you need `github.com/lib/pq` or the pgx stdlib's `pgx.Array` adapter. Since `db.SQL` is built from `stdlib.OpenDBFromPool(pool)`, **pgx's array codec is registered**, so `&[]string{}` continues to scan from a TEXT[] column unchanged. Verify this on the first repo that uses arrays (likely `library.go` for genres/moods/tags) and document the result for later repos.

### Task 5: Refactor `internal/repo/library.go`

**Files:**
- Modify: `internal/repo/library.go`

This file has the most surface area (15 `r.pool.*` calls) and is the only site that uses `pgconn.PgError` directly (lines ~99–108). Worth doing first to validate the recipe.

- [ ] **Step 1: Apply the substitutions from the canonical pattern**

Replace imports:

```go
import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/db/dberr"
	"github.com/blackforge/embookshelf/internal/model"
)
```

(`pgx`, `pgconn`, `pgxpool` all drop.)

Struct + constructor:

```go
type LibraryRepo struct {
	db *db.DB
}

func NewLibraryRepo(d *db.DB) *LibraryRepo {
	return &LibraryRepo{db: d}
}
```

Sweep `r.pool.` → `r.db.SQL.…Context` per the canonical pattern.

Replace the unique-violation branch in `CreateLibrary` (current lines ~99–108):

```go
if err != nil {
	if ok, name := dberr.IsUniqueViolation(err); ok {
		switch name {
		case "libraries_slug_key":
			return model.Library{}, ErrLibraryNameTaken
		case "libraries_path_key":
			return model.Library{}, ErrLibraryPathTaken
		}
	}
	return model.Library{}, err
}
```

Replace `errors.Is(err, pgx.ErrNoRows)` at lines 145 and 376 with `dberr.IsNotFound(err)`.

Update `collectBooks(rows pgx.Rows)` → `collectBooks(rows *sql.Rows)` (line ~536).

The local `scanner` interface (line ~509) stays unchanged (it's already abstract over Scan).

- [ ] **Step 2: Build and verify arrays still scan**

Run: `go build ./...`
Expected: success.

Run a smoke check that array columns (genres/moods/tags) still scan from PG. Easiest path: bring the dev stack up and hit any endpoint that returns books.

```bash
make db-up
make seed
go run ./cmd/embookshelf &
SERVER_PID=$!
sleep 2
curl -s http://localhost:6060/api/libraries | head -50
kill $SERVER_PID
```

Expected: book records contain populated `genres`/`moods`/`tags` arrays in the JSON response. If they're empty/null where they should have values, pgx's array codec needs an explicit registration step — open an issue and stop the plan; a workaround is needed before continuing.

- [ ] **Step 3: Run existing tests**

Run: `make test`
Expected: PASS — no regressions in handler/service/auth tests that exercise libraries.

- [ ] **Step 4: Commit**

```bash
git add internal/repo/library.go
git commit -m "refactor(repo): library uses *db.DB and dberr"
```

---

### Tasks 6–18: Refactor remaining repos

Apply the canonical pattern to each of the following files, **one per task, one commit per task**. Each task is structurally identical to Task 5 — the substitutions are listed in the canonical pattern table above. Per-file deltas to watch for:

#### Task 6: `internal/repo/shelf.go`
- 12 `r.pool.*` calls.
- `errors.Is(err, pgx.ErrNoRows)` at lines 195 and 352 → `dberr.IsNotFound`.
- `ILIKE` SQL clauses on lines 488, 490, 541 stay as-is — Plan 2 deals with dialect-tagged queries; in Plan 1 we keep PG-only SQL.
- Run `make test` and commit `refactor(repo): shelf uses *db.DB`.

#### Task 7: `internal/repo/user.go`
- 17 `r.pool.*` calls.
- `errors.Is(err, pgx.ErrNoRows)` at line 229 → `dberr.IsNotFound`.
- Commit message: `refactor(repo): user uses *db.DB`.

#### Task 8: `internal/repo/session.go`
- 6 `r.pool.*` calls.
- `pgx.ErrNoRows` at line 46 → `dberr.IsNotFound`.
- Commit message: `refactor(repo): session uses *db.DB`.

#### Task 9: `internal/repo/bookdrop.go`
- 8 `r.pool.*` calls.
- `pgx.ErrNoRows` at line 141 → `dberr.IsNotFound`.
- `collectBookDrop(rows pgx.Rows)` at line 150 → `collectBookDrop(rows *sql.Rows)`.
- Inline `scanBookDrop(s scanner)` at line 130 stays — `scanner` is locally defined as an interface with `Scan(...) error`; both `*sql.Row` and `*sql.Rows` satisfy it.
- Commit message: `refactor(repo): bookdrop uses *db.DB`.

#### Task 10: `internal/repo/progress.go`
- 1 `r.pool.*` call.
- No `pgx.ErrNoRows` sites.
- Commit message: `refactor(repo): progress uses *db.DB`.

#### Task 11: `internal/repo/annotation.go`
- 6 `r.pool.*` calls.
- `pgx.ErrNoRows` at lines 131 and 161 → `dberr.IsNotFound`.
- `collectAnnotations(rows pgx.Rows)` at line 167 → `collectAnnotations(rows *sql.Rows)`.
- Commit message: `refactor(repo): annotation uses *db.DB`.

#### Task 12: `internal/repo/stats.go`
- 8 `r.pool.*` calls.
- No `pgx.ErrNoRows` sites.
- Commit message: `refactor(repo): stats uses *db.DB`.

#### Task 13: `internal/repo/reading_session.go`
- 6 `r.pool.*` calls.
- No `pgx.ErrNoRows` sites.
- Commit message: `refactor(repo): reading_session uses *db.DB`.

#### Task 14: `internal/repo/device.go`
- 6 `r.pool.*` calls.
- `pgx.ErrNoRows` at line 125 → `dberr.IsNotFound`.
- Commit message: `refactor(repo): device uses *db.DB`.

#### Task 15: `internal/repo/app_settings.go`
- 2 `r.pool.*` calls.
- `pgx.ErrNoRows` at line 130 → `dberr.IsNotFound`.
- Commit message: `refactor(repo): app_settings uses *db.DB`.

#### Task 16: `internal/repo/provider_settings.go`
- 9 `r.pool.*` calls.
- No `pgx.ErrNoRows` sites (verify via grep before committing).
- Commit message: `refactor(repo): provider_settings uses *db.DB`.

For each repo task above, the steps are:

- [ ] **Step 1: Apply substitutions per canonical pattern**
- [ ] **Step 2: Run `go build ./...`** — expect success
- [ ] **Step 3: Run `make test`** — expect existing tests pass
- [ ] **Step 4: Commit**

### Task 17: Final repo sweep — verify no `pool` field or pgx-error usage remains

**Files:**
- (Inspection only)

- [ ] **Step 1: Search for stragglers**

Run all four greps and confirm they return nothing under `internal/repo/`:

```bash
grep -rn "pgxpool\|pgx\.ErrNoRows\|pgconn\.PgError" internal/repo/ || echo "clean"
grep -rn "r\.pool\." internal/repo/                               || echo "clean"
```

Expected: both print "clean".

- [ ] **Step 2: Run the full test suite**

Run: `make ci-local` (or at minimum `make test && make go-lint && go vet ./...`).
Expected: all green.

- [ ] **Step 3: No commit needed if everything was clean.**

If a straggler is found, fix it in the appropriate repo file and commit `refactor(repo): cleanup straggler in <file>`.

---

## Phase 5 — Queue Refactor

### Task 18: `queue.New` takes `*db.DB`

**Files:**
- Modify: `internal/queue/queue.go`

River keeps `*pgxpool.Pool` as its driver — we expose it via `db.PG`.

- [ ] **Step 1: Update the function signature and body**

Change the `New` function in `internal/queue/queue.go`:

```go
import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/task"
)

// ...

func New(
	ctx context.Context,
	d *db.DB,
	bdropSvc *service.BookDropService,
	libSvc *service.LibraryService,
) (*RiverClient, error) {
	if d.Dialect != db.DialectPostgres {
		return nil, errors.New("queue: only Postgres backend supported in Plan 1")
	}
	if d.PG == nil {
		return nil, errors.New("queue: db.PG is nil for postgres dialect")
	}

	driver := riverpgxv5.New(d.PG)
	// rest of body unchanged — migrator, workers, river.NewClient, c.Start.
}
```

The `pgxpool` import drops here — the pool is reached through `d.PG`.

- [ ] **Step 2: Update the call site in `cmd/embookshelf/main.go`**

Find the existing call:

```go
q, err := queue.New(ctx, pool, bdropSvc, libSvc)
```

Change to:

```go
q, err := queue.New(ctx, dbh, bdropSvc, libSvc)
```

…where `dbh` is the `*db.DB` returned by `db.Open` (introduced in the next task).

- [ ] **Step 3: Build to verify**

Run: `go build ./...`
Expected: success (tied to Task 19's main.go updates — if Task 19 hasn't landed yet, expect a compile error here naming the unknown `dbh` symbol; that's resolved by Task 19, so finish Task 19 before testing).

- [ ] **Step 4: Commit**

```bash
git add internal/queue/queue.go
git commit -m "refactor(queue): take *db.DB, use db.PG for River"
```

---

## Phase 6 — `cmd/embookshelf/main.go` Wiring

### Task 19: Replace `newPool` with `db.Open` and thread `*db.DB` everywhere

**Files:**
- Modify: `cmd/embookshelf/main.go`

- [ ] **Step 1: Remove `newPool`, add `db.Open`**

Delete the `newPool` function at the bottom of the file. Replace the `pool` setup (around line 74) and the existing `runAppMigrations(ctx, pool)` call:

```go
// Old:
pool, err := newPool(ctx, cfg)
if err != nil {
	slog.Error("db connect", "err", err)
	os.Exit(1)
}
defer pool.Close()

if cfg.MigrateOnStart {
	if err := runAppMigrations(ctx, pool); err != nil {
		// ...
	}
}
```

becomes:

```go
dbh, err := db.Open(ctx, cfg)
if err != nil {
	slog.Error("db connect", "err", err)
	os.Exit(1)
}
defer dbh.Close()

if cfg.MigrateOnStart {
	if err := runAppMigrations(ctx, dbh); err != nil {
		slog.Error("migrate", "err", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Update every repo constructor call**

Every line of the form:

```go
libRepo := repo.NewLibraryRepo(pool)
```

becomes:

```go
libRepo := repo.NewLibraryRepo(dbh)
```

This applies to all 14 repo constructions (libRepo, shelfRepo, userRepo, sessionRepo, bdropRepo, progressRepo, annotationRepo, statsRepo, readingSessionRepo, deviceRepo, appSettingsRepo, providerSettingsRepo — note `providerSettingsRepo` is constructed mid-block).

- [ ] **Step 3: Update the queue construction**

```go
q, err := queue.New(ctx, dbh, bdropSvc, libSvc)
```

- [ ] **Step 4: Update imports**

Remove `"github.com/jackc/pgx/v5/pgxpool"` from main.go's import block. Add `"github.com/blackforge/embookshelf/internal/db"`.

- [ ] **Step 5: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 6: Run the full local CI**

```bash
make ci-local
```

Expected: lint, vet, tests all pass.

- [ ] **Step 7: Manual smoke test**

```bash
make db-up
make migrate
make seed
make up
```

In a second terminal:

```bash
curl -sf http://localhost:6060/api/libraries
curl -sf http://localhost:6060/api/health || true
```

Expected: API responds, the SPA loads at `http://localhost:5173`. Stop with Ctrl-C.

- [ ] **Step 8: Commit**

```bash
git add cmd/embookshelf/main.go
git commit -m "refactor(main): use db.Open and pass *db.DB to repos and queue"
```

---

## Phase 7 — Verification

### Task 20: End-to-end verification and import-graph check

**Files:**
- (Inspection / verification only.)

- [ ] **Step 1: Confirm pgx is reachable only from `internal/db`, `internal/queue`, and the migrator**

```bash
grep -rln "jackc/pgx" --include="*.go" .
```

Expected paths only:
- `internal/db/db.go`
- `internal/queue/queue.go`
- `go.sum` entries (irrelevant)

If any other Go file is listed (especially under `internal/repo/`, `internal/service/`, `internal/handler/`), that's a leak — open the file, route through `*db.DB` or `dberr`, and re-run.

- [ ] **Step 2: Verify migrations apply cleanly to a fresh DB**

```bash
make db-down 2>/dev/null || true
make db-up
DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' \
  go run ./cmd/migrate version
DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' \
  go run ./cmd/migrate up
DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' \
  go run ./cmd/migrate version
```

Expected: `none` → `ok` → `23 (dirty=false)`.

- [ ] **Step 3: Run the full Playwright e2e against the refactored backend**

```bash
make seed
make up &
sleep 5
make e2e
kill %1
```

Expected: Playwright suite green. (If e2e isn't installed, run `make e2e-install` first.)

- [ ] **Step 4: Confirm `make ci-local` passes one more time**

```bash
make ci-local
```

Expected: green.

- [ ] **Step 5: Tag the foundation milestone (no commit needed)**

This isn't a release — it's a marker for Plan 2 to start from:

```bash
git tag --annotate plan1-foundation -m "DB abstraction landed; SQLite work begins on top"
```

(Optional — skip if the team doesn't use local tags.)

---

## Self-Review

**Spec coverage (against `2026-04-28-sqlite-support-design.md`):**
- §2 Architecture decision 1 (`internal/db` package owns dialect/connection): Tasks 1–2.
- §2 Architecture decision 2 (repos take `*db.DB`): Tasks 5–17.
- §3 Phased internal migration step 1 (constructor refactor + working PG): Tasks 5–19.
- §3 Phased internal migration step 2 (drop pgx-specific types): Tasks 5–17 + Task 18 (queue keeps pgx for River, which §2 explicitly preserves).
- §3 Errors / `dberr` helper: Task 3 implements; Tasks 5–17 consume.
- §4 Migrator dialect parameter: Task 4.
- §4 PG migrations move to `migrations/postgres/`: Task 4 step 2.
- §5 Queue interface + SQLite worker: **deferred to Plan 3** (out-of-scope for Plan 1).
- §6 Configuration default flip: **deferred to Plan 2** (config still defaults to PG; SQLite fails loudly at `db.Open`).
- §7 Testing strategy (matrix runner): **deferred to Plan 2** (the `repotest` harness ships when SQLite arrives so it can serve both backends from day one).

**Placeholder scan:** None remaining. The `driverer` placeholder in Task 4 Step 1 is explicitly called out in the task as illustrative and corrected inline before commit.

**Type consistency:**
- `Dialect` defined in Task 1, used in Tasks 4, 18, 19, 20 — consistent name and type.
- `*db.DB` field name `db` used everywhere repos hold the value (Tasks 5–17).
- `dberr.IsUniqueViolation(err) (bool, string)` signature matches usage in Task 5.
- `dberr.IsNotFound(err) bool` signature matches usage in Tasks 5–17.
- `migrator.New(d db.Dialect, sqlDB *sql.DB)` signature defined in Task 4, called identically in Task 4 Step 4 and Task 4 Step 5.

**Next plans (sketched, not committed):**
- **Plan 2 — SQLite Migration Tree + Dialect Queries:** modernc.org/sqlite driver in `internal/db`, squashed `migrations/sqlite/0000_init.up.sql`, FTS5 schema, dialect-tagged query strings repo-by-repo, `repotest` harness for the test matrix, `DATABASE_URL` default flip.
- **Plan 3 — Queue Split:** `Queue` interface in `internal/queue`, task functions extracted from River workers, SQLite polling worker, restart recovery.
- **Plan 4 — CI, E2E, Docs, Compose:** GitHub Actions matrix lane, Playwright SQLite run, `compose.sqlite.yml`, README/PRD/architecture updates, release-please breaking-change footer.
