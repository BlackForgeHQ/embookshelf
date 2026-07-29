// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/storage"
)

// ErrNotNarratable is returned for a book whose format has no text to
// read. EPUB is the only Narratable format: there is no PDF library in
// go.mod, CBZ is images, and MOBI/AZW3/FB2 have no extractor (ADR-0028
// §4).
//
// Unlike a reading guide, there is no degraded mode to fall back to —
// nobody wants a narrated blurb — so this is a gate, not a downgrade.
//
// The message names the formats from the spec table rather than spelling
// EPUB out, because this sentence had four other copies (#192).
var ErrNotNarratable = fmt.Errorf("only %s books can be narrated", model.NarratableFormatList())

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
	// RecordSegment writes one segment's result and returns what the run
	// does next, both under the run row's lock. The narrow declarations
	// the segment worker and the finalize worker each kept over the same
	// repo collapsed into this one (#190).
	RecordSegment(ctx context.Context, bookID string, seq int, res model.SegmentResult) (model.AudiobookOutcome, error)
	GetByBookID(ctx context.Context, bookID string) (model.Audiobook, error)
	// Transition is the one write to the run's state. It reports whether
	// the row moved, which is what keeps the publish that tells the UI to
	// stop polling to exactly one (#210).
	Transition(ctx context.Context, bookID string, t model.Transition) (bool, error)
	ListUnfinishedSegments(ctx context.Context, bookID string) ([]model.AudiobookSegment, error)
	Coverage(ctx context.Context, bookID string) (model.AudiobookCoverage, error)
	Delete(ctx context.Context, bookID string) error
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
	// d is complete by construction: NewAudiobookService fills every
	// field, so nothing below branches on a missing dependency.
	d AudiobookDeps
}

// StagingSweeper discards a run's staged segments.
//
// Injected rather than done inline so this service keeps its property of
// touching neither storage nor the filesystem, which is what lets the
// whole lifecycle be exercised without either.
type StagingSweeper func(bookID string)

// AudiobookDeps is everything the run service reaches for.
//
// One struct rather than three constructor arguments and six chained
// setters. The setters bought partial construction for tests and charged
// every field a nil branch at each use and its own answer to "what does
// absent mean" — six answers to one question, none of them written down
// together (#209). There is one production construction site and it
// supplies all of them.
//
// Absence is now decided once, in newAudiobookService below, and every
// use site can call the field.
type AudiobookDeps struct {
	Store audiobookStore
	// Enqueue is an ordinary jobs.Enqueuer: internal/queue imports this
	// package to build its worker registry, so this package depends on
	// the leaf interface rather than on internal/queue itself, and gets
	// either a live client or a jobs.Deferred standing in for one.
	Enqueue jobs.Enqueuer
	// Books yields a book's bytes. Absent, planning refuses — a narration
	// cannot be split without the text.
	Books bookSourceOpener
	// Settings reads the AUDIOBOOK row, per call rather than captured, so
	// an admin changing engine or voice takes effect on the next request
	// instead of the next restart. Absent means the feature is not
	// configured at all, which Preflight reports as such.
	Settings func(context.Context) (repo.AudiobookConfig, error)
	// Publish emits a run's change over SSE. Absent, transitions are
	// silent: a deployment with no hub still runs, it just does not push.
	Publish func(bookID string)
	// SweepStaging discards a run's staged segments on cancel. Injected
	// rather than done inline so this service keeps its property of
	// touching neither storage nor the filesystem.
	SweepStaging StagingSweeper
	// ContentHash reads the book's current file hash, for provenance and
	// the staleness comparison. Injected because it lives on the files
	// row behind a library handle, which this service deliberately cannot
	// reach (#191). Absent, a narration is never reported stale — the
	// honest answer when the comparison never happened.
	ContentHash func(context.Context, model.Book) []byte
	// Artifacts removes what finalize wrote outside the run's own table.
	Artifacts narrationArtifacts
	// SweepNarration removes a finished narration's bytes.
	SweepNarration func(ctx context.Context, book model.Book, run model.Audiobook) error
}

// noNarrationArtifacts stands in for a service constructed without them:
// there is nothing outside the run's table to remove.
type noNarrationArtifacts struct{}

func (noNarrationArtifacts) DeleteFile(context.Context, string) error     { return nil }
func (noNarrationArtifacts) ClearBookAudio(context.Context, string) error { return nil }

