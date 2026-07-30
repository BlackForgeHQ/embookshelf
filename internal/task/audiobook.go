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
	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/tts"
)

// ErrEngineDisabledForJob ends a segment job because the feature is off.
// Permanent — a disabled feature will still be disabled in thirty
// seconds — and it wraps jobs.ErrDoNotRetry, which is what actually
// stops River rather than a comment asserting it does (#185).
//
// Named for the job rather than for the feature because it used to be
// called ErrAudiobooksDisabled, the same name service.ErrAudiobooksDisabled
// carries, and the two were unrelated by errors.Is — one reader in three
// assumed a handler mapping the service sentinel would also catch this
// one. Renamed rather than related: they answer different questions.
// The service's is a refusal a user is owed an explanation for, and the
// handler turns it into a 503 with a code; this one is a retry verdict
// addressed to River, which has no user and no status code. Wiring them
// together with errors.Is would let a future mapper case catch a queue
// error, and would make a do-not-retry marker leak into the HTTP tier's
// vocabulary (#221).
var ErrEngineDisabledForJob = fmt.Errorf("audiobook generation is not enabled: %w", jobs.ErrDoNotRetry)

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
	GetSegment(ctx context.Context, bookID string, seq int) (model.AudiobookSegment, error)
	MarkSegmentRunning(ctx context.Context, bookID string, seq int) (bool, error)
}

// runAdvancer records a segment's result and carries out whatever the run
// needs as a consequence.
//
// One seam where the worker used to hold the second half of a three-way
// switch on model.AudiobookNext. Deciding what a landing means, and doing
// it, is AudiobookService's job now — this worker's job is the engine
// call and the bytes (#190).
type runAdvancer interface {
	AdvanceAfterSegment(ctx context.Context, bookID string, seq int, res model.SegmentResult) error
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
	Config  func(context.Context) (repo.AudiobookConfig, error)
	Engine  func(repo.AudiobookConfig) (repo.ConfiguredEngine, error)
	Runs    segmentStore
	Advance runAdvancer
	Books   bookReader
	// Open yields the book's bytes with random access. Always through the
	// library handle, never os.Open(book.Path), which is how device push
	// on S3 libraries was once silently broken.
	Open    func(context.Context, model.Book) (storage.Source, error)
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
//
// An empty dataPath yields an empty directory, which every caller reads
// as "no staging area is configured". That reading used to be agreed by
// two of the three callers: joining an empty root gives the *relative*
// path audiobooks/{book_id}, so the segment worker created it under the
// process working directory and wrote hundreds of megabytes into it,
// while the sweeper that reclaims staging had already decided there was
// nothing to reclaim (#207).
func StagingDir(dataPath, bookID string) string {
	if dataPath == "" {
		return ""
	}
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
		return ErrEngineDisabledForJob
	}
	// Before the engine call, deliberately. By the time staging is
	// written the audio has been bought, so a job that cannot stage must
	// find that out while a retry is still free (#207).
	dir := StagingDir(deps.DataPath, a.BookID)
	if dir == "" {
		return fmt.Errorf("audiobook segment %d: no staging area configured", a.Seq)
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

// recordSegment hands a segment's result to the module that owns what
// follows from it.
//
// Deliberately does not return an error. A failure here is worth a log
// and nothing more: returning it would hand River a segment to retry
// whose audio is already staged and already paid for.
func recordSegment(ctx context.Context, deps SegmentDeps, a jobs.AudiobookSegmentArgs, res model.SegmentResult) {
	if err := deps.Advance.AdvanceAfterSegment(ctx, a.BookID, a.Seq, res); err != nil {
		slog.Warn("audiobook: advance run", "book", a.BookID, "seq", a.Seq, "err", err)
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

	text, err := segmentText(ctx, book, run, a.Seq, deps)
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
// source of truth for what a character range contains. Two things make
// that safe. The split uses the run's own cap, not the live settings
// value, so an admin editing the setting mid-run cannot hand the
// remaining jobs a different division of the same book. And the range the
// planner stored is compared against the range re-extraction produced: a
// file edited under the run moves every offset after the edit while
// keeping its segment count, which the count comparison this replaced
// waved through and narrated from text nobody planned (#189).
func segmentText(
	ctx context.Context,
	book model.Book,
	run model.Audiobook,
	seq int,
	deps SegmentDeps,
) (string, error) {
	planned, err := deps.Runs.GetSegment(ctx, book.ID, seq)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return "", fmt.Errorf("%w: segment %d is not in this run's plan", tts.ErrPermanent, seq)
		}
		return "", fmt.Errorf("load segment %d: %w", seq, err)
	}

	src, err := deps.Open(ctx, book)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", book.ID, err)
	}
	defer func() { _ = src.Close() }()

	segs, err := service.SegmentBook(ctx, src, run.SegmentChars)
	if err != nil {
		return "", fmt.Errorf("%w: re-extract %s: %v", tts.ErrPermanent, book.ID, err)
	}
	text, err := service.SegmentTextAt(segs, planned)
	if err != nil {
		return "", fmt.Errorf("%w: %v", tts.ErrPermanent, err)
	}
	return text, nil
}
