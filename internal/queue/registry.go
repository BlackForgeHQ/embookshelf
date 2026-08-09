// SPDX-License-Identifier: AGPL-3.0-or-later

package queue

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/riverqueue/river"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/sse"
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
type riverWorker[T jobs.Args] struct {
	river.WorkerDefaults[T]
	work func(context.Context, T, jobs.Attempt) error
}

func (w *riverWorker[T]) Work(ctx context.Context, job *river.Job[T]) error {
	// Where the one fact a worker sometimes needs from River crosses into
	// the task tier: the attempt, as two ints rather than as the job. A
	// worker whose durable record depends on whether anything will run
	// again — the audiobook segment worker, recording a transient failure
	// as retrying or as failed (ADR-0032) — cannot ask River itself, and
	// giving it the *river.Job would put River in internal/task's imports
	// to answer a question about a number.
	err := w.work(ctx, job.Args, jobs.Attempt{Number: job.Attempt, Max: job.MaxAttempts})
	// A work function saying its failure is closed is the only place the
	// task tier can express that — it does not import River. Honouring it
	// here is what makes the claim true rather than a comment (#185).
	if err != nil && errors.Is(err, jobs.ErrDoNotRetry) {
		return river.JobCancel(err)
	}
	return err
}

// register builds a registration from a job's args type and the
// function that works it. The args type supplies its own kind, so a
// registration cannot disagree with the payload it names.
func register[T jobs.Args](work func(context.Context, T, jobs.Attempt) error) registration {
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

	// The one collaborator the guide and audiobook jobs share (spec §3):
	// wired once here rather than constructed per job.
	openBook := service.NewLibraryBookOpener(deps.LibStore).Open

	// Settings is read per job so an admin can change model, language or
	// cap without a restart. Registered unconditionally, like the email
	// jobs: the worker itself refuses when the feature is disabled.
	readingGuide := task.ReadingGuideDeps{
		Config: deps.AppSettings.GetReadingGuide,
		Completer: func(c repo.ReadingGuideConfig) (service.GuideCompleter, error) {
			// Explicit rather than returning c.Client() directly: on
			// error that would box a nil *llm.Client into a non-nil
			// interface, and the caller's nil check would miss it.
			cl, err := c.Client()
			if err != nil {
				return nil, err
			}
			return cl, nil
		},
		Guides: deps.Guides,
		Books:  deps.Books,
		Open:   openBook,
	}
	if deps.Hub != nil {
		readingGuide.Publish = func(bookID string) {
			_ = deps.Hub.Publish(sse.ReadingGuideUpdated{BookID: bookID})
		}
	}

	// Markdown renditions (ADR-0033). Config per job for the same
	// hot-reload reason as the guide; the library-touching steps are
	// per-op closures so the worker holds no LibraryStore.
	markdownRendition := task.MarkdownRenditionDeps{
		Config:     deps.AppSettings.GetConverter,
		Renditions: deps.Renditions,
		Books:      deps.Books,
		Open: func(ctx context.Context, book model.Book) (io.Reader, int64, io.Closer, error) {
			handle, err := deps.LibStore.For(ctx, book.LibraryID)
			if err != nil {
				return nil, 0, nil, fmt.Errorf("resolve library: %w", err)
			}
			return handle.OpenBook(ctx, book)
		},
		SourceHash: func(ctx context.Context, book model.Book) []byte {
			handle, err := deps.LibStore.For(ctx, book.LibraryID)
			if err != nil {
				return nil
			}
			return handle.PrimaryContentHash(ctx, book)
		},
		Convert: (&service.ConverterClient{}).Convert,
		Place: func(ctx context.Context, book model.Book, srcPath string) (service.PlaceResult, error) {
			handle, err := deps.LibStore.For(ctx, book.LibraryID)
			if err != nil {
				return service.PlaceResult{}, fmt.Errorf("resolve library: %w", err)
			}
			return handle.PlaceMarkdown(ctx, book, srcPath)
		},
	}

	// publishAudiobook is the SSE side of every audiobook state change —
	// segment progress and finalize both report on the same topic.
	publishAudiobook := func(bookID string) {
		_ = deps.Hub.Publish(sse.AudiobookUpdated{BookID: bookID})
	}

	// Audiobook generation runs on its own queue, declared by its args
	// types.
	segment := task.SegmentDeps{
		Config:  deps.AppSettings.GetAudiobook,
		Engine:  repo.AudiobookConfig.SelectEngine,
		Runs:    deps.Audiobooks,
		Advance: deps.AudiobookSvc,
		Books:   deps.Books,
		Open:    openBook,
		Staging: deps.Staging,
	}
	if deps.Hub != nil {
		segment.Publish = publishAudiobook
	}

	finalize := task.FinalizeDeps{
		Runs:    deps.Audiobooks,
		Report:  deps.AudiobookSvc,
		Fail:    deps.AudiobookSvc.FailRun,
		Books:   deps.Books,
		Files:   deps.FileRepo,
		Staging: deps.Staging,
		Place: func(ctx context.Context, book model.Book, srcPath string) (service.PlaceResult, error) {
			handle, err := deps.LibStore.For(ctx, book.LibraryID)
			if err != nil {
				return service.PlaceResult{}, fmt.Errorf("resolve library: %w", err)
			}
			// Deliberately PlaceNarration rather than the generic Placer:
			// the book's folder already exists, and Placer would answer
			// that with a "Title (2)" sibling — a second leaf that scan
			// reads as a second book.
			return handle.PlaceNarration(ctx, book, srcPath)
		},
	}
	if deps.Covers != nil {
		finalize.Cover = deps.Covers.Open
	}

	// The attempt is passed to the one worker whose durable record depends
	// on it and ignored by the rest, rather than plumbed only where it is
	// wanted: the adapter that fills it in is generic over every job type,
	// so a second worker that needs it needs no new seam.
	return []registration{
		register(func(ctx context.Context, a jobs.BookDropIngestArgs, _ jobs.Attempt) error {
			return task.BookDropIngest(ctx, a, bookdrop)
		}),
		register(func(ctx context.Context, a jobs.BookDropAutoEnrichArgs, _ jobs.Attempt) error {
			return task.BookDropAutoEnrich(ctx, a, autoEnrich)
		}),
		register(func(ctx context.Context, a jobs.LibraryScanArgs, _ jobs.Attempt) error {
			return task.LibraryScan(ctx, a, libraryScan)
		}),
		register(func(ctx context.Context, a jobs.SendToKindleArgs, _ jobs.Attempt) error {
			return task.SendToKindle(ctx, a, sendToKindle)
		}),
		register(func(ctx context.Context, a jobs.ReadingGuideArgs, _ jobs.Attempt) error {
			return task.ReadingGuide(ctx, a, readingGuide)
		}),
		register(func(ctx context.Context, a jobs.MarkdownRenditionArgs, _ jobs.Attempt) error {
			return task.MarkdownRendition(ctx, a, markdownRendition)
		}),
		register(func(ctx context.Context, a jobs.AudiobookSegmentArgs, at jobs.Attempt) error {
			return task.AudiobookSegment(ctx, a, at, segment)
		}),
		register(func(ctx context.Context, a jobs.AudiobookFinalizeArgs, _ jobs.Attempt) error {
			return task.AudiobookFinalize(ctx, a, finalize)
		}),
	}
}
