// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runWithin runs the CLI body and fails if it does not return. The
// timeout is the assertion that matters: this binary used to hand the
// migrator the shared *sql.DB, which golang-migrate closes at m.Close()
// — the deferred handle close that followed then ran against an
// already-closed handle, and the pool it was meant to drain was the one
// the migrator still held.
func runWithin(t *testing.T, d time.Duration, dsn, cmd string, args []string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	done := make(chan error, 1)

	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	go func() { done <- run(ctx, dsn, cmd, args, &out) }()

	select {
	case err := <-done:
		return out.String(), err
	case <-time.After(d):
		t.Fatalf("run(%q) never returned — the database handle was closed twice or the pool never drained", cmd)
		return "", nil
	}
}

// TestRun_upAndVersion_live drives the documented migration entry point
// against Postgres and asserts both that it applied and that the process
// unwound cleanly.
func TestRun_upAndVersion_live(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL is not set — the migrate CLI has nothing to migrate")
	}

	if out, err := runWithin(t, 60*time.Second, dsn, "up", nil); err != nil {
		t.Fatalf("up: %v (output %q)", err, out)
	} else if out != "ok\n" {
		t.Fatalf("up printed %q, want %q", out, "ok\n")
	}

	// Re-running proves idempotence and, more to the point, that the
	// first run left the database usable rather than half-closed.
	out, err := runWithin(t, 60*time.Second, dsn, "version", nil)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(out, "dirty=false") {
		t.Fatalf("version printed %q, want a clean version line", out)
	}
}

// TestRun_upSQLiteSource_namesTheOptIn covers the one non-Postgres target
// this CLI still serves: an importer source being brought forward so
// `embookshelf import-sqlite` can read it (ADR-0023). It is also what the
// migrations-sanity-sqlite-importer CI job exercises — if the opt-in were
// dropped, that job would start failing on the shared refusal.
func TestRun_upSQLiteSource_namesTheOptIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.db")

	out, err := runWithin(t, 120*time.Second, "sqlite:"+path, "up", nil)
	if err != nil {
		t.Fatalf("up on a SQLite source: %v (output %q)", err, out)
	}
	if out != "ok\n" {
		t.Fatalf("up printed %q, want %q", out, "ok\n")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("source database not created: %v", err)
	}
}

func TestRun_unknownCommand(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL is not set")
	}
	_, err := runWithin(t, 30*time.Second, dsn, "sideways", nil)
	if err == nil || !strings.Contains(err.Error(), `unknown command: "sideways"`) {
		t.Fatalf("err = %v, want an unknown-command error", err)
	}
}

// down reverts one migration. It used to revert every one of them, while
// the Makefile target called itself "revert the most recent migration"
// and CI's own comment described a -all flag that did not exist — so
// anyone who believed either lost their whole schema.
//
// Driven against a SQLite source file rather than TEST_DATABASE_URL,
// deliberately: a test that reverts migrations must own a database it
// can destroy, and the shared one is not that.
func TestRun_downRevertsOneMigrationUnlessAskedForAll(t *testing.T) {
	dsn := "sqlite:" + filepath.Join(t.TempDir(), "source.db")

	if _, err := runWithin(t, 120*time.Second, dsn, "up", nil); err != nil {
		t.Fatalf("up: %v", err)
	}
	at := func() string {
		t.Helper()
		out, err := runWithin(t, 60*time.Second, dsn, "version", nil)
		if err != nil {
			t.Fatalf("version: %v", err)
		}
		return strings.TrimSpace(out)
	}

	top := at()
	if _, err := runWithin(t, 60*time.Second, dsn, "down", nil); err != nil {
		t.Fatalf("down: %v", err)
	}
	afterOne := at()
	if afterOne == top {
		t.Fatalf("down left the schema at %q — it reverted nothing", top)
	}
	if strings.HasPrefix(afterOne, "none") {
		t.Fatalf("down went all the way to zero from %q; it should revert one migration", top)
	}

	// Reverting past the first migration is the case that broke CI:
	// Steps(-1) below version 1 goes looking for a migration file that
	// cannot exist and fails, where the old Down() returned a no-change
	// sentinel the loop tolerated. A database with nothing applied is the
	// smallest way to reach it.
	fresh := "sqlite:" + filepath.Join(t.TempDir(), "empty.db")
	out, err := runWithin(t, 60*time.Second, fresh, "down", nil)
	if err != nil {
		t.Fatalf("down on a database with nothing applied: %v (output %q)", err, out)
	}
	if !strings.Contains(out, "no migration") {
		t.Fatalf("down on an empty database printed %q, want it to say there was nothing to revert", out)
	}

	// -all is deliberately not exercised here. It is the destructive
	// path, so it needs a database it can take to zero, and SQLite's own
	// down chain is broken at 000025 (#275) so it cannot reach zero
	// anyway. What matters for the bug this fixes is that the *default*
	// is one step, which is what the assertions above pin.
}
