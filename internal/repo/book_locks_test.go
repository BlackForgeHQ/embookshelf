// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"strconv"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
)

// The repo half of the lock parity check. These are pure string
// assertions — no database — because what they guard is the agreement
// between four renderings of one declaration, not any query result.

// TestBookColsCarriesEveryLock asserts the shared SELECT list names every
// declared lock column exactly once, in model.LockSpecs order. scanBook
// binds model.LockFlags at the matching offset, so an extra or missing
// column here is a crossed row, not a compile error.
func TestBookColsCarriesEveryLock(t *testing.T) {
	want := lockColumnList("b.")
	if !strings.Contains(bookCols, want) {
		t.Fatalf("bookCols does not contain the derived lock block\nwant: %s", want)
	}
	for _, c := range model.LockColumns() {
		if n := strings.Count(bookCols, "b."+c); n != 1 {
			t.Errorf("bookCols names b.%s %d times, want 1", c, n)
		}
	}
	// Nothing may name a *_locked column outside the derived block.
	if got, want := strings.Count(bookCols, "_locked"), len(model.LockSpecs); got != want {
		t.Errorf("bookCols has %d _locked columns, want %d", got, want)
	}
}

// TestLockColumnListOrder pins the qualifier handling and the order.
func TestLockColumnListOrder(t *testing.T) {
	got := strings.Split(lockColumnList("b."), ", ")
	cols := model.LockColumns()
	if len(got) != len(cols) {
		t.Fatalf("lockColumnList rendered %d columns, want %d", len(got), len(cols))
	}
	for i, c := range cols {
		if got[i] != "b."+c {
			t.Errorf("lockColumnList()[%d] = %q, want %q", i, got[i], "b."+c)
		}
	}
	// Unqualified rendering is what RETURNING-style contexts would use.
	if bare := strings.Split(lockColumnList(""), ", "); bare[0] != cols[0] {
		t.Errorf("lockColumnList(\"\")[0] = %q, want %q", bare[0], cols[0])
	}
}

// TestLockSetListNumbering is the UPDATE half: placeholders must run
// consecutively from the first free slot, in model.LockSpecs order, so
// the SET list and model.LockValues line up and the trailing id lands
// past the end of the lock block.
func TestLockSetListNumbering(t *testing.T) {
	const first = 24
	parts := strings.Split(lockSetList(first), ", ")
	cols := model.LockColumns()
	if len(parts) != len(cols) {
		t.Fatalf("lockSetList rendered %d assignments, want %d", len(parts), len(cols))
	}
	for i, c := range cols {
		want := c + " = $" + strconv.Itoa(first+i)
		if parts[i] != want {
			t.Errorf("lockSetList()[%d] = %q, want %q", i, parts[i], want)
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
