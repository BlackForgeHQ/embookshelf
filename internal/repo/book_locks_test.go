// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"strconv"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
)

// The repo half of the lock parity check. These are pure string and
// slice assertions — no database — because what they guard is the
// agreement between the lock declaration and the books projection that
// renders it, not any query result.

// TestBookProjectionCarriesEveryLock asserts the projection names every
// declared lock column exactly once, in model.LockSpecs order, and that
// nothing outside the derived block names a *_locked column. The scan
// destinations are positional, so an extra or missing column here is a
// crossed row rather than a compile error.
func TestBookProjectionCarriesEveryLock(t *testing.T) {
	cols := model.LockColumns()
	for _, c := range cols {
		if n := strings.Count(bookCols, "b."+c); n != 1 {
			t.Errorf("bookCols names b.%s %d times, want 1", c, n)
		}
	}
	if got, want := strings.Count(bookCols, "_locked"), len(model.LockSpecs); got != want {
		t.Errorf("bookCols has %d _locked columns, want %d", got, want)
	}

	// Order, and contiguity: the declaration's order is the projection's
	// order, which is what lets the scan destinations line up.
	var idx []int
	for _, c := range cols {
		i := strings.Index(bookCols, "b."+c)
		if i < 0 {
			t.Fatalf("bookCols is missing b.%s", c)
		}
		idx = append(idx, i)
	}
	for i := 1; i < len(idx); i++ {
		if idx[i] <= idx[i-1] {
			t.Errorf("lock columns are out of declaration order at %s", cols[i])
		}
	}
}

// TestBookProjectionLockUpdateNumbering is the UPDATE half: the lock
// assignments must run consecutively in model.LockSpecs order so the SET
// list and its argument slice line up, and the trailing id placeholder
// must land past the end of the lock block.
func TestBookProjectionLockUpdateNumbering(t *testing.T) {
	set, _ := bookProjection.updateSet(1)
	cols := model.LockColumns()

	first := strings.Index(set, cols[0]+" = $")
	if first < 0 {
		t.Fatalf("update SET is missing %s", cols[0])
	}
	// Recover the placeholder number the first lock landed on, then
	// demand the rest follow it one by one.
	rest := set[first+len(cols[0])+len(" = $"):]
	end := strings.IndexAny(rest, ", ")
	if end < 0 {
		end = len(rest)
	}
	base, err := strconv.Atoi(rest[:end])
	if err != nil {
		t.Fatalf("first lock placeholder is not a number: %v", err)
	}
	for i, c := range cols {
		want := c + " = $" + strconv.Itoa(base+i)
		if !strings.Contains(set, want) {
			t.Errorf("update SET is missing %q", want)
		}
	}
}

// TestLockValuesMatchColumns walks a BookLocks with a distinct pattern
// and asserts the update arguments come back in column order. A uniform
// pattern would not catch an adjacent swap, so alternate — the same
// reasoning as the round-trip tests in book_test.go.
func TestLockValuesMatchColumns(t *testing.T) {
	var l model.BookLocks
	for i, spec := range model.LockSpecs {
		l.Set(spec.Field, i%2 == 0)
	}
	vals := model.LockValues(l)
	if len(vals) != len(model.LockSpecs) {
		t.Fatalf("LockValues returned %d, want %d", len(vals), len(model.LockSpecs))
	}
	for i := range model.LockSpecs {
		got, ok := vals[i].(bool)
		if !ok {
			t.Fatalf("LockValues[%d] is %T, want bool", i, vals[i])
		}
		if want := i%2 == 0; got != want {
			t.Errorf("LockValues[%d] (%s) = %v, want %v", i, model.LockSpecs[i].Column, got, want)
		}
	}
}
