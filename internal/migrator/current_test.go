// SPDX-License-Identifier: AGPL-3.0-or-later

package migrator_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/blackforge/embookshelf/internal/migrator"
)

// openTestDB connects to the Postgres named by TEST_DATABASE_URL. A
// missing variable is a hard failure rather than a skip, matching
// repotest: a silently skipped migration test is how a broken schema
// read reaches production.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal(`TEST_DATABASE_URL is not set — this test needs Postgres.

  export TEST_DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable'`)
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return sqlDB
}

func TestCurrentReportsRecordedVersion(t *testing.T) {
	ctx := context.Background()
	sqlDB := openTestDB(t)

	// A scratch schema keeps the assertion independent of whatever the
	// shared test database happens to be migrated to.
	if _, err := sqlDB.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS migrator_current_test`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS migrator_current_test CASCADE`)
	})
	if _, err := sqlDB.ExecContext(ctx, `SET search_path TO migrator_current_test`); err != nil {
		t.Fatalf("set search_path: %v", err)
	}

	// No table at all — the caller gets an error, not a silent zero.
	if _, _, err := migrator.Current(ctx, sqlDB); err == nil {
		t.Error("Current with no schema_migrations table returned nil error; want a read failure")
	}

	if _, err := sqlDB.ExecContext(ctx, `
		CREATE TABLE schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	// An empty table means "never migrated", which is a fact.
	v, dirty, err := migrator.Current(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Current on empty table: %v", err)
	}
	if v != 0 || dirty {
		t.Errorf("Current on empty table = (%d, %v), want (0, false)", v, dirty)
	}

	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, dirty) VALUES (38, true)`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	v, dirty, err = migrator.Current(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if v != 38 {
		t.Errorf("version = %d, want 38", v)
	}
	if !dirty {
		t.Error("dirty = false, want true — a dirty row must survive the read")
	}
}
