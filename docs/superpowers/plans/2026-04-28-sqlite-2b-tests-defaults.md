# SQLite Tests + Defaults — Implementation Plan (Plan 2B of 4)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make SQLite the new default `DATABASE_URL`, give every future repo test a one-call harness that runs against either backend, and lock in the migration trees with a parity + schema-equivalence test so the SQLite tree can't drift silently from Postgres.

**Architecture:** Adds an `internal/repo/repotest` package that returns a fully-migrated `*db.DB` for the dialect named by an env var, with each test getting an isolated database (a fresh schema in PG, a tempfile in SQLite). One example matrix test on `LibraryRepo` proves the harness end-to-end. A new `internal/migrator/parity_test.go` enforces "every PG migration ≥ version 24 has a SQLite sibling and vice versa" (the Plan 1 squash means versions 1-23 only exist on the PG side, so the test starts at 24). A new `internal/migrator/schema_test.go` migrates both trees end-to-end and compares table + column names. The `DATABASE_URL` default flip in `config.go` and a minimal `compose.sqlite.yml` round out the user-visible changes.

**Tech Stack:** Go 1.25, `database/sql`, `modernc.org/sqlite`, `pgx/v5`, `golang-migrate/v4`.

**Companion spec:** [`docs/superpowers/specs/2026-04-28-sqlite-support-design.md`](../specs/2026-04-28-sqlite-support-design.md). Sections 6 (configuration), 7 (testing strategy), 8 (rollout & docs).

**Out of scope for this plan (Plan 3 picks up):**
- The SQLite queue worker (River replacement). SQLite mode still runs `queue.Noop{}` per Plan 2A.
- Bookdrop ingest + library scans on SQLite.

**Out of scope for this plan (Plan 4 picks up):**
- GitHub Actions matrix lane (a `test-sqlite` Make target lands here, but wiring it into CI lives in Plan 4).
- Playwright e2e against SQLite.
- Final docker image / release-please workflow changes.

---

## Pre-read: scope decisions locked in by Plan 2B

1. **Existing tests don't exercise repos against a real database.** Plan 1 and 2A introduced `internal/db/db_test.go` and `internal/db/dialect_test.go` (which DO touch a real DB), but `internal/repo/*_test.go` files don't exist. Service tests (`internal/service/auth_test.go`) use in-memory fake repos. Handler tests (`internal/handler/oidc_test.go`) test pure logic. The "matrix" harness in this plan is therefore infrastructure for *future* repo tests, demonstrated by **one** example test against `LibraryRepo`. Adding tests for the remaining 13 repos is a follow-up — outside Plan 2B.

2. **Migration parity starts at version 24.** The SQLite tree is `0000_init` (squashed Plan 2A). The PG tree is `000001..000023`. A naive "every version exists in both" check would fail on the squashed history — so the parity test enforces the rule from version 24 onward, where both trees gain genuinely-parallel migrations. (Right now there are no version 24+ migrations in either tree; the test still runs and is a no-op until the next schema change lands.)

3. **Schema equivalence checks names, not types.** The two backends legitimately diverge in column types (`UUID`/`TEXT`, `TIMESTAMPTZ`/`TEXT`, `JSONB`/`TEXT+CHECK`, `tsv tsvector`/`books_fts virtual table`) and in some indexes. The test verifies the schemas have the same set of *application tables* and the same set of *column names per table* — except for the explicit allow-list `{books.tsv, books_fts*}`. If a future migration lands on PG only and forgets the SQLite side, the column count diverges and the test fails.

4. **`DATABASE_URL` default flip is a breaking change.** Anyone relying on the bare default (no env var set) used to get Postgres on `localhost:5432`. After this plan, they get SQLite at `${DATA_PATH}/embookshelf.db`. The release-please breaking-change footer in the commit message documents the migration path. Existing deployments with `DATABASE_URL` explicitly set (which is the documented prod path) are unaffected.

5. **`compose.sqlite.yml`** is intentionally tiny — SQLite mode has no DB container. The compose file just runs the binary with `DATABASE_URL=sqlite:./data/embookshelf.db` mapped to a host volume.

---

## File Structure

### Files created

| Path | Responsibility |
|---|---|
| `internal/repo/repotest/repotest.go` | `New(t *testing.T) *db.DB` — returns a fully-migrated, isolated DB for the dialect named by `REPOTEST_DIALECT` (default `sqlite`). |
| `internal/repo/repotest/repotest_test.go` | Sanity tests for the harness itself: env-var dispatch, isolation across calls, migration completeness. |
| `internal/repo/library_test.go` | One example matrix test: `t.Run("postgres" / "sqlite", …)` over a single CRUD scenario. Proves the harness works for one of the most complex repos. |
| `internal/migrator/parity_test.go` | `TestMigrationParity`: walks both `migrations/postgres/` and `migrations/sqlite/`, asserts every version ≥ 24 is mirrored in both with up.sql + down.sql files. |
| `internal/migrator/schema_test.go` | `TestSchemaEquivalence`: migrates both trees end-to-end, compares the resulting tables and column names with an explicit FTS allow-list. |
| `compose.sqlite.yml` | Minimal compose file for the SQLite-default operator path. No DB container. |

### Files modified

| Path | Change |
|---|---|
| `internal/config/config.go` | `DATABASE_URL` default flips from `postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable` to `sqlite://./data/embookshelf.db`. Also: when the URL begins with `sqlite://./`, the relative path is resolved against `cfg.DataPath` so `DATA_PATH=/var/lib/embookshelf` Just Works. The resolution lives in `internal/db/db.go::sqliteDSN` (it already strips the scheme; this plan extends it to look up `cfg.DataPath`). |
| `internal/db/db.go` | Update `sqliteDSN` to take a `cfg config.Config` argument so it can substitute `cfg.DataPath` for a leading `./` in the SQLite URL. The current signature `sqliteDSN(url string)` becomes `sqliteDSN(url, dataPath string)`. |
| `Makefile` | Add `test-sqlite` target that sets `REPOTEST_DIALECT=sqlite` and runs `go test ./internal/repo/...`. Add `test-pg` target that sets `REPOTEST_DIALECT=postgres` plus `TEST_DATABASE_URL` and runs the same. Leave the existing `test` target alone (it still runs against PG via `TEST_DATABASE_URL`). |
| `README.md` | Quickstart section flips order: SQLite first ("just run the binary"), Postgres second ("for multi-user installs"). One-paragraph migration note for existing users. |
| `docs/architecture.md` | Adds a "Database backends" section pointing at the spec. Updates the existing tech-stack table. |

