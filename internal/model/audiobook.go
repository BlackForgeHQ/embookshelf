// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"fmt"
	"time"
)

// AudiobookState is where a generation run has got to.
//
// Canceled is a first-class state rather than a flavour of failure
// because the two are treated oppositely: a cancel sweeps the staging
// directory immediately, since the user said stop and does not want the
// partial, while a failure retains every paid-for segment so Retry costs
// nothing (ADR-0028 §6).
type AudiobookState string

const (
	AudiobookPending  AudiobookState = "pending"
	AudiobookRunning  AudiobookState = "running"
	AudiobookReady    AudiobookState = "ready"
	AudiobookFailed   AudiobookState = "failed"
	AudiobookCanceled AudiobookState = "canceled"
)

// AllAudiobookStates is every state a run can be in. Exists so a rule
// quantified over the states — the transition guard's parity test — can
// be written against the model's own list rather than a copy of it that
// a sixth state would not reach.
func AllAudiobookStates() []AudiobookState {
	return []AudiobookState{
		AudiobookPending, AudiobookRunning, AudiobookReady,
		AudiobookFailed, AudiobookCanceled,
	}
}

// Terminal reports whether no further work will happen without a new
// user action.
func (s AudiobookState) Terminal() bool {
	return s == AudiobookReady || s == AudiobookFailed || s == AudiobookCanceled
}

