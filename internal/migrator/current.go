// SPDX-License-Identifier: AGPL-3.0-or-later

package migrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Current reports the migration version the database records and whether
// the last attempt left it dirty.
//
// Read on demand rather than captured at boot: app.RunMigrations already
// reads the version and discards it, but that value is stale the moment
// MigrateOnStart is false, and absent entirely when an operator migrates
// out of band with the CLI.
//
// No rows means the table exists but nothing has been applied — a fresh
// database, which is a fact rather than a failure, so it reports version
// zero. A missing table is a genuine read failure and is returned as one.
func Current(ctx context.Context, sqlDB *sql.DB) (version int, dirty bool, err error) {
	row := sqlDB.QueryRowContext(ctx, `SELECT version, dirty FROM schema_migrations LIMIT 1`)
	if err := row.Scan(&version, &dirty); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("read schema_migrations: %w", err)
	}
	return version, dirty, nil
}
