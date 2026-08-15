// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"

	"github.com/blackforge/embookshelf/internal/model"
)

// Staleness is the badge composition stated once (#340): the artifact's
// CanBeStale gate, then model.Stale over the current-hash lookup. It
// used to be composed three times across two tiers — the handler's
// sourceStale, the audiobook preflight's stale and the markdown feed's —
// each with its own nil-policy comment explaining how it differed from
// the other two.
//
// The one nil policy lives here: a nil hash lookup is an install with no
// library store to read bytes through, and it answers "never stale" —
// the same reads-as-fresh degrade model.Stale already gives an empty
// hash, because a badge shown on a comparison that never happened is a
// lie in the direction that costs money. Callers whose lookup is always
// set (the feed's, the audiobook service's) pass it and pay nothing for
// the check.
type Staleness struct {
	hash func(context.Context, model.Book) []byte
}

// NewStaleness wraps a current-hash lookup — usually NewPrimaryHash's,
// which already owns the warn-and-degrade on an unresolvable library.
// A nil lookup is a legal, degraded instance: everything reads fresh.
func NewStaleness(hash func(context.Context, model.Book) []byte) Staleness {
	return Staleness{hash: hash}
}

// staleGate is what an artifact's state must answer: both
// model.RenditionState and model.AudiobookState do.
type staleGate interface{ CanBeStale() bool }

// Stale answers the badge for one artifact: false unless the state has
// concluded in a shape staleness applies to, the lookup exists, and the
// book's current bytes disagree with the recorded provenance.
func (s Staleness) Stale(ctx context.Context, book model.Book, state staleGate, recorded []byte) bool {
	if state == nil || !state.CanBeStale() || s.hash == nil {
		return false
	}
	return model.Stale(s.hash(ctx, book), recorded)
}
