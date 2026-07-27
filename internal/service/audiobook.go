// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
)

// ErrNotNarratable is returned for a book whose format has no text to
// read. EPUB is the only Narratable format: there is no PDF library in
// go.mod, CBZ is images, and MOBI/AZW3/FB2 have no extractor (ADR-0028
// §4).
//
// Unlike a reading guide, there is no degraded mode to fall back to —
// nobody wants a narrated blurb — so this is a gate, not a downgrade.
var ErrNotNarratable = errors.New("only EPUB books can be narrated")

// charsPerMinuteOfSpeech converts characters to audio duration for the
// pre-flight estimate. Around 150 words per minute at roughly 6
// characters per word, which is where every commercial narrator sits.
// An estimate, and labelled as one — the real duration is measured from
// the frames at finalize.
const charsPerMinuteOfSpeech = 900

// audiobookStore is the slice of BookAudiobookRepo this service needs.
// Narrow so the lifecycle is exercisable without a database.
type audiobookStore interface {
	Start(ctx context.Context, ab model.Audiobook, segments []model.AudiobookSegment) error
	GetByBookID(ctx context.Context, bookID string) (model.Audiobook, error)
	SetState(ctx context.Context, bookID string, state model.AudiobookState, msg string) error
	ListUnfinishedSegments(ctx context.Context, bookID string) ([]model.AudiobookSegment, error)
	Coverage(ctx context.Context, bookID string) (model.AudiobookCoverage, error)
}

// SegmentDispatcher enqueues one segment job.
//
// A function rather than a queue.Client for the same reason GuideRunner
// takes one: internal/queue imports this package, so depending on it here
// would be an import cycle.
type SegmentDispatcher func(ctx context.Context, bookID string, seq int) error

// FinalizeDispatcher enqueues the job that concatenates a finished run.
type FinalizeDispatcher func(ctx context.Context, bookID string) error

// AudiobookDispatch carries both dispatchers to workers that need them.
//
// A mutable holder rather than plain values because of an ordering knot:
// the queue's worker registry is assembled inside queue.New, but the
// dispatchers close over the client that call returns. Passing a pointer
// lets the registry capture it before it is filled and the composition
// root populate it immediately afterwards — the same trick GuideRunner
// sidesteps by being built entirely after queue.New, which a worker
// cannot be.
type AudiobookDispatch struct {
	Segment  SegmentDispatcher
	Finalize FinalizeDispatcher
}

// AudiobookOptions is one run's configuration, resolved from the
// settings row plus whatever the generate dialog overrode.
type AudiobookOptions struct {
	Engine string
	Voice  string
	Model  string
	// SegmentChars bounds one job. Not the engine's per-request cap —
	// the adapter splits a segment into as many engine calls as it needs.
	SegmentChars int
	// PricePerMillionChars prices the estimate. Zero is legitimate: a
	// local engine is free, and quoting the catalog price for it would be
	// a lie in the expensive direction.
	PricePerMillionChars float64
	// SourceContentHash is the hash of the EPUB being narrated, supplied
	// by the caller because it lives on the files row rather than on the
	// book, and this service deliberately cannot reach the files table.
	// Recorded so the UI can later say the audio predates a newer copy.
	SourceContentHash []byte
}

// AudiobookEstimate is what an admin sees before spending anything.
type AudiobookEstimate struct {
	Chars        int
	Segments     int
	AudioSeconds int
	CostUSD      float64
	Engine       string
	Voice        string
}

// AudiobookService owns planning a narration, starting it, and the
// cancel/retry lifecycle around it.
//
// Deliberately does not own synthesis: that is the segment worker's job,
// one River job per segment, so a failure costs one segment rather than a
// book (ADR-0028 §3).
type AudiobookService struct {
	store    audiobookStore
	books    bookSourceOpener
	dispatch SegmentDispatcher
	finalize FinalizeDispatcher
	sweep    StagingSweeper
}

// StagingSweeper discards a run's staged segments.
//
// Injected rather than done inline so this service keeps its property of
// touching neither storage nor the filesystem, which is what lets the
// whole lifecycle be exercised without either.
type StagingSweeper func(bookID string)

func NewAudiobookService(
	store audiobookStore,
	books bookSourceOpener,
	dispatch SegmentDispatcher,
) *AudiobookService {
	return &AudiobookService{store: store, books: books, dispatch: dispatch}
}

// WithStagingSweeper wires the cleanup Cancel performs.
func (s *AudiobookService) WithStagingSweeper(sweep StagingSweeper) *AudiobookService {
	s.sweep = sweep
	return s
}

// WithFinalizeDispatcher wires the enqueue that turns a complete run into
// a published book. Needed here as well as in the segment worker because
// this is where a run that lost its finalize job gets it back.
func (s *AudiobookService) WithFinalizeDispatcher(finalize FinalizeDispatcher) *AudiobookService {
	s.finalize = finalize
	return s
}