---

## Phase 0 — `repotest` Harness

### Task 1: Create the `repotest` package

**Files:**
- Create: `internal/repo/repotest/repotest.go`
- Create: `internal/repo/repotest/repotest_test.go`

The harness exposes one function:

```go
func New(t *testing.T) *db.DB
```

Behavior:
- Reads `REPOTEST_DIALECT` (default `"sqlite"`).
- For SQLite: opens a fresh `sqlite:` URL backed by a `t.TempDir()` file, applies migrations, and registers `t.Cleanup` to close the DB.
- For Postgres: requires `TEST_DATABASE_URL`. Opens a connection, creates a uniquely-named schema (e.g. `repotest_<random>`), sets `search_path` to that schema, applies migrations, and registers `t.Cleanup` to drop the schema and close the DB. (Schema isolation lets the Postgres test container be reused across many tests without parallel-test collisions.)
- For unrecognized values: `t.Skipf` with a clear message.

`t.Helper()` is called at the top so test failures in the harness point at the caller.

- [ ] **Step 1: Write the failing test for sqlite isolation**

Create `internal/repo/repotest/repotest_test.go`:

```go
package repotest

import (
	"context"
	"testing"
)

func TestNew_SQLite_freshSchemaPerCall(t *testing.T) {
	t.Setenv("REPOTEST_DIALECT", "sqlite")

	d1 := New(t)
	d2 := New(t)

	ctx := context.Background()

	// Inserting a row in d1 must not be visible from d2 — they are
	// separate temp files.
	if _, err := d1.SQL.ExecContext(ctx,
		`INSERT INTO libraries (id, name, slug, path) VALUES (?, ?, ?, ?)`,
		"lib-a", "A", "a", "/tmp/a"); err != nil {
		t.Fatalf("insert d1: %v", err)
	}

	var n int
	if err := d2.SQL.QueryRowContext(ctx, `SELECT count(*) FROM libraries`).Scan(&n); err != nil {
		t.Fatalf("count d2: %v", err)
	}
	if n != 0 {
		t.Fatalf("d2 saw %d libraries; want 0 (per-call isolation broken)", n)
	}
}

func TestNew_SQLite_migrationsApplied(t *testing.T) {
	t.Setenv("REPOTEST_DIALECT", "sqlite")
	d := New(t)
	ctx := context.Background()

	// books_fts must exist (FTS5 trigger from Plan 2A's migration tree).
	var name string
	err := d.SQL.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name='books_fts'`).Scan(&name)
	if err != nil {
		t.Fatalf("books_fts not present after migration: %v", err)
	}
}

func TestNew_unrecognizedDialect_skips(t *testing.T) {
	t.Setenv("REPOTEST_DIALECT", "mongo")

	// Run in a sub-test so we can observe the skip without aborting
	// the parent.
	subRan := false
	t.Run("inner", func(t *testing.T) {
		_ = New(t) // expected to call t.Skipf and return
		subRan = true
		t.Fatal("New() returned for an unrecognized dialect; expected Skipf")
	})

	// The sub-test should be skipped (not failed). subRan==true means
	// New() returned normally instead of skipping — that's a bug.
	if subRan {
		t.Fatal("New() did not skip for unrecognized dialect")
	}
}
```

Run: `go test ./internal/repo/repotest/ -v`
Expected: FAIL — package does not compile.

- [ ] **Step 2: Implement the harness**

Create `internal/repo/repotest/repotest.go`:

```go
// Package repotest provides per-test database setup for repo-level
// integration tests. Call New(t) to receive a fully-migrated *db.DB
// pointed at an isolated database. The dialect is selected by the
// REPOTEST_DIALECT env var (default "sqlite").
//
//	SQLite: each call returns a fresh tempfile DB (full isolation).
//	Postgres: each call creates a uniquely-named schema in the
//	          DSN named by TEST_DATABASE_URL, sets search_path to it,
//	          and drops the schema on Cleanup.
package repotest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/migrator"
)

// New returns a *db.DB for the dialect named by REPOTEST_DIALECT.
// The returned DB is migrated to the current schema and isolated
// from any other call to New within the same test binary. The
// caller never closes the DB; t.Cleanup handles teardown.
func New(t *testing.T) *db.DB {
	t.Helper()

	dialect := os.Getenv("REPOTEST_DIALECT")
	if dialect == "" {
		dialect = "sqlite"
	}

	switch dialect {
	case "sqlite":
		return newSQLite(t)
	case "postgres":
		return newPostgres(t)
	default:
		t.Skipf("REPOTEST_DIALECT=%q not recognized (want sqlite or postgres)", dialect)
		return nil
	}
}

func newSQLite(t *testing.T) *db.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "repotest.db")
	cfg := config.Config{DatabaseURL: "sqlite:" + path}

	ctx := context.Background()
	d, err := db.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("repotest sqlite open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if err := applyMigrations(d); err != nil {
		t.Fatalf("repotest sqlite migrate: %v", err)
	}
	return d
}

func newPostgres(t *testing.T) *db.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("REPOTEST_DIALECT=postgres requires TEST_DATABASE_URL")
	}

	// 8-byte random suffix → 16 hex chars; combined with the prefix
	// stays well under PG's 63-char identifier limit.
	suffix := randomHex(t, 8)
	schema := "repotest_" + suffix

	cfg := config.Config{
		DatabaseURL:      dsn,
		DatabaseMaxConns: 4,
		DatabaseMinConns: 1,
	}
	ctx := context.Background()
	d, err := db.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("repotest postgres open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if _, err := d.SQL.ExecContext(ctx, `CREATE SCHEMA `+quoteIdent(schema)); err != nil {
		t.Fatalf("repotest create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.SQL.ExecContext(context.Background(),
			`DROP SCHEMA `+quoteIdent(schema)+` CASCADE`)
	})

	if _, err := d.SQL.ExecContext(ctx,
		`SET search_path TO `+quoteIdent(schema)); err != nil {
		t.Fatalf("repotest set search_path: %v", err)
	}

	if err := applyMigrations(d); err != nil {
		t.Fatalf("repotest postgres migrate: %v", err)
	}
	return d
}