// Audiobook is one book's generated narration.
//
// The audio itself is an ordinary files row in the book's own folder —
// a portable library artifact, not a cache — so this record holds only
// what that row cannot say (ADR-0025).
type Audiobook struct {
	BookID string
	State  AudiobookState
	// Generation is which run of this book's narration this row is, bumped
	// on every start. It is the run's identity, and it exists because
	// (book, seq) is not one: a regeneration wipes the plan and installs
	// another, so sequence 12 of the run that went and sequence 12 of the
	// run that replaced it are one address. Segment jobs carry it and the
	// two writes that touch a segment refuse a mismatch, which is what
	// makes a superseded job a no-op by construction (ADR-0031).
	Generation int
	Engine     string
	Voice      string
	Model      string
	// SegmentChars is the cap the plan was split at, pinned for the same
	// reason engine and voice are: every segment job re-extracts the book
	// from the EPUB, and a cap edited mid-run would hand the remaining
	// jobs a different split of the same text (#189).
	SegmentChars int
	// SourceContentHash is the EPUB this narration was made from.
	// Compared against the book's current hash to tell the user the audio
	// predates their newer copy. Never used to invalidate anything.
	SourceContentHash []byte
	// FileID points at the generated files row. This pointer *is* the
	// provenance — every other files row was ingested.
	FileID     *string
	Error      string
	TotalChars int
	DurationMS int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// SegmentState is where one unit of synthesis has got to.
type SegmentState string

const (
	SegmentPending SegmentState = "pending"
	SegmentRunning SegmentState = "running"
	SegmentDone    SegmentState = "done"
	SegmentFailed  SegmentState = "failed"
)

// AudiobookSegment is one engine call's worth of the book: one River job,
// one retry unit, and one row of the alignment map.
//
// ChapterIndex is not Seq. A long chapter is split across several
// segments sharing a title and a chapter index, because the reader's
// drawer should show the chapter the author wrote rather than the pieces
// an engine's request cap forced on us.
type AudiobookSegment struct {
	ID           string
	BookID       string
	Seq          int
	ChapterIndex int
	ChapterTitle string
	// CharStart and CharEnd index the book's extracted text. Together
	// with StartMS and DurationMS they are the alignment map that lets
	// reading and listening share one progress value.
	CharStart  int
	CharEnd    int
	StartMS    int64
	DurationMS int64
	StagedPath string
	State      SegmentState
	Error      string
}

// AudiobookCoverage is the progress a live run reports: two counts from
// one query, so they cannot be read a moment apart and disagree while
// segments are landing.
type AudiobookCoverage struct {
	Total  int
	Done   int
	Failed int
}

// Complete reports that every segment landed.
func (c AudiobookCoverage) Complete() bool {
	return c.Total > 0 && c.Done == c.Total
}

// Settled reports that no segment is still outstanding — each one either
// landed or gave up.
func (c AudiobookCoverage) Settled() bool {
	return c.Total > 0 && c.Done+c.Failed == c.Total
}

// FailureMessage is what a partially failed run records for the user.
// Lives here rather than at the two call sites that used to build it, so
// the write path and the recovery path cannot phrase the same outcome
// differently.
func (c AudiobookCoverage) FailureMessage() string {
	return fmt.Sprintf("%d of %d segments failed", c.Failed, c.Total)
}

// Transition is one move of a run's state, with the states it is
// allowed to move from.
//
// From is not optional and there is no "any" value: every transition in
// the system names what it expects the run to be, so a write that
// arrives late cannot undo a conclusion reached while it was in flight.
// That was not hypothetical — the → ready write was unguarded, so a
// finalize job already assembling when the user cancelled marked the run
// ready anyway, billing them for a run they stopped (#210).
//
// The repo reports whether the row moved, which is what makes the
// publish fire exactly once: two segments settling a run at the same
// instant both attempt the transition and only one of them moved it.
type Transition struct {
	To    AudiobookState
	From  []AudiobookState
	Error string
	// FileID and DurationMS are written only by the → ready transition,
	// which is the one that has them. Nil leaves the columns alone.
	FileID     *string
	DurationMS *int64
}

// Admits reports whether a run in this state may take the transition —
// the guard itself, answered where From is declared.
//
// The rule is stated once here and rendered wherever it is needed, the
// move the column projection made for column order. It used to be
// reimplemented: as a Go loop inside the service's test double, so tests
// could tell a refused transition from one that happened, while the repo
// spelled the same membership test as a SQL predicate. Nothing forced
// the two to agree, and every transition test asserted against the
// double's copy (#233).
func (t Transition) Admits(state AudiobookState) bool {
	for _, from := range t.From {
		if from == state {
			return true
		}
	}
	return false
}

// FromStrings renders From as the argument to the SQL guard's array
// membership, which is Admits expressed in the one language that can ask
// the question inside the locked write. `repo` tests the two agree for
// every state.
func (t Transition) FromStrings() []string {
	out := make([]string, 0, len(t.From))
	for _, s := range t.From {
		out = append(out, string(s))
	}
	return out
}

// LiveStates are the states a run can still move out of under its own
// steam: it has not concluded. The guard for every transition that a
// concluded run must not accept.
func LiveStates() []AudiobookState {
	return []AudiobookState{AudiobookPending, AudiobookRunning}
}

// SegmentResult is everything a worker learned about one segment.
//
// A value rather than three repo methods, because it is the argument to
// the single operation that records a segment and moves the run: a
// worker states the outcome, and the transition is derived from it.
type SegmentResult struct {
	State      SegmentState
	StagedPath string
	DurationMS int64
	Error      string
}

// AudiobookNext is what a run needs once its coverage has been read.
type AudiobookNext string

const (
	AudiobookNextNothing  AudiobookNext = ""
	AudiobookNextFinalize AudiobookNext = "finalize"
	AudiobookNextFail     AudiobookNext = "fail"
)

// AudiobookOutcome is what recording a segment result did to the run: the
// coverage the write observed, and the transition that follows from it.
type AudiobookOutcome struct {
	Coverage AudiobookCoverage
	Next     AudiobookNext
}

// NextForRun derives a run's transition from its Coverage, consulting the
// state column only to stop work that is already concluded.
//
// Coverage first and state second is the whole point. The segment rows
// are the fact — one row per unit of synthesis, written by the worker
// that did it — while `book_audiobooks.state` is a summary written by a
// second statement, and a process killed between the two left every
// segment done, the run running, and no finalize job. A rule that trusts
// the column cannot recover that run; a rule that reads the segments
// finalizes it on sight, which is what makes the recovery total rather
// than a special case someone has to remember to run.
//
// State is consulted for the three conclusions that outrank coverage: a
// ready run already has its file, a canceled run was stopped on purpose
// and must not be resurrected (ADR-0028 §6), and a failed run does not
// need its failure recorded a second time.
func NextForRun(state AudiobookState, cov AudiobookCoverage) AudiobookNext {
	// All three outrank Coverage, and failed belongs here rather than on
	// the fail arm alone. A run whose every Segment landed but whose
	// *finalize* failed keeps complete Coverage forever, so guarding only
	// the fail arm left this answering Finalize on every read — and it is
	// read on every book-detail load, so each answer re-assembled a file
	// that can be half a gigabyte, failed again, and re-marked failed.
	// Unbounded, unlogged, and contradicted by this function's own
	// docstring (#206).
	//
	// Recovering such a run is Retry's job: an explicit action, once,
	// rather than reconcile-on-read guessing forever.
	if state == AudiobookReady || state == AudiobookCanceled || state == AudiobookFailed {
		return AudiobookNextNothing
	}
	switch {
	case cov.Complete():
		return AudiobookNextFinalize
	case cov.Settled() && cov.Failed > 0:
		return AudiobookNextFail
	default:
		return AudiobookNextNothing
	}
}
