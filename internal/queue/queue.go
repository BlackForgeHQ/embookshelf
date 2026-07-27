// SPDX-License-Identifier: AGPL-3.0-or-later

// Package queue is the background-job seam over River. Callers see a
// two-method Client and never import a driver; a job registry declares
// each kind once and derives River's typed-worker plumbing from it.
// Postgres is the only supported database (ADR-0023).
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
	"github.com/blackforge/embookshelf/internal/sse"
	"github.com/blackforge/embookshelf/internal/storage"
)

// JobArgs is the payload of an enqueued job: a JSON-serializable
// struct that names its own kind. Declaring it here rather than
// reusing River's identical interface keeps the driver import out of
// every caller. The concrete task.*Args types satisfy both.
type JobArgs interface {
	Kind() string
}

// Client is the minimal surface the rest of the app uses. One Enqueue
// for every job type — the kind travels with the payload, so adding a
// job does not widen this interface.
//
// Crash recovery is River's JobRescuer, which reclaims jobs left
// `running` by a killed process after a timeout (default 1h).
type Client interface {
	Enqueue(ctx context.Context, args JobArgs) error
	Stop(ctx context.Context) error
}

// Deps groups the seams every backend needs. Splitting from the
// long positional list of New() keeps the boot wiring readable when
// new workers (Send-to-Kindle, future jobs) join.
type Deps struct {
	BookDropSvc *service.BookDropService
	LibSvc      *service.LibraryService
	Resolver    storage.Resolver
	LibStore    service.LibraryStore
	FileRepo    *repo.FileRepo
	Books       *repo.BookRepo
	Users       *repo.UserRepo
	Notifier    *service.Notifier
	Hub         *sse.Hub
	// Reading guides (ADR-0024). AppSettings is read per job so config
	// changes take effect without a restart.
	AppSettings *repo.AppSettingsRepo
	Guides      *repo.BookReadingGuideRepo
}

// New constructs the River-backed Client. Postgres is the only supported
// database (ADR-0023); the SQLite polling worker is gone.
func New(ctx context.Context, d *db.DB, deps Deps) (Client, error) {
	if d.Dialect != db.DialectPostgres {
		return nil, fmt.Errorf("queue: unsupported dialect %q — embookshelf requires Postgres", d.Dialect)
	}
	return newRiver(ctx, d, deps)
}

// RiverClient implements Client against river's *river.Client.
type RiverClient struct {
	c *river.Client[pgx.Tx]
}

// newRiver builds and starts the River-backed implementation. The
// caller is queue.New, which dispatches by dialect.
func newRiver(ctx context.Context, d *db.DB, deps Deps) (*RiverClient, error) {
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
	for _, reg := range registry(deps) {
		if err := reg.addToRiver(workers); err != nil {
			return nil, fmt.Errorf("register %s worker: %w", reg.kind, err)
		}
	}

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
	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("river start: %w", err)
	}
	return rc, nil
}

// Enqueue inserts a job. River derives the kind from the args type, so
// any registered JobArgs works without a per-job method here.
func (r *RiverClient) Enqueue(ctx context.Context, args JobArgs) error {
	_, err := r.c.Insert(ctx, args, nil)
	return err
}

// Stop gracefully drains in-flight work and shuts the client down.
func (r *RiverClient) Stop(ctx context.Context) error {
	return r.c.Stop(ctx)
}
