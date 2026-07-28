// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/blackforge/embookshelf/internal/audio"
	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/tts"
)

// ErrAudiobooksDisabled is returned when the feature is off. Permanent —
// a disabled feature will still be disabled in thirty seconds.
var ErrAudiobooksDisabled = errors.New("audiobook generation is not enabled")

// errCanceled unwinds a segment whose run was cancelled mid-flight. Never
// recorded as a failure: the run is already in its final state and
// overwriting it would lose the reason it stopped.
var errCanceled = errors.New("audiobook run canceled")

// canceled reports whether the run has been stopped since the job began.
// A read per engine call is cheap next to the call it guards.
func canceled(ctx context.Context, bookID string, deps SegmentDeps) bool {
	run, err := deps.Runs.GetByBookID(ctx, bookID)
	if err != nil {
		return false
	}
	return run.State == model.AudiobookCanceled
}

// segmentStore is the slice of BookAudiobookRepo the segment worker
// touches. Narrow so the claim, cancel and failure branches are
// exercisable without a database — the property AudiobookService has had
// since it was written, and these workers did not (#177).
type segmentStore interface {
	GetByBookID(ctx context.Context, bookID string) (model.Audiobook, error)
	MarkSegmentRunning(ctx context.Context, bookID string, seq int) (bool, error)
	RecordSegment(ctx context.Context, bookID string, seq int, res model.SegmentResult) (model.AudiobookOutcome, error)
}

// bookReader is the one book-row read every generation job makes.
type bookReader interface {
	GetByID(ctx context.Context, userID, id string) (model.Book, error)
}

// SegmentDeps groups the seams the segment worker needs.
//
// Config is read per job rather than captured at boot, so an admin
// changing voice, engine or key takes effect on the next segment instead
// of at the next restart — the same hot-reload the guide worker gets.
//
// Config and Engine are separate rather than one resolve step: the
// disabled check and its permanent sentinel stay in the worker body,
// where a reader asking why River does not retry will find them.
type SegmentDeps struct {
	Config func(context.Context) (repo.AudiobookConfig, error)
	Engine func(repo.AudiobookConfig) (repo.ConfiguredEngine, error)
	Runs   segmentStore
	Books  bookReader
	// Open yields the book's bytes with random access. Always through the
	// library handle, never os.Open(book.Path), which is how device push
	// on S3 libraries was once silently broken.
	Open func(context.Context, model.Book) (storage.Source, error)
	// Enqueue dispatches the finalize job when this segment completes
	// the run. An ordinary jobs.Enqueuer, resolved by the composition
	// root before queue.Client.Start is ever called, so no worker here
	// can observe it unset.
	Enqueue jobs.Enqueuer
	Publish func(bookID string)
	// DataPath roots the staging directory. Per-segment MP3s live on
	// local disk until finalize, outside storage.Storage, following the
	// coverstore precedent for derived bytes.
	DataPath string
}

// publish emits the run's terminal event. A missing publisher is a
// deployment with no SSE hub, not an error worth a branch at each call.
func (d SegmentDeps) publish(bookID string) {
	if d.Publish != nil {
		d.Publish(bookID)
	}
}

// StagingDir is where one book's per-segment MP3s live until finalize.
func StagingDir(dataPath, bookID string) string {
	return filepath.Join(dataPath, "audiobooks", bookID)
}

func segmentPath(dir string, seq int) string {
	return filepath.Join(dir, "seg-"+strconv.Itoa(seq)+".mp3")
}

// AudiobookSegment synthesizes one segment and stages its audio.
//
// Every exit path leaves the run legible: a claimed segment either
// finishes, or is marked failed with the reason, or is abandoned because
// the whole run was cancelled. A segment that simply stops is the one
// outcome nothing downstream can recover from — the finalize step waits
// on a count that would never complete.
func AudiobookSegment(ctx context.Context, a jobs.AudiobookSegmentArgs, deps SegmentDeps) error {
	cfg, err := deps.Config(ctx)
	if err != nil {
		return fmt.Errorf("read audiobook settings: %w", err)
	}
	if !cfg.Enabled {
		return ErrAudiobooksDisabled
	}

	run, err := deps.Runs.GetByBookID(ctx, a.BookID)
	if err != nil {
		return fmt.Errorf("load audiobook %s: %w", a.BookID, err)
	}
	// Cancel is checked here, before every engine call, because it is the
	// only thing standing between a user pressing stop and the rest of a
	// $170 run being billed anyway.
	if run.State == model.AudiobookCanceled || run.State == model.AudiobookReady {
		slog.Debug("audiobook segment skipped", "book", a.BookID, "seq", a.Seq, "state", run.State)
		return nil
	}

	claimed, err := deps.Runs.MarkSegmentRunning(ctx, a.BookID, a.Seq)
	if err != nil {
		return fmt.Errorf("claim segment %d: %w", a.Seq, err)
	}
	if !claimed {
		// Already done. Re-synthesizing would buy the same audio twice.
		return nil
	}

	audioBytes, err := synthesizeSegment(ctx, a, cfg, run, deps)
	if errors.Is(err, errCanceled) {
		slog.Debug("audiobook segment abandoned: run canceled", "book", a.BookID, "seq", a.Seq)
		return nil
	}
	if err != nil {
		recordSegment(ctx, deps, a, model.SegmentResult{
			State: model.SegmentFailed,
			Error: err.Error(),
		})
		if errors.Is(err, tts.ErrPermanent) || errors.Is(err, service.ErrNotNarratable) {
			// Returning nil stops River retrying something that cannot
			// improve; the failed row and its message carry the outcome.
			deps.publish(a.BookID)
			return nil
		}
		return err
	}

	dir := StagingDir(deps.DataPath, a.BookID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}
	path := segmentPath(dir, a.Seq)
	frames, durationMS, err := audio.Payload(audioBytes)
	if err != nil {
		recordSegment(ctx, deps, a, model.SegmentResult{
			State: model.SegmentFailed,
			Error: fmt.Sprintf("engine returned unusable audio: %v", err),
		})
		deps.publish(a.BookID)
		return nil
	}
	if err := os.WriteFile(path, frames, 0o600); err != nil {
		return fmt.Errorf("stage segment %d: %w", a.Seq, err)
	}

	recordSegment(ctx, deps, a, model.SegmentResult{
		State:      model.SegmentDone,
		StagedPath: path,
		DurationMS: durationMS,
	})
	return nil
}