// NewAudiobookService fills in what the caller left out, so that no use
// site below has to ask whether a dependency is there.
//
// Each default is this module's single answer to "what does absent
// mean", stated where the others are rather than at the six call sites
// that used to each carry one.
func NewAudiobookService(d AudiobookDeps) *AudiobookService {
	if d.Publish == nil {
		d.Publish = func(string) {}
	}
	if d.SweepStaging == nil {
		d.SweepStaging = func(string) {}
	}
	if d.ContentHash == nil {
		d.ContentHash = func(context.Context, model.Book) []byte { return nil }
	}
	if d.Settings == nil {
		d.Settings = func(context.Context) (repo.AudiobookConfig, error) {
			return repo.AudiobookConfig{}, ErrAudiobooksNotConfigured
		}
	}
	if d.Books == nil {
		d.Books = openerFunc(func(context.Context, model.Book) (storage.Source, error) {
			return nil, errors.New("audiobook: no book opener configured")
		})
	}
	if d.Artifacts == nil {
		d.Artifacts = noNarrationArtifacts{}
	}
	if d.SweepNarration == nil {
		d.SweepNarration = func(context.Context, model.Book, model.Audiobook) error { return nil }
	}
	return &AudiobookService{d: d}
}

// openerFunc adapts a function to bookSourceOpener, for the refusing
// default above.
type openerFunc func(context.Context, model.Book) (storage.Source, error)

func (f openerFunc) Open(ctx context.Context, book model.Book) (storage.Source, error) {
	return f(ctx, book)
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
	run, err := s.d.Store.GetByBookID(ctx, bookID)
	if err != nil {
		return model.Audiobook{}, model.AudiobookCoverage{}, err
	}
	cov, err := s.d.Store.Coverage(ctx, bookID)
	if err != nil {
		return model.Audiobook{}, model.AudiobookCoverage{}, err
	}
	return s.reconcile(ctx, run, cov), cov, nil
}

// reconcile applies whatever NextForRun derives, returning the run as it
// stands afterwards.
//
// Best effort by construction: a queue that is down costs the recovery,
// not the read — a status endpoint that 500s because finalize could not
// be enqueued would hide the very progress it exists to show.
func (s *AudiobookService) reconcile(ctx context.Context, run model.Audiobook, cov model.AudiobookCoverage) model.Audiobook {
	state, err := s.advance(ctx, run.BookID, run.State, cov)
	if err != nil {
		slog.Warn("audiobook: reconcile", "book", run.BookID, "err", err)
		return run
	}
	if state == model.AudiobookFailed {
		run.State, run.Error = model.AudiobookFailed, cov.FailureMessage()
	}
	return run
}

func (s *AudiobookService) dispatchFinalize(ctx context.Context, bookID string) error {
	return s.d.Enqueue.Enqueue(ctx, jobs.AudiobookFinalizeArgs{BookID: bookID})
}

// Narratable reports whether a book's format can be read aloud. Checked
// at three points like Send-to-Kindle's Eligible format — the UI button,
// the handler, and the worker — because a re-import can change a book's
// format between enqueue and dispatch.
//
// The set itself is model.FormatSpecs. This stays as the name the
// audiobook code calls, so the three gates read the same as they did,
// but it no longer knows which formats qualify (#192).
func Narratable(format string) bool {
	return model.Narratable(format)
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
		SegmentChars:      resolveSegmentChars(opts.SegmentChars),
		SourceContentHash: opts.SourceContentHash,
		TotalChars:        chars,
	}
	if err := s.d.Store.Start(ctx, run, segments); err != nil {
		return fmt.Errorf("persist audiobook plan: %w", err)
	}

	// Running before dispatch, not after. A segment that lands while the
	// rest are still being enqueued can drive the run forward, and a
	// trailing write would put it back — the window is milliseconds
	// against a synthesis measured in seconds, but ordering it this way
	// costs nothing and removes the question (#210).
	s.transition(ctx, book.ID, model.Transition{
		To:   model.AudiobookRunning,
		From: []model.AudiobookState{model.AudiobookPending},
	})
	return s.dispatchAll(ctx, book.ID, segments)
}

