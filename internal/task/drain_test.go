// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/task"
)

// intList returns a list func that produces sequential batches from items.
// Each call to the returned function returns the next batchSize elements
// of items that haven't yet been returned, until exhausted.
func intList(items []int) func(ctx context.Context, batchSize int) ([]int, error) {
	idx := 0
	return func(_ context.Context, batchSize int) ([]int, error) {
		if idx >= len(items) {
			return nil, nil
		}
		end := idx + batchSize
		if end > len(items) {
			end = len(items)
		}
		batch := items[idx:end]
		idx = end
		return batch, nil
	}
}

// keyOfInt converts an int to a string key.
func keyOfInt(i int) string { return fmt.Sprintf("%d", i) }

// TestDrain_emptyList — list always returns empty; returns (0, nil); process never called.
func TestDrain_emptyList(t *testing.T) {
	t.Parallel()
	called := false
	n, err := task.Drain(
		context.Background(),
		task.DrainConfig{Name: "empty"},
		func(_ context.Context, _ int) ([]int, error) { return nil, nil },
		keyOfInt,
		func(_ context.Context, _ int) error { called = true; return nil },
	)
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	if n != 0 {
		t.Fatalf("got n=%d, want 0", n)
	}
	if called {
		t.Fatal("process was called but should not have been")
	}
}

// TestDrain_happyPath — list returns 5, then 3, then 0; all succeed; returns (8, nil).
func TestDrain_happyPath(t *testing.T) {
	t.Parallel()
	items := make([]int, 8)
	for i := range items {
		items[i] = i
	}
	list := intList(items)
	n, err := task.Drain(
		context.Background(),
		task.DrainConfig{Name: "happy", BatchSize: 5},
		list,
		keyOfInt,
		func(_ context.Context, _ int) error { return nil },
	)
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	if n != 8 {
		t.Fatalf("got n=%d, want 8", n)
	}
}

// TestDrain_perItemFailure — list returns 3 items, process fails on item[1] only.
// Returns (2, nil). Failed item's key appears in log output.
func TestDrain_perItemFailure(t *testing.T) {
	t.Parallel()

	// Capture slog output.
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(slog.Default()) })

	items := []int{10, 20, 30}
	list := intList(items)
	processErr := errors.New("boom")

	n, err := task.Drain(
		context.Background(),
		task.DrainConfig{Name: "per-item-fail"},
		list,
		keyOfInt,
		func(_ context.Context, item int) error {
			if item == 20 {
				return processErr
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	if n != 2 {
		t.Fatalf("got n=%d, want 2", n)
	}
	// Verify the failed key ("20") was logged.
	logOutput := buf.String()
	if !strings.Contains(logOutput, "20") {
		t.Fatalf("expected key '20' in log output, got:\n%s", logOutput)
	}
}

// TestDrain_persistentFailureNoProgress — list keeps returning the same 2 items,
// process always fails. Returns (0, nil) after one batch (no infinite loop).
func TestDrain_persistentFailureNoProgress(t *testing.T) {
	t.Parallel()
	callCount := 0
	alwaysFail := errors.New("always fail")

	n, err := task.Drain(
		context.Background(),
		task.DrainConfig{Name: "no-progress"},
		func(_ context.Context, _ int) ([]int, error) {
			callCount++
			// Always return the same 2 items so the predicate never exhausts.
			return []int{1, 2}, nil
		},
		keyOfInt,
		func(_ context.Context, _ int) error { return alwaysFail },
	)
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	if n != 0 {
		t.Fatalf("got n=%d, want 0", n)
	}
	// list should have been called at most twice: once for the first batch
	// (all fail → skip set grows), once for the second batch (all skipped
	// → progress==0 → stop). With the current implementation, the first
	// batch failing sets the skip set and progress==0 fires immediately,
	// so list is called exactly once.
	if callCount > 3 {
		t.Fatalf("list called %d times; expected at most 3 (no infinite loop)", callCount)
	}
}

// TestDrain_ctxCancellation — cancel ctx mid-run, expect ctx error.
func TestDrain_ctxCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())

	calls := 0
	n, err := task.Drain(
		ctx,
		task.DrainConfig{Name: "cancel"},
		func(_ context.Context, _ int) ([]int, error) {
			calls++
			if calls == 2 {
				cancel()
			}
			return []int{calls}, nil
		},
		keyOfInt,
		func(_ context.Context, _ int) error { return nil },
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got err=%v, want context.Canceled", err)
	}
	_ = n // partial progress is fine
}

// TestDrain_skipSetPerCall — two consecutive Drain calls share no state;
// first call's skipped items don't poison the second.
func TestDrain_skipSetPerCall(t *testing.T) {
	t.Parallel()
	alwaysFail := errors.New("fail")

	// First call: all items fail → 0 processed.
	n1, err := task.Drain(
		context.Background(),
		task.DrainConfig{Name: "skip-state"},
		intList([]int{1, 2, 3}),
		keyOfInt,
		func(_ context.Context, _ int) error { return alwaysFail },
	)
	if err != nil {
		t.Fatalf("first call: got error %v, want nil", err)
	}
	if n1 != 0 {
		t.Fatalf("first call: n=%d, want 0", n1)
	}

	// Second call: same items, process succeeds → all 3 processed.
	n2, err := task.Drain(
		context.Background(),
		task.DrainConfig{Name: "skip-state"},
		intList([]int{1, 2, 3}),
		keyOfInt,
		func(_ context.Context, _ int) error { return nil },
	)
	if err != nil {
		t.Fatalf("second call: got error %v, want nil", err)
	}
	if n2 != 3 {
		t.Fatalf("second call: n=%d, want 3 (skip set should be fresh)", n2)
	}
}

// TestDrain_listError — list returns (nil, errBoom); expect (0, errBoom).
func TestDrain_listError(t *testing.T) {
	t.Parallel()
	errBoom := errors.New("boom list")
	n, err := task.Drain(
		context.Background(),
		task.DrainConfig{Name: "list-err"},
		func(_ context.Context, _ int) ([]int, error) { return nil, errBoom },
		keyOfInt,
		func(_ context.Context, _ int) error { return nil },
	)
	if !errors.Is(err, errBoom) {
		t.Fatalf("got err=%v, want errBoom", err)
	}
	if n != 0 {
		t.Fatalf("got n=%d, want 0", n)
	}
}

// TestDrain_nilFuncArgs — passing nil for any of list/keyOf/process returns (0, nil)
// without panic.
func TestDrain_nilFuncArgs(t *testing.T) {
	t.Parallel()
	cfg := task.DrainConfig{Name: "nil-args"}
	list := func(_ context.Context, _ int) ([]int, error) { return []int{1}, nil }
	key := keyOfInt
	proc := func(_ context.Context, _ int) error { return nil }

	for _, tc := range []struct {
		name    string
		list    func(context.Context, int) ([]int, error)
		keyOf   func(int) string
		process func(context.Context, int) error
	}{
		{"nil list", nil, key, proc},
		{"nil keyOf", list, nil, proc},
		{"nil process", list, key, nil},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n, err := task.Drain(context.Background(), cfg, tc.list, tc.keyOf, tc.process)
			if err != nil {
				t.Fatalf("%s: got error %v, want nil", tc.name, err)
			}
			if n != 0 {
				t.Fatalf("%s: got n=%d, want 0", tc.name, n)
			}
		})
	}
}