// Status reports a run and its Coverage, reconciling the two before it
// answers.
//
// This is where recovery lives, and reconcile-on-read is a deliberate
// choice over the two alternatives. It cannot live in the write alone:
// the write commits to Postgres and the finalize job goes to River, two
// systems no transaction spans, so a crash in between will always be
// possible. And a sweeper would be a second schedule carrying a second
// copy of the completeness rule, running hourly, for an observation that
// is already happening — the status endpoint is polled every four
// seconds while a run is live (ADR-0028 §7) and hit on every book-detail
// page load. Putting it here makes recovery a property of the module:
// every caller that asks how a run is doing gets an answer that is true
// when it arrives, and there is nothing to remember to schedule.
//
// The reconciliation is best effort. A queue that is down costs the
// recovery, not the read — a status endpoint that 500s because finalize
// could not be enqueued would hide the very progress it exists to show.
func (s *AudiobookService) Status(ctx context.Context, bookID string) (model.Audiobook, model.AudiobookCoverage, error) {
	run, err := s.store.GetByBookID(ctx, bookID)
	if err != nil {
		return model.Audiobook{}, model.AudiobookCoverage{}, err
	}
	cov, err := s.store.Coverage(ctx, bookID)
	if err != nil {
		return model.Audiobook{}, model.AudiobookCoverage{}, err
	}
	return s.reconcile(ctx, run, cov), cov, nil
}

// reconcile applies whatever NextForRun derives, returning the run as it
// stands afterwards.
func (s *AudiobookService) reconcile(ctx context.Context, run model.Audiobook, cov model.AudiobookCoverage) model.Audiobook {
	switch model.NextForRun(run.State, cov) {
	case model.AudiobookNextFinalize:
		if err := s.dispatchFinalize(ctx, run.BookID); err != nil {
			slog.Warn("audiobook: reconcile finalize", "book", run.BookID, "err", err)
		}
	case model.AudiobookNextFail:
		msg := cov.FailureMessage()
		if err := s.store.SetState(ctx, run.BookID, model.AudiobookFailed, msg); err != nil {
			slog.Warn("audiobook: reconcile failure", "book", run.BookID, "err", err)
			return run
		}
		run.State, run.Error = model.AudiobookFailed, msg
	case model.AudiobookNextNothing:
	}
	return run
}

func (s *AudiobookService) dispatchFinalize(ctx context.Context, bookID string) error {
	if s.finalize == nil {
		return errors.New("no queue configured for audiobook generation")
	}
	return s.finalize(ctx, bookID)
}

// Narratable reports whether a book's format can be read aloud. Checked
// at three points like Send-to-Kindle's Eligible format — the UI button,
// the handler, and the worker — because a re-import can change a book's
// format between enqueue and dispatch.
func Narratable(format string) bool {
	return strings.EqualFold(strings.TrimSpace(format), "EPUB")
}

// Estimate reports what a run would cost without starting one.
//
// Does the full extraction rather than guessing from file size: the
// number is money, an admin is being asked to authorise it, and a guess
// that is wrong by 3× in the cheap direction is exactly the failure this
// guardrail exists to prevent.
func (s *AudiobookService) Estimate(ctx context.Context, book model.Book, opts AudiobookOptions) (AudiobookEstimate, error) {
	segments, err := s.plan(ctx, book, opts)
	if err != nil {
		return AudiobookEstimate{}, err
	}
	chars := 0
	for _, seg := range segments {
		chars += seg.CharEnd - seg.CharStart
	}
	return AudiobookEstimate{
		Chars:        chars,
		Segments:     len(segments),
		AudioSeconds: chars * 60 / charsPerMinuteOfSpeech,
		CostUSD:      float64(chars) / 1_000_000 * opts.PricePerMillionChars,
		Engine:       opts.Engine,
		Voice:        opts.Voice,
	}, nil
}

// Start plans the run, persists it, and enqueues one job per segment.
//
// Persist-then-dispatch, not the reverse: a job that arrives before its
// row exists has nothing to claim, and River would retry it 25 times
// before anyone noticed. The reverse ordering leaves rows briefly
// pending with no job, which the failure path below cleans up.
func (s *AudiobookService) Start(ctx context.Context, book model.Book, opts AudiobookOptions) error {
	segments, err := s.plan(ctx, book, opts)
	if err != nil {
		return err
	}

	chars := 0
	for _, seg := range segments {
		chars += seg.CharEnd - seg.CharStart
	}
	run := model.Audiobook{
		BookID:            book.ID,
		State:             model.AudiobookPending,
		Engine:            opts.Engine,
		Voice:             opts.Voice,
		Model:             opts.Model,
		SourceContentHash: opts.SourceContentHash,
		TotalChars:        chars,
	}
	if err := s.store.Start(ctx, run, segments); err != nil {
		return fmt.Errorf("persist audiobook plan: %w", err)
	}

	if err := s.dispatchAll(ctx, book.ID, segments); err != nil {
		return err
	}
	return s.store.SetState(ctx, book.ID, model.AudiobookRunning, "")
}

