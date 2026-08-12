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

// The three state sets the subsystem reasons in. Each is declared once
// here and consulted — never restated — by the rules below, by both
// workers, by the retry path and by the two SQL predicates that ask the
// same question inside a write. "Which states mean stop" used to be
// written five times in three languages, so changing what a state meant
// was a five-site hunt with one site in SQL (#252).
//
// They nest: Live and Terminal partition the states, and Sealed is
// Terminal minus failed. That one-state difference is the whole reason
// there are two dispositions rather than one — see NextForRecovery.

// LiveStates are the states a run can still move out of under its own
// steam: it has not concluded. The guard for every transition that a
// concluded run must not accept.
func LiveStates() []AudiobookState {
	return []AudiobookState{AudiobookPending, AudiobookRunning}
}

// TerminalStates are the states a run has concluded in: no further work
// will happen without a new user action.
//
// Rendered into SQL twice — the staging sweep's reclaim predicate and
// Start's replaceable-run guard — through StateStrings, for the reason
// Transition.FromStrings exists: the question has to be askable inside
// the statement, and a hand-written IN list is a copy nothing holds to
// this declaration. `repo` tests the two agree for every state.
func TerminalStates() []AudiobookState {
	return []AudiobookState{AudiobookReady, AudiobookFailed, AudiobookCanceled}
}

// SealedStates are the states whose outcome is the one the user has, and
// which nothing may add to: ready already has its file, and canceled was
// stopped on purpose and must not be resurrected (ADR-0028 §6).
//
// Narrower than TerminalStates by exactly one state, and that state is
// the point. A failed run has concluded, but it is also precisely what a
// user pressing Retry is asking to recover, so it belongs in one set and
// not the other. Conflating the two is what #206 was: reconcile-on-read
// treating "concluded" and "beyond recovery" as one question re-dispatched
// a failed run's finalize on every page load.
func SealedStates() []AudiobookState {
	return []AudiobookState{AudiobookReady, AudiobookCanceled}
}

// Terminal reports whether no further work will happen without a new
// user action.
func (s AudiobookState) Terminal() bool {
	return stateIn(TerminalStates(), s)
}

// Sealed reports that no action on this run — automatic or explicitly
// asked for — may add to its outcome. See SealedStates.
func (s AudiobookState) Sealed() bool {
	return stateIn(SealedStates(), s)
}

// CanBeStale reports whether a run in this state may receive a
// staleness verdict — ready only, the parallel of RenditionState's
// CanBeStale. A failed or canceled run's outcome is already settled,
// and nothing about that outcome was ever compared against the book's
// current file, so a hash the run happens to carry cannot answer a
// question staleness never asked of it. Before this gate existed, the
// preflight wrapper had no state check at all, and a failed or
// canceled run computed a verdict against whatever hash it had (#322).
func (s AudiobookState) CanBeStale() bool {
	return s == AudiobookReady
}

func stateIn(set []AudiobookState, s AudiobookState) bool {
	for _, member := range set {
		if member == s {
			return true
		}
	}
	return false
}

// StateStrings renders a state set as the argument to a SQL predicate's
// array membership — the one language the question can be asked in
// inside a locked write. The rendering, never a second statement of the
// set: every caller passes one of the declarations above.
func StateStrings(states []AudiobookState) []string {
	out := make([]string, 0, len(states))
	for _, s := range states {
		out = append(out, string(s))
	}
	return out
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

// Retrying is a first-class state rather than a flavour of failure, for
// the reason Canceled is one on the run: a segment the queue is going to
// try again is outstanding, and a segment that has given up is settled,
// and Coverage has to be able to tell them apart. Recording both as
// Failed meant a sibling landing while a retry was still in flight
// counted it as a settled failure and concluded the run — after which the
// successful retry was a no-op, because the disposition refuses to act on
// a failed run (ADR-0032).
const (
	SegmentPending  SegmentState = "pending"
	SegmentRunning  SegmentState = "running"
	SegmentRetrying SegmentState = "retrying"
	SegmentDone     SegmentState = "done"
	SegmentFailed   SegmentState = "failed"
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
//
// A segment the queue is going to try again is neither. It is counted in
// Total and in neither Done nor Failed, so a run holding one is not
// settled and does not conclude: the retry either lands, or exhausts its
// attempts and is recorded Failed, and the conclusion follows from
// whichever happened (ADR-0032).
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
	return StateStrings(t.From)
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

// There are two dispositions, because there are two questions.
//
// NextForRun answers what should happen to a run *on its own*, from a
// write that just landed or a read that just observed it. NextForRecovery
// answers what should happen when a *user explicitly asks* for this run
// to be recovered. They read the same Coverage and consult the same state
// sets, so what a state means is stated once — but they are not one
// function with an exception, and the difference is load-bearing: the
// automatic rule must refuse a failed run, and the explicit one must not.
// Conflating them cost an unbounded re-finalize loop on every page load
// (#206).

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
	if state.Terminal() {
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

// NextForRecovery derives what an explicitly requested recovery — a user
// pressing Retry — needs doing to the run as a whole, before the
// per-segment re-enqueue that is the rest of that action.
//
// Complete Coverage means every Segment landed, so whatever stopped this
// run was finalize, and a finalize is the entire route back. That is the
// answer for a *failed* run too, which is exactly where this parts
// company with NextForRun. The two are not in contradiction: NextForRun
// runs on every book-detail load and must not re-assemble a
// half-gigabyte file forever on a run whose finalize is broken, while
// this runs once, when someone asked (#206).
//
// Sealed outranks Coverage here for the same reasons it does there, and
// they are the only two: ready already has its file, and canceled was
// stopped on purpose — Retry must not be a way around the stop the user
// pressed (ADR-0028 §6).
//
// Nothing is not a refusal of the whole action. It says this recovery has
// no run-wide step to take, and the caller carries on to the outstanding
// segments; the answers that concern them are the segment rows', not this
// rule's.
func NextForRecovery(state AudiobookState, cov AudiobookCoverage) AudiobookNext {
	if state.Sealed() {
		return AudiobookNextNothing
	}
	if cov.Complete() {
		return AudiobookNextFinalize
	}
	return AudiobookNextNothing
}
