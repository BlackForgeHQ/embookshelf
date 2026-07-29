// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
)

// The planner and the segment worker split the same book hours apart. One
// function owns the cap so the two cannot disagree about it (#189).
func TestSegmentBookSplitsAtTheCapItIsGiven(t *testing.T) {
	t.Parallel()

	src := buildTestEPUB(t, strings.Repeat("abcdefghij", 100))

	wide, err := SegmentBook(context.Background(), src, 2000)
	if err != nil {
		t.Fatalf("SegmentBook: %v", err)
	}
	narrow, err := SegmentBook(context.Background(), src, 200)
	if err != nil {
		t.Fatalf("SegmentBook: %v", err)
	}

	if len(narrow) <= len(wide) {
		t.Errorf("a 200-char cap produced %d segments and a 2000-char cap %d — the cap is not being applied",
			len(narrow), len(wide))
	}
}

// Zero means the default, matching what a run with no recorded cap was
// planned at.
func TestSegmentBookFallsBackToTheDefaultCap(t *testing.T) {
	t.Parallel()

	src := buildTestEPUB(t, strings.Repeat("abcdefghij", 100))

	zero, err := SegmentBook(context.Background(), src, 0)
	if err != nil {
		t.Fatalf("SegmentBook: %v", err)
	}
	explicit, err := SegmentBook(context.Background(), src, fileproc.DefaultSegmentChars)
	if err != nil {
		t.Fatalf("SegmentBook: %v", err)
	}

	if len(zero) != len(explicit) {
		t.Errorf("a zero cap produced %d segments, the default cap %d", len(zero), len(explicit))
	}
}

func planFor(t *testing.T, text string, maxChars int) []fileproc.Segment {
	t.Helper()
	segs, err := SegmentBook(context.Background(), buildTestEPUB(t, text), maxChars)
	if err != nil {
		t.Fatalf("SegmentBook: %v", err)
	}
	return segs
}

// The happy path: the file has not moved, so the stored range and the
// re-extracted one agree and the prose is handed over.
func TestSegmentTextAtReturnsTheProseThePlanPriced(t *testing.T) {
	t.Parallel()

	segs := planFor(t, strings.Repeat("abcdefghij", 100), 500)
	want := segs[1]

	got, err := SegmentTextAt(segs, model.AudiobookSegment{
		Seq: want.Seq, CharStart: want.CharStart, CharEnd: want.CharEnd,
	})
	if err != nil {
		t.Fatalf("SegmentTextAt: %v", err)
	}
	if got != want.Text {
		t.Errorf("got %d characters, want the planned segment's %d", len(got), len(want.Text))
	}
}

// The verification the count comparison could not make: a book edited
// mid-run can keep its segment count and still move every offset. The
// stored range is what says so, and the message has to name the drift —
// an operator reading "count mismatch" would go looking for a missing
// chapter that is still there.
func TestSegmentTextAtRefusesASegmentWhoseRangeMoved(t *testing.T) {
	t.Parallel()

	segs := planFor(t, strings.Repeat("abcdefghij", 100), 500)
	moved := model.AudiobookSegment{Seq: 1, CharStart: segs[1].CharStart + 40, CharEnd: segs[1].CharEnd + 40}

	_, err := SegmentTextAt(segs, moved)

	if err == nil {
		t.Fatal("SegmentTextAt accepted a segment whose text no longer sits where the plan put it")
	}
	msg := err.Error()
	for _, want := range []string{"1", "changed"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
	if !strings.Contains(msg, "–") && !strings.Contains(msg, "-") {
		t.Errorf("error %q does not name the ranges that disagree", msg)
	}
}

// A book that lost a chapter no longer has the segment the job addresses.
// Narrating segment 12 of a different book is worse than failing.
func TestSegmentTextAtRefusesASegmentThatNoLongerExists(t *testing.T) {
	t.Parallel()

	segs := planFor(t, strings.Repeat("abcdefghij", 100), 500)

	_, err := SegmentTextAt(segs, model.AudiobookSegment{Seq: len(segs), CharStart: 0, CharEnd: 10})

	if err == nil {
		t.Fatal("SegmentTextAt accepted a seq the re-extracted book does not have")
	}
}
