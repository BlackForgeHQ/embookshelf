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
	Engine string
	Voice  string
	Model  string
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
	if state == AudiobookReady || state == AudiobookCanceled {
		return AudiobookNextNothing
	}
	switch {
	case cov.Complete():
		return AudiobookNextFinalize
	case cov.Settled() && cov.Failed > 0 && state != AudiobookFailed:
		return AudiobookNextFail
	default:
		return AudiobookNextNothing
	}
}
