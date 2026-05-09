// SPDX-License-Identifier: AGPL-3.0-or-later

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
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/blackforge/embookshelf/internal/db"
)

//go:embed all:migrations
var FS embed.FS

// New builds a *migrate.Migrate bound to the embedded migrations for the
// given dialect and the provided *sql.DB. Ownership of sqlDB transfers to
// the Postgres driver: m.Close() will call sqlDB.Close(). Callers must
// therefore pass a dedicated connection (db.DB.OpenMigrationDB) rather than
// the shared application *sql.DB.
func New(d db.Dialect, sqlDB *sql.DB) (*migrate.Migrate, error) {
	subpath, err := subpathFor(d)
	if err != nil {
		return nil, err
	}

	driver, dbName, err := driverFor(d, sqlDB)
	if err != nil {
		return nil, err
	}

	src, err := iofs.New(FS, subpath)
	if err != nil {
		return nil, fmt.Errorf("migrate source %q: %w", subpath, err)
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
		drv, err := sqlite.WithInstance(sqlDB, &sqlite.Config{})
		if err != nil {
			return nil, "", fmt.Errorf("migrate sqlite driver: %w", err)
		}
		return drv, "sqlite", nil
	default:
		return nil, "", fmt.Errorf("migrator: unknown dialect %q", d)
	}
}
