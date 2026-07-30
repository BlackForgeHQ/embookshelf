// SPDX-License-Identifier: AGPL-3.0-or-later

// Package repotest provides per-test database setup for repo-level
// integration tests. Call New(t) to receive a fully-migrated *db.DB.
//
// embookshelf is Postgres-only (ADR-0023), so there is no dialect to
// choose: each New(t) creates a uniquely-named schema in the database
// named by TEST_DATABASE_URL, sets search_path to it, and drops the
// schema on Cleanup.
//
// A missing TEST_DATABASE_URL is a hard failure rather than a skip. A
// skipped integration test is an unrun one, and three tests silently
// covering only SQLite is how the drift ADR-0023 describes went
// unnoticed. `make test` starts the compose.dev.yml service and sets the
// variable.
//
// NewSQLiteSource exists solely to build a source database for the
// `import-sqlite` tests and goes away with the importer.
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

// New returns a migrated Postgres *db.DB in its own schema.
func New(t *testing.T) *db.DB {
	t.Helper()
	return newPostgres(t)
}

// NewSQLiteSource returns a migrated SQLite database in a temp file.
//
// This is not a dialect option — it exists only so the `import-sqlite`
// tests have a realistic source to read, and it should be deleted along
// with the importer once the deprecation window closes. Production code
// must never open SQLite.
func NewSQLiteSource(t *testing.T) *db.DB {
	t.Helper()
	return newSQLite(t)
}

func newSQLite(t *testing.T) *db.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "repotest.db")
	cfg := config.Config{DatabaseURL: "sqlite:" + path}

	ctx := context.Background()
	// Names the ADR-0023 opt-in like the importer it builds fixtures for;
	// db.Open refuses SQLite to everyone else.
	d, err := db.Open(ctx, cfg, db.AllowSQLite())
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
		t.Fatal(`TEST_DATABASE_URL is not set — repo tests need Postgres.

Run "make test", which starts the compose.dev.yml service, or start one
yourself and export the DSN:

  docker compose -f compose.dev.yml up -d postgres
  export TEST_DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable'`)
	}

	suffix := randomHex(t, 8)
	schema := "repotest_" + suffix

	cfg := config.Config{
		DatabaseURL: dsn,
		// One connection per test database, not four. Every test in every
		// package now opens its own pool against the same server, and the
		// default max_connections of 100 is reachable when packages run in
		// parallel — the symptom is an intermittent "pg ping: context
		// deadline exceeded". SetMaxOpenConns(1) below already limits us
		// to one in-flight query, so a larger pool bought nothing.
		DatabaseMaxConns: 1,
		DatabaseMinConns: 0,
	}
	ctx := context.Background()
	d, err := db.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("repotest postgres open: %v", err)
	}

	// Single connection so SET search_path affects all subsequent queries.
	d.SQL.SetMaxOpenConns(1)

	t.Cleanup(func() { _ = d.Close() })

	ensurePgcrypto(t, d)

	if _, err := d.SQL.ExecContext(ctx, `CREATE SCHEMA `+quoteIdent(schema)); err != nil {
		t.Fatalf("repotest create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.SQL.ExecContext(context.Background(),
			`DROP SCHEMA `+quoteIdent(schema)+` CASCADE`)
	})

	if _, err := d.SQL.ExecContext(ctx, `SET search_path TO `+quoteIdent(schema)); err != nil {
		t.Fatalf("repotest set search_path: %v", err)
	}

	if err := applyMigrations(d); err != nil {
		t.Fatalf("repotest postgres migrate: %v", err)
	}
	return d
}

// ensurePgcrypto creates the extension once, under a lock, before any
// schema migrates.
//
// `CREATE EXTENSION IF NOT EXISTS` is not atomic against a concurrent
// twin: the existence check and the insert into pg_extension are
// separate, so two sessions running it at the same instant both pass the
// check and the loser gets "duplicate key value violates unique
// constraint pg_extension_name_index". Every repotest.New runs the
// migration set, and `go test ./...` runs packages in parallel, so a
// fresh database gets a burst of exactly that.
//
// It never reproduced locally because a development database has had the
// extension since its first migration, which makes the statement a
// no-op. CI builds a new Postgres per run, so it hit on the first
// enabled run and took six tests with it.
//
// An advisory lock rather than a retry: the contention is one statement
// long and only at the very start of a run, and a lock says what is
// actually true — this statement may not run twice at once.
func ensurePgcrypto(t *testing.T, d *db.DB) {
	t.Helper()
	ctx := context.Background()

	// Arbitrary but fixed: every repotest process must pick the same key
	// for the lock to mean anything.
	const lockKey = 4207180921

	if _, err := d.SQL.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		t.Fatalf("repotest advisory lock: %v", err)
	}
	defer func() {
		if _, err := d.SQL.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, lockKey); err != nil {
			t.Fatalf("repotest advisory unlock: %v", err)
		}
	}()

	// Into public deliberately, not the test's own schema. The extension
	// is database-wide — pg_extension is keyed by name alone — so letting
	// the first test schema own it means dropping that schema on cleanup
	// takes pgcrypto away from every schema still running.
	if _, err := d.SQL.ExecContext(ctx,
		`CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public`); err != nil {
		t.Fatalf("repotest create pgcrypto: %v", err)
	}
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

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return hex.EncodeToString(b)
}

func quoteIdent(s string) string {
	return `"` + s + `"`
}
