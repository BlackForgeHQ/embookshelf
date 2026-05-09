// SPDX-License-Identifier: AGPL-3.0-or-later

package repotest

import (
	"context"
	"os"
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
	subRan := false
	t.Run("inner", func(t *testing.T) {
		_ = New(t)
		subRan = true
		t.Fatal("New() returned for an unrecognized dialect; expected Skipf")
	})
	if subRan {
		t.Fatal("New() did not skip for unrecognized dialect")
	}
}

func TestNewWithDialect_overridesEnv(t *testing.T) {
	t.Setenv("REPOTEST_DIALECT", "mongo") // would skip if New were used
	d := NewWithDialect(t, "sqlite")
	if d == nil {
		t.Fatal("NewWithDialect returned nil for sqlite")
	}
	if d.Dialect != "sqlite" {
		t.Fatalf("dialect=%q want sqlite", d.Dialect)
	}
}

func TestNewPostgres_live(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	d := NewWithDialect(t, "postgres")
	ctx := context.Background()
	var n int
	if err := d.SQL.QueryRowContext(ctx, `SELECT count(*) FROM libraries`).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	// fresh schema → 0 libraries
	if n != 0 {
		t.Fatalf("got %d libraries, want 0", n)
	}
}
