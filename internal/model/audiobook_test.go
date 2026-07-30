// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import "testing"

// Admits is the transition guard, stated where the transition declares
// what it moves from. It is quantified over every state the model knows
// rather than a hand-listed few, because the states it must *refuse* are
// the interesting half and a literal list here would go stale the day a
// sixth state is added.
func TestTransitionAdmitsExactlyTheStatesItDeclares(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		tr   Transition
	}{
		{"live states", Transition{To: AudiobookCanceled, From: LiveStates()}},
		{"a single state", Transition{To: AudiobookRunning, From: []AudiobookState{AudiobookPending}}},
		{"every state", Transition{To: AudiobookFailed, From: AllAudiobookStates()}},
		// No "any" value exists, and an empty From is the degenerate case
		// of that: it admits nothing, exactly as `state = ANY('{}')` does.
		{"nothing", Transition{To: AudiobookReady}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			declared := make(map[AudiobookState]bool, len(tc.tr.From))
			for _, s := range tc.tr.From {
				declared[s] = true
			}
			for _, state := range AllAudiobookStates() {
				if got := tc.tr.Admits(state); got != declared[state] {
					t.Errorf("Admits(%q) = %v, want %v — the guard disagrees with From %v",
						state, got, declared[state], tc.tr.From)
				}
			}
		})
	}
}

// NextForRun is the whole subsystem's rule — CONTEXT calls Coverage the
// run's authority on its own lifecycle — and until now every cell of it
// was reached only through the service's fakes, so the matrix was
// sampled rather than covered (#206).
func TestNextForRun(t *testing.T) {
	t.Parallel()

	complete := AudiobookCoverage{Total: 3, Done: 3}
	settledWithFailures := AudiobookCoverage{Total: 3, Done: 2, Failed: 1}
	outstanding := AudiobookCoverage{Total: 3, Done: 1}
	noPlan := AudiobookCoverage{}

	for _, tc := range []struct {
		name  string
		state AudiobookState
		cov   AudiobookCoverage
		want  AudiobookNext
		why   string
	}{
		{"running, every segment landed", AudiobookRunning, complete, AudiobookNextFinalize,
			"the run is one finalize away from a finished book"},
		{"pending, every segment landed", AudiobookPending, complete, AudiobookNextFinalize,
			"a crash between dispatch and the state write must still recover"},
		{"running, settled with failures", AudiobookRunning, settledWithFailures, AudiobookNextFail,
			"nothing is outstanding and some of it failed"},
		{"running, segments outstanding", AudiobookRunning, outstanding, AudiobookNextNothing,
			"a finalize here would assemble a part-book"},
		{"running, no plan yet", AudiobookRunning, noPlan, AudiobookNextNothing,
			"zero total is not complete"},

		// The three conclusions the docstring says outrank Coverage.
		{"ready", AudiobookReady, complete, AudiobookNextNothing,
			"a ready run already has its file"},
		{"canceled with complete coverage", AudiobookCanceled, complete, AudiobookNextNothing,
			"a canceled run must not be resurrected (ADR-0028 §6)"},
		{"failed, settled with failures", AudiobookFailed, settledWithFailures, AudiobookNextNothing,
			"a failed run does not need its failure recorded twice"},

		// The cell this test was written for. A run whose every segment
		// landed but whose *finalize* failed keeps complete Coverage
		// forever, so the rule answered Finalize on every read — and it
		// is read on every book-detail load. Each answer re-assembled a
		// file that can be half a gigabyte, failed again, and re-marked
		// failed, unbounded and unlogged.
		{"failed at finalize, coverage still complete", AudiobookFailed, complete, AudiobookNextNothing,
			"reconcile-on-read must not retry finalize forever; Retry is the route back"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NextForRun(tc.state, tc.cov); got != tc.want {
				t.Errorf("NextForRun(%s, %+v) = %q, want %q — %s",
					tc.state, tc.cov, got, tc.want, tc.why)
			}
		})
	}
}
