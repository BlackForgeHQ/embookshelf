// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"reflect"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/provider"
)

// This is the parity test for the enrichment half of the lock
// vocabulary — the projection that failed most quietly. Missing a
// writability check here did not break a build or a request; it just
// meant a field the user had locked was overwritten by the next provider
// match, which is the one outcome the lock exists to prevent.
//
// Rather than restate the checks, the test probes ApplyMatch: for each
// model.LockSpec it applies a fully populated match with that lock set
// and with it clear, and asks whether the lock changed the outcome. Every
// declared lock must land in exactly one of the three buckets below, so
// adding a lock field to model.LockSpecs without deciding what enrichment
// does about it fails here.

// enrichUnsourced are locks that provider.Match cannot supply, so
// ApplyMatch has nothing to gate. The lock is still real — it shields the
// field from other write paths — but no writability check belongs here.
var enrichUnsourced = map[model.LockField]string{
	model.LockSubtitle: "provider.Match has no subtitle",
	model.LockMoods:    "moods are a local curation field; no provider emits them",
	model.LockTags:     "provider categories land in Genres, not Tags",
	model.LockPages:    "provider.Match has no page count",
}

// enrichSideEffect are locks that gate something other than a books
// column, so a book-to-book comparison cannot observe them. Cover gates
// the ImportCoverFromURL call, whose failure ApplyMatch deliberately
// swallows; the fake harness has no allow-listed host to fetch from, so
// the check is pinned by TestApplyMatchCoverLockNamed instead.
var enrichSideEffect = map[model.LockField]string{
	model.LockCover: "gates the cover import side effect, not a books column",
}

// isbn13Match and isbn10Match differ only in ISBN width. ApplyMatch routes
// a single provider ISBN slot to ISBN-13 or ISBN-10 by digit count, so a
// probe using one width can never exercise the other's lock.
func lockProbeMatches() []provider.Match {
	thirteen := fullMatch
	thirteen.ISBN = "9780306406157"
	ten := fullMatch
	ten.ISBN = "0306406152"
	return []provider.Match{thirteen, ten}
}

// applyWithLocks runs ApplyMatch on a blank book with exactly the given
// lock set, returning the merged book.
func applyWithLocks(t *testing.T, m provider.Match, locked ...model.LockField) model.Book {
	t.Helper()
	svc, _ := applyHarness(t)
	var book model.Book
	book.ID = "b1"
	for _, f := range locked {
		book.Locks.Set(f, true)
	}
	out, err := svc.ApplyMatch(context.Background(), book, m, ApplyOptions{}, TriggerManualEdit)
	if err != nil {
		t.Fatalf("ApplyMatch: %v", err)
	}
	return out
}

// TestApplyMatchHonoursEveryLock is the exhaustive half: every lock in
// model.LockSpecs either demonstrably changes what ApplyMatch writes, or
// is declared above as unsourced / side-effecting.
func TestApplyMatchHonoursEveryLock(t *testing.T) {
	matches := lockProbeMatches()

	// Baseline per match: nothing locked.
	baselines := make([]model.Book, len(matches))
	for i, m := range matches {
		baselines[i] = applyWithLocks(t, m)
	}

	for _, spec := range model.LockSpecs {
		f := spec.Field

		observed := false
		for i, m := range matches {
			got := applyWithLocks(t, m, f)
			// Locks themselves differ by construction; compare the
			// metadata only.
			got.Locks = model.BookLocks{}
			base := baselines[i]
			base.Locks = model.BookLocks{}
			if !reflect.DeepEqual(got, base) {
				observed = true
				break
			}
		}

		_, unsourced := enrichUnsourced[f]
		_, sideEffect := enrichSideEffect[f]

		switch {
		case observed && (unsourced || sideEffect):
			t.Errorf("lock %q changes what ApplyMatch writes, but is declared as unsourced/side-effecting — remove it from that list", f)
		case !observed && !unsourced && !sideEffect:
			t.Errorf("lock %q is declared in model.LockSpecs but ApplyMatch ignores it: add a writable(model.Lock%s, ...) check, or declare it in enrichUnsourced/enrichSideEffect with a reason",
				f, f)
		}
	}
}

// TestApplyMatchCoverLockNamed pins the one lock the probe cannot see.
// With the cover lock set, ApplyMatch must not reach the cover store even
// when the caller asked for the cover and the match carries a URL.
func TestApplyMatchCoverLockNamed(t *testing.T) {
	svc, _ := applyHarness(t)
	covers := &fakeCoverStore{}
	svc.covers = covers

	m := fullMatch
	m.CoverURL = "https://books.google.com/books/content?id=x"

	var book model.Book
	book.ID = "b1"
	book.Locks.Set(model.LockCover, true)

	if _, err := svc.ApplyMatch(context.Background(), book, m,
		ApplyOptions{ApplyCover: true}, TriggerManualEdit); err != nil {
		t.Fatalf("ApplyMatch: %v", err)
	}
	if len(covers.saved) > 0 {
		t.Errorf("cover lock set but %d cover bytes reached the store", len(covers.saved))
	}
}

// TestEnrichLockBucketsAreDisjoint keeps the two declaration lists from
// overlapping, which would make the switch above unreadable.
func TestEnrichLockBucketsAreDisjoint(t *testing.T) {
	for f := range enrichUnsourced {
		if _, dup := enrichSideEffect[f]; dup {
			t.Errorf("lock %q declared both unsourced and side-effecting", f)
		}
	}
	for f := range enrichUnsourced {
		if _, ok := model.ParseLockField(string(f)); !ok {
			t.Errorf("enrichUnsourced names %q, which is not a declared lock field", f)
		}
	}
	for f := range enrichSideEffect {
		if _, ok := model.ParseLockField(string(f)); !ok {
			t.Errorf("enrichSideEffect names %q, which is not a declared lock field", f)
		}
	}
}
