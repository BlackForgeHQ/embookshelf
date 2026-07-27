// SPDX-License-Identifier: AGPL-3.0-or-later

package queue

import (
	"context"

	"github.com/riverqueue/river"

	"github.com/blackforge/embookshelf/internal/task"
)

// registration is one job type, expressed once. It still earns its keep
// with a single backend: the kind, the args type, and the work function
// are declared together in one place, and River's typed-worker plumbing
// is derived rather than hand-written per job.
type registration struct {
	kind string
	// queue is where jobs of this kind run, read off the args type so it
	// cannot disagree with what Enqueue routes to.
	queue string
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
// registration cannot disagree with the payload it names.
func register[T JobArgs](work func(context.Context, T) error) registration {
	var zero T
	return registration{
		kind:  zero.Kind(),
		queue: queueOf(zero),
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
	// Auto-enrich is requested by BookDropService.Approve, which has
	// already read the enable setting — the worker only does the fan-out.
	autoEnrich := task.BookDropAutoEnrichDeps{
		Books:  deps.Books,
		Enrich: deps.Enrich,
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

	// Settings is read per job so an admin can change model, language or
	// cap without a restart. Registered unconditionally, like the email
	// jobs: the worker itself refuses when the feature is disabled.
	readingGuide := task.ReadingGuideDeps{
		Settings: deps.AppSettings,
		Guides:   deps.Guides,
		Books:    deps.Books,
		LibStore: deps.LibStore,
		Hub:      deps.Hub,
	}

	// Audiobook generation runs on its own queue, declared by its args
	// types. Dispatch is a pointer the composition root fills in after
	// queue.New returns, because the finalize dispatcher closes over the
	// very client this registry is being built for.
	audiobook := task.AudiobookDeps{
		Settings:   deps.AppSettings,
		Audiobooks: deps.Audiobooks,
		Books:      deps.Books,
		Files:      deps.FileRepo,
		LibStore:   deps.LibStore,
		Covers:     deps.Covers,
		Hub:        deps.Hub,
		Dispatch:   deps.AudiobookDispatch,
		DataPath:   deps.DataPath,
	}

	return []registration{
		register(func(ctx context.Context, a task.BookDropIngestArgs) error {
			return task.BookDropIngest(ctx, a, bookdrop)
		}),
		register(func(ctx context.Context, a task.BookDropAutoEnrichArgs) error {
			return task.BookDropAutoEnrich(ctx, a, autoEnrich)
		}),
		register(func(ctx context.Context, a task.LibraryScanArgs) error {
			return task.LibraryScan(ctx, a, libraryScan)
		}),
		register(func(ctx context.Context, a task.SendToKindleArgs) error {
			return task.SendToKindle(ctx, a, sendToKindle)
		}),
		register(func(ctx context.Context, a task.ReadingGuideArgs) error {
			return task.ReadingGuide(ctx, a, readingGuide)
		}),
		register(func(ctx context.Context, a task.AudiobookSegmentArgs) error {
			return task.AudiobookSegment(ctx, a, audiobook)
		}),
		register(func(ctx context.Context, a task.AudiobookFinalizeArgs) error {
			return task.AudiobookFinalize(ctx, a, audiobook)
		}),
	}
}