// recordSegment writes a segment's result and carries out whatever the
// run needs as a consequence.
//
// The write and the transition are one operation in the repo, so this is
// no longer a pair of calls a worker has to sequence — the only thing
// left outside the transaction is the enqueue, which is a different
// system and cannot join it. A crash between the commit and the dispatch
// still loses the finalize job, but no longer loses the *fact*: the
// segment rows say the run is complete, and AudiobookService.Status
// re-derives the same transition on every read, so whoever next looks at
// the book dispatches what this dropped (#157).
//
// Deliberately does not return an error. A failure here is worth a log
// and nothing more: returning it would hand River a segment to retry
// whose audio is already staged and already paid for.
func recordSegment(ctx context.Context, deps SegmentDeps, a jobs.AudiobookSegmentArgs, res model.SegmentResult) {
	outcome, err := deps.Runs.RecordSegment(ctx, a.BookID, a.Seq, res)
	if err != nil {
		slog.Warn("audiobook: record segment", "book", a.BookID, "seq", a.Seq, "err", err)
		return
	}
	switch outcome.Next {
	case model.AudiobookNextFinalize:
		if err := deps.Enqueue.Enqueue(ctx, jobs.AudiobookFinalizeArgs{BookID: a.BookID}); err != nil {
			slog.Warn("audiobook: dispatch finalize", "book", a.BookID, "err", err)
		}
	case model.AudiobookNextFail:
		deps.publish(a.BookID)
	case model.AudiobookNextNothing:
	}
}

// synthesizeSegment turns one segment's text into MP3 bytes.
//
// The text is re-extracted rather than stored: the plan holds character
// ranges, and the EPUB is the source of truth for what sits in them. A
// book whose file changed mid-run would otherwise be narrated from stale
// text with no way to notice.
func synthesizeSegment(
	ctx context.Context,
	a jobs.AudiobookSegmentArgs,
	cfg repo.AudiobookConfig,
	run model.Audiobook,
	deps SegmentDeps,
) ([]byte, error) {
	book, err := deps.Books.GetByID(ctx, "", a.BookID)
	if err != nil {
		return nil, fmt.Errorf("load book %s: %w", a.BookID, err)
	}
	if !service.Narratable(book.Format) {
		return nil, fmt.Errorf("%w: %s is %s", service.ErrNotNarratable, book.ID, book.Format)
	}

	sel, err := deps.Engine(cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", tts.ErrPermanent, err)
	}
	// The run records what it started with. An admin switching engines
	// mid-run must not produce a book narrated half in one voice and half
	// in another, so the run's own choice wins over the current setting.
	if run.Engine != "" && run.Engine != string(sel.ID) {
		return nil, fmt.Errorf("%w: run uses %s but %s is now selected", tts.ErrPermanent, run.Engine, sel.ID)
	}

	text, err := segmentText(ctx, book, cfg, a.Seq, deps)
	if err != nil {
		return nil, err
	}

	// The cancel check travels with the request: the adapter splits this
	// segment into as many engine calls as its cap needs, and runs this
	// before each one. It is the only thing between a user pressing stop
	// and the rest of a $170 run being billed anyway (ADR-0028 §6).
	return sel.Engine.Synthesize(ctx, tts.Request{
		Text:  text,
		Voice: run.Voice,
		Model: run.Model,
		BeforeChunk: func(ctx context.Context) error {
			if canceled(ctx, a.BookID, deps) {
				return errCanceled
			}
			return nil
		},
	})
}

// segmentText re-extracts the book and returns the seq-th segment's prose.
//
// Re-extraction rather than a stored copy keeps the EPUB the single
// source of truth for what a character range contains. The guard below is
// what makes that safe: if the plan and the file no longer agree on how
// many segments the book has, the file changed under the run, and
// narrating segment 12 of a different book is worse than failing.
func segmentText(
	ctx context.Context,
	book model.Book,
	cfg repo.AudiobookConfig,
	seq int,
	deps SegmentDeps,
) (string, error) {
	src, err := deps.Open(ctx, book)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", book.ID, err)
	}
	defer func() { _ = src.Close() }()

	segs, err := fileproc.ExtractEPUBSegments(ctx, src, fileproc.SegmentOptions{MaxChars: cfg.SegmentChars})
	if err != nil {
		return "", fmt.Errorf("%w: re-extract %s: %v", tts.ErrPermanent, book.ID, err)
	}
	if seq < 0 || seq >= len(segs) {
		return "", fmt.Errorf("%w: segment %d no longer exists — the source file changed mid-run",
			tts.ErrPermanent, seq)
	}
	return segs[seq].Text, nil
}
