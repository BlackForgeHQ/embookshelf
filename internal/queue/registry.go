// SPDX-License-Identifier: AGPL-3.0-or-later

package queue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/riverqueue/river"

	"github.com/blackforge/embookshelf/internal/task"
)

// registration is one job type, expressed once. Both backends consume
// the same entry: River wants a typed worker registered against its
// generic machinery, the SQLite loop wants a decode-and-dispatch
// closure keyed by kind. Neither is written per job by hand.
type registration struct {
	kind string
	// sqliteHandler decodes a stored JSON payload and runs the job.
	sqliteHandler kindHandler
	// addToRiver registers the typed worker with River's registry.
	addToRiver func(*river.Workers) error
}

// riverWorker adapts a plain work function to River's typed worker
// interface, so task packages never import river.
type riverWorker[T JobArgs] struct {
	river.WorkerDefaults[T]
	work func(context.Context, T) error
}

func (w *riverWorker[T]) Work(ctx context.Context, job *river.Job[T]) error {
	return w.work(ctx, job.Args)
}

// register builds a registration from a job's args type and the
// function that works it. The args type supplies its own kind, so a
// registration cannot disagree with the payload it decodes.
func register[T JobArgs](work func(context.Context, T) error) registration {
	var zero T
	return registration{
		kind: zero.Kind(),
		sqliteHandler: func(ctx context.Context, raw string) error {
			var args T
			if err := json.Unmarshal([]byte(raw), &args); err != nil {
				return fmt.Errorf("decode args: %w", err)
			}
			return work(ctx, args)
		},
		addToRiver: func(w *river.Workers) error {
			return river.AddWorkerSafely(w, &riverWorker[T]{work: work})
		},
	}
}

// registry is the single list of job types this binary knows. Adding a
// job means adding one line here — no interface change, no per-backend
// enqueue method, no second registration site.
func registry(deps Deps) []registration {
	bookdrop := task.BookDropDeps{
		Svc:      deps.BookDropSvc,
		Resolver: deps.Resolver,
	}
	libraryScan := task.LibraryScanDeps{
		Lib:      deps.LibSvc,
		LibStore: deps.LibStore,
		Files:    deps.FileRepo,
	}
	// Notifier is always non-nil — its runtime state gates whether the
	// send actually fires. Registering unconditionally lets admins
	// hot-enable email without a restart and have queued jobs picked up.
	sendToKindle := task.SendToKindleDeps{
		Notifier: deps.Notifier,
		Books:    deps.Books,
		Users:    deps.Users,
		Hub:      deps.Hub,
	}

	return []registration{
		register(func(ctx context.Context, a task.BookDropIngestArgs) error {
			return task.BookDropIngest(ctx, a, bookdrop)
		}),
		register(func(ctx context.Context, a task.LibraryScanArgs) error {
			return task.LibraryScan(ctx, a, libraryScan)
		}),
		register(func(ctx context.Context, a task.SendToKindleArgs) error {
			return task.SendToKindle(ctx, a, sendToKindle)
		}),
	}
}