// dispatchAll enqueues every segment, failing the run if the queue is
// unavailable. A run left at pending with no jobs is invisible: it shows
// 0% forever and no error explains why.
func (s *AudiobookService) dispatchAll(ctx context.Context, bookID string, segments []model.AudiobookSegment) error {
	for _, seg := range segments {
		if err := s.d.Enqueue.Enqueue(ctx, jobs.AudiobookSegmentArgs{BookID: bookID, Seq: seg.Seq}); err != nil {
			// %w rather than %v: jobs.ErrNoQueue now actually flows through
			// here, and wrapping it lets a caller tell "queue is down" apart
			// from "engine rejected the text" with errors.Is. Same text
			// either way — %w formats identically to %v in Error().
			wrapped := fmt.Errorf("could not queue segment %d: %w", seg.Seq, err)
			// FailRun logs rather than returning: what the caller needs to
			// hear about is the dispatch failure it is already returning.
			_ = s.FailRun(ctx, bookID, wrapped.Error())
			return wrapped
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
	src, err := s.d.Books.Open(ctx, book)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", book.ID, err)
	}
	defer func() { _ = src.Close() }()

	raw, err := SegmentBook(ctx, src, opts.SegmentChars)
	if err != nil {
		return nil, fmt.Errorf("segment %s: %w", book.ID, err)
	}

	out := make([]model.AudiobookSegment, 0, len(raw))
	for _, seg := range raw {
		out = append(out, model.AudiobookSegment{
			BookID:       book.ID,
			Seq:          seg.Seq,
			ChapterIndex: seg.ChapterIndex,
			ChapterTitle: seg.ChapterTitle,
			// The offsets come from the splitter rather than being
			// recomputed here, so the range a worker re-extracts and the
			// range stored on the row are the same arithmetic.
			CharStart: seg.CharStart,
			CharEnd:   seg.CharEnd,
			State:     model.SegmentPending,
		})
	}
	return out, nil
}

// Cancel stops a run in flight. The segment workers check this state
// before each engine call, which makes it the only way to stop spending
// on a run that is already going.
func (s *AudiobookService) Cancel(ctx context.Context, bookID string) error {
	run, err := s.d.Store.GetByBookID(ctx, bookID)
	if err != nil {
		return err
	}
	if run.State.Terminal() {
		return fmt.Errorf("audiobook for %s is already %s", bookID, run.State)
	}
	s.transition(ctx, bookID, model.Transition{
		To:   model.AudiobookCanceled,
		From: model.LiveStates(),
	})
	// Swept immediately, unlike a failure. A user who pressed stop does
	// not want the partial, so holding half a gigabyte for the seven-day
	// retry window would be the failure semantics exactly inverted
	// (ADR-0028 §6). State first: if the sweep panics or the process dies
	// between the two, a cancelled run with stale staging is recoverable
	// and a running run with no staging is not.
	s.d.SweepStaging(bookID)
	return nil
}

// Retry re-enqueues the segments that never finished, and only those.
//
// Re-running the completed ones would buy the same audio twice — the
// whole reason segments are rows rather than a counter (ADR-0028 §6).
func (s *AudiobookService) Retry(ctx context.Context, bookID string) error {
	run, err := s.d.Store.GetByBookID(ctx, bookID)
	if err != nil {
		return err
	}
	cov, err := s.d.Store.Coverage(ctx, bookID)
	if err != nil {
		return err
	}
	// Checked before the already-running guard, and before the
	// nothing-outstanding refusal below, because a stranded run is
	// precisely a run whose state says running and whose segments say
	// done. Both of those guards fired on it, which is how the one thing
	// the user could still press told them there was nothing to do.
	//
	// Coverage complete means every Segment landed, so whatever stopped
	// this run was finalize. Asked directly rather than through
	// NextForRun, because that rule deliberately answers Nothing for a
	// failed run now — reconcile-on-read must not retry finalize on every
	// page load, and this is the route back that replaces it (#206).
	// Ready already has its file; canceled was stopped on purpose and
	// must not be resurrected (ADR-0028 §6). Everything else with
	// complete Coverage is a run that only needs finalize.
	if cov.Complete() && run.State != model.AudiobookReady && run.State != model.AudiobookCanceled {
		return s.dispatchFinalize(ctx, bookID)
	}
	if run.State == model.AudiobookRunning {
		return fmt.Errorf("audiobook for %s is already running", bookID)
	}
	outstanding, err := s.d.Store.ListUnfinishedSegments(ctx, bookID)
	if err != nil {
		return err
	}
	if len(outstanding) == 0 {
		return fmt.Errorf("audiobook for %s has no outstanding segments to retry", bookID)
	}
	// Before dispatch, for the reason Start gives.
	s.transition(ctx, bookID, model.Transition{
		To:   model.AudiobookRunning,
		From: []model.AudiobookState{model.AudiobookPending, model.AudiobookFailed},
	})
	return s.dispatchAll(ctx, bookID, outstanding)
}
