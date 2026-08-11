// SPDX-License-Identifier: AGPL-3.0-or-later

package model

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