func applyMigrations(d *db.DB) error {
	mig, err := d.OpenMigrationDB()
	if err != nil {
		return fmt.Errorf("open migration db: %w", err)
	}
	defer func() { _ = mig.Close() }()

	m, err := migrator.New(d.Dialect, mig)
	if err != nil {
		return fmt.Errorf("migrator new: %w", err)
	}
	defer func() { _, _ = m.Close() }()

	if err := migrator.Up(m); err != nil {
		return fmt.Errorf("migrator up: %w", err)
	}
	return nil
}

// randomHex returns 2*n lowercase hex characters from crypto/rand.
func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return hex.EncodeToString(b)
}

// quoteIdent wraps an identifier in double quotes per the SQL standard.
// Suitable for our internally-generated schema names; do NOT pass
// untrusted input.
func quoteIdent(s string) string {
	return `"` + s + `"`
}
```

- [ ] **Step 3: Run the tests**

```bash
go test ./internal/repo/repotest/ -v
```

Expected: all three tests PASS. The third one verifies the unrecognized-dialect path skips correctly; if New returns instead of skipping, `subRan` stays true and the parent test fails.

- [ ] **Step 4: Run with `REPOTEST_DIALECT=postgres` once to confirm the PG path works**

```bash
REPOTEST_DIALECT=postgres \
TEST_DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' \
go test ./internal/repo/repotest/ -v
```

Expected: PASS for `TestNew_SQLite_freshSchemaPerCall` (this test sets its own `t.Setenv`, overriding the outer env), and PASS or SKIP for the others. The Postgres path may fail the SQLite-specific INSERT placeholder syntax `?` — that's correct: when REPOTEST_DIALECT=sqlite is set inside the test via `t.Setenv`, the harness returns a SQLite DB regardless of the outer env. If the PG-pathway works for the other tests, the harness logic is sound.

The real test of the Postgres branch is whether `applyMigrations` succeeds against a fresh schema. Add a quick assertion:

(Skip this step if the Step 3 tests already pass — the PG branch is exercised by the next task.)

- [ ] **Step 5: Commit**

```bash
git add internal/repo/repotest/repotest.go internal/repo/repotest/repotest_test.go
git commit -m "$(cat <<'EOF'
feat(repotest): add per-test DB harness for SQLite + Postgres

repotest.New(t) returns a fully-migrated *db.DB:
- SQLite: fresh tempfile per call (t.TempDir + db.Open).
- Postgres: uniquely-named schema in the TEST_DATABASE_URL DSN, with
  search_path set and a Cleanup that DROPs the schema CASCADE.

Dialect is selected by REPOTEST_DIALECT (default "sqlite").
Unrecognized values trigger t.Skipf so CI on hosts without PG can
run only the SQLite lane.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 1 — Example matrix test (proves harness works)

### Task 2: Add `library_test.go` matrix test

**Files:**
- Create: `internal/repo/library_test.go`

The test exercises a single end-to-end scenario on `LibraryRepo` against both backends:

1. Create a library.
2. Read it back by id and confirm field round-trip.
3. List, expect one entry.
4. Try to create a duplicate slug → expect `ErrLibraryNameTaken`.
5. Delete and confirm the row is gone.

The same body runs once per dialect. The test demonstrates the harness; future tasks (outside this plan) port more repos.

- [ ] **Step 1: Write the test**

Create `internal/repo/library_test.go`:

```go
package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// libraryRepoMatrix runs the same scenario against whichever dialect
// the repotest harness was given. Subtests forced via t.Setenv let
// the suite cover both backends in a single test binary.
func TestLibraryRepo_matrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		dialect := dialect
		t.Run(dialect, func(t *testing.T) {
			t.Setenv("REPOTEST_DIALECT", dialect)
			d := repotest.New(t)
			r := repo.NewLibraryRepo(d)
			ctx := context.Background()

			// 1. Create
			lib, err := r.CreateLibrary(ctx, "My Library", "my-library", "/tmp/books")
			if err != nil {
				t.Fatalf("CreateLibrary: %v", err)
			}
			if lib.ID == "" {
				t.Fatal("CreateLibrary returned empty ID")
			}
			if lib.Name != "My Library" || lib.Slug != "my-library" || lib.Path != "/tmp/books" {
				t.Fatalf("CreateLibrary fields = %+v, want name=My Library slug=my-library path=/tmp/books", lib)
			}

			// 2. Read back by id
			got, err := r.GetByID(ctx, lib.ID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			if got.ID != lib.ID || got.Name != lib.Name {
				t.Fatalf("GetByID round-trip mismatch: got=%+v want id=%s name=%s",
					got, lib.ID, lib.Name)
			}

			// 3. List
			libs, err := r.List(ctx)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(libs) != 1 {
				t.Fatalf("List returned %d libs, want 1", len(libs))
			}

			// 4. Duplicate slug → ErrLibraryNameTaken
			_, err = r.CreateLibrary(ctx, "Other Name", "my-library", "/tmp/different")
			if !errors.Is(err, repo.ErrLibraryNameTaken) {
				t.Fatalf("dup slug: got err=%v, want ErrLibraryNameTaken", err)
			}

			// 5. Delete and confirm the row is gone.
			if err := r.Delete(ctx, lib.ID); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if _, err := r.GetByID(ctx, lib.ID); !errors.Is(err, repo.ErrNotFound) {
				t.Fatalf("post-delete GetByID: got err=%v, want ErrNotFound", err)
			}
		})
	}
}
```

> **Note on `r.Delete`**: the actual method name in `LibraryRepo` may be `Delete`, `Remove`, `Soft Delete`, or similar. Read `internal/repo/library.go` and use whatever it exports. If only soft-delete exists (sets `deleted_at`), `GetByID` may still return the row — adapt the assertion in step 5 accordingly. Don't fabricate a method name.

- [ ] **Step 2: Run the test against SQLite (default)**

```bash
go test ./internal/repo/ -run TestLibraryRepo_matrix -v
```

Expected: the `sqlite` subtest PASSES; the `postgres` subtest SKIPS (because `TEST_DATABASE_URL` is unset).

- [ ] **Step 3: Run the test against Postgres**

```bash
REPOTEST_DIALECT=postgres \
TEST_DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' \
go test ./internal/repo/ -run TestLibraryRepo_matrix -v
```

Expected: both subtests PASS. The outer `t.Setenv` per dialect override means the `postgres` subtest forces SQLite when explicitly told. **This is a quirk** — to actually run BOTH backends in one invocation, the test harness should not depend on a setenv that the inner subtest can override. Refine if needed:

