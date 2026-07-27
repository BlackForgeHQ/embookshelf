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
	"github.com/blackforge/embookshelf/internal/coverstore"
	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/sse"
	"github.com/blackforge/embookshelf/internal/tts"
)

// AudiobookQueue is the River queue narration runs on.
//
// Its own queue rather than the default one because a run is tens of
// long jobs per book: sharing the four default workers would stall
// BookDrop ingest and Library scan for as long as the run lasts
// (ADR-0028 §3).
const AudiobookQueue = "audiobook"

// ErrAudiobooksDisabled is returned when the feature is off. Permanent —
// a disabled feature will still be disabled in thirty seconds.
var ErrAudiobooksDisabled = errors.New("audiobook generation is not enabled")

// errCanceled unwinds a segment whose run was cancelled mid-flight. Never
// recorded as a failure: the run is already in its final state and
// overwriting it would lose the reason it stopped.
var errCanceled = errors.New("audiobook run canceled")

// canceled reports whether the run has been stopped since the job began.
// A read per engine call is cheap next to the call it guards.
func canceled(ctx context.Context, bookID string, deps AudiobookDeps) bool {
	run, err := deps.Audiobooks.GetByBookID(ctx, bookID)
	if err != nil {
		return false
	}
	return run.State == model.AudiobookCanceled
}

// AudiobookSegmentArgs addresses one unit of synthesis.
//
// Book and seq rather than the segment's own id, because that pair is
// what the plan is keyed on and what a Retry re-enqueues; carrying a row
// id would mean a retry could address a row a regeneration has replaced.
type AudiobookSegmentArgs struct {
	BookID string `json:"book_id"`
	Seq    int    `json:"seq"`
}

func (AudiobookSegmentArgs) Kind() string  { return "audiobook.segment" }
func (AudiobookSegmentArgs) Queue() string { return AudiobookQueue }

// AudiobookFinalizeArgs addresses the concatenation of a finished run.
type AudiobookFinalizeArgs struct {
	BookID string `json:"book_id"`
}

func (AudiobookFinalizeArgs) Kind() string  { return "audiobook.finalize" }
func (AudiobookFinalizeArgs) Queue() string { return AudiobookQueue }

