// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

type fakeAudiobookStore struct {
	run        model.Audiobook
	segments   []model.AudiobookSegment
	started    bool
	state      model.AudiobookState
	stateMsg   string
	getErr     error
	startErr   error
	unfinished []model.AudiobookSegment
	coverage   model.AudiobookCoverage
	// gets counts run reads, so a test can prove the advancer trusts the
	// outcome a locked write handed it instead of re-reading.
	gets int
	// recorded and outcome stand in for RecordSegment: what the caller
	// wrote, and what the repo's locked transaction decided follows.
	recorded []model.SegmentResult
	outcome  model.AudiobookOutcome
	deleted  bool
	// onSetState fires on each state write, so a test can assert when it
	// happened relative to the dispatches.
	onSetState func(model.AudiobookState)
	// onDelete records when the row went, so a test can assert the
	// ordering against the byte cleanup rather than just the outcome.
	onDelete func()
}

func (f *fakeAudiobookStore) RecordSegment(
	_ context.Context, _ string, _ int, res model.SegmentResult,
) (model.AudiobookOutcome, error) {
	f.recorded = append(f.recorded, res)
	return f.outcome, nil
}

func (f *fakeAudiobookStore) Start(_ context.Context, ab model.Audiobook, segs []model.AudiobookSegment) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.started = true
	f.run = ab
	f.segments = segs
	return nil
}

func (f *fakeAudiobookStore) GetByBookID(context.Context, string) (model.Audiobook, error) {
	f.gets++
	if f.getErr != nil {
		return model.Audiobook{}, f.getErr
	}
	return f.run, nil
}

// Transition mirrors the repo's guarded write: the run has to be in one
// of the states the caller expects, and the caller is told whether it
// moved. Without the guard here a test could not tell a transition that
// was refused from one that happened (#210).
//
// The guard is asked, not reimplemented. This used to be a Go loop over
// tr.From standing in for a SQL predicate nothing held it to, so every
// transition test in this file asserted against the double's copy of the
// rule; repo's parity test now holds the predicate to the same
// model.Transition.Admits called here (#233).
func (f *fakeAudiobookStore) Transition(
	_ context.Context, _ string, tr model.Transition,
) (bool, error) {
	current := f.state
	if current == "" {
		current = f.run.State
	}
	if !tr.Admits(current) {
		return false, nil
	}
	f.state, f.stateMsg = tr.To, tr.Error
	if f.onSetState != nil {
		f.onSetState(tr.To)
	}
	return true, nil
}

func (f *fakeAudiobookStore) ListUnfinishedSegments(context.Context, string) ([]model.AudiobookSegment, error) {
	return f.unfinished, nil
}

func (f *fakeAudiobookStore) Coverage(context.Context, string) (model.AudiobookCoverage, error) {
	return f.coverage, nil
}

func (f *fakeAudiobookStore) Delete(context.Context, string) error {
	f.deleted = true
	if f.onDelete != nil {
		f.onDelete()
	}
	return nil
}

// recordingEnqueuer captures the payloads a run hands to the pool, so a
// test can assert on what was queued rather than on the arguments of an
// anonymous function.
type recordingEnqueuer struct {
	mu   sync.Mutex
	args []jobs.Args
	err  error
	// onEnqueue fires per accepted job, for tests asserting when a
	// dispatch happened relative to a write.
	onEnqueue func()
}

func (r *recordingEnqueuer) Enqueue(_ context.Context, a jobs.Args) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.args = append(r.args, a)
	if r.onEnqueue != nil {
		r.onEnqueue()
	}
	return nil
}

// segmentDispatch is one queued AudiobookSegmentArgs. Seq and BookID
// travel together so a test can catch a segment addressed at the wrong
// book, not just one dispatched out of order.
type segmentDispatch struct {
	Seq    int
	BookID string
}

