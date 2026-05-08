package queue

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// newTestQueue spins up a sqliteQueue against a fresh repotest DB
// with a stub handler registered for "test.echo" jobs.
//
// The polling interval is dropped to 10ms so tests don't wait a
// full second per tick. Returns the queue plus a thread-safe call
// recorder and an error slot the test can prime.
func newTestQueue(t *testing.T) (*sqliteQueue, *callLog, *atomicErr) {
	t.Helper()
	t.Setenv("REPOTEST_DIALECT", "sqlite")
	d := repotest.New(t)

	calls := &callLog{}
	failNext := &atomicErr{}

	q := &sqliteQueue{
		db:       d,
		interval: 10 * time.Millisecond,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		handlers: map[string]kindHandler{},
	}
	q.handlers["test.echo"] = func(ctx context.Context, raw string) error {
		calls.add(raw)
		if e := failNext.swap(nil); e != nil {
			return e
		}
		return nil
	}
	go q.loop(context.Background())
	t.Cleanup(func() { _ = q.Stop(context.Background()) })
	return q, calls, failNext
}

// callLog is a tiny thread-safe slice recorder. The handler runs on
// the worker goroutine while assertions run on the test goroutine, so
// every read/write needs the mutex to keep -race quiet.
type callLog struct {
	mu sync.Mutex
	xs []string
}

func (c *callLog) add(s string) {
	c.mu.Lock()
	c.xs = append(c.xs, s)
	c.mu.Unlock()
}

func (c *callLog) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.xs)
}

type atomicErr struct {
	mu  sync.Mutex
	err error
}

func (a *atomicErr) set(e error) { a.mu.Lock(); a.err = e; a.mu.Unlock() }
func (a *atomicErr) swap(_ error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	e := a.err
	a.err = nil
	return e
}

func TestSQLiteQueue_runsToCompletion(t *testing.T) {
	q, calls, _ := newTestQueue(t)

	if _, err := q.db.SQL.ExecContext(context.Background(),
		`INSERT INTO jobs (kind, args) VALUES ('test.echo', '{"v":1}')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	waitFor(t, func() bool { return calls.len() == 1 })

	var state string
	if err := q.db.SQL.QueryRowContext(context.Background(),
		`SELECT state FROM jobs LIMIT 1`).Scan(&state); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if state != "completed" {
		t.Fatalf("state=%q want completed", state)
	}
}

func TestSQLiteQueue_retriesOnError(t *testing.T) {
	q, calls, failNext := newTestQueue(t)
	failNext.set(errors.New("first attempt fails"))

	if _, err := q.db.SQL.ExecContext(context.Background(),
		`INSERT INTO jobs (kind, args, max_attempts) VALUES ('test.echo', '{"v":1}', 3)`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// First call fails; second succeeds. Backoff is 2s ± 25% normally; for
	// tests we shorten by manually resetting scheduled_at after the first
	// failure.
	waitFor(t, func() bool {
		if calls.len() < 1 {
			return false
		}
		_, _ = q.db.SQL.ExecContext(context.Background(),
			`UPDATE jobs SET scheduled_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE state='pending'`)
		return calls.len() >= 2
	})

	var (
		state    string
		attempts int
	)
	if err := q.db.SQL.QueryRowContext(context.Background(),
		`SELECT state, attempts FROM jobs LIMIT 1`).Scan(&state, &attempts); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if state != "completed" {
		t.Fatalf("state=%q want completed", state)
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d want 2", attempts)
	}
}

func TestSQLiteQueue_marksFailedAfterMaxAttempts(t *testing.T) {
	q, _, _ := newTestQueue(t)
	q.handlers["test.echo"] = func(ctx context.Context, raw string) error {
		return errors.New("perma-fail")
	}

	if _, err := q.db.SQL.ExecContext(context.Background(),
		`INSERT INTO jobs (kind, args, max_attempts) VALUES ('test.echo', '{}', 2)`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	waitFor(t, func() bool {
		_, _ = q.db.SQL.ExecContext(context.Background(),
			`UPDATE jobs SET scheduled_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE state='pending'`)
		var state string
		_ = q.db.SQL.QueryRowContext(context.Background(),
			`SELECT state FROM jobs LIMIT 1`).Scan(&state)
		return state == "failed"
	})

	var lastErr string
	if err := q.db.SQL.QueryRowContext(context.Background(),
		`SELECT COALESCE(last_error,'') FROM jobs LIMIT 1`).Scan(&lastErr); err != nil {
		t.Fatalf("scan last_error: %v", err)
	}
	if lastErr != "perma-fail" {
		t.Fatalf("last_error=%q want perma-fail", lastErr)
	}
}

func TestSQLiteQueue_restartRecovery(t *testing.T) {
	t.Setenv("REPOTEST_DIALECT", "sqlite")
	d := repotest.New(t)

	// Pretend a prior process left a job in 'running'.
	if _, err := d.SQL.ExecContext(context.Background(),
		`INSERT INTO jobs (kind, args, state) VALUES ('test.echo', '{}', 'running')`); err != nil {
		t.Fatalf("insert running: %v", err)
	}

	// Construct via the real path — newSQLiteQueue runs the recovery query.
	// Pass nil services because this test only exercises the recovery query,
	// not the handlers.
	q, err := newSQLiteQueue(context.Background(), d, Deps{})
	if err != nil {
		t.Fatalf("newSQLiteQueue: %v", err)
	}
	t.Cleanup(func() { _ = q.Stop(context.Background()) })

	var state string
	if err := d.SQL.QueryRowContext(context.Background(),
		`SELECT state FROM jobs LIMIT 1`).Scan(&state); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if state != "pending" {
		t.Fatalf("state=%q want pending (recovery)", state)
	}
}

// waitFor polls cond every 20ms up to 5s. Fails the test if cond
// never returns true.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}
