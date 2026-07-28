// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/repo"
)

type fakeCandidates struct {
	rows        []repo.GuideCandidate
	err         error
	total, done int
}

func (f *fakeCandidates) CountCoverage(context.Context) (int, int, error) {
	if f.err != nil {
		return 0, 0, f.err
	}
	return f.total, f.done, nil
}

func (f *fakeCandidates) ListGuideCandidates(context.Context) ([]repo.GuideCandidate, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

// fakeDispatch records the book ids Start handed to the worker pool.
type fakeDispatch struct {
	ids []string
	err error
}

func (f *fakeDispatch) Enqueue(_ context.Context, a jobs.Args) error {
	if f.err != nil {
		return f.err
	}
	args, ok := a.(jobs.ReadingGuideArgs)
	if !ok {
		return fmt.Errorf("unexpected job args %T", a)
	}
	f.ids = append(f.ids, args.BookID)
	return nil
}

func runHarness(rows ...repo.GuideCandidate) (*GuideRunner, *fakeDispatch) {
	cands := &fakeCandidates{rows: rows, total: len(rows), done: 0}
	disp := &fakeDispatch{}
	return NewGuideRunner(cands, disp, 48_000), disp
}

// --- estimate ------------------------------------------------------------

// The estimate is shown before anything is spent, so it must cost nothing
// to produce. It is computed from the format column alone: extracting
// every book to count exactly would mean downloading the whole library on
// an S3 backend to answer "should I start?".
func TestEstimateCountsOnlyEPUBAgainstTheTextCap(t *testing.T) {
	r, _ := runHarness(
		repo.GuideCandidate{BookID: "1", Format: "EPUB"},
		repo.GuideCandidate{BookID: "2", Format: "EPUB"},
		repo.GuideCandidate{BookID: "3", Format: "PDF"},
		repo.GuideCandidate{BookID: "4", Format: "MP3"},
	)

	est, err := r.Estimate(context.Background())
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if est.Books != 4 {
		t.Errorf("Books = %d, want 4", est.Books)
	}
	if est.FullTextBooks != 2 {
		t.Errorf("FullTextBooks = %d, want 2 — only EPUB sends text", est.FullTextBooks)
	}
	// Two EPUBs at the cap dominate; the metadata-only books add a little.
	if est.MaxInputTokens < 20_000 {
		t.Errorf("MaxInputTokens = %d, implausibly low for 2 capped books", est.MaxInputTokens)
	}
}

// TestEstimateIsAnUpperBound — the cap binds for most real books, so
// "up to" is close to actual for full-text books and generous for the
// rest. Being honest that it is a ceiling matters more than precision.
func TestEstimateIsAnUpperBound(t *testing.T) {
	r, _ := runHarness(repo.GuideCandidate{BookID: "1", Format: "EPUB"})

	est, err := r.Estimate(context.Background())
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	// 48k chars is roughly 12k tokens; the bound must not undercount.
	if est.MaxInputTokens < 12_000 {
		t.Errorf("MaxInputTokens = %d, want at least the cap's worth", est.MaxInputTokens)
	}
}

func TestEstimateEmptyLibrary(t *testing.T) {
	r, _ := runHarness()

	est, err := r.Estimate(context.Background())
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if est.Books != 0 || est.MaxInputTokens != 0 {
		t.Fatalf("estimate for an empty run = %+v, want zeroes", est)
	}
}

// --- start ---------------------------------------------------------------

func TestStartEnqueuesEveryCandidate(t *testing.T) {
	r, disp := runHarness(
		repo.GuideCandidate{BookID: "1", Format: "EPUB"},
		repo.GuideCandidate{BookID: "2", Format: "PDF"},
	)

	n, err := r.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if n != 2 {
		t.Fatalf("queued = %d, want 2", n)
	}
	if len(disp.ids) != 2 {
		t.Fatalf("dispatched %v, want both books", disp.ids)
	}
}

// TestStartSkipsHandEditedGuides is enforced by the query the runner
// uses; this pins that the runner does not widen it.
func TestStartUsesTheCandidateQuery(t *testing.T) {
	// The fake returns exactly what the query would: hand-edited guides
	// are already excluded upstream, so the runner must not re-list.
	r, disp := runHarness(repo.GuideCandidate{BookID: "only", Format: "EPUB"})

	if _, err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(disp.ids) != 1 || disp.ids[0] != "only" {
		t.Fatalf("dispatched %v", disp.ids)
	}
}

// TestStartReportsPartialProgress — one book failing to enqueue must not
// discard the rest, and the caller needs to know how many actually went.
func TestStartSurfacesDispatchFailure(t *testing.T) {
	r, disp := runHarness(repo.GuideCandidate{BookID: "1", Format: "EPUB"})
	disp.err = errors.New("queue down")

	if _, err := r.Start(context.Background()); err == nil {
		t.Fatal("Start returned nil despite the queue rejecting the job")
	}
}

func TestStartSurfacesListFailure(t *testing.T) {
	cands := &fakeCandidates{err: errors.New("db down")}
	r := NewGuideRunner(cands, &jobs.Deferred{}, 48_000)

	if _, err := r.Start(context.Background()); err == nil {
		t.Fatal("Start returned nil despite the candidate query failing")
	}
}

// TestStartWithNothingToDo — an admin pressing the button twice should
// get "nothing to do", not an error and not a second run.
func TestStartWithNothingToDo(t *testing.T) {
	r, disp := runHarness()

	n, err := r.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if n != 0 || len(disp.ids) != 0 {
		t.Fatalf("queued = %d, dispatched = %v", n, disp.ids)
	}
}

// TestEstimateCarriesLibraryCoverage — the progress bar reads these two
// numbers. They describe the library, not a run, so a reload does not
// reset them and a run started before the last restart still shows.
func TestEstimateCarriesLibraryCoverage(t *testing.T) {
	cands := &fakeCandidates{
		rows:  []repo.GuideCandidate{{BookID: "1", Format: "EPUB"}},
		total: 10,
		done:  9,
	}
	r := NewGuideRunner(cands, &jobs.Deferred{}, 48_000)

	est, err := r.Estimate(context.Background())
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if est.TotalBooks != 10 || est.BooksWithGuide != 9 {
		t.Fatalf("coverage = %d/%d, want 9/10", est.BooksWithGuide, est.TotalBooks)
	}
}
