// Package queue wraps the river client so callers don't need to know about
// river's generics / driver types. Keeps the service layer free of river
// imports and makes it trivial to swap implementations later.
package queue

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

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
	pool *pgxpool.Pool,
	bdropSvc *service.BookDropService,
	libSvc *service.LibraryService,
) (*RiverClient, error) {
	driver := riverpgxv5.New(pool)

	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		return nil, fmt.Errorf("river migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return nil, fmt.Errorf("river migrate: %w", err)
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, &task.BookDropWorker{Svc: bdropSvc})

	// The scan worker needs to enqueue bookdrop.ingest jobs; wire that up
	// after the client is constructed (circular dep resolved via the
	// BookDropEnqueuer interface).
	scanWorker := &task.LibraryScanWorker{
		BookDrop: bdropSvc,
		Lib:      libSvc,
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
	scanWorker.Queue = rc // now the scan worker can enqueue further jobs

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
