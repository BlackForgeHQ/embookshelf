// SPDX-License-Identifier: AGPL-3.0-or-later

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/jobs"
)

// buildForTest wires a real App against TEST_DATABASE_URL and hands it
// back unstarted. Nothing here serves HTTP or calls Start — the point of
// the split is that construction can be inspected on its own.
//
// A missing TEST_DATABASE_URL fails rather than skips, for the reason
// repotest gives: a skipped integration test is an unrun one.
func buildForTest(t *testing.T) *App {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal(`TEST_DATABASE_URL is not set — the boot test needs Postgres.

Run "make test", which starts the compose.dev.yml service, or start one
yourself and export the DSN:

  docker compose -f compose.dev.yml up -d postgres
  export TEST_DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable'`)
	}

	dataPath := t.TempDir()
	dataRoot, err := config.NewDataRoot(dataPath)
	if err != nil {
		t.Fatalf("NewDataRoot(%q): %v", dataPath, err)
	}
	cfg := config.Config{
		DatabaseURL:      dsn,
		DatabaseMaxConns: 4,
		DatabaseMinConns: 0,
		DataPath:         dataRoot,
		BookDropPath:     filepath.Join(dataPath, "bookdrop"),
		BookDropInterval: time.Second,
		MigrateOnStart:   true,
		PresignTTL:       10 * time.Minute,
		AllowedOrigins:   []string{"*"},
	}

	a, err := Build(context.Background(), cfg, "test", "test")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() {
		if err := a.Close(context.Background()); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return a
}

// TestBuildWiresEverySeam is the check that used to require booting the
// process: a seam Build forgets to assign is a nil field, and the first
// request that touches it panics. Asserting on the built value catches
// that here instead.
func TestBuildWiresEverySeam(t *testing.T) {
	a := buildForTest(t)

	v := reflect.ValueOf(a).Elem()
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		if isNilValue(v.Field(i)) {
			t.Errorf("App.%s is nil after Build", name)
		}
	}
}

// TestBuildWiresHandlerDependencies walks the Handler the composition
// root produced. Its required groups are positional, so a missing one is
// a compile error — but Options are not, and a nil there degrades
// silently at the use site. Every one of them is supplied by a full
// build, so all of them are asserted.
func TestBuildWiresHandlerDependencies(t *testing.T) {
	a := buildForTest(t)

	v := reflect.ValueOf(a.handler).Elem()
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		if isNilValue(v.Field(i)) {
			t.Errorf("Handler.%s is nil after Build", name)
		}
	}
}

// TestBuildLeavesTheQueueUnstarted pins the half of the contract that a
// field check cannot see: Build constructs, it does not run. An enqueuer
// resolved by Build would mean the queue was started there too, and the
// window Start closes (#184) would be back.
func TestBuildLeavesTheQueueUnstarted(t *testing.T) {
	a := buildForTest(t)

	if err := a.enq.Enqueue(context.Background(), nil); !errors.Is(err, jobs.ErrNoQueue) {
		t.Fatalf("deferred enqueuer resolved during Build: got %v, want %v", err, jobs.ErrNoQueue)
	}
}

// The audiobook staging area is a struct value, so the nil walk above
// cannot see it: a Build that forgot to assign it would hand the workers
// a zero Staging, which refuses every operation, and narration would stop
// working with no nil dereference anywhere. Asserted here instead, along
// with where it points — under the configured data root, absolute, the
// property the whole DataRoot type exists to guarantee (#207).
func TestBuildConfiguresTheAudiobookStagingArea(t *testing.T) {
	a := buildForTest(t)

	if !a.staging.Configured() {
		t.Fatal("App.staging is unconfigured after Build — every narration would refuse to stage")
	}
	dir, err := a.staging.Dir("book-1")
	if err != nil {
		t.Fatalf("Staging.Dir: %v", err)
	}
	root, err := a.cfg.DataPath.Path()
	if err != nil {
		t.Fatalf("DataPath.Path: %v", err)
	}
	if !filepath.IsAbs(dir) || !strings.HasPrefix(dir, root+string(filepath.Separator)) {
		t.Errorf("staging dir = %q, want an absolute path under the data root %q", dir, root)
	}
}

// TestCloseWaitsForRegisteredBackgroundWork pins the promise Close's doc
// comment makes: it is the single shutdown path. A registered loop that
// is still running when Close is called must be told to stop and must
// have stopped by the time Close returns — otherwise the pool goes away
// under a live query, which is exactly what the two backfills used to do
// by detaching onto context.Background() (#224).
//
// The `default` arm below is the whole assertion: reading the
// observation without blocking only succeeds if the task was already
// finished when Close returned.
func TestCloseWaitsForRegisteredBackgroundWork(t *testing.T) {
	a := buildForTest(t)

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	started := make(chan struct{})
	observed := make(chan error, 1)
	a.goBackground("test probe", func(taskCtx context.Context) {
		close(started)
		<-taskCtx.Done()
		observed <- taskCtx.Err()
	})
	<-started

	returned := make(chan error, 1)
	go func() { returned <- a.Close(context.Background()) }()

	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Minute):
		t.Fatal("Close did not return while a registered task was in flight")
	}

	select {
	case err := <-observed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("registered task saw %v, want context.Canceled", err)
		}
	default:
		t.Fatal("Close returned before the registered task observed cancellation")
	}
}

// isNilValue reports whether a field holds nothing. Reflection rather
// than a hand-written list so a seam added to App or Handler is covered
// the moment it is declared; IsNil is legal on unexported fields, unlike
// Interface().
func isNilValue(f reflect.Value) bool {
	switch f.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return f.IsNil()
	default:
		return false
	}
}
