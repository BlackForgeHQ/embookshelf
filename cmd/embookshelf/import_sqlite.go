// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/blackforge/embookshelf/internal/app"
	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/sqliteimport"
)

// importSQLiteCmd is the migration path off the retired SQLite backend
// (ADR-0023):
//
//	embookshelf import-sqlite --from ./data/embookshelf.db
//
// DATABASE_URL names the Postgres target, exactly as it does when
// serving, so an operator does not have to learn a second config
// mechanism to run this once.
func importSQLiteCmd(args []string) int {
	fs := flag.NewFlagSet("import-sqlite", flag.ContinueOnError)
	from := fs.String("from", "", "path to the SQLite database file to import")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: embookshelf import-sqlite --from <file.db>

Copies an existing SQLite library into the Postgres database named by
DATABASE_URL. The target must be empty; migrations are applied to it
first if needed.

`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *from == "" {
		fs.Usage()
		return 2
	}
	if _, err := os.Stat(*from); err != nil {
		fmt.Fprintf(os.Stderr, "cannot read %s: %v\n", *from, err)
		return 1
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}

	ctx := context.Background()

	target, err := openMigratedPostgres(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	defer func() { _ = target.Close() }()

	source, err := openMigratedSQLite(ctx, *from)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	defer func() { _ = source.Close() }()

	fmt.Printf("importing %s → %s\n", *from, redactDSN(cfg.DatabaseURL))

	rep, err := sqliteimport.Run(ctx, source.SQL, target.SQL)
	if err != nil {
		if errors.Is(err, sqliteimport.ErrTargetNotEmpty) {
			fmt.Fprintf(os.Stderr, `
refusing to import: %v

The target Postgres database already holds data. Importing on top would
interleave two libraries. Point DATABASE_URL at an empty database, or
drop and recreate this one, then re-run.
`, err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "import failed (target left unchanged): %v\n", err)
		return 1
	}

	printReport(rep)
	return 0
}

// openMigratedPostgres opens the DATABASE_URL target and brings it up to
// the current schema. Refuses a SQLite DSN — importing SQLite into
// SQLite is not the point.
func openMigratedPostgres(ctx context.Context, cfg config.Config) (*db.DB, error) {
	target, err := db.Open(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open target database: %w", err)
	}
	if target.Dialect != db.DialectPostgres {
		_ = target.Close()
		return nil, errors.New(
			"DATABASE_URL must point at Postgres for an import — set it to your postgres:// DSN")
	}
	if err := app.RunMigrations(target); err != nil {
		_ = target.Close()
		return nil, fmt.Errorf("migrate target: %w", err)
	}
	return target, nil
}

// openMigratedSQLite opens the source file and applies migrations to it.
// Migrating the source sounds odd but is deliberate: an install that has
// been sitting on an older release needs its schema brought forward
// before its rows can map onto the current Postgres schema.
func openMigratedSQLite(ctx context.Context, path string) (*db.DB, error) {
	source, err := db.Open(ctx, config.Config{DatabaseURL: "sqlite:" + path})
	if err != nil {
		return nil, fmt.Errorf("open source database: %w", err)
	}
	if err := app.RunMigrations(source); err != nil {
		_ = source.Close()
		return nil, fmt.Errorf("migrate source: %w", err)
	}
	return source, nil
}

func printReport(rep sqliteimport.Report) {
	tables := make([]string, 0, len(rep.Rows))
	for t := range rep.Rows {
		tables = append(tables, t)
	}
	sort.Strings(tables)

	for _, t := range tables {
		fmt.Printf("  %-22s %d\n", t, rep.Rows[t])
	}
	fmt.Printf("\nimported %d row(s) across %d table(s).\n", rep.Total(), len(rep.Rows))

	if len(rep.UnknownTables) > 0 {
		sort.Strings(rep.UnknownTables)
		fmt.Fprintf(os.Stderr, `
WARNING: %d table(s) in the source were NOT imported because this build
         does not recognise them: %s

         Their rows are still in the SQLite file, but they will not be
         in Postgres. This usually means the source came from a newer
         release than this binary. Check for a newer embookshelf before
         relying on the imported library.
`, len(rep.UnknownTables), strings.Join(rep.UnknownTables, ", "))
	}

	if n := rep.TotalOrphans(); n > 0 {
		tables := make([]string, 0, len(rep.Orphans))
		for t := range rep.Orphans {
			tables = append(tables, fmt.Sprintf("%s (%d)", t, rep.Orphans[t]))
		}
		sort.Strings(tables)
		fmt.Printf(`
warning: %d row(s) were left behind because they referenced records that
         no longer exist: %s
         SQLite does not enforce foreign keys by default, so an older
         database can accumulate these. Postgres rejects them. Nothing
         you can see in the app is affected.
`, n, strings.Join(tables, ", "))
	}

	if rep.SkippedJobs > 0 {
		fmt.Printf(`
note: %d queued background job(s) were not transferred — the SQLite
      queue and Postgres (River) do not share a table. Re-trigger a
      library scan from Settings, and re-upload anything still pending
      in BookDrop.
`, rep.SkippedJobs)
	}
	fmt.Printf("\nNext: set DATABASE_URL to this Postgres DSN and restart embookshelf.\n")
}

// redactDSN hides credentials so the printed line is safe to paste into
// a bug report.
func redactDSN(dsn string) string {
	at := strings.LastIndex(dsn, "@")
	if at < 0 {
		return dsn
	}
	scheme := 0
	if i := strings.Index(dsn, "://"); i >= 0 {
		scheme = i + 3
	}
	return dsn[:scheme] + "***@" + dsn[at+1:]
}