// segments returns every segment job queued, in order.
func (r *recordingEnqueuer) segments() []segmentDispatch {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []segmentDispatch
	for _, a := range r.args {
		if s, ok := a.(jobs.AudiobookSegmentArgs); ok {
			out = append(out, segmentDispatch{Seq: s.Seq, BookID: s.BookID})
		}
	}
	return out
}

// finalizes returns the BookID of every finalize job queued. A count
// alone would pass a finalize dispatched for the wrong book; the payload
// this whole seam exists to carry is the point of the assertion.
func (r *recordingEnqueuer) finalizes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []string
	for _, a := range r.args {
		if f, ok := a.(jobs.AudiobookFinalizeArgs); ok {
			ids = append(ids, f.BookID)
		}
	}
	return ids
}

// epubOpener serves one synthetic EPUB for any book.
type epubOpener struct {
	src storage.Source
	err error
}

func (o *epubOpener) Open(context.Context, model.Book) (storage.Source, error) {
	if o.err != nil {
		return nil, o.err
	}
	return o.src, nil
}

// buildTestEPUB assembles a two-chapter EPUB whose prose length is
// controlled by the caller, so a test can force a known segment count.
func buildTestEPUB(t *testing.T, chapterText string) storage.Source {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name, body string) {
		t.Helper()
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	write("META-INF/container.xml", `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`)
	write("OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="c1" href="one.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="two.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="c1"/><itemref idref="c2"/></spine>
</package>`)
	write("OEBPS/nav.xhtml", `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><body>
	<nav><ol>
	  <li><a href="one.xhtml">Chapter One</a></li>
	  <li><a href="two.xhtml">Chapter Two</a></li>
	</ol></nav></body></html>`)
	for _, name := range []string{"one.xhtml", "two.xhtml"} {
		write("OEBPS/"+name, `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><body><p>`+
			chapterText+`</p></body></html>`)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return &byteSource{Reader: bytes.NewReader(buf.Bytes())}
}

func narratableBook() model.Book {
	return model.Book{ID: "b1", LibraryID: "lib1", Format: "EPUB", Title: "A Book", Author: "An Author"}
}

func testOptions() AudiobookOptions {
	return AudiobookOptions{
		Engine:               "openai",
		Voice:                "alloy",
		Model:                "tts-1",
		SegmentChars:         500,
		PricePerMillionChars: 15,
	}
}

// ---------------------------------------------------------------------------
// Estimate — the guardrail on an admin-only, real-money action
// ---------------------------------------------------------------------------

func TestEstimateReportsCharsSegmentsAndMoney(t *testing.T) {
	t.Parallel()

	// 1000 chars per chapter, two chapters ≈ 2000 characters of prose.
	text := strings.Repeat("abcdefghij", 100)
	svc := NewAudiobookService(AudiobookDeps{Store: &fakeAudiobookStore{}, Books: &epubOpener{src: buildTestEPUB(t, text)}, Enqueue: &jobs.Deferred{}})

	est, err := svc.Estimate(context.Background(), narratableBook(), testOptions())
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}

	if est.Chars < 1900 || est.Chars > 2100 {
		t.Errorf("Chars = %d, want ~2000", est.Chars)
	}
	// 500-char segments over ~2000 characters.
	if est.Segments < 4 {
		t.Errorf("Segments = %d, want at least 4 at a 500-char cap", est.Segments)
	}
	// $15 per million characters: 2000 chars is 3 cents. Worked by hand,
	// not by re-running the code's own arithmetic.
	if est.CostUSD < 0.028 || est.CostUSD > 0.032 {
		t.Errorf("CostUSD = %v, want ~0.03", est.CostUSD)
	}
	if est.AudioSeconds <= 0 {
		t.Errorf("AudioSeconds = %d, want a positive duration", est.AudioSeconds)
	}
}

// A local engine costs nothing, and the estimate must say so rather than
// quoting the catalog's cloud price.
func TestEstimateIsFreeAtAZeroPrice(t *testing.T) {
	t.Parallel()

	opts := testOptions()
	opts.PricePerMillionChars = 0
	svc := NewAudiobookService(AudiobookDeps{Store: &fakeAudiobookStore{}, Books: &epubOpener{src: buildTestEPUB(t, strings.Repeat("x ", 500))}, Enqueue: &jobs.Deferred{}})

	est, err := svc.Estimate(context.Background(), narratableBook(), opts)
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if est.CostUSD != 0 {
		t.Errorf("CostUSD = %v, want 0 for a free engine", est.CostUSD)
	}
}

// Only EPUB carries extractable text, and the gate has to hold in the
// service as well as the handler — the worker re-validates for the same
// reason Send-to-Kindle does.
func TestEstimateRefusesANonNarratableFormat(t *testing.T) {
	t.Parallel()

	svc := NewAudiobookService(AudiobookDeps{Store: &fakeAudiobookStore{}, Books: &epubOpener{}, Enqueue: &jobs.Deferred{}})
	book := narratableBook()
	book.Format = "PDF"

	_, err := svc.Estimate(context.Background(), book, testOptions())
	if !errors.Is(err, ErrNotNarratable) {
		t.Fatalf("want ErrNotNarratable for a PDF, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Start — plan, persist, dispatch
// ---------------------------------------------------------------------------

func TestStartPersistsThePlanAndDispatchesEverySegment(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{}
	rec := &recordingEnqueuer{}
	svc := NewAudiobookService(AudiobookDeps{Store: store, Books: &epubOpener{src: buildTestEPUB(t, strings.Repeat("abcdefghij", 100))}, Enqueue: rec})

	if err := svc.Start(context.Background(), narratableBook(), testOptions()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if !store.started {
		t.Fatal("the plan was never persisted")
	}
	if len(store.segments) == 0 {
		t.Fatal("the plan has no segments")
	}
	segs := rec.segments()
	if len(segs) != len(store.segments) {
		t.Errorf("dispatched %d jobs for %d segments", len(segs), len(store.segments))
	}
	// Every segment must be dispatched exactly once, by seq, and
	// addressed at the book it belongs to — a job addresses its work by
	// (book, seq).
	for i, d := range segs {
		if d.Seq != i {
			t.Errorf("dispatched[%d] = seq %d, want %d", i, d.Seq, i)
		}
		if d.BookID != narratableBook().ID {
			t.Errorf("dispatched[%d] book = %q, want %q", i, d.BookID, narratableBook().ID)
		}
	}
	if store.run.Engine != "openai" || store.run.Voice != "alloy" {
		t.Errorf("run records engine/voice %q/%q, want openai/alloy", store.run.Engine, store.run.Voice)
	}
	if store.run.TotalChars <= 0 {
		t.Error("run does not record the character count the estimate was based on")
	}
}

// The cap is pinned on the run for the same reason engine and voice are:
// every segment job re-extracts the book, and an admin editing the
// setting mid-run would otherwise hand the later jobs a different split
// of the same book (#189).
func TestStartRecordsTheCapItPlannedWith(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{}
	svc := NewAudiobookService(AudiobookDeps{Store: store, Books: &epubOpener{src: buildTestEPUB(t, strings.Repeat("abcdefghij", 100))}, Enqueue: &recordingEnqueuer{}})

	if err := svc.Start(context.Background(), narratableBook(), testOptions()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if store.run.SegmentChars != 500 {
		t.Errorf("run records SegmentChars %d, want the 500 it planned with", store.run.SegmentChars)
	}
}

// A caller that names no cap gets the default, and the run says so: a
// zero on the row would leave a worker unable to tell "planned at the
// default" from "planned before the column existed".
func TestStartRecordsTheDefaultCapWhenNoneWasAsked(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{}
	opts := testOptions()
	opts.SegmentChars = 0
	svc := NewAudiobookService(AudiobookDeps{Store: store, Books: &epubOpener{src: buildTestEPUB(t, strings.Repeat("abcdefghij", 100))}, Enqueue: &recordingEnqueuer{}})

	if err := svc.Start(context.Background(), narratableBook(), opts); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if store.run.SegmentChars != fileproc.DefaultSegmentChars {
		t.Errorf("run records SegmentChars %d, want the default %d",
			store.run.SegmentChars, fileproc.DefaultSegmentChars)
	}
}

// Char offsets are the alignment map. Contiguous and monotonic, or the
// text-to-audio correspondence silently points at the wrong paragraph.
func TestStartRecordsAContiguousAlignmentMap(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{}
	rec := &recordingEnqueuer{}
	svc := NewAudiobookService(AudiobookDeps{Store: store, Books: &epubOpener{src: buildTestEPUB(t, strings.Repeat("abcdefghij", 100))}, Enqueue: rec})

	if err := svc.Start(context.Background(), narratableBook(), testOptions()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	prevEnd := 0
	for i, s := range store.segments {
		if s.CharStart != prevEnd {
			t.Errorf("segment %d starts at %d, want %d — the map has a gap", i, s.CharStart, prevEnd)
		}
		if s.CharEnd <= s.CharStart {
			t.Errorf("segment %d spans %d..%d, want a positive range", i, s.CharStart, s.CharEnd)
		}
		prevEnd = s.CharEnd
	}
}

// Chapter titles survive the split, so a long chapter is still one entry
// in the reader's drawer.
func TestStartKeepsChapterIdentityAcrossSplitSegments(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{}
	rec := &recordingEnqueuer{}
	svc := NewAudiobookService(AudiobookDeps{Store: store, Books: &epubOpener{src: buildTestEPUB(t, strings.Repeat("abcdefghij", 200))}, Enqueue: rec})

	if err := svc.Start(context.Background(), narratableBook(), testOptions()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	titles := map[int]string{}
	for _, s := range store.segments {
		if prev, seen := titles[s.ChapterIndex]; seen && prev != s.ChapterTitle {
			t.Errorf("chapter %d has two titles: %q and %q", s.ChapterIndex, prev, s.ChapterTitle)
		}
		titles[s.ChapterIndex] = s.ChapterTitle
	}
	if titles[0] != "Chapter One" {
		t.Errorf("chapter 0 title = %q, want %q", titles[0], "Chapter One")
	}
	if len(store.segments) <= len(titles) {
		t.Fatal("this fixture is meant to split a chapter across several segments")
	}
}

// A dispatch that fails leaves rows claiming work that will never run.
// The run has to be marked failed rather than sitting at pending forever.
func TestStartFailsTheRunWhenDispatchBreaks(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{}
	rec := &recordingEnqueuer{err: errors.New("queue is down")}
	svc := NewAudiobookService(AudiobookDeps{Store: store, Books: &epubOpener{src: buildTestEPUB(t, strings.Repeat("abcdefghij", 100))}, Enqueue: rec})

	if err := svc.Start(context.Background(), narratableBook(), testOptions()); err == nil {
		t.Fatal("want an error when dispatch fails, got nil")
	}
	if store.state != model.AudiobookFailed {
		t.Errorf("run state = %q, want failed", store.state)
	}
	if !strings.Contains(store.stateMsg, "queue is down") {
		t.Errorf("failure message %q does not carry the cause", store.stateMsg)
	}
}

func TestStartRefusesABookWithNoReadableText(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{}
	svc := NewAudiobookService(AudiobookDeps{Store: store, Books: &epubOpener{src: buildTestEPUB(t, "")}, Enqueue: &recordingEnqueuer{}})

	if err := svc.Start(context.Background(), narratableBook(), testOptions()); err == nil {
		t.Fatal("want an error for an EPUB with no prose, got nil")
	}
	if store.started {
		t.Error("an unnarratable book was persisted as a run")
	}
}

// A run that cannot be queued must fail loudly. Left pending with no
// jobs it shows 0% forever and no error explains why — the failure the
// nil-dispatcher guard used to produce silently.
func TestStartFailsTheRunWhenTheQueueIsNotUp(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{}
	svc := NewAudiobookService(AudiobookDeps{Store: store, Books: &epubOpener{src: buildTestEPUB(t, strings.Repeat("abcdefghij", 100))}, Enqueue: &jobs.Deferred{}})

	err := svc.Start(context.Background(), model.Book{ID: "b1", Format: "EPUB"}, AudiobookOptions{SegmentChars: 400})

	if err == nil {
		t.Fatal("Start returned nil with no queue configured")
	}
	if store.state != model.AudiobookFailed {
		t.Errorf("run state = %q, want failed so the UI can say why", store.state)
	}
	if !strings.Contains(store.stateMsg, "no queue configured") {
		t.Errorf("failure message %q does not carry the reason", store.stateMsg)
	}
	// dispatchAll wraps rather than flattens: a caller can tell "the queue
	// was never configured" apart from any other dispatch failure.
	if !errors.Is(err, jobs.ErrNoQueue) {
		t.Errorf("err = %v, want it to unwrap to jobs.ErrNoQueue", err)
	}
}

// ---------------------------------------------------------------------------
// Cancel and Retry
// ---------------------------------------------------------------------------

// Cancel is the only stop-loss on a run that may cost $170, so it must
// take effect on a run that is actively working.
func TestCancelMarksARunningRunCanceled(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{run: model.Audiobook{BookID: "b1", State: model.AudiobookRunning}}
	svc := NewAudiobookService(AudiobookDeps{Store: store, Books: &epubOpener{}, Enqueue: &jobs.Deferred{}})

	if err := svc.Cancel(context.Background(), "b1"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if store.state != model.AudiobookCanceled {
		t.Errorf("state = %q, want canceled", store.state)
	}
}

func TestCancelRefusesAFinishedRun(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{run: model.Audiobook{BookID: "b1", State: model.AudiobookReady}}
	svc := NewAudiobookService(AudiobookDeps{Store: store, Books: &epubOpener{}, Enqueue: &jobs.Deferred{}})

	if err := svc.Cancel(context.Background(), "b1"); err == nil {
		t.Fatal("want an error cancelling a finished run, got nil")
	}
}

// Retry must re-enqueue only what is missing. Re-running the done
// segments would pay a second time for audio already bought — the entire
// reason segments are rows.
func TestRetryDispatchesOnlyUnfinishedSegments(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{
		run: model.Audiobook{BookID: "b1", State: model.AudiobookFailed},
		unfinished: []model.AudiobookSegment{
			{Seq: 3, State: model.SegmentFailed},
			{Seq: 7, State: model.SegmentPending},
		},
	}
	rec := &recordingEnqueuer{}
	svc := NewAudiobookService(AudiobookDeps{Store: store, Books: &epubOpener{}, Enqueue: rec})

	if err := svc.Retry(context.Background(), "b1"); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	segs := rec.segments()
	if len(segs) != 2 || segs[0].Seq != 3 || segs[1].Seq != 7 {
		t.Errorf("dispatched %v, want only the unfinished segments [3 7]", segs)
	}
	for _, d := range segs {
		if d.BookID != "b1" {
			t.Errorf("dispatched seq %d for book %q, want b1", d.Seq, d.BookID)
		}
	}
	if store.state != model.AudiobookRunning {
		t.Errorf("state = %q, want running", store.state)
	}
}

func TestRetryRefusesARunWithNothingLeftToDo(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{
		run:        model.Audiobook{BookID: "b1", State: model.AudiobookFailed},
		unfinished: nil,
		// No plan at all: nothing to finalize and nothing to re-enqueue.
		coverage: model.AudiobookCoverage{},
	}
	svc := NewAudiobookService(AudiobookDeps{Store: store, Books: &epubOpener{}, Enqueue: &recordingEnqueuer{}})

	if err := svc.Retry(context.Background(), "b1"); err == nil {
		t.Fatal("want an error retrying a run with no outstanding segments, got nil")
	}
}

// ---------------------------------------------------------------------------
// Reconcile-on-read — the run whose segments and state disagree (#157)
// ---------------------------------------------------------------------------

// The whole defect in one test. Every segment landed, the process died
// before the finalize job was enqueued, and the state column still says
// running. Nothing else recovers this: River saw a job that succeeded,
// so reading the run has to be what notices.
func TestStatusFinalizesARunStrandedWithCompleteCoverage(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{
		run:      model.Audiobook{BookID: "b1", State: model.AudiobookRunning},
		coverage: model.AudiobookCoverage{Total: 12, Done: 12},
	}
	rec := &recordingEnqueuer{}
	svc := NewAudiobookService(AudiobookDeps{Store: store, Books: &epubOpener{}, Enqueue: rec})

	run, cov, err := svc.Status(context.Background(), "b1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if ids := rec.finalizes(); len(ids) != 1 || ids[0] != "b1" {
		t.Fatalf("finalize dispatched %v, want exactly one for b1 — the run is stranded", ids)
	}
	if cov.Done != 12 || cov.Total != 12 {
		t.Errorf("coverage = %d/%d, want 12/12", cov.Done, cov.Total)
	}
	// The status read must not invent a state of its own; finalize is what
	// moves the run to ready, once the file actually exists.
	if run.State != model.AudiobookRunning {
		t.Errorf("state = %q, want it left to finalize", run.State)
	}
}

// A run still working is not stranded, and dispatching finalize for it
// would concatenate a book that is missing its last chapters.
func TestStatusLeavesARunWithOutstandingSegmentsAlone(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{
		run:      model.Audiobook{BookID: "b1", State: model.AudiobookRunning},
		coverage: model.AudiobookCoverage{Total: 12, Done: 5},
	}
	rec := &recordingEnqueuer{}
	svc := NewAudiobookService(AudiobookDeps{Store: store, Books: &epubOpener{}, Enqueue: rec})

	if _, _, err := svc.Status(context.Background(), "b1"); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if ids := rec.finalizes(); len(ids) != 0 {
		t.Fatalf("finalize dispatched %v for a run at %d/%d", ids, 5, 12)
	}
	if store.state != "" {
		t.Errorf("state written as %q, want a live run left alone", store.state)
	}
}

// Cancel is the only stop-loss on a $170 run. A cancel that arrived after
// the last segment landed must stay a cancel — publishing the partial
// would be the ADR-0028 §6 semantics exactly inverted.
func TestStatusDoesNotResurrectACanceledRun(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{
		run:      model.Audiobook{BookID: "b1", State: model.AudiobookCanceled},
		coverage: model.AudiobookCoverage{Total: 12, Done: 12},
	}
	rec := &recordingEnqueuer{}
	svc := NewAudiobookService(AudiobookDeps{Store: store, Books: &epubOpener{}, Enqueue: rec})

	if _, _, err := svc.Status(context.Background(), "b1"); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if ids := rec.finalizes(); len(ids) != 0 {
		t.Fatalf("a canceled run was finalized: %v", ids)
	}
}

// Every segment has been attempted and some gave up: the run fails, and
// it says how many. Staging is not this seam's to touch — failure keeps
// the paid-for work (ADR-0028 §6), which the absence of a sweep here is
// what guarantees.
func TestStatusFailsARunWhoseSegmentsHaveAllSettledWithFailures(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{
		run:      model.Audiobook{BookID: "b1", State: model.AudiobookRunning},
		coverage: model.AudiobookCoverage{Total: 12, Done: 9, Failed: 3},
	}
	rec := &recordingEnqueuer{}
	swept := 0
	var published []string
	svc := NewAudiobookService(AudiobookDeps{
		Store: store, Books: &epubOpener{}, Enqueue: rec,
		SweepStaging: func(string) { swept++ },
		Publish:      func(bookID string) { published = append(published, bookID) },
	})

	run, _, err := svc.Status(context.Background(), "b1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if store.state != model.AudiobookFailed {
		t.Fatalf("state = %q, want failed", store.state)
	}
	if !strings.Contains(store.stateMsg, "3 of 12") {
		t.Errorf("failure message %q does not say how much was lost", store.stateMsg)
	}
	if run.State != model.AudiobookFailed || run.Error != store.stateMsg {
		t.Errorf("returned run = %q/%q, want the reconciled failure", run.State, run.Error)
	}
	if ids := rec.finalizes(); len(ids) != 0 {
		t.Errorf("an incomplete run was finalized: %v", ids)
	}
	if swept != 0 {
		t.Error("a failed run's staging was swept — Retry would have to buy nine segments again")
	}
	if len(published) != 1 {
		t.Errorf("published %d times, want one so an open client stops polling", len(published))
	}
}

// A queue that is down costs the recovery, not the read. A status
// endpoint that 500s hides the very progress it exists to report.
func TestStatusStillAnswersWhenFinalizeCannotBeDispatched(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{
		run:      model.Audiobook{BookID: "b1", State: model.AudiobookRunning},
		coverage: model.AudiobookCoverage{Total: 4, Done: 4},
	}
	rec := &recordingEnqueuer{err: errors.New("queue is down")}
	svc := NewAudiobookService(AudiobookDeps{Store: store, Books: &epubOpener{}, Enqueue: rec})

	_, cov, err := svc.Status(context.Background(), "b1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if cov.Done != 4 {
		t.Errorf("coverage = %d, want the progress reported regardless", cov.Done)
	}
}

// Retry is the button a user reaches for when a run looks stuck. On a
// stranded run both of its guards used to fire — "already running", then
// "no outstanding segments" — so the one available action reported there
// was nothing to do.
func TestRetryFinalizesAStrandedRunInsteadOfRefusing(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{
		run:        model.Audiobook{BookID: "b1", State: model.AudiobookRunning},
		coverage:   model.AudiobookCoverage{Total: 12, Done: 12},
		unfinished: nil,
	}
	rec := &recordingEnqueuer{}
	svc := NewAudiobookService(AudiobookDeps{Store: store, Books: &epubOpener{}, Enqueue: rec})

	if err := svc.Retry(context.Background(), "b1"); err != nil {
		t.Fatalf("Retry on a stranded run: %v", err)
	}
	if ids := rec.finalizes(); len(ids) != 1 || ids[0] != "b1" {
		t.Fatalf("finalize dispatched %v, want exactly one for b1", ids)
	}
	if segs := rec.segments(); len(segs) != 0 {
		t.Errorf("re-synthesized %v — every one of those segments is already paid for", segs)
	}
}

// Reconcile-on-read no longer re-dispatches finalize for a failed run
// (#206), so Retry is the only route back for one whose Segments all
// landed and whose finalize was what broke. If this stops working, that
// run is stranded with its audio bought and no way to assemble it.
func TestRetryFinalizesARunThatFailedAtFinalize(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{
		run:      model.Audiobook{BookID: "b1", State: model.AudiobookFailed},
		coverage: model.AudiobookCoverage{Total: 3, Done: 3},
	}
	rec := &recordingEnqueuer{}
	svc := NewAudiobookService(AudiobookDeps{Store: store, Enqueue: rec})

	if err := svc.Retry(context.Background(), "b1"); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	if got := rec.finalizes(); len(got) != 1 || got[0] != "b1" {
		t.Errorf("finalizes = %v, want one for b1 — the run is stranded otherwise", got)
	}
}

// A cancelled run stays cancelled however complete its Coverage is. The
// user pressed stop; Retry must not be a way around that (ADR-0028 §6).
func TestRetryDoesNotResurrectACanceledRunWithCompleteCoverage(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{
		run:      model.Audiobook{BookID: "b1", State: model.AudiobookCanceled},
		coverage: model.AudiobookCoverage{Total: 3, Done: 3},
	}
	rec := &recordingEnqueuer{}
	svc := NewAudiobookService(AudiobookDeps{Store: store, Enqueue: rec})

	_ = svc.Retry(context.Background(), "b1")

	if got := rec.finalizes(); len(got) != 0 {
		t.Errorf("finalizes = %v, want none for a canceled run", got)
	}
}
