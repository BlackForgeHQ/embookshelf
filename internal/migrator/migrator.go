// Package migrator wraps golang-migrate/migrate with the embedded migrations
// and the Postgres driver so the same code can run migrations from the CLI
// tool and from the server binary on boot.
package migrator

import (
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// New builds a *migrate.Migrate bound to the given migration FS subpath and
// Postgres pool. The caller owns the pool lifecycle; we open a sql.DB handle
// from it for the migrate driver and close it when m.Close() is called.
func New(files embed.FS, subpath string, pool *pgxpool.Pool) (*migrate.Migrate, error) {
	src, err := iofs.New(files, subpath)
	if err != nil {
		return nil, fmt.Errorf("migrate source: %w", err)
	}

	db := stdlib.OpenDBFromPool(pool)
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate postgres driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		_ = db.Close()
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
