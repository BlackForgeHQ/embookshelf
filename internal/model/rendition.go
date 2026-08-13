// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import "bytes"

// RenditionState is the lifecycle of one book's derived rendition —
// Markdown (ADR-0033) and generated EPUB (ADR-0034) share it. Far
// simpler than the audiobook's: conversion is one HTTP call, so there
// is no cancel and no segment machinery.
type RenditionState string

const (
	RenditionPending RenditionState = "pending"
	RenditionRunning RenditionState = "running"
	RenditionReady   RenditionState = "ready"
	RenditionFailed  RenditionState = "failed"
)

// AllRenditionStates enumerates the lifecycle for tests that quantify
// over every state, the same job AllAudiobookStates does.
func AllRenditionStates() []RenditionState {
	return []RenditionState{RenditionPending, RenditionRunning, RenditionReady, RenditionFailed}
}

// Stale reports whether a derived artifact's recorded source hash no
// longer matches the book's current file — the one staleness predicate
// behind every "made from an older copy" badge (markdown, EPUB,
// narration alike). Answerable only when both hashes exist: an empty
// hash on either side reads fresh, because a badge shown on a
// comparison that never happened is a lie — stated here once, not at
// each call site. Staleness labels, never auto-invalidates.
func Stale(current, recorded []byte) bool {
	if len(current) == 0 || len(recorded) == 0 {
		return false
	}
	return !bytes.Equal(current, recorded)
}

// CanBeStale reports whether a row in this state may receive a
// staleness verdict at all — ready only. Every other state has no
// artifact anything was made from a comparison against: a pending or
// running row has not produced one yet, and a failed row's recorded
// hash (if any) describes an attempt, not a result. Declared beside
// Stale so every caller asks the same question instead of restating
// its own gate — the handler used to gate on ready, the feed relied on
// an upstream switch reaching the same answer implicitly, and neither
// agreed in writing with the other (#322).
func (s RenditionState) CanBeStale() bool {
	return s == RenditionReady
}

// RenditionTransition is one worker write of a rendition row, with the
// states it may move the row out of — the audiobook Transition's shape,
// for the audiobook Transition's reason: a write that arrives late must
// not undo a conclusion reached while it was in flight.
type RenditionTransition struct {
	To   RenditionState
	From []RenditionState
}

// Admits reports whether a row in this state may take the transition —
// the guard itself, answered where From is declared. The repo renders
// the same question as SQL array membership, and a parity test holds
// the two together.
func (t RenditionTransition) Admits(state RenditionState) bool {
	for _, from := range t.From {
		if from == state {
			return true
		}
	}
	return false
}

// FromStrings renders From as the argument to the SQL guard's array
// membership — the rendering, never a second statement of the set.
func (t RenditionTransition) FromStrings() []string {
	out := make([]string, 0, len(t.From))
	for _, s := range t.From {
		out = append(out, string(s))
	}
	return out
}

// RenditionWrite declares the transition behind one worker write.
//
// Every From is the same set, and that is the rule rather than an
// accident: ready is sealed. A ready row records an artifact a consumer
// may be reading, and the only thing allowed to reopen it is Start —
// the user asking for a regeneration. A late MarkFailed from a
// superseded job lands as a refused no-op instead of overwriting the
// conclusion (the bug class book_audiobook.go's #210 records).
func RenditionWrite(to RenditionState) RenditionTransition {
	return RenditionTransition{
		To:   to,
		From: []RenditionState{RenditionPending, RenditionRunning, RenditionFailed},
	}
}
