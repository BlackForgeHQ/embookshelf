// SPDX-License-Identifier: AGPL-3.0-or-later

// Package db owns database connection setup and dialect identity for the
// app. It exposes a single `*DB` value that wraps a `database/sql.DB` for
// repo queries plus a `pgxpool.Pool` for the queue's River driver.
//
// Postgres is the only backend the server runs on (ADR-0023). SQLite is
// still recognized and openable, but for one caller only: `embookshelf
// import-sqlite`, which reads an old library and writes it into Postgres.
// `cmd/embookshelf` refuses a sqlite:// DSN before opening it, so nothing
// that serves requests reaches the SQLite path. Retire `openSQLite`,
// `sqliteDSN`, and the driver registration in sqlite_driver.go together
// with the importer.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
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
	dsn     string        // SQLite only; the resolved file path passed to sql.Open("sqlite", …)
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
	case strings.HasPrefix(low, "sqlite:"), strings.HasPrefix(low, "file:"):
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

// Open builds a *DB from configuration. Postgres is the server's only
// backend (ADR-0023); the SQLite branch serves `import-sqlite` alone and
// `cmd/embookshelf` refuses a sqlite:// DSN before reaching it.
func Open(ctx context.Context, cfg config.Config) (*DB, error) {
	d, err := DetectDialect(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	switch d {
	case DialectPostgres:
		return openPostgres(ctx, cfg)
	case DialectSQLite:
		return openSQLite(ctx, cfg)
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

	// stdlib.OpenDBFromPool wires pgx's decoding into the *sql.DB.
	// Timestamps arrive as time.Time and scan straight into a model field;
	// text[] does not — see db.TextArray for the one column type that
	// still needs an adapter.
	return &DB{
		SQL:     stdlib.OpenDBFromPool(pool),
		Dialect: DialectPostgres,
		PG:      pool,
	}, nil
}

func openSQLite(ctx context.Context, cfg config.Config) (*DB, error) {
	dsn, err := sqliteDSN(cfg.DatabaseURL, cfg.DataPath)
	if err != nil {
		return nil, err
	}

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}

	// Single writer to avoid SQLITE_BUSY storms. Plan 2A's spec records
	// the decision to revisit if read latency suffers; the simplest model
	// for a small/single-user install is one connection serializing both
	// reads and writes.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("sqlite ping: %w", err)
	}

	// Apply pragmas on the live connection. WAL gives concurrent reads
	// while a writer is active; foreign_keys=ON enforces the FK
	// constraints we declare in the squashed init.
	pragmas := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
		`PRAGMA foreign_keys=ON`,
		`PRAGMA busy_timeout=5000`,
		`PRAGMA temp_store=MEMORY`,
	}
	for _, p := range pragmas {
		if _, err := sqlDB.ExecContext(ctx, p); err != nil {
			_ = sqlDB.Close()
			return nil, fmt.Errorf("sqlite pragma %q: %w", p, err)
		}
	}

	return &DB{
		SQL:     sqlDB,
		Dialect: DialectSQLite,
		PG:      nil,
		dsn:     dsn,
	}, nil
}

// sqliteDSN converts the user-facing DATABASE_URL into a path the
// modernc.org/sqlite driver accepts. Accepted inputs:
//
//	sqlite:///absolute/path/to/file.db
//	sqlite://./relative/path.db
//	sqlite://relative.db
//	file:./relative.db
//	./relative.db   (or any bare path)
//
// All forms are equivalent; the driver opens the file (creating it if
// absent) using whatever the OS does with that path.
//
// When a data root is configured and the resolved path has a leading
// "./data/" prefix, that prefix is replaced with the root so that
// DATA_PATH=/var/lib/embookshelf causes the DB to land at
// /var/lib/embookshelf/embookshelf.db.
func sqliteDSN(url string, dataRoot config.DataRoot) (string, error) {
	var path string
	low := strings.ToLower(url)
	switch {
	case strings.HasPrefix(low, "sqlite://"):
		path = url[len("sqlite://"):]
	case strings.HasPrefix(low, "sqlite:"):
		// Strip the "sqlite:" scheme prefix, then strip an optional "//"
		// authority marker (RFC 3986 §3.2). What remains is the path.
		//
		//   sqlite:///abs/path.db  → strip "sqlite:"  → "//abs/path.db"  → strip "//" → "/abs/path.db"  (absolute)
		//   sqlite://./rel.db      → strip "sqlite:"  → "//./rel.db"     → strip "//" → "./rel.db"      (relative)
		//   sqlite:/abs/path.db    → strip "sqlite:"  → "/abs/path.db"   (no "//" to strip)             (absolute)
		//   sqlite:rel.db          → strip "sqlite:"  → "rel.db"                                        (relative)
		path = url[len("sqlite:"):]
		path = strings.TrimPrefix(path, "//")
	case strings.HasPrefix(low, "file:"):
		path = url[len("file:"):]
	default:
		path = url
	}

	// Resolve a leading "./data/" prefix against the data root. This lets
	// operators set DATA_PATH=/var/lib/foo and have the SQLite file
	// land at /var/lib/foo/embookshelf.db without rewriting the URL.
	// No root configured means nothing to resolve against, and the path
	// stays as written — the root itself says so, rather than this
	// function reading an empty string and deciding what it meant.
	const prefix = "./data/"
	if root, err := dataRoot.Path(); err == nil && strings.HasPrefix(path, prefix) {
		path = filepath.Join(root, strings.TrimPrefix(path, prefix))
	}
	return path, nil
}

// OpenMigrationDB returns a fresh *sql.DB that shares the underlying pool
// but is not the shared db.SQL handle. The caller (or the tool that receives
// it, e.g. golang-migrate's Postgres driver via m.Close()) is responsible for
// closing it. This prevents the migrator from closing the application-wide
// *sql.DB when it calls m.Close().
func (d *DB) OpenMigrationDB() (*sql.DB, error) {
	switch d.Dialect {
	case DialectPostgres:
		if d.PG == nil {
			return nil, errors.New("db: PG pool is nil")
		}
		return stdlib.OpenDBFromPool(d.PG), nil
	case DialectSQLite:
		// SQLite migrations run on the same single-writer connection.
		// golang-migrate's sqlite3 driver calls Close() on the *sql.DB at
		// m.Close() time; we hand it a fresh handle bound to the SAME file
		// so it doesn't tear down our shared db.SQL.
		return openSQLiteMigrationDB(d.dsn)
	default:
		return nil, fmt.Errorf("db: OpenMigrationDB not supported for dialect %q", d.Dialect)
	}
}

func openSQLiteMigrationDB(dsn string) (*sql.DB, error) {
	if dsn == "" {
		return nil, errors.New("sqlite migration db: empty dsn (db.Open did not store one)")
	}
	mig, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite migration open: %w", err)
	}
	mig.SetMaxOpenConns(1)
	pragmas := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA foreign_keys=ON`,
		`PRAGMA busy_timeout=5000`,
	}
	for _, p := range pragmas {
		if _, err := mig.Exec(p); err != nil {
			_ = mig.Close()
			return nil, fmt.Errorf("sqlite migration pragma %q: %w", p, err)
		}
	}
	return mig, nil
}

// SQLitePath returns the resolved database file path for a SQLite
// connection, or "" on Postgres. Exposed so the deprecation warning can
// name the exact file an operator should hand to `import-sqlite`
// (ADR-0023) instead of echoing back the raw DATABASE_URL.
func (db *DB) SQLitePath() string {
	if db == nil || db.Dialect != DialectSQLite {
		return ""
	}
	return db.dsn
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
