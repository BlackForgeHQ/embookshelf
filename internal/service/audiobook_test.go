// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

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
	if f.getErr != nil {
		return model.Audiobook{}, f.getErr
	}
	return f.run, nil
}

func (f *fakeAudiobookStore) SetState(_ context.Context, _ string, s model.AudiobookState, msg string) error {
	f.state, f.stateMsg = s, msg
	return nil
}

func (f *fakeAudiobookStore) ListUnfinishedSegments(context.Context, string) ([]model.AudiobookSegment, error) {
	return f.unfinished, nil
}

type recordingDispatcher struct {
	segments []int
	err      error
}

func (d *recordingDispatcher) dispatch(_ context.Context, _ string, seq int) error {
	if d.err != nil {
		return d.err
	}
	d.segments = append(d.segments, seq)
	return nil
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
	svc := NewAudiobookService(&fakeAudiobookStore{}, &epubOpener{src: buildTestEPUB(t, text)}, nil)

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
	svc := NewAudiobookService(&fakeAudiobookStore{}, &epubOpener{src: buildTestEPUB(t, strings.Repeat("x ", 500))}, nil)

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

	svc := NewAudiobookService(&fakeAudiobookStore{}, &epubOpener{}, nil)
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
	disp := &recordingDispatcher{}
	svc := NewAudiobookService(store, &epubOpener{src: buildTestEPUB(t, strings.Repeat("abcdefghij", 100))}, disp.dispatch)

	if err := svc.Start(context.Background(), narratableBook(), testOptions()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if !store.started {
		t.Fatal("the plan was never persisted")
	}
	if len(store.segments) == 0 {
		t.Fatal("the plan has no segments")
	}
	if len(disp.segments) != len(store.segments) {
		t.Errorf("dispatched %d jobs for %d segments", len(disp.segments), len(store.segments))
	}
	// Every segment must be dispatched exactly once, and by seq — a job
	// addresses its work by (book, seq).
	for i, seq := range disp.segments {
		if seq != i {
			t.Errorf("dispatched[%d] = seq %d, want %d", i, seq, i)
		}
	}
	if store.run.Engine != "openai" || store.run.Voice != "alloy" {
		t.Errorf("run records engine/voice %q/%q, want openai/alloy", store.run.Engine, store.run.Voice)
	}
	if store.run.TotalChars <= 0 {
		t.Error("run does not record the character count the estimate was based on")
	}
}

// Char offsets are the alignment map. Contiguous and monotonic, or the
// text-to-audio correspondence silently points at the wrong paragraph.
func TestStartRecordsAContiguousAlignmentMap(t *testing.T) {
	t.Parallel()

	store := &fakeAudiobookStore{}
	disp := &recordingDispatcher{}
	svc := NewAudiobookService(store, &epubOpener{src: buildTestEPUB(t, strings.Repeat("abcdefghij", 100))}, disp.dispatch)

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
	disp := &recordingDispatcher{}
	svc := NewAudiobookService(store, &epubOpener{src: buildTestEPUB(t, strings.Repeat("abcdefghij", 200))}, disp.dispatch)

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
	disp := &recordingDispatcher{err: errors.New("queue is down")}
	svc := NewAudiobookService(store, &epubOpener{src: buildTestEPUB(t, strings.Repeat("abcdefghij", 100))}, disp.dispatch)

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
	svc := NewAudiobookService(store, &epubOpener{src: buildTestEPUB(t, "")}, (&recordingDispatcher{}).dispatch)

	if err := svc.Start(context.Background(), narratableBook(), testOptions()); err == nil {
		t.Fatal("want an error for an EPUB with no prose, got nil")
	}
	if store.started {
		t.Error("an unnarratable book was persisted as a run")
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
	svc := NewAudiobookService(store, &epubOpener{}, nil)

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
	svc := NewAudiobookService(store, &epubOpener{}, nil)

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
	disp := &recordingDispatcher{}
	svc := NewAudiobookService(store, &epubOpener{}, disp.dispatch)

	if err := svc.Retry(context.Background(), "b1"); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if len(disp.segments) != 2 || disp.segments[0] != 3 || disp.segments[1] != 7 {
		t.Errorf("dispatched %v, want only the unfinished segments [3 7]", disp.segments)
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
	}
	svc := NewAudiobookService(store, &epubOpener{}, (&recordingDispatcher{}).dispatch)

	if err := svc.Retry(context.Background(), "b1"); err == nil {
		t.Fatal("want an error retrying a run with no outstanding segments, got nil")
	}
}
