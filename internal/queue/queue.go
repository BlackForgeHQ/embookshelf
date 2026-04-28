// Package queue wraps the river client so callers don't need to know about
// river's generics / driver types. Keeps the service layer free of river
// imports and makes it trivial to swap implementations later.
package queue

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/task"
)

// Client is the minimal surface the rest of the app uses.
type Client interface {
	EnqueueBookDrop(ctx context.Context, itemID string) error
	EnqueueLibraryScan(ctx context.Context, libraryID string) error
	Stop(ctx context.Context) error
}

// RiverClient implements Client against river's *river.Client.
type RiverClient struct {
	c *river.Client[pgx.Tx]
}

// New constructs, migrates, and starts a river.Client. Workers are registered
// for every job kind the app supports.
func New(
	ctx context.Context,
	d *db.DB,
	bdropSvc *service.BookDropService,
	libSvc *service.LibraryService,
) (Client, error) {
	if d.Dialect != db.DialectPostgres {
		return nil, errors.New("queue: SQLite backend lands in Plan 3; use a Postgres DATABASE_URL or wait for the SQLite queue worker")
	}
	if d.PG == nil {
		return nil, errors.New("queue: db.PG is nil for postgres dialect")
	}

	driver := riverpgxv5.New(d.PG)

	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		return nil, fmt.Errorf("river migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return nil, fmt.Errorf("river migrate: %w", err)
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, &task.BookDropWorker{Deps: task.BookDropDeps{Svc: bdropSvc}})

	// The scan worker needs to enqueue bookdrop.ingest jobs; wire that up
	// after the client is constructed (circular dep resolved via the
	// BookDropEnqueuer interface).
	scanWorker := &task.LibraryScanWorker{
		Deps: task.LibraryScanDeps{
			BookDrop: bdropSvc,
			Lib:      libSvc,
			// Queue is set after the river.Client is constructed (cyclic dep).
		},
	}
	river.AddWorker(workers, scanWorker)

	c, err := river.NewClient(driver, &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 4},
		},
		Workers: workers,
	})
	if err != nil {
		return nil, fmt.Errorf("river client: %w", err)
	}

	rc := &RiverClient{c: c}
	scanWorker.Deps.Queue = rc // now the scan worker can enqueue further jobs

	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("river start: %w", err)
	}
	return rc, nil
}

// EnqueueBookDrop inserts a bookdrop.ingest job for the given item.
func (r *RiverClient) EnqueueBookDrop(ctx context.Context, itemID string) error {
	_, err := r.c.Insert(ctx, task.BookDropIngestArgs{ItemID: itemID}, nil)
	return err
}

// EnqueueLibraryScan inserts a library.scan job for the given library.
func (r *RiverClient) EnqueueLibraryScan(ctx context.Context, libraryID string) error {
	_, err := r.c.Insert(ctx, task.LibraryScanArgs{LibraryID: libraryID}, nil)
	return err
}

// Stop gracefully drains in-flight work and shuts the client down.
func (r *RiverClient) Stop(ctx context.Context) error {
	return r.c.Stop(ctx)
}

// Noop is a queue implementation that fails every enqueue. Used in
// SQLite mode until Plan 3 lands the homegrown worker. Stop is a
// no-op so deferred cleanup in main.go is safe.
type Noop struct{}

func (Noop) EnqueueBookDrop(_ context.Context, _ string) error {
	return errors.New("queue: bookdrop disabled on sqlite (Plan 3)")
}

func (Noop) EnqueueLibraryScan(_ context.Context, _ string) error {
	return errors.New("queue: library scan disabled on sqlite (Plan 3)")
}

func (Noop) Stop(_ context.Context) error { return nil }

// Compile-time interface conformance check.
var _ Client = Noop{}
