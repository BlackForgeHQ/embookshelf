package db

import (
	"context"
	"os"
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

func TestOpenSQLite_notYetSupported(t *testing.T) {
	cfg := config.Config{DatabaseURL: "file:./does-not-matter.db"}
	if _, err := Open(context.Background(), cfg); err == nil {
		t.Fatal("expected error for sqlite dialect in Plan 1")
	}
}
