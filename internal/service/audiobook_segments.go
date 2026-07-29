// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"fmt"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
)

// SegmentBook splits a book into the units a narration run works through.
//
// Here rather than in fileproc, which owns the splitting itself: what
// this pair adds is run policy — which cap a run is entitled to split at,
// and what counts as the file having changed underneath it. fileproc is a
// format leaf and knows nothing about runs; the segment worker already
// depends on this package for Narratable and ErrNotNarratable, so the
// policy sits with the rest of it.
//
// The only place fileproc.SegmentOptions is constructed. The planner
// splits the book to price and store it; every segment job splits it
// again hours later, because the EPUB stays the single source of truth
// for what a character range contains. Two call sites each carrying their
// own options is how those two splits came to be able to disagree — one
// took the cap from the run and the other from the live settings row
// (#189).
func SegmentBook(ctx context.Context, src storage.Source, maxChars int) ([]fileproc.Segment, error) {
	return fileproc.ExtractEPUBSegments(ctx, src, fileproc.SegmentOptions{
		MaxChars: resolveSegmentChars(maxChars),
	})
}

// SegmentTextAt returns the prose of the segment `want` describes,
// refusing it when the re-extracted book no longer agrees with the range
// the planner stored.
//
// The stored range is the verification, not the segment count. A book
// re-uploaded with a paragraph added keeps its chapter count and moves
// every offset after the edit, which a count comparison waves through and
// narrates from text nobody planned. Comparing the ranges is also what
// makes the alignment map's columns load-bearing rather than merely
// persisted.
func SegmentTextAt(segs []fileproc.Segment, want model.AudiobookSegment) (string, error) {
	if want.Seq < 0 || want.Seq >= len(segs) {
		return "", fmt.Errorf("segment %d no longer exists — the source file changed mid-run", want.Seq)
	}
	got := segs[want.Seq]
	if got.CharStart != want.CharStart || got.CharEnd != want.CharEnd {
		return "", fmt.Errorf(
			"segment %d now covers characters %d–%d but was planned as %d–%d — the source file changed mid-run",
			want.Seq, got.CharStart, got.CharEnd, want.CharStart, want.CharEnd)
	}
	return got.Text, nil
}

// resolveSegmentChars is the cap a run is actually planned at.
//
// One rule, applied in both the places a zero can appear: the split
// itself, and the number the run records. Resolved before it is recorded,
// never after — a zero on the row would leave a segment worker unable to
// tell "planned at the default" from "planned before the column existed",
// and the whole point of pinning the cap is that the worker can reproduce
// the split without guessing (#189).
func resolveSegmentChars(maxChars int) int {
	if maxChars > 0 {
		return maxChars
	}
	return fileproc.DefaultSegmentChars
}
