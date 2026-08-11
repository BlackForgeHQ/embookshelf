// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/blackforge/embookshelf/internal/jobs"
)

type failedWrites struct {
	msgs []string
}

func (f *failedWrites) MarkFailed(_ context.Context, _, msg string) error {
	f.msgs = append(f.msgs, msg)
	return nil
}

// TestRenditionRunWritesRowThenMapsVerdict — the loud-failure
// choreography exists once (#302): a failing step's message lands on
// the row before the error returns, and the permanent flag is what
// appends jobs.ErrDoNotRetry. A step cannot return a failure without
// the row being written — the ADR-0033 §5 invariant, structural.
func TestRenditionRunWritesRowThenMapsVerdict(t *testing.T) {
	boom := errors.New("boom")
	prewrapped := fmt.Errorf("already closed: %w", jobs.ErrDoNotRetry)

	cases := map[string]struct {
		step      renditionStep
		wantMsgs  []string
		permanent bool
	}{
		"transient failure writes the row and retries": {
			step:      func(context.Context) (string, bool, error) { return "dial refused", false, boom },
			wantMsgs:  []string{"dial refused"},
			permanent: false,
		},
		"permanent failure writes the row and cancels": {
			step:      func(context.Context) (string, bool, error) { return "the document is refused", true, boom },
			wantMsgs:  []string{"the document is refused"},
			permanent: true,
		},
		"an already-wrapped permanent error is not double-wrapped": {
			step:      func(context.Context) (string, bool, error) { return "closed", true, prewrapped },
			wantMsgs:  []string{"closed"},
			permanent: true,
		},
		"no message skips the row write": {
			step:      func(context.Context) (string, bool, error) { return "", false, boom },
			wantMsgs:  nil,
			permanent: false,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rows := &failedWrites{}
			err := renditionRun(context.Background(), rows, "b1", tc.step)
			if err == nil {
				t.Fatal("want the step's error back")
			}
			if got := errors.Is(err, jobs.ErrDoNotRetry); got != tc.permanent {
				t.Errorf("ErrDoNotRetry = %v, want %v (err %v)", got, tc.permanent, err)
			}
			if len(rows.msgs) != len(tc.wantMsgs) {
				t.Fatalf("row writes = %v, want %v", rows.msgs, tc.wantMsgs)
			}
			for i := range tc.wantMsgs {
				if rows.msgs[i] != tc.wantMsgs[i] {
					t.Errorf("row write %d = %q, want %q", i, rows.msgs[i], tc.wantMsgs[i])
				}
			}
		})
	}

	t.Run("steps run in order and stop at the first failure", func(t *testing.T) {
		rows := &failedWrites{}
		var ran []int
		err := renditionRun(context.Background(), rows, "b1",
			func(context.Context) (string, bool, error) { ran = append(ran, 1); return "", false, nil },
			func(context.Context) (string, bool, error) { ran = append(ran, 2); return "second failed", false, boom },
			func(context.Context) (string, bool, error) { ran = append(ran, 3); return "", false, nil },
		)
		if err == nil || len(ran) != 2 || ran[0] != 1 || ran[1] != 2 {
			t.Fatalf("ran = %v, err = %v", ran, err)
		}
		if len(rows.msgs) != 1 || rows.msgs[0] != "second failed" {
			t.Fatalf("row writes = %v", rows.msgs)
		}
	})

	t.Run("all steps green is nil and writes nothing", func(t *testing.T) {
		rows := &failedWrites{}
		if err := renditionRun(context.Background(), rows, "b1",
			func(context.Context) (string, bool, error) { return "", false, nil },
		); err != nil || len(rows.msgs) != 0 {
			t.Fatalf("err = %v, writes = %v", err, rows.msgs)
		}
	})
}