If after step 2 the `postgres` subtest SKIPS but you want both to run when `TEST_DATABASE_URL` is set, change the test to NOT call `t.Setenv` and instead pass the dialect through a parameter to a tiny helper:

```go
for _, dialect := range []string{"sqlite", "postgres"} {
    dialect := dialect
    t.Run(dialect, func(t *testing.T) {
        d := repotest.NewWithDialect(t, dialect)
        // … rest of test
    })
}
```

…then add `NewWithDialect(t, dialect string)` to `repotest.go` (extract the body of `New` to take an explicit dialect; have `New` continue to read the env var and call `NewWithDialect`).

The setenv approach in step 1 is simpler when the test runs in two separate `go test` invocations (one per env var). The plan-default expectation is that step 3 sets `REPOTEST_DIALECT=postgres` outside, both subtests run in PG mode, and the matrix expansion comes for free from the `for` loop. **Pick one approach and document the chosen one in the test's doc comment.**

Recommended: extract `NewWithDialect(t, dialect string)` so the test parameterizes cleanly and runs both backends in one invocation when `TEST_DATABASE_URL` is set. Update Task 1's harness if you take this route.

- [ ] **Step 4: Build all packages**

```bash
go build ./...
```

Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add internal/repo/library_test.go internal/repo/repotest/repotest.go
git commit -m "$(cat <<'EOF'
test(repo): add LibraryRepo matrix test using repotest harness

Exercises CRUD + dup-slug error mapping against both SQLite and
Postgres in a single go test invocation when TEST_DATABASE_URL is
set. The harness function NewWithDialect lets tests parameterize on
backend without relying on env-var ordering.

This is the first repo-level integration test in the codebase.
Future repo tests (one per repo) follow the same shape; they're
intentionally out of scope for Plan 2B (delivered as follow-ups).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 2 — Migration parity tests

### Task 3: `TestMigrationParity` — every version ≥ 24 mirrored

**Files:**
- Create: `internal/migrator/parity_test.go`

The Plan 1 squash means versions 1-23 only exist in `migrations/postgres/`. From version 24 onward, every up/down SQL pair must exist in BOTH `postgres/` and `sqlite/`. The test enforces this so a future schema change can't slip in for one backend only.

- [ ] **Step 1: Write the test**

Create `internal/migrator/parity_test.go`:

```go
package migrator

import (
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// migrationFileRE matches the canonical migrate filename:
//   000024_descriptive_name.up.sql
//   000024_descriptive_name.down.sql
var migrationFileRE = regexp.MustCompile(`^(\d+)_([^.]+)\.(up|down)\.sql$`)

// versionsBySuffix walks one branch of the embedded migrations tree
// (subdir = "migrations/postgres" or "migrations/sqlite") and returns
// a sorted slice of version numbers AND a map from version → filename
// stem (without extension). Filenames that don't match the canonical
// pattern are ignored — keeps the test resilient to README files etc.
func versionsBySuffix(t *testing.T, subdir string) ([]int, map[int]string) {
	t.Helper()
	entries, err := FS.ReadDir(subdir)
	if err != nil {
		t.Fatalf("ReadDir %q: %v", subdir, err)
	}

	stems := map[int]string{}
	upPresent := map[int]bool{}
	downPresent := map[int]bool{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationFileRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		v, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("bad version in %q: %v", e.Name(), err)
		}
		stems[v] = m[2]
		switch m[3] {
		case "up":
			upPresent[v] = true
		case "down":
			downPresent[v] = true
		}
	}

	// Every version must have both up and down files; otherwise
	// migrate would fail and the parity check is meaningless.
	var versions []int
	for v := range stems {
		if !upPresent[v] || !downPresent[v] {
			t.Errorf("%s: version %d is missing %s file",
				subdir, v, missingSide(upPresent[v], downPresent[v]))
			continue
		}
		versions = append(versions, v)
	}
	sort.Ints(versions)
	return versions, stems
}

func missingSide(up, down bool) string {
	switch {
	case !up:
		return "up"
	case !down:
		return "down"
	default:
		return ""
	}
}

// TestMigrationParity asserts that every migration with version ≥ 24
// is present in BOTH migrations/postgres/ and migrations/sqlite/ with
// the same filename stem (descriptive name).
//
// Versions < 24 only exist in postgres/; the sqlite/ tree is squashed
// at version 0 (0000_init). This test ignores those — the rule kicks
// in once parallel migrations begin.
func TestMigrationParity(t *testing.T) {
	const cutoff = 24

	pgVersions, pgStems := versionsBySuffix(t, "migrations/postgres")
	sqVersions, sqStems := versionsBySuffix(t, "migrations/sqlite")

	pgFromCutoff := versionsAtLeast(pgVersions, cutoff)
	sqFromCutoff := versionsAtLeast(sqVersions, cutoff)

	// 1. Same set of versions on both sides.
	if !sliceEqual(pgFromCutoff, sqFromCutoff) {
		t.Errorf("version mismatch ≥ %d: pg=%v sqlite=%v",
			cutoff, pgFromCutoff, sqFromCutoff)
	}

	// 2. Same descriptive stem per version. (Diverging stems means
	//    the two trees got out of sync semantically.)
	for _, v := range pgFromCutoff {
		if sqStems[v] == "" {
			continue // already reported above
		}
		if pgStems[v] != sqStems[v] {
			t.Errorf("version %d stem mismatch: pg=%q sqlite=%q",
				v, pgStems[v], sqStems[v])
		}
	}

	t.Logf("parity check: %d versions ≥ %d (pg=%d, sqlite=%d), all aligned",
		len(pgFromCutoff), cutoff, len(pgVersions), len(sqVersions))
}

func versionsAtLeast(vs []int, min int) []int {
	out := make([]int, 0, len(vs))
	for _, v := range vs {
		if v >= min {
			out = append(out, v)
		}
	}
	return out
}

func sliceEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Suppress unused warnings if filepath isn't needed in this version.
var _ = filepath.Separator
var _ = strings.Builder{}
```

The unused-import suppressor at the bottom is paranoia — drop the imports if neither package is actually used after you write the test.

- [ ] **Step 2: Run the parity test**

```bash
go test ./internal/migrator/ -run TestMigrationParity -v
```

