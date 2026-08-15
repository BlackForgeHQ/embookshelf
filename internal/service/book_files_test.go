// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
)

type listerFunc func(ctx context.Context, bookID string) ([]model.File, error)

func (f listerFunc) ListByBook(ctx context.Context, bookID string) ([]model.File, error) {
	return f(ctx, bookID)
}

// The files seam's one absence policy (#346): a nil lister answers "no
// rows" everywhere — it used to be four separately-written degrades. A
// lookup that FAILED is a different thing, and the seam keeps the
// difference where a caller can act on it.
func TestBookFilesAbsencePolicy(t *testing.T) {
	ctx := context.Background()
	book := model.Book{ID: "b1", Format: "EPUB"}
	none := bookFiles{}

	if locs, err := none.locations(ctx, "b1"); err != nil || len(locs) != 0 {
		t.Errorf("nil lister locations = (%v, %v), want empty and no error", locs, err)
	}
	if _, found := none.byID(ctx, "b1", "f1"); found {
		t.Error("nil lister byID found a row")
	}
	if _, found, err := none.primary(ctx, book); found || err != nil {
		t.Errorf("nil lister primary = (found=%v, err=%v), want the no-rows degrade", found, err)
	}
	if h := none.primaryHash(ctx, book); h != nil {
		t.Errorf("nil lister primaryHash = %x, want nil (reads as fresh)", h)
	}

	broken := bookFiles{lister: listerFunc(func(context.Context, string) ([]model.File, error) {
		return nil, errors.New("db unavailable")
	})}
	// The two arms whose callers act on the difference keep the error…
	if _, err := broken.locations(ctx, "b1"); err == nil {
		t.Error("a failed listing read as an empty book — the delete would drop the byte cleanup silently")
	}
	if _, _, err := broken.primary(ctx, book); err == nil {
		t.Error("a failed primary lookup read as an absent file")
	}
	// …and the two advisory arms fold, by contract.
	if _, found := broken.byID(ctx, "b1", "f1"); found {
		t.Error("a failed byID lookup claimed a row")
	}
	if h := broken.primaryHash(ctx, book); h != nil {
		t.Errorf("a failed hash lookup returned %x, want nil (advisory)", h)
	}
}
