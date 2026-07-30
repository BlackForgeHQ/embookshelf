// SPDX-License-Identifier: AGPL-3.0-or-later

// Tiny CLI around internal/migrator. Usage:
//
//	go run ./cmd/migrate up
//	go run ./cmd/migrate down
//	go run ./cmd/migrate force <version>
//	go run ./cmd/migrate version
//
// DATABASE_URL env var (or -dsn flag) selects the target.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/joho/godotenv"

	"github.com/blackforge/embookshelf/internal/app"
	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/migrator"
)

func main() {
	_ = godotenv.Load()

	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "database URL (defaults to $DATABASE_URL)")
	flag.Parse()

	if *dsn == "" {
		fatal("no DSN: set DATABASE_URL or pass -dsn")
	}

	cmd := flag.Arg(0)
	if cmd == "" {
		cmd = "up"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var rest []string
	if flag.NArg() > 1 {
		rest = flag.Args()[1:]
	}

	if err := run(ctx, *dsn, cmd, rest, os.Stdout); err != nil {
		fatal("%v", err)
	}
}

// run is main's body with the process exit factored out, so the deferred
// close below actually runs on the failure paths — os.Exit inside the
// switch skipped every defer, which is how this binary managed to leak a
// pool on every error while looking like it closed one.
func run(ctx context.Context, dsn, cmd string, args []string, out io.Writer) error {
	cfg := config.Config{
		DatabaseURL:      dsn,
		DatabaseMaxConns: 2,
		DatabaseMinConns: 1,
	}
	// AllowSQLite (ADR-0023): this is not a serving process. The SQLite
	// migration tree survives for one purpose — bringing an old source
	// database forward so `embookshelf import-sqlite` can read it — and
	// this CLI is what applies it (see the migrations-sanity-sqlite-importer
	// CI job). Every other entry point takes the refusal.
	d, err := db.Open(ctx, cfg, db.AllowSQLite())
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}
	defer func() { _ = d.Close() }()

	switch cmd {
	case "up":
		// Through app.RunMigrations rather than migrator.New: the
		// dedicated-connection dance it performs is load bearing, and
		// this CLI used to hand the migrator the shared handle — which
		// golang-migrate's Postgres driver then closed at m.Close(),
		// leaving the deferred d.Close() above closing an already-closed
		// handle.
		if err := app.RunMigrations(d); err != nil {
			return fmt.Errorf("up: %w", err)
		}
		// The storage_v2 backfill is Postgres-only SQL (ADR-0023). A
		// SQLite target here is an importer source being brought forward
		// so `import-sqlite` can read it — schema only. Its rows land in
		// Postgres, which runs the backfill on first boot.
		if d.Dialect == db.DialectPostgres {
			if err := migrator.BackfillStorageV2(ctx, d); err != nil {
				return fmt.Errorf("storage_v2 backfill: %w", err)
			}
		}
		fmt.Fprintln(out, "ok")
		return nil

	case "down", "force", "version":
		return runMigratorCmd(d, cmd, args, out)

	default:
		return fmt.Errorf("unknown command: %q", cmd)
	}
}

// runMigratorCmd serves the three commands that have no boot-path
// equivalent: down, force and version are operator tools, and app exposes
// only the forward path. They still take a dedicated connection from
// OpenMigrationDB — m.Close() closes whatever *sql.DB it was handed, so
// the shared handle must never be that one.
func runMigratorCmd(d *db.DB, cmd string, args []string, out io.Writer) error {
	migDB, err := d.OpenMigrationDB()
	if err != nil {
		return fmt.Errorf("migration db: %w", err)
	}
	m, err := migrator.New(d.Dialect, migDB)
	if err != nil {
		// migDB not yet owned by the migrator — close it ourselves.
		_ = migDB.Close()
		return fmt.Errorf("migrator: %w", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			slog.Warn("migrate source close", "err", srcErr)
		}
		if dbErr != nil {
			slog.Warn("migrate db close", "err", dbErr)
		}
	}()

	switch cmd {
	case "down":
		if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return fmt.Errorf("down: %w", err)
		}
		fmt.Fprintln(out, "ok")
		return nil

	case "force":
		if len(args) < 1 {
			return errors.New("force requires a version argument")
		}
		v, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("force version: %w", err)
		}
		if err := m.Force(v); err != nil {
			return fmt.Errorf("force: %w", err)
		}
		fmt.Fprintf(out, "forced version %d\n", v)
		return nil

	default: // "version"
		v, dirty, err := m.Version()
		if errors.Is(err, migrate.ErrNilVersion) {
			fmt.Fprintln(out, "none")
			return nil
		}
		if err != nil {
			return fmt.Errorf("version: %w", err)
		}
		fmt.Fprintf(out, "%d (dirty=%t)\n", v, dirty)
		return nil
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
