// SPDX-License-Identifier: AGPL-3.0-or-later

// Package queue's SQLite implementation. A single goroutine polls the
// jobs table for work, claims rows atomically via UPDATE, dispatches
// by kind to a registered handler, and updates the row on success or
// failure. Polling beats LISTEN/NOTIFY here — SQLite doesn't have
// the latter, the table will rarely have many pending rows in
// single-user installs, and a 1s ticker is cheap.
package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/blackforge/embookshelf/internal/db"
)

// sqliteQueue persists jobs in a SQLite table and runs them in a
// single goroutine. Implements Client.
type sqliteQueue struct {
	db       *db.DB
	handlers map[string]kindHandler
	interval time.Duration
	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

type kindHandler func(ctx context.Context, rawArgs string) error

// Compile-time interface check.
var _ Client = (*sqliteQueue)(nil)

func newSQLiteQueue(ctx context.Context, d *db.DB, deps Deps) (*sqliteQueue, error) {
	q := &sqliteQueue{
		db:       d,
		interval: time.Second,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}

	q.handlers = make(map[string]kindHandler)
	for _, reg := range registry(deps) {
		q.handlers[reg.kind] = reg.sqliteHandler
	}

	// Restart recovery: any 'running' jobs left from a previous process
	// were interrupted. Put them back in 'pending' so the loop picks
	// them up.
	if _, err := q.db.SQL.ExecContext(ctx,
		`UPDATE jobs SET state='pending', started_at=NULL WHERE state='running'`); err != nil {
		return nil, fmt.Errorf("queue restart recovery: %w", err)
	}

	go q.loop(ctx)
	return q, nil
}

// Enqueue serializes the payload and inserts a pending row. The kind
// comes from the args themselves, so this works for any registered job
// without a method per type.
func (q *sqliteQueue) Enqueue(ctx context.Context, args JobArgs) error {
	payload, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("encode args: %w", err)
	}
	_, err = q.db.SQL.ExecContext(ctx, `
		INSERT INTO jobs (kind, args) VALUES (?, ?)
	`, args.Kind(), string(payload))
	return err
}

func (q *sqliteQueue) Stop(ctx context.Context) error {
	q.stopOnce.Do(func() { close(q.stopCh) })
	select {
	case <-q.doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *sqliteQueue) loop(ctx context.Context) {
	defer close(q.doneCh)
	t := time.NewTicker(q.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-q.stopCh:
			return
		case <-t.C:
			if err := q.tryOne(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Warn("queue tick failed", "err", err)
			}
		}
	}
}

// tryOne claims at most one pending job and runs it. Returns nil
// when there's nothing to do, an error only for unexpected failures
// (claim/handler errors are recorded on the row, not surfaced).
func (q *sqliteQueue) tryOne(ctx context.Context) error {
	var (
		id          int64
		kind        string
		rawArgs     string
		attempts    int
		maxAttempts int
	)
	err := q.db.SQL.QueryRowContext(ctx, `
		SELECT id, kind, args, attempts, max_attempts FROM jobs
		WHERE state='pending' AND scheduled_at <= strftime('%Y-%m-%dT%H:%M:%fZ','now')
		ORDER BY id LIMIT 1
	`).Scan(&id, &kind, &rawArgs, &attempts, &maxAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	// Atomic claim: fail to update if another worker beat us (won't
	// happen with a single goroutine but the guard is cheap).
	res, err := q.db.SQL.ExecContext(ctx, `
		UPDATE jobs
		SET state='running', started_at=datetime('now'), attempts=attempts+1
		WHERE id=? AND state='pending'
	`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}

	handler, ok := q.handlers[kind]
	if !ok {
		_, _ = q.db.SQL.ExecContext(ctx, `
			UPDATE jobs SET state='failed', finished_at=datetime('now'),
			               last_error=?
			WHERE id=?
		`, "unknown kind: "+kind, id)
		return nil
	}

	runErr := handler(ctx, rawArgs)
	if runErr == nil {
		_, _ = q.db.SQL.ExecContext(ctx, `
			UPDATE jobs SET state='completed', finished_at=datetime('now')
			WHERE id=?
		`, id)
		return nil
	}

	// Failure: backoff if we have attempts left, else mark failed.
	newAttempts := attempts + 1
	if newAttempts < maxAttempts {
		scheduledAt := time.Now().Add(nextBackoff(newAttempts)).UTC().Format(time.RFC3339)
		_, _ = q.db.SQL.ExecContext(ctx, `
			UPDATE jobs SET state='pending', scheduled_at=?, last_error=?
			WHERE id=?
		`, scheduledAt, runErr.Error(), id)
		return nil
	}
	_, _ = q.db.SQL.ExecContext(ctx, `
		UPDATE jobs SET state='failed', finished_at=datetime('now'),
		               last_error=?
		WHERE id=?
	`, runErr.Error(), id)
	return nil
}