// dispatchAll enqueues every segment, failing the run if the queue is
// unavailable. A run left at pending with no jobs is invisible: it shows
// 0% forever and no error explains why.
func (s *AudiobookService) dispatchAll(ctx context.Context, bookID string, segments []model.AudiobookSegment) error {
	if s.dispatch == nil {
		return errors.New("no queue configured for audiobook generation")
	}
	for _, seg := range segments {
		if err := s.dispatch(ctx, bookID, seg.Seq); err != nil {
			msg := fmt.Sprintf("could not queue segment %d: %v", seg.Seq, err)
			if serr := s.store.SetState(ctx, bookID, model.AudiobookFailed, msg); serr != nil {
				slog.Warn("audiobook: mark failed after dispatch error", "book", bookID, "err", serr)
			}
			return errors.New(msg)
		}
	}
	return nil
}

// plan extracts the book and turns it into the segment rows a run works
// through, carrying the character offsets that become the alignment map.
func (s *AudiobookService) plan(ctx context.Context, book model.Book, opts AudiobookOptions) ([]model.AudiobookSegment, error) {
	if !Narratable(book.Format) {
		return nil, ErrNotNarratable
	}
	if s.books == nil {
		return nil, errors.New("no book opener configured")
	}
	src, err := s.books.Open(ctx, book)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", book.ID, err)
	}
	defer func() { _ = src.Close() }()

	raw, err := fileproc.ExtractEPUBSegments(ctx, src, fileproc.SegmentOptions{MaxChars: opts.SegmentChars})
	if err != nil {
		return nil, fmt.Errorf("segment %s: %w", book.ID, err)
	}

	out := make([]model.AudiobookSegment, 0, len(raw))
	offset := 0
	for _, seg := range raw {
		length := len([]rune(seg.Text))
		out = append(out, model.AudiobookSegment{
			BookID:       book.ID,
			Seq:          seg.Seq,
			ChapterIndex: seg.ChapterIndex,
			ChapterTitle: seg.ChapterTitle,
			// Offsets are contiguous over the narrated text, not over the
			// EPUB's raw bytes: skipped front matter and stripped markup
			// have no audio, so including them would make every later
			// position drift.
			CharStart: offset,
			CharEnd:   offset + length,
			State:     model.SegmentPending,
		})
		offset += length
	}
	return out, nil
}

// Cancel stops a run in flight. The segment workers check this state
// before each engine call, which makes it the only way to stop spending
// on a run that is already going.
func (s *AudiobookService) Cancel(ctx context.Context, bookID string) error {
	run, err := s.store.GetByBookID(ctx, bookID)
	if err != nil {
		return err
	}
	if run.State.Terminal() {
		return fmt.Errorf("audiobook for %s is already %s", bookID, run.State)
	}
	if err := s.store.SetState(ctx, bookID, model.AudiobookCanceled, ""); err != nil {
		return err
	}
	// Swept immediately, unlike a failure. A user who pressed stop does
	// not want the partial, so holding half a gigabyte for the seven-day
	// retry window would be the failure semantics exactly inverted
	// (ADR-0028 §6). State first: if the sweep panics or the process dies
	// between the two, a cancelled run with stale staging is recoverable
	// and a running run with no staging is not.
	if s.sweep != nil {
		s.sweep(bookID)
	}
	return nil
}

// Retry re-enqueues the segments that never finished, and only those.
//
// Re-running the completed ones would buy the same audio twice — the
// whole reason segments are rows rather than a counter (ADR-0028 §6).
func (s *AudiobookService) Retry(ctx context.Context, bookID string) error {
	run, err := s.store.GetByBookID(ctx, bookID)
	if err != nil {
		return err
	}
	cov, err := s.store.Coverage(ctx, bookID)
	if err != nil {
		return err
	}
	// Checked before the already-running guard, and before the
	// nothing-outstanding refusal below, because a stranded run is
	// precisely a run whose state says running and whose segments say
	// done. Both of those guards fired on it, which is how the one thing
	// the user could still press told them there was nothing to do.
	if model.NextForRun(run.State, cov) == model.AudiobookNextFinalize {
		return s.dispatchFinalize(ctx, bookID)
	}
	if run.State == model.AudiobookRunning {
		return fmt.Errorf("audiobook for %s is already running", bookID)
	}
	outstanding, err := s.store.ListUnfinishedSegments(ctx, bookID)
	if err != nil {
		return err
	}
	if len(outstanding) == 0 {
		return fmt.Errorf("audiobook for %s has no outstanding segments to retry", bookID)
	}
	if err := s.dispatchAll(ctx, bookID, outstanding); err != nil {
		return err
	}
	return s.store.SetState(ctx, bookID, model.AudiobookRunning, "")
}
