// Package db owns database connection setup and dialect identity for the
// app. It exposes a single `*DB` value that wraps a `database/sql.DB` for
// repo queries plus a `pgxpool.Pool` for the queue's River driver. The
// SQLite branch is wired in Plan 2; today this package only supports
// Postgres URLs but already returns a typed Dialect so callers can branch.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/blackforge/embookshelf/internal/config"
)

type Dialect string

const (
	DialectPostgres Dialect = "postgres"
	DialectSQLite   Dialect = "sqlite"
)

// DB is the single handle every repo and the migrator hold. The SQL field
// is the canonical entry point; PG is non-nil only when Dialect == DialectPostgres
// and is reserved for the queue layer's River driver.
type DB struct {
	SQL     *sql.DB
	Dialect Dialect
	PG      *pgxpool.Pool // nil when Dialect == DialectSQLite
}

// DetectDialect parses a DSN-or-path string and returns the dialect it
// describes. Schemes are matched case-insensitively. Bare paths (no
// scheme) are treated as SQLite filenames.
func DetectDialect(url string) (Dialect, error) {
	if url == "" {
		return "", errors.New("empty database URL")
	}
	low := strings.ToLower(url)
	switch {
	case strings.HasPrefix(low, "postgres://"), strings.HasPrefix(low, "postgresql://"):
		return DialectPostgres, nil
	case strings.HasPrefix(low, "sqlite://"), strings.HasPrefix(low, "file:"):
		return DialectSQLite, nil
	case strings.Contains(low, "://"):
		return "", fmt.Errorf("unsupported database URL scheme: %q", url)
	case strings.Contains(low, ":") && !strings.HasPrefix(low, "file:"):
		// Reject strings that look like a malformed/unknown scheme (e.g.
		// "redis:something") but lack "://". The explicit !HasPrefix("file:")
		// guard is belt-and-suspenders: file: is matched earlier and never
		// reaches here, but it prevents a future reordering from accidentally
		// classifying a file: URI as an unknown scheme.
		//
		// Note: Windows absolute paths (e.g. C:\data.db) also contain ":"
		// and would be rejected. The project documents SQLite paths using
		// forward-slash forms (./data/embookshelf.db, sqlite:///…, file:./…),
		// so Windows-style paths are not a supported input.
		return "", fmt.Errorf("unsupported database URL scheme: %q", url)
	default:
		// Bare path → SQLite
		return DialectSQLite, nil
	}
}

// Open builds a *DB from configuration. In this plan only DialectPostgres
// is implemented; SQLite returns an explicit error so anyone running on
// SQLite during Plan 1 fails loudly. Plan 2 adds the SQLite branch.
func Open(ctx context.Context, cfg config.Config) (*DB, error) {
	d, err := DetectDialect(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	switch d {
	case DialectPostgres:
		return openPostgres(ctx, cfg)
	case DialectSQLite:
		return nil, errors.New("sqlite backend not yet supported (Plan 2)")
	default:
		return nil, fmt.Errorf("unknown dialect: %q", d)
	}
}

func openPostgres(ctx context.Context, cfg config.Config) (*DB, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse pg config: %w", err)
	}
	poolCfg.MaxConns = cfg.DatabaseMaxConns
	poolCfg.MinConns = cfg.DatabaseMinConns

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("pgxpool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pg ping: %w", err)
	}

	return &DB{
		SQL:     stdlib.OpenDBFromPool(pool),
		Dialect: DialectPostgres,
		PG:      pool,
	}, nil
}

// Close releases all underlying handles. Safe to call multiple times.
func (db *DB) Close() error {
	if db == nil {
		return nil
	}
	var firstErr error
	if db.SQL != nil {
		if err := db.SQL.Close(); err != nil {
			firstErr = err
		}
	}
	if db.PG != nil {
		db.PG.Close()
	}
	return firstErr
}
