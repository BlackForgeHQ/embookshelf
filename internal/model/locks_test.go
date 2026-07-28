// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"reflect"
	"testing"
)

// wantLockSpecs is the exhaustive lock vocabulary, restated here so that
// adding a lock field is a deliberate two-line change rather than a
// silent one. Same shape as internal/queue's registry_test.go: the
// registry declares, the test insists the declaration was intended.
var wantLockSpecs = []struct {
	field  LockField
	column string
	// flag names the BookLocks field this spec must drive. Checked by
	// reflection, so a copy-pasted closure pointing at the neighbouring
	// flag fails here instead of crossing two locks on every request.
	flag string
}{
	{LockTitle, "title_locked", "Title"},
	{LockSubtitle, "subtitle_locked", "Subtitle"},
	{LockAuthor, "author_locked", "Author"},
	{LockDescription, "description_locked", "Description"},
	{LockPublisher, "publisher_locked", "Publisher"},
	{LockSeries, "series_locked", "Series"},
	{LockISBN, "isbn_locked", "ISBN"},
	{LockISBN10, "isbn10_locked", "ISBN10"},
	{LockLanguage, "language_locked", "Language"},
	{LockPublishDate, "publish_date_locked", "PublishDate"},
	{LockGenres, "genres_locked", "Genres"},
	{LockMoods, "moods_locked", "Moods"},
	{LockTags, "tags_locked", "Tags"},
	{LockPages, "pages_locked", "Pages"},
	{LockCover, "cover_locked", "Cover"},
}

// TestLockSpecsExhaustive pins the declaration itself: every lock the
// binary knows is listed above, in order, with the column and the flag
// the rest of the system will derive from it.
func TestLockSpecsExhaustive(t *testing.T) {
	if len(LockSpecs) != len(wantLockSpecs) {
		t.Fatalf("LockSpecs has %d entries, want %d: add the new lock to wantLockSpecs (and confirm every projection derives from LockSpecs)",
			len(LockSpecs), len(wantLockSpecs))
	}
	for i, want := range wantLockSpecs {
		got := LockSpecs[i]
		if got.Field != want.field {
			t.Errorf("LockSpecs[%d].Field = %q, want %q", i, got.Field, want.field)
		}
		if got.Column != want.column {
			t.Errorf("LockSpecs[%d].Column = %q, want %q", i, got.Column, want.column)
		}
	}
}

// TestLockSpecsCoverBookLocks is the structural half of the parity
// check: BookLocks must have exactly one field per spec, and each spec's
// Flag must return that field and no other.
func TestLockSpecsCoverBookLocks(t *testing.T) {
	typ := reflect.TypeOf(BookLocks{})
	if typ.NumField() != len(LockSpecs) {
		// Fatal, not Error: the walk below indexes both lists.
		t.Fatalf("BookLocks has %d fields but LockSpecs declares %d: every lock flag needs a spec, or the projections will not see it",
			typ.NumField(), len(LockSpecs))
	}

	seen := make(map[string]LockField, len(LockSpecs))
	for i, spec := range LockSpecs {
		var l BookLocks
		*spec.Flag(&l) = true

		var set []string
		v := reflect.ValueOf(l)
		for f := 0; f < typ.NumField(); f++ {
			if v.Field(f).Bool() {
				set = append(set, typ.Field(f).Name)
			}
		}
		if len(set) != 1 {
			t.Fatalf("LockSpecs[%d] (%s).Flag set %v, want exactly one field", i, spec.Field, set)
		}
		if prev, dup := seen[set[0]]; dup {
			t.Errorf("LockSpecs[%d] (%s).Flag targets BookLocks.%s, already claimed by %s", i, spec.Field, set[0], prev)
		}
		seen[set[0]] = spec.Field

		if i >= len(wantLockSpecs) {
			continue
		}
		if want := wantLockSpecs[i].flag; set[0] != want {
			t.Errorf("LockSpecs[%d] (%s).Flag targets BookLocks.%s, want %s", i, spec.Field, set[0], want)
		}
	}
}

// TestLockColumnsMatchFlags is the ordering half: LockColumns and
// LockFlags are what the repo binds together, so they must stay the same
// length and the same traversal.
func TestLockColumnsMatchFlags(t *testing.T) {
	var l BookLocks
	cols, flags := LockColumns(), LockFlags(&l)
	if len(cols) != len(flags) || len(cols) != len(LockSpecs) {
		t.Fatalf("LockColumns=%d LockFlags=%d LockSpecs=%d, want all equal", len(cols), len(flags), len(LockSpecs))
	}
	// Each returned flag pointer must be the one its column names.
	for i, spec := range LockSpecs {
		if cols[i] != spec.Column {
			t.Errorf("LockColumns()[%d] = %q, want %q", i, cols[i], spec.Column)
		}
		p, ok := flags[i].(*bool)
		if !ok {
			t.Fatalf("LockFlags()[%d] is %T, want *bool", i, flags[i])
		}
		if p != spec.Flag(&l) {
			t.Errorf("LockFlags()[%d] is not the pointer LockSpecs[%d].Flag returns", i, i)
		}
	}
}

// TestParseLockFieldRoundTrips is the wire half: every declared field
// parses, and parsing is the same lookup Set uses — so a key that
// validates is a key that applies.
func TestParseLockFieldRoundTrips(t *testing.T) {
	for _, spec := range LockSpecs {
		got, ok := ParseLockField(string(spec.Field))
		if !ok {
			t.Errorf("ParseLockField(%q) rejected a declared lock field", spec.Field)
			continue
		}
		var l BookLocks
		if !l.Set(got, true) {
			t.Errorf("BookLocks.Set(%q) reported unknown for a declared lock field", spec.Field)
		}
		if !l.Get(got) {
			t.Errorf("BookLocks.Get(%q) = false after Set(true)", spec.Field)
		}
	}
	if _, ok := ParseLockField("nope"); ok {
		t.Error("ParseLockField accepted an undeclared field")
	}
	var l BookLocks
	if l.Set(LockField("nope"), true) {
		t.Error("BookLocks.Set accepted an undeclared field")
	}
	if l.Get(LockField("nope")) {
		t.Error("BookLocks.Get reported an undeclared field as locked")
	}
}

// TestBookLocksLocked checks the sparse projection the book DTO is built
// from: LockSpecs order, set fields only, nil when nothing is locked.
func TestBookLocksLocked(t *testing.T) {
	var empty BookLocks
	if got := empty.Locked(); got != nil {
		t.Errorf("zero BookLocks.Locked() = %v, want nil", got)
	}

	var l BookLocks
	l.Set(LockCover, true)
	l.Set(LockTitle, true)
	l.Set(LockSeries, true)
	want := []LockField{LockTitle, LockSeries, LockCover}
	if got := l.Locked(); !reflect.DeepEqual(got, want) {
		t.Errorf("Locked() = %v, want %v (LockSpecs order)", got, want)
	}
}
