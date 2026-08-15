// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
)

// The badge composition's three arms, stated once for every surface
// that consumes it (#340): the state gate, the no-lookup install
// degrade, and the comparison itself.
func TestStalenessComposition(t *testing.T) {
	book := model.Book{ID: "b1"}
	current := []byte{1, 2, 3}
	hash := func(context.Context, model.Book) []byte { return current }

	t.Run("a state that cannot be stale never compares", func(t *testing.T) {
		called := false
		s := NewStaleness(func(context.Context, model.Book) []byte { called = true; return current })
		if s.Stale(context.Background(), book, model.RenditionPending, []byte{9}) {
			t.Error("a pending rendition was labelled stale")
		}
		if called {
			t.Error("the hash lookup ran for a state the gate excludes")
		}
	})

	t.Run("no lookup reads as fresh — the install degrade", func(t *testing.T) {
		s := NewStaleness(nil)
		if s.Stale(context.Background(), book, model.RenditionReady, []byte{9}) {
			t.Error("an install with no hash lookup labelled a rendition stale")
		}
	})

	t.Run("disagreeing hashes are stale, agreeing ones fresh", func(t *testing.T) {
		s := NewStaleness(hash)
		if !s.Stale(context.Background(), book, model.RenditionReady, []byte{9}) {
			t.Error("a recorded hash that disagrees with the current bytes read as fresh")
		}
		if s.Stale(context.Background(), book, model.RenditionReady, current) {
			t.Error("matching hashes read as stale")
		}
	})

	t.Run("both state vocabularies satisfy the gate", func(t *testing.T) {
		s := NewStaleness(hash)
		if !s.Stale(context.Background(), book, model.AudiobookReady, []byte{9}) {
			t.Error("a ready audiobook run with drifted provenance read as fresh")
		}
		if s.Stale(context.Background(), book, model.AudiobookRunning, []byte{9}) {
			t.Error("a running audiobook run was labelled stale")
		}
	})
}