// AudiobookDeps groups the seams both workers need.
//
// Settings is read per job rather than captured at boot, so an admin
// changing voice, engine or key takes effect on the next segment instead
// of at the next restart — the same hot-reload the guide worker gets.
type AudiobookDeps struct {
	Settings   *repo.AppSettingsRepo
	Audiobooks *repo.BookAudiobookRepo
	Books      *repo.BookRepo
	Files      *repo.FileRepo
	LibStore   service.LibraryStore
	Covers     *coverstore.Store
	Hub        *sse.Hub
	Dispatch   *service.AudiobookDispatch
	// DataPath roots the staging directory. Per-segment MP3s live on
	// local disk until finalize, outside storage.Storage, following the
	// coverstore precedent for derived bytes.
	DataPath string
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
func AudiobookSegment(ctx context.Context, a AudiobookSegmentArgs, deps AudiobookDeps) error {
	cfg, err := deps.Settings.GetAudiobook(ctx)
	if err != nil {
		return fmt.Errorf("read audiobook settings: %w", err)
	}
	if !cfg.Enabled {
		return ErrAudiobooksDisabled
	}

	run, err := deps.Audiobooks.GetByBookID(ctx, a.BookID)
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

	claimed, err := deps.Audiobooks.MarkSegmentRunning(ctx, a.BookID, a.Seq)
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
		msg := err.Error()
		if merr := deps.Audiobooks.MarkSegmentFailed(ctx, a.BookID, a.Seq, msg); merr != nil {
			slog.Warn("audiobook: mark segment failed", "book", a.BookID, "seq", a.Seq, "err", merr)
		}
		advanceRun(ctx, a.BookID, deps)
		if errors.Is(err, tts.ErrPermanent) || errors.Is(err, service.ErrNotNarratable) {
			// Returning nil stops River retrying something that cannot
			// improve; the failed row and its message carry the outcome.
			publishAudiobook(deps, a.BookID)
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
		msg := fmt.Sprintf("engine returned unusable audio: %v", err)
		_ = deps.Audiobooks.MarkSegmentFailed(ctx, a.BookID, a.Seq, msg)
		advanceRun(ctx, a.BookID, deps)
		publishAudiobook(deps, a.BookID)
		return nil
	}
	if err := os.WriteFile(path, frames, 0o600); err != nil {
		return fmt.Errorf("stage segment %d: %w", a.Seq, err)
	}
	if err := deps.Audiobooks.MarkSegmentDone(ctx, a.BookID, a.Seq, path, durationMS); err != nil {
		return fmt.Errorf("record segment %d: %w", a.Seq, err)
	}

	advanceRun(ctx, a.BookID, deps)
	return nil
}

// synthesizeSegment turns one segment's text into MP3 bytes.
//
// The text is re-extracted rather than stored: the plan holds character
// ranges, and the EPUB is the source of truth for what sits in them. A
// book whose file changed mid-run would otherwise be narrated from stale
// text with no way to notice.
func synthesizeSegment(
	ctx context.Context,
	a AudiobookSegmentArgs,
	cfg repo.AudiobookConfig,
	run model.Audiobook,
	deps AudiobookDeps,
) ([]byte, error) {
	book, err := deps.Books.GetByID(ctx, "", a.BookID)
	if err != nil {
		return nil, fmt.Errorf("load book %s: %w", a.BookID, err)
	}
	if !service.Narratable(book.Format) {
		return nil, fmt.Errorf("%w: %s is %s", service.ErrNotNarratable, book.ID, book.Format)
	}

	sel, err := cfg.SelectEngine()
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

	// A segment is a job, not a request. Every engine caps a single call
	// far below the segment size, so one segment is several calls whose
	// audio is joined the same way the whole book is.
	chunks := fileproc.SplitForSynthesis(text, sel.Info.MaxRequestChars)
	parts := make([][]byte, 0, len(chunks))
	for _, chunk := range chunks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Re-read the run before every call, not once per job. A 40k
		// segment is a dozen engine calls over several minutes, and a
		// cancel that only took effect between segments would keep
		// spending for most of that (ADR-0028 §6).
		if canceled(ctx, a.BookID, deps) {
			return nil, errCanceled
		}
		part, err := sel.Engine.Synthesize(ctx, tts.Request{
			Text:  chunk,
			Voice: run.Voice,
			Model: run.Model,
		})
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return joinParts(parts)
}

func joinParts(parts [][]byte) ([]byte, error) {
	if len(parts) == 1 {
		return parts[0], nil
	}
	var buf []byte
	for i, p := range parts {
		frames, _, err := audio.Payload(p)
		if err != nil {
			return nil, fmt.Errorf("chunk %d: %w", i, err)
		}
		buf = append(buf, frames...)
	}
	return buf, nil
}

// advanceRun moves the run's own state after a segment changed.
//
// Derived from the counts rather than tracked incrementally, so a
// concurrent worker finishing at the same moment cannot leave the run
// disagreeing with its segments. Finalize is dispatched exactly when the
// last segment lands.
func advanceRun(ctx context.Context, bookID string, deps AudiobookDeps) {
	cov, err := deps.Audiobooks.Coverage(ctx, bookID)
	if err != nil {
		slog.Warn("audiobook: coverage", "book", bookID, "err", err)
		return
	}
	switch {
	case cov.Total > 0 && cov.Done == cov.Total:
		if deps.Dispatch == nil || deps.Dispatch.Finalize == nil {
			slog.Warn("audiobook: no finalize dispatcher", "book", bookID)
			return
		}
		if err := deps.Dispatch.Finalize(ctx, bookID); err != nil {
			slog.Warn("audiobook: dispatch finalize", "book", bookID, "err", err)
		}
	case cov.Failed > 0 && cov.Done+cov.Failed == cov.Total:
		// Every segment has been attempted and some did not make it. The
		// staging directory is deliberately left alone: Retry re-enqueues
		// only the missing pieces, so the paid-for ones must survive.
		msg := fmt.Sprintf("%d of %d segments failed", cov.Failed, cov.Total)
		if err := deps.Audiobooks.SetState(ctx, bookID, model.AudiobookFailed, msg); err != nil {
			slog.Warn("audiobook: mark run failed", "book", bookID, "err", err)
		}
		publishAudiobook(deps, bookID)
	}
}

func publishAudiobook(deps AudiobookDeps, bookID string) {
	if deps.Hub != nil {
		_ = deps.Hub.Publish(sse.AudiobookUpdated{BookID: bookID})
	}
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
	deps AudiobookDeps,
) (string, error) {
	opener := service.NewLibraryBookOpener(deps.LibStore)
	src, err := opener.Open(ctx, book)
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
