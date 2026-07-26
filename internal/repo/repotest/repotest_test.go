// SPDX-License-Identifier: AGPL-3.0-or-later

package repotest

import (
	"context"
	"testing"
)

// Each New(t) must land in its own schema, or tests would see each
// other's rows and pass or fail depending on ordering.
func TestNew_freshSchemaPerCall(t *testing.T) {
	ctx := context.Background()
	d1 := New(t)
	d2 := New(t)

	if _, err := d1.SQL.ExecContext(ctx,
		`INSERT INTO libraries (id, name, slug, path) VALUES ($1, $2, $3, $4)`,
		"aaaaaaaa-0001-4001-8001-000000000001", "A", "a", "/tmp/a"); err != nil {
		t.Fatalf("insert into first schema: %v", err)
	}

	var n int
	if err := d2.SQL.QueryRowContext(ctx, `SELECT count(*) FROM libraries`).Scan(&n); err != nil {
		t.Fatalf("count in second schema: %v", err)
	}
	if n != 0 {
		t.Fatalf("second schema saw %d libraries, want 0 — per-call isolation is broken", n)
	}
}

func TestNew_migrationsApplied(t *testing.T) {
	d := New(t)
	ctx := context.Background()

	// A late-migration column: proves the whole tree ran, not just the init.
	var exists bool
	if err := d.SQL.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'shelves' AND column_name = 'icon'
		)`).Scan(&exists); err != nil {
		t.Fatalf("inspect schema: %v", err)
	}
	if !exists {
		t.Fatal("shelves.icon missing — migrations did not fully apply")
	}
}

// The importer's source database is the one remaining SQLite path.
func TestNewSQLiteSource_migrationsApplied(t *testing.T) {
	d := NewSQLiteSource(t)
	if d.Dialect != "sqlite" {
		t.Fatalf("dialect = %q, want sqlite", d.Dialect)
	}
	var name string
	if err := d.SQL.QueryRowContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type='table' AND name='books_fts'`).Scan(&name); err != nil {
		t.Fatalf("books_fts not present after migration: %v", err)
	}
}
