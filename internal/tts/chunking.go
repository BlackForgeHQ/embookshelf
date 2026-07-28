// SPDX-License-Identifier: AGPL-3.0-or-later

package tts

import (
	"context"
	"fmt"

	"github.com/blackforge/embookshelf/internal/audio"
	"github.com/blackforge/embookshelf/internal/textsplit"
)

// speaker is the per-request primitive: one engine call, one piece of
// audio. Each adapter implements this, and chunking is written once
// around it.
type speaker interface {
	speak(ctx context.Context, r Request) ([]byte, error)
	ListVoices(ctx context.Context) ([]Voice, error)
}

// chunked turns a per-request speaker into an Engine that accepts a
// whole segment.
//
// The cap is an implementation detail here rather than a caller's
// homework. ADR-0026 §1 already settled that chunking is engine-specific —
// the catalog's caps differ by a factor of two — so the interface should
// admit it instead of making every caller look the number up.
type chunked struct {
	speaker
	maxChars int
}

// Synthesize narrates a whole segment, one engine call per piece.
func (c chunked) Synthesize(ctx context.Context, r Request) ([]byte, error) {
	pieces := textsplit.OnSentences(r.Text, c.maxChars)
	parts := make([][]byte, 0, len(pieces))
	for _, piece := range pieces {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Before every call, not once per segment. A 40k segment is a
		// dozen calls over several minutes, and a stop that only took
		// effect between segments would keep spending for most of that
		// (ADR-0028 §6). The error is returned unwrapped so the caller
		// can match its own sentinel.
		if r.BeforeChunk != nil {
			if err := r.BeforeChunk(ctx); err != nil {
				return nil, err
			}
		}
		part, err := c.speak(ctx, Request{Text: piece, Voice: r.Voice, Model: r.Model})
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return join(parts)
}

// join concatenates the pieces' MPEG frames.
//
// A single piece is returned untouched, tag and all — the caller strips
// it once when measuring. Only a multi-piece result has to be decoded,
// and that asymmetry is why unusable audio is a permanent failure at one
// chunk and a retryable error at several (#185). Preserved deliberately:
// fixing it here would be a behaviour change inside a refactor.
func join(parts [][]byte) ([]byte, error) {
	if len(parts) == 1 {
		return parts[0], nil
	}
	var buf []byte
	for i, p := range parts {
		frames, _, err := audio.Payload(p)
		if err != nil {
			return nil, fmt.Errorf("chunk %d: %w", i, err)
		}
		buf = append(buf, frames...)
	}
	return buf, nil
}
