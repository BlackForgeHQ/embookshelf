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

// The state sets are what the two dispositions, both workers, the retry
// path and two SQL predicates now share instead of each restating "which
// states mean stop" (#252). Their shape is the thing to hold: Live and
// Terminal partition the states, Sealed is Terminal minus the one state
// an explicit recovery acts on, and a sixth state added to the enum and
// to neither Live nor Terminal is a hole this catches.
func TestStateSetsPartitionAndNest(t *testing.T) {
	t.Parallel()

	for _, state := range AllAudiobookStates() {
		live := stateIn(LiveStates(), state)
		if live == state.Terminal() {
			t.Errorf("%q is live = %v and terminal = %v — a state is exactly one of the two",
				state, live, state.Terminal())
		}
		if state.Sealed() && !state.Terminal() {
			t.Errorf("%q is sealed but not terminal — sealed is the narrower set", state)
		}
	}

	// The one state that separates them, named rather than derived: it is
	// the reason NextForRecovery exists, and a change that let failed seal
	// a run would silently turn Retry into a no-op for the run it was
	// written for.
	if !AudiobookFailed.Terminal() || AudiobookFailed.Sealed() {
		t.Errorf("failed is terminal = %v, sealed = %v — want concluded but recoverable",
			AudiobookFailed.Terminal(), AudiobookFailed.Sealed())
	}
}

// StateStrings is the SQL rendering of a state set, and the only thing
// standing between a declaration here and a predicate in `repo`.
func TestStateStringsRendersEveryMemberInOrder(t *testing.T) {
	t.Parallel()

	got := StateStrings(TerminalStates())
	want := []string{"ready", "failed", "canceled"}
	if len(got) != len(want) {
		t.Fatalf("StateStrings(TerminalStates()) = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("StateStrings(TerminalStates()) = %v, want %v", got, want)
		}
	}
	if len(StateStrings(nil)) != 0 {
		t.Error("StateStrings(nil) must render the empty array, which is the guard that admits nothing")
	}
}

// The two dispositions over every declared state × every shape Coverage
// can take. Quantified rather than sampled: the expectation for a state
// missing from a row is not "the zero value", it is a failure, so adding
// a sixth state without deciding what both rules answer for it breaks
// here rather than in production (#252).
//
// The cells that matter are the ones where the two answers differ. There
// is exactly one shape where they do — complete Coverage on a failed run
// — and that is the whole of the run service's former divergence, now
// stated in the model instead of in a comment explaining a contradiction.
func TestDispositionsOverEveryStateAndCoverage(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		shape    string
		cov      AudiobookCoverage
		run      map[AudiobookState]AudiobookNext
		recovery map[AudiobookState]AudiobookNext
	}{
		{
			shape: "every segment landed",
			cov:   AudiobookCoverage{Total: 3, Done: 3},
			run: map[AudiobookState]AudiobookNext{
				AudiobookPending:  AudiobookNextFinalize,
				AudiobookRunning:  AudiobookNextFinalize,
				AudiobookReady:    AudiobookNextNothing,
				AudiobookFailed:   AudiobookNextNothing,
				AudiobookCanceled: AudiobookNextNothing,
			},
			recovery: map[AudiobookState]AudiobookNext{
				AudiobookPending: AudiobookNextFinalize,
				AudiobookRunning: AudiobookNextFinalize,
				AudiobookReady:   AudiobookNextNothing,
				// The one cell where the two rules part company.
				AudiobookFailed:   AudiobookNextFinalize,
				AudiobookCanceled: AudiobookNextNothing,
			},
		},
		{
			shape: "settled with failures",
			cov:   AudiobookCoverage{Total: 3, Done: 2, Failed: 1},
			run: map[AudiobookState]AudiobookNext{
				AudiobookPending:  AudiobookNextFail,
				AudiobookRunning:  AudiobookNextFail,
				AudiobookReady:    AudiobookNextNothing,
				AudiobookFailed:   AudiobookNextNothing,
				AudiobookCanceled: AudiobookNextNothing,
			},
			// Nothing run-wide: coverage is short, so recovery's business is
			// with the segments that failed, and Retry re-enqueues those.
			recovery: map[AudiobookState]AudiobookNext{
				AudiobookPending:  AudiobookNextNothing,
				AudiobookRunning:  AudiobookNextNothing,
				AudiobookReady:    AudiobookNextNothing,
				AudiobookFailed:   AudiobookNextNothing,
				AudiobookCanceled: AudiobookNextNothing,
			},
		},
		{
			// A retrying segment lands here: counted in Total and in neither
			// column, so the run is neither complete nor settled and neither
			// rule concludes it (ADR-0032).
			shape: "segments still outstanding",
			cov:   AudiobookCoverage{Total: 3, Done: 1},
			run: map[AudiobookState]AudiobookNext{
				AudiobookPending:  AudiobookNextNothing,
				AudiobookRunning:  AudiobookNextNothing,
				AudiobookReady:    AudiobookNextNothing,
				AudiobookFailed:   AudiobookNextNothing,
				AudiobookCanceled: AudiobookNextNothing,
			},
			recovery: map[AudiobookState]AudiobookNext{
				AudiobookPending:  AudiobookNextNothing,
				AudiobookRunning:  AudiobookNextNothing,
				AudiobookReady:    AudiobookNextNothing,
				AudiobookFailed:   AudiobookNextNothing,
				AudiobookCanceled: AudiobookNextNothing,
			},
		},
		{
			shape: "no plan at all",
			cov:   AudiobookCoverage{},
			run: map[AudiobookState]AudiobookNext{
				AudiobookPending:  AudiobookNextNothing,
				AudiobookRunning:  AudiobookNextNothing,
				AudiobookReady:    AudiobookNextNothing,
				AudiobookFailed:   AudiobookNextNothing,
				AudiobookCanceled: AudiobookNextNothing,
			},
			recovery: map[AudiobookState]AudiobookNext{
				AudiobookPending:  AudiobookNextNothing,
				AudiobookRunning:  AudiobookNextNothing,
				AudiobookReady:    AudiobookNextNothing,
				AudiobookFailed:   AudiobookNextNothing,
				AudiobookCanceled: AudiobookNextNothing,
			},
		},
	} {
		t.Run(tc.shape, func(t *testing.T) {
			for _, state := range AllAudiobookStates() {
				want, declared := tc.run[state]
				if !declared {
					t.Fatalf("NextForRun(%q, %v) has no expected answer — a state was added "+
						"without deciding what the automatic rule does with it", state, tc.cov)
				}
				if got := NextForRun(state, tc.cov); got != want {
					t.Errorf("NextForRun(%q, %+v) = %q, want %q", state, tc.cov, got, want)
				}

				want, declared = tc.recovery[state]
				if !declared {
					t.Fatalf("NextForRecovery(%q, %v) has no expected answer — a state was added "+
						"without deciding what a user's explicit Retry does with it", state, tc.cov)
				}
				if got := NextForRecovery(state, tc.cov); got != want {
					t.Errorf("NextForRecovery(%q, %+v) = %q, want %q", state, tc.cov, got, want)
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