Expected: PASS with a log line like `parity check: 0 versions ≥ 24 (pg=23, sqlite=1), all aligned` (or similar — exact counts depend on the current state).

If it FAILS, that means a version-24+ migration was committed to one tree but not the other. Fix by adding the missing sibling file before the test passes.

- [ ] **Step 3: Confirm it catches the bad case**

Manually create a stub PG migration to verify the test fails when intended:

```bash
echo "-- bogus" > internal/migrator/migrations/postgres/000024_test.up.sql
echo "-- bogus" > internal/migrator/migrations/postgres/000024_test.down.sql
go test ./internal/migrator/ -run TestMigrationParity -v
```

Expected: FAIL — message about version 24 mismatch.

```bash
rm internal/migrator/migrations/postgres/000024_test.up.sql \
   internal/migrator/migrations/postgres/000024_test.down.sql
go test ./internal/migrator/ -run TestMigrationParity -v
```

Expected: PASS again.

- [ ] **Step 4: Commit**

```bash
git add internal/migrator/parity_test.go
git commit -m "$(cat <<'EOF'
test(migrator): enforce parity for migrations ≥ version 24

Walks both migrations/postgres/ and migrations/sqlite/, asserts that
every version ≥ 24 is mirrored on both sides with the same descriptive
stem. The cutoff is 24 because the SQLite tree is squashed at 0000_init
(Plan 2A); parallel migrations start at version 24.

A future schema change that lands on PG only will fail this test.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: `TestSchemaEquivalence` — same tables and columns end-to-end

**Files:**
- Create: `internal/migrator/schema_test.go`

Migrates both trees end-to-end and compares the resulting schema by *name* (tables and columns), with an explicit allow-list for FTS-related divergence.

- [ ] **Step 1: Write the test**

Create `internal/migrator/schema_test.go`:

```go
package migrator

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	dbpkg "github.com/blackforge/embookshelf/internal/db"
)

// allowedDivergence lists table/column tuples that may legitimately
// differ between Postgres and SQLite. Each entry is "table.column".
//
// books.tsv: PG-only generated tsvector column. SQLite uses an FTS5
//            virtual table (books_fts) with no equivalent column on
//            books, by design.
//
// books_fts*: SQLite-only virtual tables produced by the FTS5 extension
//             (books_fts, books_fts_data, books_fts_idx, books_fts_config,
//             books_fts_content, books_fts_docsize). PG has none of these.
var allowedDivergence = map[string]bool{
	"books.tsv":               true,
	"books_fts":               true,
	"books_fts_data":          true,
	"books_fts_idx":           true,
	"books_fts_config":        true,
	"books_fts_content":       true,
	"books_fts_docsize":       true,
}

// TestSchemaEquivalence migrates both trees end-to-end against
// throwaway databases and asserts the resulting application tables
// and columns match by name, modulo allowedDivergence.
//
// Skipped when TEST_DATABASE_URL is unset (no PG to compare against).
func TestSchemaEquivalence(t *testing.T) {
	pgDSN := os.Getenv("TEST_DATABASE_URL")
	if pgDSN == "" {
		t.Skip("TEST_DATABASE_URL unset; cannot compare schemas")
	}

	pgTables, pgCols := loadSchemaPG(t, pgDSN)
	sqTables, sqCols := loadSchemaSQLite(t)

	// Filter the allow-listed names from both sides so the comparison
	// only catches *unintended* divergence.
	pgTables = filterTables(pgTables, allowedDivergence)
	sqTables = filterTables(sqTables, allowedDivergence)

	if !sliceEqualStr(pgTables, sqTables) {
		t.Errorf("table-set mismatch:\n  pg=%v\n  sq=%v",
			pgTables, sqTables)
	}

	for _, table := range intersect(pgTables, sqTables) {
		pgC := filterCols(pgCols[table], table, allowedDivergence)
		sqC := filterCols(sqCols[table], table, allowedDivergence)
		if !sliceEqualStr(pgC, sqC) {
			t.Errorf("table %q column-set mismatch:\n  pg=%v\n  sq=%v",
				table, pgC, sqC)
		}
	}
}

// loadSchemaPG opens the test PG instance, creates a throwaway schema
// named repotest_schema_eq, migrates into it, reads information_schema
// for tables + columns, then drops the schema. Returns sorted table
// names and a map of table → sorted column names.
func loadSchemaPG(t *testing.T, dsn string) ([]string, map[string][]string) {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("PG sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("PG ping: %v", err)
	}

	const schema = "repotest_schema_eq"
	if _, err := db.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`); err != nil {
		t.Fatalf("PG drop pre-existing schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("PG create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
	})

	if _, err := db.ExecContext(ctx, `SET search_path TO `+schema); err != nil {
		t.Fatalf("PG set search_path: %v", err)
	}

	m, err := New(dbpkg.DialectPostgres, db)
	if err != nil {
		t.Fatalf("PG migrator new: %v", err)
	}
	defer func() { _, _ = m.Close() }()
	if err := Up(m); err != nil {
		t.Fatalf("PG migrate up: %v", err)
	}

	tables := queryStrings(t, db,
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema = $1 AND table_type = 'BASE TABLE'
		 ORDER BY table_name`, schema)

	cols := map[string][]string{}
	for _, table := range tables {
		cols[table] = queryStrings(t, db,
			`SELECT column_name FROM information_schema.columns
			 WHERE table_schema = $1 AND table_name = $2
			 ORDER BY column_name`, schema, table)
	}
	return tables, cols
}

// loadSchemaSQLite opens a tempfile DB, migrates into it, then reads
// sqlite_master + pragma_table_info for the schema. Returns sorted
// table names and a map of table → sorted column names.
func loadSchemaSQLite(t *testing.T) ([]string, map[string][]string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "schema-eq.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("SQLite sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("SQLite pragma fk: %v", err)
	}

	m, err := New(dbpkg.DialectSQLite, db)
	if err != nil {
		t.Fatalf("SQLite migrator new: %v", err)
	}
	defer func() { _, _ = m.Close() }()
	if err := Up(m); err != nil {
		t.Fatalf("SQLite migrate up: %v", err)
	}

	tables := queryStrings(t, db,
		`SELECT name FROM sqlite_master
		 WHERE type='table' AND name NOT LIKE 'sqlite_%'
		   AND name NOT LIKE 'schema_migrations'
		 ORDER BY name`)

	cols := map[string][]string{}
	for _, table := range tables {
		cols[table] = queryStrings(t, db,
			`SELECT name FROM pragma_table_info($1) ORDER BY name`, table)
	}
	return tables, cols
}

