// SPDX-License-Identifier: AGPL-3.0-or-later

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
// differ between Postgres and SQLite. Each entry is "table.column"
// for column-level allowances or just "table" for table-level.
//
// books.tsv: PG-only generated tsvector column. SQLite uses an FTS5
//
//	virtual table (books_fts) with no equivalent column on
//	books, by design.
//
// books_fts*: SQLite-only virtual tables produced by the FTS5 extension.
//
//	PG has none of these.
//
// book_reading_guides: added after ADR-0023 froze SQLite. Nothing creates
//
//	a SQLite database any more — the tree survives only so
//	`import-sqlite` can read an operator's old file — so a
//	post-freeze table has no SQLite counterpart by design.
//	Any further Postgres-only table belongs here too.
var allowedDivergence = map[string]bool{
	"books.tsv":         true,
	"books_fts":         true,
	"books_fts_data":    true,
	"books_fts_idx":     true,
	"books_fts_config":  true,
	"books_fts_content": true,
	"books_fts_docsize": true,
	"jobs":              true, // SQLite-only; PG uses River's river_job

	"book_reading_guides": true, // PG-only, post-freeze (ADR-0023, ADR-0024)
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

	pgTables = filterTables(pgTables, allowedDivergence)
	sqTables = filterTables(sqTables, allowedDivergence)

	if !sliceEqualStr(pgTables, sqTables) {
		t.Errorf("table-set mismatch:\n  pg=%v\n  sq=%v", pgTables, sqTables)
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

// loadSchemaPG opens the test PG instance, creates a throwaway schema,
// migrates into it, reads information_schema for tables + columns,
// then drops the schema.
//
// We deliberately use TWO separate *sql.DB handles:
//   - migDB: handed to the golang-migrate postgres driver, which takes
//     ownership and calls Close() on m.Close(). MaxOpenConns=1 is needed
//     by golang-migrate to keep the migration session alive.
//   - queryDB: a separate handle we use for information_schema queries
//     after the migrator is done. The schema name is embedded in the
//     DSN via the "options" parameter so every new connection inherits
//     the right search_path.
func loadSchemaPG(t *testing.T, dsn string) ([]string, map[string][]string) {
	t.Helper()
	ctx := context.Background()

	// --- schema lifecycle management ---
	adminDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("PG admin sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	if err := adminDB.PingContext(ctx); err != nil {
		t.Fatalf("PG admin ping: %v", err)
	}

	const schema = "repotest_schema_eq"
	if _, err := adminDB.ExecContext(ctx, `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
		t.Fatalf("PG drop pre-existing schema: %v", err)
	}
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatalf("PG create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminDB.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`)
	})

	// --- migration ---
	// A dedicated *sql.DB with MaxOpenConns=1 for the migrate driver.
	// The search_path is set before handing it off.
	migDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("PG migDB sql.Open: %v", err)
	}
	migDB.SetMaxOpenConns(1)
	if _, err := migDB.ExecContext(ctx, `SET search_path TO "`+schema+`"`); err != nil {
		_ = migDB.Close()
		t.Fatalf("PG migDB set search_path: %v", err)
	}

	m, err := New(dbpkg.DialectPostgres, migDB)
	if err != nil {
		_ = migDB.Close()
		t.Fatalf("PG migrator new: %v", err)
	}
	if err := Up(m); err != nil {
		_, _ = m.Close()
		t.Fatalf("PG migrate up: %v", err)
	}
	// Close the migrator (and migDB) before we open a query connection.
	if _, err := m.Close(); err != nil {
		t.Logf("PG migrator close warning: %v", err)
	}

	// --- schema inspection ---
	// A fresh connection for information_schema queries.
	queryDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("PG queryDB sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = queryDB.Close() })

	tables := queryStrings(t, queryDB,
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema = $1 AND table_type = 'BASE TABLE'
		   AND table_name != 'schema_migrations'
		 ORDER BY table_name`, schema)

	cols := map[string][]string{}
	for _, table := range tables {
		cols[table] = queryStrings(t, queryDB,
			`SELECT column_name FROM information_schema.columns
			 WHERE table_schema = $1 AND table_name = $2
			 ORDER BY column_name`, schema, table)
	}
	return tables, cols
}

// loadSchemaSQLite opens a tempfile DB, migrates into it, reads
// sqlite_master + pragma_table_info for the schema.
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
	if err := Up(m); err != nil {
		_, _ = m.Close()
		t.Fatalf("SQLite migrate up: %v", err)
	}
	// Close the migrator but NOT db — we still need db for schema queries.
	// For SQLite, m.Close() calls Close on the *sql.DB we passed in, so
	// we must not close it ourselves afterward and must open a fresh handle.
	if _, err := m.Close(); err != nil {
		t.Logf("SQLite migrator close warning: %v", err)
	}

	// Open a fresh read-only handle for schema inspection.
	queryDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("SQLite queryDB sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = queryDB.Close() })
	queryDB.SetMaxOpenConns(1)

	tables := queryStrings(t, queryDB,
		`SELECT name FROM sqlite_master
		 WHERE type='table' AND name NOT LIKE 'sqlite_%'
		   AND name != 'schema_migrations'
		 ORDER BY name`)

	cols := map[string][]string{}
	for _, table := range tables {
		cols[table] = queryStrings(t, queryDB,
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
		if !allow[t] {
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
