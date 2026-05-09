// SPDX-License-Identifier: AGPL-3.0-or-later

// Package repotest provides per-test database setup for repo-level
// integration tests. Call New(t) to receive a fully-migrated *db.DB
// pointed at an isolated database. The dialect is selected by the
// REPOTEST_DIALECT env var (default "sqlite").
//
//	SQLite: each call returns a fresh tempfile DB (full isolation).
//	Postgres: each call creates a uniquely-named schema in the
//	          DSN named by TEST_DATABASE_URL, sets search_path to it,
//	          and drops the schema on Cleanup.
//
// Use NewWithDialect when a single test wants to exercise both backends
// (e.g. a t.Run-per-dialect matrix test) without depending on the env var.
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

// New returns a *db.DB for the dialect named by REPOTEST_DIALECT
// (default "sqlite").
func New(t *testing.T) *db.DB {
	t.Helper()
	dialect := os.Getenv("REPOTEST_DIALECT")
	if dialect == "" {
		dialect = "sqlite"
	}
	return NewWithDialect(t, dialect)
}

// NewWithDialect returns a *db.DB for an explicit dialect, ignoring
// REPOTEST_DIALECT. Use this in matrix tests that iterate over both
// backends in a single go test invocation.
func NewWithDialect(t *testing.T, dialect string) *db.DB {
	t.Helper()
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

	// Single connection so SET search_path affects all subsequent queries.
	d.SQL.SetMaxOpenConns(1)

	t.Cleanup(func() { _ = d.Close() })

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