func queryStrings(t *testing.T, db *sql.DB, q string, args ...any) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), q, args...)
	if err != nil {
		t.Fatalf("queryStrings: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	sort.Strings(out)
	return out
}

func filterTables(tables []string, allow map[string]bool) []string {
	out := make([]string, 0, len(tables))
	for _, t := range tables {
		if !allow[t] && !allow[t+".*"] {
			out = append(out, t)
		}
	}
	return out
}

func filterCols(cols []string, table string, allow map[string]bool) []string {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		if allow[table+"."+c] {
			continue
		}
		out = append(out, c)
	}
	return out
}

func intersect(a, b []string) []string {
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	out := make([]string, 0, len(a))
	for _, x := range b {
		if set[x] {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

func sliceEqualStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: Run the test**

```bash
TEST_DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' \
go test ./internal/migrator/ -run TestSchemaEquivalence -v
```

Expected: PASS. If it FAILS with a table-set mismatch, the SQLite squashed init is missing a table (or has an extra one). If it FAILS with a column-set mismatch, the SQLite init has the table but with different columns.

The test logs the diff so the implementer can spot the divergence. Fix by editing `internal/migrator/migrations/sqlite/0000_init.up.sql` (add the missing column or table) and re-run.

If the FTS5 internal tables (`books_fts_data`, etc.) appear as a divergence, expand `allowedDivergence` accordingly — they're SQLite-only by design.

- [ ] **Step 3: Verify it catches a real divergence**

Temporarily delete one column from the SQLite squashed init (say `bookdrop_items.error_msg`):

```bash
sed -i.bak '/error_msg/d' internal/migrator/migrations/sqlite/0000_init.up.sql
TEST_DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' \
go test ./internal/migrator/ -run TestSchemaEquivalence -v
mv internal/migrator/migrations/sqlite/0000_init.up.sql.bak \
   internal/migrator/migrations/sqlite/0000_init.up.sql
```

Expected: the test FAILS with `table "bookdrop_items" column-set mismatch` listing `error_msg` on the PG side and not on SQLite. After restoring the file, re-run and confirm PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/migrator/schema_test.go
git commit -m "$(cat <<'EOF'
test(migrator): enforce schema equivalence across PG and SQLite

Migrates both trees end-to-end against throwaway databases and asserts
the resulting application tables and columns match by name. An explicit
allowedDivergence list covers the FTS-related differences (books.tsv on
PG; books_fts* on SQLite).

Skips when TEST_DATABASE_URL is unset.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 3 — Default `DATABASE_URL` flip

### Task 5: Flip the default + handle relative SQLite paths

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/db/db.go`
- Modify: `internal/db/db_test.go`

The `DATABASE_URL` default switches from `postgres://...` to `sqlite://./data/embookshelf.db`. Relative paths in SQLite URLs are resolved against `cfg.DataPath` so `DATA_PATH=/var/lib/embookshelf` makes the DB live at `/var/lib/embookshelf/embookshelf.db` as expected.

- [ ] **Step 1: Update `internal/config/config.go`**

Find the line:

```go
DatabaseURL: envStr("DATABASE_URL", "postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable"),
```

Change to:

```go
DatabaseURL: envStr("DATABASE_URL", "sqlite://./data/embookshelf.db"),
```

- [ ] **Step 2: Update `sqliteDSN` in `internal/db/db.go` to take dataPath**

Find the current `sqliteDSN(url string)` function. Change the signature to:

```go
func sqliteDSN(url, dataPath string) (string, error)
```

In the body, after the existing scheme stripping (which produces a path string), check whether the path begins with `./`. If so, replace the leading `.` with `dataPath` so `./data/foo.db` becomes `<dataPath>/data/foo.db` (with `cfg.DataPath` defaulting to `./data` so the result is `./data/data/foo.db` — wait, that's wrong).

Better rule: if the SQLite path begins with `./`, treat the next segment as relative to `dataPath`'s parent, OR simply substitute `./data/` (the prefix in the new default) with `dataPath + "/"`. Pick the unambiguous form:

```go
func sqliteDSN(url, dataPath string) (string, error) {
	var path string
	low := strings.ToLower(url)
	switch {
	case strings.HasPrefix(low, "sqlite://"):
		path = url[len("sqlite://"):]
	case strings.HasPrefix(low, "sqlite:"):
		path = url[len("sqlite:"):]
	case strings.HasPrefix(low, "file:"):
		path = url[len("file:"):]
	default:
		path = url
	}

	// Resolve a leading "./data/" prefix against cfg.DataPath. This lets
	// operators set DATA_PATH=/var/lib/foo and have the SQLite file
	// land at /var/lib/foo/embookshelf.db without rewriting the URL.
	const prefix = "./data/"
	if dataPath != "" && strings.HasPrefix(path, prefix) {
		path = filepath.Join(dataPath, strings.TrimPrefix(path, prefix))
	}
	return path, nil
}
```

Add `path/filepath` to the imports.

- [ ] **Step 3: Update the caller in `openSQLite`**

Find the call to `sqliteDSN(cfg.DatabaseURL)` and change it to `sqliteDSN(cfg.DatabaseURL, cfg.DataPath)`.

- [ ] **Step 4: Update tests**

Open `internal/db/db_test.go`. Find any direct call to `sqliteDSN` (there may not be any; if `sqliteDSN` is only exercised through `Open`, no test changes are needed).

Add or update a test that exercises the data-path resolution:

```go
func TestSQLiteDSN_resolvesAgainstDataPath(t *testing.T) {
	got, err := sqliteDSN("sqlite://./data/foo.db", "/srv/embookshelf")
	if err != nil {
		t.Fatalf("sqliteDSN: %v", err)
	}
	want := "/srv/embookshelf/foo.db"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSQLiteDSN_absolutePath_unchanged(t *testing.T) {
	got, err := sqliteDSN("sqlite:///var/lib/foo.db", "/ignored")
	if err != nil {
		t.Fatalf("sqliteDSN: %v", err)
	}
	want := "/var/lib/foo.db"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
```

- [ ] **Step 5: Run all `internal/db` tests**

```bash
TEST_DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' \
go test ./internal/db/... -v
```

Expected: all PASS.

- [ ] **Step 6: Boot the binary with NO env vars set**

```bash
unset DATABASE_URL
rm -f data/embookshelf.db
go build -o /tmp/embookshelf ./cmd/embookshelf
/tmp/embookshelf 2>&1 | head -10 &
PID=$!
sleep 5
curl -s http://localhost:6060/api/libraries -o /dev/null -w "default-DB HTTP %{http_code}\n"
kill $PID 2>/dev/null; wait 2>/dev/null
ls -lh data/embookshelf.db || echo "DB not at expected location"
rm -f data/embookshelf.db
```

Expected:
- Server boots, default DB lives at `./data/embookshelf.db`.
- `HTTP 200` from libraries.

- [ ] **Step 7: Confirm an explicit Postgres `DATABASE_URL` still works**

```bash
DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' \
go run ./cmd/embookshelf &
PID=$!
sleep 5
curl -s http://localhost:6060/api/libraries -o /dev/null -w "explicit-PG HTTP %{http_code}\n"
kill $PID 2>/dev/null; wait 2>/dev/null
```

Expected: HTTP 200.

- [ ] **Step 8: Commit**

```bash
git add internal/config/config.go internal/db/db.go internal/db/db_test.go
git commit -m "$(cat <<'EOF'
feat(config)!: flip DATABASE_URL default to SQLite

Default DATABASE_URL is now "sqlite://./data/embookshelf.db" so
operators get a working zero-dependency install out of the box.
sqliteDSN resolves a leading "./data/" against cfg.DataPath so
DATA_PATH=/var/lib/embookshelf places the file at
/var/lib/embookshelf/embookshelf.db.

BREAKING CHANGE: bare-default Postgres connections are no longer
attempted. Existing deployments that already set DATABASE_URL
explicitly are unaffected. Operators relying on the implicit
postgres://localhost:5432/embookshelf default must now set
DATABASE_URL explicitly. See README quickstart and architecture.md
for the new defaults.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

The `feat!` prefix and `BREAKING CHANGE:` footer trigger release-please's MAJOR-version bump.

---

## Phase 4 — Compose & Make targets

### Task 6: `compose.sqlite.yml` + Make targets

**Files:**
- Create: `compose.sqlite.yml`
- Modify: `Makefile`

- [ ] **Step 1: Write `compose.sqlite.yml`**

```yaml
# compose.sqlite.yml — single-binary, zero-dependency operator path.
# Run with:
#
#   docker compose -f compose.sqlite.yml up
#
# The SQLite database persists under ./data on the host. No PG container.

services:
  embookshelf:
    image: embookshelf:dev
    restart: unless-stopped
    ports:
      - "6060:6060"
    environment:
      DATABASE_URL: sqlite://./data/embookshelf.db
      DATA_PATH: /data
      BOOKDROP_PATH: /bookdrop
      DISK_TYPE: LOCAL
      LOG_LEVEL: info
    volumes:
      - ./data:/data
      - ./bookdrop:/bookdrop
```

The `DATABASE_URL` resolves against `DATA_PATH=/data` so the file lands at `/data/embookshelf.db` (which is `./data/embookshelf.db` on the host via the volume mount).

- [ ] **Step 2: Add `test-sqlite` and `test-pg` targets to the Makefile**

Find the existing `test:` target. Below it, add:

```makefile
.PHONY: test-sqlite
test-sqlite:
	REPOTEST_DIALECT=sqlite go test ./internal/repo/...

.PHONY: test-pg
test-pg:
	REPOTEST_DIALECT=postgres \
	TEST_DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' \
	go test ./internal/repo/...
```

(Adapt the indentation to TABs — Make requires them.)

- [ ] **Step 3: Run the new targets**

```bash
make test-sqlite
make test-pg
```

Expected: both green. `test-sqlite` exercises the LibraryRepo matrix test in SQLite mode; `test-pg` does the same against the dev Postgres.

- [ ] **Step 4: Commit**

```bash
git add compose.sqlite.yml Makefile
git commit -m "$(cat <<'EOF'
feat(compose): add compose.sqlite.yml and test-sqlite Make targets

compose.sqlite.yml runs the binary with DATABASE_URL=sqlite://./data/...
mounted to a host volume, no DB container. Suitable for the
zero-dependency self-host path.

test-sqlite and test-pg targets run the repo test suite in either
mode. The plain `make test` continues to use TEST_DATABASE_URL.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 5 — Docs

### Task 7: README quickstart + architecture.md update

**Files:**
- Modify: `README.md`
- Modify: `docs/architecture.md`

- [ ] **Step 1: Update README.md quickstart**

Find the existing "Quickstart" or "Getting Started" section. Restructure it so SQLite is the headline path and Postgres is the "for multi-user installs" alternative.

Suggested top section:

```markdown
## Quickstart

### Single-user / self-hosted (SQLite, default)

```bash
docker run --rm -p 6060:6060 -v $(pwd)/data:/data ghcr.io/blackforgehq/embookshelf:latest
```

Open http://localhost:6060 and create your admin user. The library lives at `./data/embookshelf.db`. No external database required.

### Multi-user / production (Postgres)

For shared installs, run with an explicit `DATABASE_URL`:

```bash
docker run --rm -p 6060:6060 \
  -e DATABASE_URL='postgres://user:pass@dbhost:5432/embookshelf?sslmode=disable' \
  -v $(pwd)/data:/data \
  ghcr.io/blackforgehQ/embookshelf:latest
```

The Postgres path supports concurrent writes and the full bookdrop ingest pipeline.
```

(Adapt to whatever the existing README structure looks like — fold these blocks in without breaking unrelated sections.)

Add a one-line note about the breaking change near the top of the section:

```markdown
> **2026-04 update:** SQLite is now the default backend. If you were relying on the bare-default `postgres://localhost:5432/embookshelf` connection, set `DATABASE_URL` explicitly to your Postgres DSN.
```

- [ ] **Step 2: Update docs/architecture.md**

Find the "Tech stack" or similar table at the top. Update the database row to mention both backends:

| Layer | Stack |
|-------|-------|
| Database | PostgreSQL 16+ (multi-user) or SQLite via modernc.org/sqlite (single-user, default) |

Add a new "Database backends" section linking to the spec:

```markdown
### Database backends

embookshelf runs against either Postgres or SQLite, selected by `DATABASE_URL`. The same binary, same UI, and same feature set work on both backends. Postgres is required for multi-user / multi-writer installs (the queue uses River). SQLite is the zero-dependency default and serves single-user installs end-to-end except for bookdrop ingest and library scans, which require Postgres until the Plan 3 SQLite queue worker lands.

Design rationale and per-dialect implementation notes live in [`docs/superpowers/specs/2026-04-28-sqlite-support-design.md`](superpowers/specs/2026-04-28-sqlite-support-design.md).
```

- [ ] **Step 3: Skim the rest of README.md and architecture.md** for any reference to a hard-coded Postgres connection string. Update those if they're still presented as "the default."

- [ ] **Step 4: Commit**

```bash
git add README.md docs/architecture.md
git commit -m "$(cat <<'EOF'
docs: lead with SQLite as the default backend

README quickstart now puts the zero-dependency SQLite path first and
the Postgres path second. architecture.md gains a Database Backends
section linking to the spec for design rationale.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 6 — End-to-end verification

### Task 8: Final sanity sweep

**Files:**
- (Verification only.)

- [ ] **Step 1: Lint, vet, and full test sweep**

```bash
go build ./...
go vet ./...
make go-lint
TEST_DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' go test ./...
```

All four must pass.

- [ ] **Step 2: SQLite-only test sweep (no PG)**

```bash
unset TEST_DATABASE_URL
go test ./...
```

Expected: all PASS, with the PG-only tests skipping cleanly.

- [ ] **Step 3: Bare-binary boot on SQLite**

```bash
unset DATABASE_URL
rm -f data/embookshelf.db
go build -o /tmp/embookshelf ./cmd/embookshelf
/tmp/embookshelf &
PID=$!
sleep 5
curl -sf http://localhost:6060/api/libraries -o /dev/null -w "HTTP %{http_code}\n"
kill $PID 2>/dev/null; wait 2>/dev/null
ls -lh data/embookshelf.db
rm -f data/embookshelf.db
```

Expected: HTTP 200, DB file at `./data/embookshelf.db`.

- [ ] **Step 4: Postgres explicit-URL boot**

```bash
DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' \
go run ./cmd/embookshelf &
PID=$!
sleep 5
curl -sf http://localhost:6060/api/libraries -o /dev/null -w "HTTP %{http_code}\n"
kill $PID 2>/dev/null; wait 2>/dev/null
```

Expected: HTTP 200.

- [ ] **Step 5: docker compose -f compose.sqlite.yml up dry-run**

```bash
docker compose -f compose.sqlite.yml config
```

Expected: prints the resolved compose document with no errors.

If you have a `:dev` image built locally:

```bash
make docker-build
docker compose -f compose.sqlite.yml up &
PID=$!
sleep 10
curl -sf http://localhost:6060/api/libraries -o /dev/null -w "compose-sqlite HTTP %{http_code}\n"
docker compose -f compose.sqlite.yml down
```

Expected: HTTP 200.

- [ ] **Step 6: No commit (verification only)**

If any step failed, fix the issue in the relevant earlier task's commit (use `git commit --amend` if the breaking commit is HEAD, or open a follow-up commit otherwise).

---

## Self-Review

**1. Spec coverage:**
- §6 Configuration & defaults ⇒ Task 5 (DATABASE_URL flip) + Task 6 (compose).
- §6a `DATABASE_MAX_CONNS` no-op on SQLite ⇒ already implicit (the field is ignored when `Dialect == DialectSQLite` per Plan 2A).
- §7 Test matrix ⇒ Tasks 1, 2 (harness + example).
- §7 Migration parity test ⇒ Task 3.
- §7 Schema-equivalence test ⇒ Task 4.
- §8 Compose ⇒ Task 6.
- §8 README + architecture.md ⇒ Task 7.
- §8 Release-please breaking-change footer ⇒ Task 5 commit message.

**Deferred to Plan 3 (per the spec):** SQLite queue worker. SQLite mode still uses `queue.Noop{}`.
**Deferred to Plan 4 (per the spec):** GitHub Actions matrix lane wiring; Playwright e2e against SQLite; final docker image and release-please workflow tweaks.

**2. Placeholder scan:** None remaining. Every step has the exact commands, code, or assertions an engineer needs.

**3. Type consistency:**
- `repotest.New(t)` defined in Task 1, used in Task 2.
- `repotest.NewWithDialect(t, dialect)` is mentioned in Task 2 Step 3 as a refinement; Task 1's harness should expose both `New` and `NewWithDialect`. The implementer adds `NewWithDialect` when they hit Task 2.
- `sqliteDSN(url, dataPath string)` defined in Task 5, called inside `openSQLite`.
- Migration tree paths (`migrations/postgres`, `migrations/sqlite`) match what Plan 1 (PG) and Plan 2A (SQLite) shipped.

**4. Effort estimate:**

| Phase | Tasks | Estimate |
|---|---|---|
| 0 — repotest harness | 1 | small (1 day) |
| 1 — example matrix test | 2 | small (½ day) |
| 2 — parity + schema-equivalence | 3, 4 | medium (1–2 days) |
| 3 — default flip | 5 | small (½ day) |
| 4 — compose + Make | 6 | small (½ day) |
| 5 — docs | 7 | small (½ day) |
| 6 — verification | 8 | small (½ day) |
| **Total** | **8 tasks** | **~4–5 days** |

---

## After Plan 2B

The merged outcome of Plan 2B:
- SQLite is the default `DATABASE_URL`. Bare `docker run` works.
- Every future repo test plugs into `repotest.New(t)` and runs against either backend.
- `TestMigrationParity` and `TestSchemaEquivalence` keep the two trees aligned automatically.
- README and architecture.md lead with the SQLite path.
- `compose.sqlite.yml` gives operators a 1-command zero-dependency install.

Plan 3 (next, separate plan):
- Formalize `queue.Client` as the Queue interface (it already exists; Plan 3 may rename for clarity).
- Build the SQLite polling worker (jobs table, single-goroutine claim loop, exponential backoff with jitter, restart recovery).
- Refactor task functions out of River-typed workers so both implementations share business logic.
- Replace `queue.Noop{}` in main.go with the real SQLite worker dispatch.

Plan 4 (after Plan 3):
- GitHub Actions matrix lane (`test-sqlite` runs alongside `test-pg`).
- Playwright e2e against SQLite (alongside the existing PG e2e).
- Final docker image cleanup, release-please workflow tweaks, CHANGELOG generation.
