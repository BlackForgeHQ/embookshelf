// Package queue wraps the River client (Postgres) and a homegrown
// polling worker (SQLite) so callers see a single Client interface.
// Keeps the service layer free of driver imports and makes the
// per-dialect implementation choice invisible to the rest of the app.
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
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/task"
)

// Client is the minimal surface the rest of the app uses.
type Client interface {
	EnqueueBookDrop(ctx context.Context, itemID string) error
	EnqueueLibraryScan(ctx context.Context, libraryID string) error
	Stop(ctx context.Context) error
}

// New constructs a Client appropriate for the dialect of d:
// Postgres → River-backed; SQLite → polling worker.
func New(
	ctx context.Context,
	d *db.DB,
	bdropSvc *service.BookDropService,
	libSvc *service.LibraryService,
	store storage.Storage,
	fileRepo *repo.FileRepo,
) (Client, error) {
	switch d.Dialect {
	case db.DialectPostgres:
		return newRiver(ctx, d, bdropSvc, libSvc, store, fileRepo)
	case db.DialectSQLite:
		return newSQLiteQueue(ctx, d, bdropSvc, libSvc, store, fileRepo)
	default:
		return nil, fmt.Errorf("queue: unknown dialect %q", d.Dialect)
	}
}

// RiverClient implements Client against river's *river.Client.
type RiverClient struct {
	c *river.Client[pgx.Tx]
}

// newRiver builds and starts the River-backed implementation. The
// caller is queue.New, which dispatches by dialect.
func newRiver(
	ctx context.Context,
	d *db.DB,
	bdropSvc *service.BookDropService,
	libSvc *service.LibraryService,
	store storage.Storage,
	fileRepo *repo.FileRepo,
) (*RiverClient, error) {
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
	river.AddWorker(workers, &task.BookDropWorker{Deps: task.BookDropDeps{Svc: bdropSvc, Storage: store}})

	// The scan worker needs to enqueue bookdrop.ingest jobs; wire that up
	// after the client is constructed (circular dep resolved via the
	// BookDropEnqueuer interface).
	scanWorker := &task.LibraryScanWorker{
		Deps: task.LibraryScanDeps{
			BookDrop: bdropSvc,
			Lib:      libSvc,
			Storage:  store,
			Files:    fileRepo,
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
