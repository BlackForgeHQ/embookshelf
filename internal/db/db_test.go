// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/config"
)

func TestDetectDialect(t *testing.T) {
	cases := []struct {
		url  string
		want Dialect
		err  bool
	}{
		{"postgres://u:p@host/db", DialectPostgres, false},
		{"postgresql://u:p@host/db", DialectPostgres, false},
		{"sqlite:///var/lib/app.db", DialectSQLite, false},
		{"file:./data.db", DialectSQLite, false},
		{"./data.db", DialectSQLite, false},
		{"mysql://u:p@host/db", "", true},
		{"", "", true},
		{"redis:something", "", true},
		{"mysql:user@host/db", "", true},
		{"foo:bar", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			got, err := DetectDialect(tc.url)
			if (err != nil) != tc.err {
				t.Fatalf("err=%v want_err=%v", err, tc.err)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

// dataRoot builds a configured root for the DSN tests. Absolute inputs
// only: the type resolves anything relative against the working
// directory, which would make these assertions machine-dependent.
func dataRoot(t *testing.T, p string) config.DataRoot {
	t.Helper()
	root, err := config.NewDataRoot(p)
	if err != nil {
		t.Fatalf("NewDataRoot(%q): %v", p, err)
	}
	return root
}

func TestSQLiteDSN_resolvesAgainstDataPath(t *testing.T) {
	got, err := sqliteDSN("sqlite://./data/foo.db", dataRoot(t, "/srv/embookshelf"))
	if err != nil {
		t.Fatalf("sqliteDSN: %v", err)
	}
	want := "/srv/embookshelf/foo.db"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSQLiteDSN_absolutePath_unchanged(t *testing.T) {
	got, err := sqliteDSN("sqlite:///var/lib/foo.db", dataRoot(t, "/ignored"))
	if err != nil {
		t.Fatalf("sqliteDSN: %v", err)
	}
	want := "/var/lib/foo.db"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSQLiteDSN_unsetDataRoot_noResolution(t *testing.T) {
	got, err := sqliteDSN("sqlite://./data/foo.db", config.DataRoot{})
	if err != nil {
		t.Fatalf("sqliteDSN: %v", err)
	}
	want := "./data/foo.db"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestOpen_refusesSQLiteWithoutOptIn pins the ADR-0023 invariant where it
// belongs: in the module that owns opening, not in one binary's main. A
// caller that has not named the opt-in gets a refusal, and — because the
// refusal reads the DSN rather than the handle — no database file is
// created on the way to it.
func TestOpen_refusesSQLiteWithoutOptIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "refused.db")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	d, err := Open(ctx, config.Config{DatabaseURL: "sqlite:" + path})
	if err == nil {
		_ = d.Close()
		t.Fatal("Open accepted a SQLite DSN without AllowSQLite")
	}
	if !errors.Is(err, ErrSQLiteUnsupported) {
		t.Fatalf("err = %v, want ErrSQLiteUnsupported", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("refusal created %s (stat err = %v); it must reject before opening", path, statErr)
	}
}

func TestOpenPostgres_live(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live PG test")
	}
	cfg := config.Config{
		DatabaseURL:      dsn,
		DatabaseMaxConns: 4,
		DatabaseMinConns: 1,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if db.Dialect != DialectPostgres {
		t.Fatalf("dialect=%q want postgres", db.Dialect)
	}
	if db.PG == nil {
		t.Fatal("PG handle nil for postgres dialect")
	}
	if db.SQL == nil {
		t.Fatal("SQL handle nil")
	}
	if err := db.SQL.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestOpenSQLite_live(t *testing.T) {
	dir := t.TempDir()
	dsn := "sqlite:" + dir + "/test.db"
	cfg := config.Config{DatabaseURL: dsn}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d, err := Open(ctx, cfg, AllowSQLite())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = d.Close() }()

	if d.Dialect != DialectSQLite {
		t.Fatalf("dialect=%q want sqlite", d.Dialect)
	}
	if d.PG != nil {
		t.Fatal("PG should be nil for sqlite dialect")
	}
	if d.SQL == nil {
		t.Fatal("SQL nil")
	}
	if err := d.SQL.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	// Verify pragmas took effect.
	var jm string
	if err := d.SQL.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&jm); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if jm != "wal" {
		t.Fatalf("journal_mode=%q want wal", jm)
	}

	var fk int
	if err := d.SQL.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys=%d want 1", fk)
	}

	// OpenMigrationDB must produce a working *sql.DB pointed at the same file.
	mig, err := d.OpenMigrationDB()
	if err != nil {
		t.Fatalf("OpenMigrationDB: %v", err)
	}
	defer func() { _ = mig.Close() }()
	if err := mig.PingContext(ctx); err != nil {
		t.Fatalf("migration ping: %v", err)
	}
}
