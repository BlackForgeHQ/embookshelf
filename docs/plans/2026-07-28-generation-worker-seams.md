# Generation Worker Seams Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the nine concrete types the guide and audiobook workers depend on with narrow interfaces and function seams, so 685 lines of claim, cancel and permanent-failure branching become testable without Postgres, River or a live TTS endpoint.

**Architecture:** `internal/task` declares consumer-side unexported interfaces for the repo slices each worker actually calls, and function fields for single-purpose collaborators (settings read, remote-client construction, book open, placement, publish). `AudiobookDeps` splits into `SegmentDeps` and `FinalizeDeps`; `ReadingGuideDeps` keeps its name and narrows. `internal/queue/registry.go` builds all three from the same concrete repos it already holds. No behaviour changes — the new tests characterise what ships today.

**Tech Stack:** Go 1.x, standard library `testing` only. No new dependencies. No mocking library — hand-written fakes in `package task_test`.

**Spec:** `docs/spec/generation-worker-seams.spec.md`

## Global Constraints

- **Postgres only (ADR-0023).** No dialect branches, no SQLite migrations.
- **No behaviour changes.** Every branch, error wrap, ordering decision and explanatory comment in the three workers survives verbatim unless this plan says otherwise. Only the types through which branches reach their collaborators change.
- **Every file starts with** `// SPDX-License-Identifier: AGPL-3.0-or-later` followed by a blank line.
- **New tests use no database, no River, no HTTP, no TTS endpoint.** The two existing sweeper tests keep `repotest` — they assert what a SQL predicate selects.
- **Test package is `task_test`** (external), matching every existing `_test.go` in `internal/task`.
- **The nil guard at `internal/task/audiobook.go:206` survives** in narrowed form (`deps.Finalize == nil`) and is annotated as #184's to remove. Do not delete it.
- **Comments explain why, not what.** The existing workers are heavily commented in this style; preserve those comments when moving lines, and match the register when adding.
- **Gates:** `make test` and `make go-lint` (golangci-lint v2.11.4) pass at every commit.

---

## File Structure

**Modified:**
- `internal/task/audiobook.go` — `AudiobookDeps` → `SegmentDeps`; helpers rethreaded
- `internal/task/audiobook_finalize.go` — `FinalizeDeps`; sweeper takes a lister
- `internal/task/reading_guide.go` — `ReadingGuideDeps` narrowed
- `internal/service/reading_guide.go` — `guideCompleter` → `GuideCompleter` (exported)
- `internal/queue/registry.go` — builds three deps structs from the same repos
- `cmd/embookshelf/main.go` — sweeper call site loses its struct
- `internal/task/audiobook_test.go` — two sweeper call signatures

**Created:**
- `internal/task/generation_fakes_test.go` — MP3 fixtures, EPUB builder, fake stores shared by the three new test files
- `internal/task/audiobook_segment_test.go`
- `internal/task/audiobook_finalize_test.go`
- `internal/task/reading_guide_test.go`

Fakes live in one file because all three test files need the same `bookReader`, and the segment and finalize tests both need MP3 frames. Splitting them per test file would triplicate the frame builder.

---

## Task 1: The sweeper takes a lister, not a deps struct

`SweepAudiobookStaging` and `LoopAudiobookStagingSweep` accept the full nine-field `AudiobookDeps` and read exactly one method off it. Narrowing them first is independent of everything else and proves the pattern.

**Files:**
- Modify: `internal/task/audiobook_finalize.go:309-349`
- Modify: `cmd/embookshelf/main.go:469-472`
- Test: `internal/task/audiobook_test.go:96,134`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `type stagingLister interface { ListStaleStaging(ctx context.Context, olderThanDays int) ([]string, error) }` (unexported, `internal/task`); `func SweepAudiobookStaging(ctx context.Context, runs stagingLister, dataPath string) (int, error)`; `func LoopAudiobookStagingSweep(ctx context.Context, runs stagingLister, dataPath string)`.

- [ ] **Step 1: Update the two existing tests to the new signature**

In `internal/task/audiobook_test.go`, replace both `SweepAudiobookStaging` calls. Line 96:

```go
	n, err := task.SweepAudiobookStaging(ctx, audiobooks, dataPath)
```

Line 134 is identical:

```go
	n, err := task.SweepAudiobookStaging(ctx, audiobooks, dataPath)
```

- [ ] **Step 2: Run the tests to verify they fail to compile**

```bash
go build ./... && go test ./internal/task/ -run TestSweepAudiobookStaging
```

Expected: FAIL — `too many arguments in call to task.SweepAudiobookStaging`.

- [ ] **Step 3: Narrow the sweeper**

In `internal/task/audiobook_finalize.go`, replace the block from `// SweepAudiobookStaging removes staging` (line 305) to the end of the file:

```go
// stagingLister is the one thing a sweep asks of a run store: which runs
// have been dead weight long enough to reclaim.
type stagingLister interface {
	ListStaleStaging(ctx context.Context, olderThanDays int) ([]string, error)
}

// SweepAudiobookStaging removes staging for runs whose staged segments
// have been dead weight for longer than StaleStagingTTL. Which runs
// those are is ListStaleStaging's judgement, not this loop's.
func SweepAudiobookStaging(ctx context.Context, runs stagingLister, dataPath string) (int, error) {
	ids, err := runs.ListStaleStaging(ctx, int(StaleStagingTTL/(24*time.Hour)))
	if err != nil {
		return 0, err
	}
	swept := 0
	for _, id := range ids {
		dir := StagingDir(dataPath, id)
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		cleanStaging(dataPath, id)
		swept++
	}
	return swept, nil
}

// LoopAudiobookStagingSweep runs the sweep hourly, matching the shape of
// the missing-file and orphaned-key sweepers.
func LoopAudiobookStagingSweep(ctx context.Context, runs stagingLister, dataPath string) {
	if dataPath == "" || runs == nil {
		return
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := SweepAudiobookStaging(ctx, runs, dataPath)
			if err != nil {
				slog.Warn("audiobook staging sweep", "err", err)
				continue
			}
			if n > 0 {
				slog.Info("audiobook staging sweep", "swept", n)
			}
		}
	}
}
```

Keep the `StaleStagingTTL` const and its comment exactly where they are, immediately above this block.

- [ ] **Step 4: Update the composition root**

In `cmd/embookshelf/main.go`, replace lines 469-472:

```go
	go task.LoopAudiobookStagingSweep(ctx, audiobookRepo, cfg.DataPath)
```

The three comment lines above it (`// Staging for abandoned failed or cancelled runs …`) stay unchanged.

- [ ] **Step 5: Run the tests and the linter**

```bash
go build ./... && go test ./internal/task/ -run TestSweepAudiobookStaging && make go-lint
```

Expected: both sweeper tests PASS, lint clean.

- [ ] **Step 6: Commit**

```bash
git add internal/task/audiobook_finalize.go internal/task/audiobook_test.go cmd/embookshelf/main.go
git commit -m "refactor(task): the staging sweep asks for one method

SweepAudiobookStaging took the whole nine-field AudiobookDeps and read
ListStaleStaging off it. A caller now hands it the lister and the data
path, which is the entire dependency."
```

---

## Task 2: Test support — MP3 frames, an EPUB, and the fakes

Everything after this task needs the same fixtures. Building them alone, with one test proving they work, keeps Task 3's diff about the worker rather than about scaffolding.

**Files:**
- Create: `internal/task/generation_fakes_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces, all in `package task_test`:
  - `func mp3Frames(n int) []byte` — n back-to-back MPEG-1 Layer III silent frames
  - `func epubWithChapters(t *testing.T, bodies ...string) storage.Source`
  - `type memSrc struct { *bytes.Reader; size int64 }` with `Size() int64` and `Close() error`
  - `type fakeBooks struct { book model.Book; err error; gets int; audio *audioUpdate }` implementing `GetByID` and `UpdateAudio`
  - `type audioUpdate struct { DurationSeconds *int; Narrator string; Chapters []model.Chapter }`
  - `type fakeEngine struct { reply []byte; err error; calls int; requests []tts.Request }` implementing `tts.Engine`

- [ ] **Step 1: Write the fixtures file**

Create `internal/task/generation_fakes_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

import (
	"archive/zip"
	"bytes"
	"context"
	"strconv"
	"testing"

	"github.com/BlackForgeHQ/embookshelf/internal/audio"
	"github.com/BlackForgeHQ/embookshelf/internal/fileproc"
	"github.com/BlackForgeHQ/embookshelf/internal/model"
	"github.com/BlackForgeHQ/embookshelf/internal/storage"
	"github.com/BlackForgeHQ/embookshelf/internal/tts"
)

// ---------------------------------------------------------------------------
// MP3 fixtures
// ---------------------------------------------------------------------------

// mpeg1FrameHeader is one MPEG-1 Layer III frame header: 128 kbit/s,
// 44.1 kHz, stereo, no CRC. audio.Payload rejects anything that is not
// this, and both the staging and the assembly paths run through it.
//
// Duplicated from internal/audio's own test support rather than exported
// from it: a frame builder is a fixture, and putting one in the
// production surface of a package whose job is parsing real files would
// be worse than four lines of repetition.
var mpeg1FrameHeader = []byte{0xFF, 0xFB, 0x90, 0x00}

// mp3FrameBytes is the size of the frame above: 144 * 128000 / 44100.
const mp3FrameBytes = 417

// mp3Frames builds n back-to-back frames of silence.
func mp3Frames(n int) []byte {
	var b bytes.Buffer
	for range n {
		b.Write(mpeg1FrameHeader)
		b.Write(make([]byte, mp3FrameBytes-len(mpeg1FrameHeader)))
	}
	return b.Bytes()
}

// ---------------------------------------------------------------------------
// EPUB fixture
// ---------------------------------------------------------------------------

// memSrc is a byte slice as a storage.Source.
type memSrc struct {
	*bytes.Reader
	size int64
}

func (m memSrc) Size() int64  { return m.size }
func (m memSrc) Close() error { return nil }

// epubWithChapters builds a minimal EPUB, one spine item per body, which
// is what ExtractEPUBSegments walks.
func epubWithChapters(t *testing.T, bodies ...string) storage.Source {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip: %v", err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	add("META-INF/container.xml", `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container">`+
		`<rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)

	var manifest, spine bytes.Buffer
	for i, body := range bodies {
		id := "c" + strconv.Itoa(i)
		href := id + ".xhtml"
		manifest.WriteString(`<item id="` + id + `" href="` + href + `" media-type="application/xhtml+xml"/>`)
		spine.WriteString(`<itemref idref="` + id + `"/>`)
		add(href, `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><body><p>`+body+`</p></body></html>`)
	}
	add("content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0">`+
		`<manifest>`+manifest.String()+`</manifest><spine>`+spine.String()+`</spine></package>`)

	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	b := buf.Bytes()
	return memSrc{Reader: bytes.NewReader(b), size: int64(len(b))}
}

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// audioUpdate records what a finalize wrote back onto the book row.
type audioUpdate struct {
	DurationSeconds *int
	Narrator        string
	Chapters        []model.Chapter
}

// fakeBooks satisfies both bookReader and bookAudioWriter.
type fakeBooks struct {
	book  model.Book
	err   error
	gets  int
	audio *audioUpdate
}

func (f *fakeBooks) GetByID(_ context.Context, _, _ string) (model.Book, error) {
	f.gets++
	if f.err != nil {
		return model.Book{}, f.err
	}
	return f.book, nil
}

func (f *fakeBooks) UpdateAudio(
	_ context.Context, _ string, durationSeconds *int, narrator string, chapters []model.Chapter,
) error {
	f.audio = &audioUpdate{DurationSeconds: durationSeconds, Narrator: narrator, Chapters: chapters}
	return nil
}

// fakeEngine is a tts.Engine that never leaves the process.
type fakeEngine struct {
	reply    []byte
	err      error
	calls    int
	requests []tts.Request
}

func (e *fakeEngine) Synthesize(_ context.Context, req tts.Request) ([]byte, error) {
	e.calls++
	e.requests = append(e.requests, req)
	if e.err != nil {
		return nil, e.err
	}
	return e.reply, nil
}

func (e *fakeEngine) ListVoices(context.Context) ([]tts.Voice, error) { return nil, nil }
```

- [ ] **Step 2: Write a test that proves the fixtures are usable**

Append to `internal/task/generation_fakes_test.go`:

```go
// The fixtures are load-bearing: audio.Payload rejects a frame it does
// not recognise, and ExtractEPUBSegments rejects an archive it cannot
// walk. Both would fail every test downstream with an error about the
// fixture rather than about the worker, so they are checked here once.
func TestFixturesAreWhatTheProductionParsersExpect(t *testing.T) {
	frames, durationMS, err := audio.Payload(mp3Frames(4))
	if err != nil {
		t.Fatalf("audio.Payload rejected the frame fixture: %v", err)
	}
	if len(frames) != 4*mp3FrameBytes {
		t.Errorf("payload is %d bytes, want %d", len(frames), 4*mp3FrameBytes)
	}
	if durationMS <= 0 {
		t.Errorf("duration is %dms, want a positive measurement", durationMS)
	}

	src := epubWithChapters(t, "One sentence. Another sentence.", "A second chapter.")
	segs, err := fileproc.ExtractEPUBSegments(context.Background(), src, fileproc.SegmentOptions{MaxChars: 1000})
	if err != nil {
		t.Fatalf("ExtractEPUBSegments rejected the EPUB fixture: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("extracted %d segments, want one per spine item", len(segs))
	}
}
```

- [ ] **Step 3: Run it**

```bash
go test ./internal/task/ -run TestFixturesAreWhatTheProductionParsersExpect -v
```

Expected: PASS. If `ExtractEPUBSegments` returns a different segment count, adjust `epubWithChapters` until it yields one segment per spine item at a 1000-char cap — do not adjust the assertion.

- [ ] **Step 4: Lint and commit**

```bash
make go-lint
git add internal/task/generation_fakes_test.go
git commit -m "test(task): fixtures for exercising generation without a database

MP3 frames audio.Payload accepts, an EPUB ExtractEPUBSegments can walk,
and fakes for the book row and the TTS engine. Checked against the real
parsers here so a downstream failure is about the worker."
```

---

## Task 3: `SegmentDeps` replaces `AudiobookDeps` in the segment worker

**Files:**
- Modify: `internal/task/audiobook.go` (whole file)
- Modify: `internal/queue/registry.go:91-128`
- Create: `internal/task/audiobook_segment_test.go`

**Interfaces:**
- Consumes: `mp3Frames`, `epubWithChapters`, `fakeBooks`, `fakeEngine` from Task 2.
- Produces:
  - `type SegmentDeps struct { Config func(context.Context) (repo.AudiobookConfig, error); Engine func(repo.AudiobookConfig) (repo.ConfiguredEngine, error); Runs segmentStore; Books bookReader; Open func(context.Context, model.Book) (storage.Source, error); Finalize service.FinalizeDispatcher; Publish func(bookID string); DataPath string }`
  - `type segmentStore interface { GetByBookID(ctx context.Context, bookID string) (model.Audiobook, error); MarkSegmentRunning(ctx context.Context, bookID string, seq int) (bool, error); RecordSegment(ctx context.Context, bookID string, seq int, res model.SegmentResult) (model.AudiobookOutcome, error) }`
  - `type bookReader interface { GetByID(ctx context.Context, userID, id string) (model.Book, error) }`
  - `func AudiobookSegment(ctx context.Context, a AudiobookSegmentArgs, deps SegmentDeps) error`
  - `AudiobookDeps` still exists for the finalize worker until Task 5.

- [ ] **Step 1: Write the failing tests**

Create `internal/task/audiobook_segment_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/BlackForgeHQ/embookshelf/internal/model"
	"github.com/BlackForgeHQ/embookshelf/internal/repo"
	"github.com/BlackForgeHQ/embookshelf/internal/service"
	"github.com/BlackForgeHQ/embookshelf/internal/storage"
	"github.com/BlackForgeHQ/embookshelf/internal/task"
	"github.com/BlackForgeHQ/embookshelf/internal/tts"
)

// ---------------------------------------------------------------------------
// Fakes local to the segment worker
// ---------------------------------------------------------------------------

// fakeSegmentRuns is the slice of the run store the segment worker uses.
//
// onGet exists for one case the worker was built around: cancel is
// re-read before every engine call, so a test proving a mid-run cancel
// stops the spend has to change the answer between reads.
type fakeSegmentRuns struct {
	run      model.Audiobook
	getErr   error
	gets     int
	onGet    func(n int) model.Audiobook
	claim    bool
	claimErr error
	claims   int
	recorded []model.SegmentResult
	outcome  model.AudiobookNext
}

func (f *fakeSegmentRuns) GetByBookID(context.Context, string) (model.Audiobook, error) {
	f.gets++
	if f.getErr != nil {
		return model.Audiobook{}, f.getErr
	}
	if f.onGet != nil {
		return f.onGet(f.gets), nil
	}
	return f.run, nil
}

func (f *fakeSegmentRuns) MarkSegmentRunning(context.Context, string, int) (bool, error) {
	f.claims++
	return f.claim, f.claimErr
}

func (f *fakeSegmentRuns) RecordSegment(
	_ context.Context, _ string, _ int, res model.SegmentResult,
) (model.AudiobookOutcome, error) {
	f.recorded = append(f.recorded, res)
	return model.AudiobookOutcome{Next: f.outcome}, nil
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type segmentHarness struct {
	deps       task.SegmentDeps
	runs       *fakeSegmentRuns
	books      *fakeBooks
	engine     *fakeEngine
	published  int
	finalized  int
	engineErr  error
	dataPath   string
	configCall int
}

// newSegmentHarness wires a worker whose every collaborator is in
// process: a claimable run, an EPUB of two short chapters, and an engine
// that returns four frames of silence.
func newSegmentHarness(t *testing.T) *segmentHarness {
	t.Helper()
	h := &segmentHarness{
		runs: &fakeSegmentRuns{
			run:   model.Audiobook{BookID: "b1", State: model.AudiobookRunning, Engine: "openai", Voice: "alloy"},
			claim: true,
		},
		books: &fakeBooks{book: model.Book{
			ID: "b1", LibraryID: "lib1", Title: "Dune", Author: "Frank Herbert", Format: "EPUB",
		}},
		engine:   &fakeEngine{reply: mp3Frames(4)},
		dataPath: t.TempDir(),
	}
	src := epubWithChapters(t, "One sentence. Another sentence. A third one.", "Second chapter here.")
	h.deps = task.SegmentDeps{
		Config: func(context.Context) (repo.AudiobookConfig, error) {
			h.configCall++
			return repo.AudiobookConfig{Enabled: true, Engine: "openai", SegmentChars: 1000}, nil
		},
		Engine: func(repo.AudiobookConfig) (repo.ConfiguredEngine, error) {
			if h.engineErr != nil {
				return repo.ConfiguredEngine{}, h.engineErr
			}
			return repo.ConfiguredEngine{
				ID:     tts.EngineOpenAI,
				Info:   tts.Info{ID: tts.EngineOpenAI, MaxRequestChars: 20},
				Engine: h.engine,
			}, nil
		},
		Runs:  h.runs,
		Books: h.books,
		Open: func(context.Context, model.Book) (storage.Source, error) {
			return src, nil
		},
		Finalize: func(context.Context, string) error { h.finalized++; return nil },
		Publish:  func(string) { h.published++ },
		DataPath: h.dataPath,
	}
	return h
}

func (h *segmentHarness) run(t *testing.T, seq int) error {
	t.Helper()
	return task.AudiobookSegment(context.Background(), task.AudiobookSegmentArgs{BookID: "b1", Seq: seq}, h.deps)
}

func (h *segmentHarness) staged(seq int) bool {
	_, err := os.Stat(filepath.Join(task.StagingDir(h.dataPath, "b1"), "seg-"+strconv.Itoa(seq)+".mp3"))
	return err == nil
}

// ---------------------------------------------------------------------------
// Refusals — nothing is bought
// ---------------------------------------------------------------------------

// A disabled feature is permanent, not transient: it will still be
// disabled in thirty seconds, and River must not retry it.
func TestSegmentRefusesWhenTheFeatureIsDisabled(t *testing.T) {
	h := newSegmentHarness(t)
	h.deps.Config = func(context.Context) (repo.AudiobookConfig, error) {
		return repo.AudiobookConfig{Enabled: false}, nil
	}

	err := h.run(t, 0)

	if !errors.Is(err, task.ErrAudiobooksDisabled) {
		t.Fatalf("err = %v, want ErrAudiobooksDisabled", err)
	}
	if h.engine.calls != 0 {
		t.Errorf("engine called %d times with the feature off", h.engine.calls)
	}
}

// Cancel is the only stop-loss on a run that may be a hundred and
// seventy dollars. A segment picked up after one must not claim.
func TestSegmentSkipsACanceledRunWithoutClaiming(t *testing.T) {
	h := newSegmentHarness(t)
	h.runs.run.State = model.AudiobookCanceled

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment: %v", err)
	}
	if h.runs.claims != 0 {
		t.Errorf("claimed %d times on a canceled run", h.runs.claims)
	}
	if h.engine.calls != 0 {
		t.Errorf("engine called %d times on a canceled run", h.engine.calls)
	}
}

func TestSegmentSkipsARunThatIsAlreadyReady(t *testing.T) {
	h := newSegmentHarness(t)
	h.runs.run.State = model.AudiobookReady

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment: %v", err)
	}
	if h.runs.claims != 0 || h.engine.calls != 0 {
		t.Errorf("claims=%d engine=%d, want neither on a finished run", h.runs.claims, h.engine.calls)
	}
}

// A segment that lost the claim is a segment somebody else finished.
// Re-synthesizing would buy the same audio twice — the whole reason
// segments are rows rather than a counter (ADR-0028 §6).
func TestSegmentDoesNotResynthesizeWhatItCouldNotClaim(t *testing.T) {
	h := newSegmentHarness(t)
	h.runs.claim = false

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment: %v", err)
	}
	if h.engine.calls != 0 {
		t.Errorf("engine called %d times for an unclaimed segment", h.engine.calls)
	}
}

// ---------------------------------------------------------------------------
// The run's own choice wins over the current setting
// ---------------------------------------------------------------------------

// An admin switching engines mid-run must not produce a book narrated
// half in one voice and half in another.
func TestSegmentRefusesWhenTheRunUsesADifferentEngine(t *testing.T) {
	h := newSegmentHarness(t)
	h.runs.run.Engine = "elevenlabs"

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment returned %v, want nil for a permanent failure", err)
	}
	if h.engine.calls != 0 {
		t.Errorf("engine called %d times despite the engine mismatch", h.engine.calls)
	}
	if len(h.runs.recorded) != 1 || h.runs.recorded[0].State != model.SegmentFailed {
		t.Fatalf("recorded %+v, want one failed segment", h.runs.recorded)
	}
}

func TestSegmentRefusesABookThatIsNotNarratable(t *testing.T) {
	h := newSegmentHarness(t)
	h.books.book.Format = "PDF"

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment returned %v, want nil for a permanent failure", err)
	}
	if len(h.runs.recorded) != 1 || h.runs.recorded[0].State != model.SegmentFailed {
		t.Fatalf("recorded %+v, want one failed segment", h.runs.recorded)
	}
	if h.engine.calls != 0 {
		t.Errorf("engine called %d times for a PDF", h.engine.calls)
	}
}

func TestSegmentSurfacesAMissingBook(t *testing.T) {
	h := newSegmentHarness(t)
	h.books.err = repo.ErrNotFound

	err := h.run(t, 0)

	if err == nil {
		t.Fatal("AudiobookSegment returned nil for a deleted book")
	}
	if h.staged(0) {
		t.Error("a segment was staged for a book that no longer exists")
	}
}

// ---------------------------------------------------------------------------
// Engine outcomes
// ---------------------------------------------------------------------------

// A permanent engine error stops River retrying something that cannot
// improve; the failed row and its message carry the outcome.
func TestSegmentRecordsAPermanentEngineFailureAndStopsRetrying(t *testing.T) {
	h := newSegmentHarness(t)
	h.engine.err = tts.ErrPermanent

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment returned %v, want nil so River stops", err)
	}
	if len(h.runs.recorded) != 1 || h.runs.recorded[0].State != model.SegmentFailed {
		t.Fatalf("recorded %+v, want one failed segment", h.runs.recorded)
	}
	if h.published != 1 {
		t.Errorf("published %d times, want exactly one terminal event", h.published)
	}
}

// A transient error is River's to retry, so the worker has to return it.
func TestSegmentReturnsATransientEngineFailureForRiver(t *testing.T) {
	h := newSegmentHarness(t)
	h.engine.err = errors.New("connection reset")

	err := h.run(t, 0)

	if err == nil {
		t.Fatal("AudiobookSegment returned nil for a transient failure — River would never retry")
	}
	if len(h.runs.recorded) != 1 || h.runs.recorded[0].State != model.SegmentFailed {
		t.Fatalf("recorded %+v, want one failed segment", h.runs.recorded)
	}
}

// Audio the frame parser cannot read is not something a retry improves.
func TestSegmentRecordsUnusableAudioAsAPermanentFailure(t *testing.T) {
	h := newSegmentHarness(t)
	h.engine.reply = []byte("this is not an mp3")

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment returned %v, want nil", err)
	}
	if len(h.runs.recorded) != 1 || h.runs.recorded[0].State != model.SegmentFailed {
		t.Fatalf("recorded %+v, want one failed segment", h.runs.recorded)
	}
	if h.published != 1 {
		t.Errorf("published %d times, want exactly one", h.published)
	}
}

// A 40k segment is a dozen engine calls over several minutes. A cancel
// that only took effect between segments would keep spending for most of
// that (ADR-0028 §6), so it is checked before every call.
func TestSegmentStopsSpendingWhenCancelLandsBetweenChunks(t *testing.T) {
	h := newSegmentHarness(t)
	running := model.Audiobook{BookID: "b1", State: model.AudiobookRunning, Engine: "openai", Voice: "alloy"}
	canceled := running
	canceled.State = model.AudiobookCanceled
	// Reads: 1 is the worker's own state check, 2 guards the first chunk,
	// 3 guards the second. The cancel lands after one chunk was bought.
	h.runs.onGet = func(n int) model.Audiobook {
		if n >= 3 {
			return canceled
		}
		return running
	}

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment returned %v, want nil for an abandoned segment", err)
	}
	if h.engine.calls != 1 {
		t.Errorf("engine called %d times, want the run to stop after the first chunk", h.engine.calls)
	}
	if len(h.runs.recorded) != 0 {
		t.Errorf("recorded %+v — a canceled run is already in its final state and must not be overwritten",
			h.runs.recorded)
	}
	if h.staged(0) {
		t.Error("an abandoned segment was staged")
	}
}

// The plan and the file disagreeing means the file changed under the
// run. Narrating segment 12 of a different book is worse than failing.
func TestSegmentRefusesASeqThePlanNoLongerHas(t *testing.T) {
	h := newSegmentHarness(t)

	if err := h.run(t, 99); err != nil {
		t.Fatalf("AudiobookSegment returned %v, want nil for a permanent failure", err)
	}
	if len(h.runs.recorded) != 1 || h.runs.recorded[0].State != model.SegmentFailed {
		t.Fatalf("recorded %+v, want one failed segment", h.runs.recorded)
	}
	if h.engine.calls != 0 {
		t.Errorf("engine called %d times for a segment that does not exist", h.engine.calls)
	}
}

// ---------------------------------------------------------------------------
// Completion
// ---------------------------------------------------------------------------

func TestSegmentStagesItsAudioAndRecordsTheDuration(t *testing.T) {
	h := newSegmentHarness(t)

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment: %v", err)
	}
	if len(h.runs.recorded) != 1 {
		t.Fatalf("recorded %d results, want 1", len(h.runs.recorded))
	}
	got := h.runs.recorded[0]
	if got.State != model.SegmentDone {
		t.Errorf("state = %q, want done", got.State)
	}
	if got.DurationMS <= 0 {
		t.Errorf("duration = %dms, want it measured from the frames", got.DurationMS)
	}
	if !h.staged(0) {
		t.Errorf("no file at %s", got.StagedPath)
	}
}

// The segment that completes last is what turns a run into a book. A
// dispatch that fires twice would assemble the same file twice.
func TestSegmentDispatchesFinalizeExactlyOnceWhenTheRunCompletes(t *testing.T) {
	h := newSegmentHarness(t)
	h.runs.outcome = model.AudiobookNextFinalize

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment: %v", err)
	}
	if h.finalized != 1 {
		t.Fatalf("finalize dispatched %d times, want exactly 1", h.finalized)
	}
}

// A run the write decided has failed publishes so the UI stops polling.
func TestSegmentPublishesOnceWhenTheWriteFailsTheRun(t *testing.T) {
	h := newSegmentHarness(t)
	h.runs.outcome = model.AudiobookNextFail

	if err := h.run(t, 0); err != nil {
		t.Fatalf("AudiobookSegment: %v", err)
	}
	if h.published != 1 {
		t.Fatalf("published %d times, want exactly 1", h.published)
	}
	if h.finalized != 0 {
		t.Errorf("finalize dispatched %d times for a failed run", h.finalized)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
go test ./internal/task/ -run TestSegment
```

Expected: FAIL to compile — `undefined: task.SegmentDeps`.

- [ ] **Step 3: Replace `AudiobookDeps` with `SegmentDeps` in the worker**

In `internal/task/audiobook.go`:

Replace the import block's `"github.com/BlackForgeHQ/embookshelf/internal/coverstore"` and `"github.com/BlackForgeHQ/embookshelf/internal/sse"` with `"github.com/BlackForgeHQ/embookshelf/internal/storage"`. The final block is:

```go
import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/BlackForgeHQ/embookshelf/internal/audio"
	"github.com/BlackForgeHQ/embookshelf/internal/fileproc"
	"github.com/BlackForgeHQ/embookshelf/internal/model"
	"github.com/BlackForgeHQ/embookshelf/internal/repo"
	"github.com/BlackForgeHQ/embookshelf/internal/service"
	"github.com/BlackForgeHQ/embookshelf/internal/storage"
	"github.com/BlackForgeHQ/embookshelf/internal/tts"
)
```

Replace the `canceled` helper (lines 41-49):

```go
// canceled reports whether the run has been stopped since the job began.
// A read per engine call is cheap next to the call it guards.
func canceled(ctx context.Context, bookID string, deps SegmentDeps) bool {
	run, err := deps.Runs.GetByBookID(ctx, bookID)
	if err != nil {
		return false
	}
	return run.State == model.AudiobookCanceled
}
```

Replace `AudiobookDeps` (lines 72-90) with:

```go
// segmentStore is the slice of BookAudiobookRepo the segment worker
// touches. Narrow so the claim, cancel and failure branches are
// exercisable without a database — the property AudiobookService has had
// since it was written, and these workers did not (#177).
type segmentStore interface {
	GetByBookID(ctx context.Context, bookID string) (model.Audiobook, error)
	MarkSegmentRunning(ctx context.Context, bookID string, seq int) (bool, error)
	RecordSegment(ctx context.Context, bookID string, seq int, res model.SegmentResult) (model.AudiobookOutcome, error)
}

// bookReader is the one book-row read every generation job makes.
type bookReader interface {
	GetByID(ctx context.Context, userID, id string) (model.Book, error)
}

// SegmentDeps groups the seams the segment worker needs.
//
// Config is read per job rather than captured at boot, so an admin
// changing voice, engine or key takes effect on the next segment instead
// of at the next restart — the same hot-reload the guide worker gets.
//
// Config and Engine are separate rather than one resolve step: the
// disabled check and its permanent sentinel stay in the worker body,
// where a reader asking why River does not retry will find them.
type SegmentDeps struct {
	Config func(context.Context) (repo.AudiobookConfig, error)
	Engine func(repo.AudiobookConfig) (repo.ConfiguredEngine, error)
	Runs   segmentStore
	Books  bookReader
	// Open yields the book's bytes with random access. Always through the
	// library handle, never os.Open(book.Path), which is how device push
	// on S3 libraries was once silently broken.
	Open     func(context.Context, model.Book) (storage.Source, error)
	Finalize service.FinalizeDispatcher
	Publish  func(bookID string)
	// DataPath roots the staging directory. Per-segment MP3s live on
	// local disk until finalize, outside storage.Storage, following the
	// coverstore precedent for derived bytes.
	DataPath string
}

// publish emits the run's terminal event. A missing publisher is a
// deployment with no SSE hub, not an error worth a branch at each call.
func (d SegmentDeps) publish(bookID string) {
	if d.Publish != nil {
		d.Publish(bookID)
	}
}
```

Change the `AudiobookSegment` signature and its first call:

```go
func AudiobookSegment(ctx context.Context, a AudiobookSegmentArgs, deps SegmentDeps) error {
	cfg, err := deps.Config(ctx)
	if err != nil {
		return fmt.Errorf("read audiobook settings: %w", err)
	}
```

Then inside it, replace `deps.Audiobooks.GetByBookID` with `deps.Runs.GetByBookID`, `deps.Audiobooks.MarkSegmentRunning` with `deps.Runs.MarkSegmentRunning`, and both `publishAudiobook(deps, a.BookID)` calls with `deps.publish(a.BookID)`.

Replace `recordSegment` (lines 198-217) — the doc comment above it is unchanged, only the body and signature:

```go
func recordSegment(ctx context.Context, deps SegmentDeps, a AudiobookSegmentArgs, res model.SegmentResult) {
	outcome, err := deps.Runs.RecordSegment(ctx, a.BookID, a.Seq, res)
	if err != nil {
		slog.Warn("audiobook: record segment", "book", a.BookID, "seq", a.Seq, "err", err)
		return
	}
	switch outcome.Next {
	case model.AudiobookNextFinalize:
		// Nil only for a zero-value SegmentDeps. In production the
		// registry supplies a closure that dereferences the dispatch
		// holder late, because the finalize dispatcher closes over the
		// client the registry is being built for. #184 removes both.
		if deps.Finalize == nil {
			slog.Warn("audiobook: no finalize dispatcher", "book", a.BookID)
			return
		}
		if err := deps.Finalize(ctx, a.BookID); err != nil {
			slog.Warn("audiobook: dispatch finalize", "book", a.BookID, "err", err)
		}
	case model.AudiobookNextFail:
		deps.publish(a.BookID)
	case model.AudiobookNextNothing:
	}
}
```

In `synthesizeSegment`, change the signature's last parameter to `deps SegmentDeps` and replace `sel, err := cfg.SelectEngine()` with:

```go
	sel, err := deps.Engine(cfg)
```

In `segmentText`, change the last parameter to `deps SegmentDeps` and replace the opener lines:

```go
	src, err := deps.Open(ctx, book)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", book.ID, err)
	}
	defer func() { _ = src.Close() }()
```

Delete the `publishAudiobook` function (lines 300-304) — `audiobook_finalize.go` still calls it, so temporarily move it there verbatim; Task 5 replaces it with `FinalizeDeps.publish`.

- [ ] **Step 4: Rewire the registry**

In `internal/queue/registry.go`, replace the `audiobook := task.AudiobookDeps{...}` block (lines 91-105) with:

```go
	// Audiobook generation runs on its own queue, declared by its args
	// types. The finalize dispatcher is reached through a closure rather
	// than copied, because the composition root fills the holder in after
	// queue.New returns — the value here would be nil forever (#184).
	segment := task.SegmentDeps{
		Config:   deps.AppSettings.GetAudiobook,
		Engine:   repo.AudiobookConfig.SelectEngine,
		Runs:     deps.Audiobooks,
		Books:    deps.Books,
		Open:     service.NewLibraryBookOpener(deps.LibStore).Open,
		DataPath: deps.DataPath,
		Finalize: func(ctx context.Context, bookID string) error {
			if deps.AudiobookDispatch == nil || deps.AudiobookDispatch.Finalize == nil {
				return errors.New("no queue configured for audiobook generation")
			}
			return deps.AudiobookDispatch.Finalize(ctx, bookID)
		},
	}
	if deps.Hub != nil {
		segment.Publish = func(bookID string) {
			_ = deps.Hub.Publish(sse.AudiobookUpdated{BookID: bookID})
		}
	}

	// Finalize still takes the old struct until #177's second half.
	audiobook := task.AudiobookDeps{
		Audiobooks: deps.Audiobooks,
		Books:      deps.Books,
		Files:      deps.FileRepo,
		LibStore:   deps.LibStore,
		Covers:     deps.Covers,
		Hub:        deps.Hub,
		DataPath:   deps.DataPath,
	}
```

Change the segment registration to use `segment`:

```go
		register(func(ctx context.Context, a task.AudiobookSegmentArgs) error {
			return task.AudiobookSegment(ctx, a, segment)
		}),
```

Add `"errors"`, `"github.com/BlackForgeHQ/embookshelf/internal/repo"` and `"github.com/BlackForgeHQ/embookshelf/internal/sse"` to the imports.

`AudiobookDeps` itself moves from `audiobook.go` to `audiobook_finalize.go`, alongside the `publishAudiobook` function Step 3 relocated there — the segment worker no longer reads either. Move the struct verbatim minus its `Settings` and `Dispatch` fields, which nothing left references. Task 5 deletes both.

- [ ] **Step 5: Run the tests**

```bash
go build ./... && go test ./internal/task/ -run TestSegment -v
```

Expected: all 14 `TestSegment*` PASS.

- [ ] **Step 6: Run the whole suite and the linter**

```bash
make test && make go-lint
```

Expected: clean. The `repotest`-backed tests need Postgres; if it is not running, `make up` first or run `go test ./internal/task/ -run 'TestSegment|TestFixtures'` and note the gap.

- [ ] **Step 7: Commit**

```bash
git add internal/task/audiobook.go internal/task/audiobook_finalize.go internal/task/audiobook_segment_test.go internal/queue/registry.go
git commit -m "refactor(task): the segment worker declares what it calls

Nine concrete pointers behind one struct is why 336 lines of claim,
cancel and permanent-failure branching had no test. SegmentDeps names
three narrow interfaces and five function seams instead, and the
branches are now characterised: a disabled feature is permanent, an
unclaimed segment is never re-synthesized, a cancel between chunks stops
the spend without overwriting the run's final state."
```

---

## Task 4: `ReadingGuideDeps` narrows

**Files:**
- Modify: `internal/service/reading_guide.go:32-36,63,70`
- Modify: `internal/task/reading_guide.go` (whole file)
- Modify: `internal/queue/registry.go:82-89`
- Create: `internal/task/reading_guide_test.go`

**Interfaces:**
- Consumes: `bookReader`, `epubWithChapters`, `memSrc`, `fakeBooks` from Tasks 2 and 3.
- Produces:
  - `service.GuideCompleter` — exported name for the existing `guideCompleter` interface, `ChatJSON(ctx context.Context, msgs []llm.Message, out any) error`
  - `type ReadingGuideDeps struct { Config func(context.Context) (repo.ReadingGuideConfig, error); Completer func(repo.ReadingGuideConfig) (service.GuideCompleter, error); Guides guideStore; Books bookReader; Open func(context.Context, model.Book) (storage.Source, error); Publish func(bookID string) }`
  - `type guideStore interface { Upsert(ctx context.Context, g model.ReadingGuide) error }`

- [ ] **Step 1: Write the failing tests**

Create `internal/task/reading_guide_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/BlackForgeHQ/embookshelf/internal/llm"
	"github.com/BlackForgeHQ/embookshelf/internal/model"
	"github.com/BlackForgeHQ/embookshelf/internal/repo"
	"github.com/BlackForgeHQ/embookshelf/internal/service"
	"github.com/BlackForgeHQ/embookshelf/internal/storage"
	"github.com/BlackForgeHQ/embookshelf/internal/task"
)

// fakeGuides records what the worker wrote.
type fakeGuides struct {
	saved []model.ReadingGuide
	err   error
}

func (f *fakeGuides) Upsert(_ context.Context, g model.ReadingGuide) error {
	if f.err != nil {
		return f.err
	}
	f.saved = append(f.saved, g)
	return nil
}

// fakeCompleter answers the guide prompt with a well-formed reply.
type fakeCompleter struct {
	calls int
	err   error
}

func (f *fakeCompleter) ChatJSON(_ context.Context, _ []llm.Message, out any) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	return json.Unmarshal([]byte(
		`{"about":"About","audience":"Audience","not_for":"Not for","problems":"Problems"}`), out)
}

type guideHarness struct {
	deps      task.ReadingGuideDeps
	guides    *fakeGuides
	books     *fakeBooks
	completer *fakeCompleter
	published int
	builds    int
}

func newGuideHarness(t *testing.T) *guideHarness {
	t.Helper()
	h := &guideHarness{
		guides:    &fakeGuides{},
		books:     &fakeBooks{book: model.Book{ID: "b1", Title: "Dune", Author: "Frank Herbert", Format: "EPUB"}},
		completer: &fakeCompleter{},
	}
	src := epubWithChapters(t, "The spice must flow. It is the one thing that matters.")
	h.deps = task.ReadingGuideDeps{
		Config: func(context.Context) (repo.ReadingGuideConfig, error) {
			return repo.ReadingGuideConfig{
				Enabled: true, Model: "test-model", Language: "en", TextCap: 48_000,
			}, nil
		},
		Completer: func(repo.ReadingGuideConfig) (service.GuideCompleter, error) {
			h.builds++
			return h.completer, nil
		},
		Guides:  h.guides,
		Books:   h.books,
		Open:    func(context.Context, model.Book) (storage.Source, error) { return src, nil },
		Publish: func(string) { h.published++ },
	}
	return h
}

func (h *guideHarness) run() error {
	return task.ReadingGuide(context.Background(), task.ReadingGuideArgs{BookID: "b1"}, h.deps)
}

// A disabled feature will still be disabled in thirty seconds. River
// must treat this as permanent rather than retrying it 25 times.
func TestReadingGuideRefusesWhenTheFeatureIsDisabled(t *testing.T) {
	h := newGuideHarness(t)
	h.deps.Config = func(context.Context) (repo.ReadingGuideConfig, error) {
		return repo.ReadingGuideConfig{Enabled: false}, nil
	}

	err := h.run()

	if !errors.Is(err, task.ErrReadingGuidesDisabled) {
		t.Fatalf("err = %v, want ErrReadingGuidesDisabled", err)
	}
	if h.builds != 0 {
		t.Errorf("built a client %d times with the feature off", h.builds)
	}
}

// A book edited or deleted between enqueue and dispatch is why the row
// is re-read rather than baked into the payload.
func TestReadingGuideSurfacesAMissingBook(t *testing.T) {
	h := newGuideHarness(t)
	h.books.err = repo.ErrNotFound

	err := h.run()

	if err == nil {
		t.Fatal("ReadingGuide returned nil for a deleted book")
	}
	if len(h.guides.saved) != 0 {
		t.Errorf("saved %d guides for a book that no longer exists", len(h.guides.saved))
	}
}

func TestReadingGuideSavesAndPublishesOnce(t *testing.T) {
	h := newGuideHarness(t)

	if err := h.run(); err != nil {
		t.Fatalf("ReadingGuide: %v", err)
	}
	if len(h.guides.saved) != 1 {
		t.Fatalf("saved %d guides, want 1", len(h.guides.saved))
	}
	got := h.guides.saved[0]
	if got.BookID != "b1" {
		t.Errorf("book = %q, want b1", got.BookID)
	}
	if got.Model != "test-model" {
		t.Errorf("model = %q, want the settings row's, recorded as provenance", got.Model)
	}
	if got.SourceKind != model.GuideSourceFullText {
		t.Errorf("source = %q, want full-text — the EPUB yielded text", got.SourceKind)
	}
	if h.published != 1 {
		t.Errorf("published %d times, want exactly 1", h.published)
	}
}

func TestReadingGuideDoesNotPublishWhenGenerationFails(t *testing.T) {
	h := newGuideHarness(t)
	h.completer.err = errors.New("model unreachable")

	if err := h.run(); err == nil {
		t.Fatal("ReadingGuide returned nil when the model was unreachable")
	}
	if h.published != 0 {
		t.Errorf("published %d times for a guide that was never written", h.published)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
go test ./internal/task/ -run TestReadingGuide
```

Expected: FAIL to compile — `undefined: service.GuideCompleter`, and `ReadingGuideDeps` has no field `Config`.

- [ ] **Step 3: Export the completer interface**

In `internal/service/reading_guide.go`, rename the interface at lines 32-36:

```go
// GuideCompleter is the slice of llm.Client used here. Narrow, so the
// generator is testable without a model or a network. Exported because
// the queue job's Completer seam needs a name for what it returns.
type GuideCompleter interface {
	ChatJSON(ctx context.Context, msgs []llm.Message, out any) error
}
```

Update the two references: field `llm GuideCompleter` (line 63) and parameter `completer GuideCompleter` (line 70).

- [ ] **Step 4: Narrow the worker**

Replace `internal/task/reading_guide.go` from the import block down:

```go
import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/BlackForgeHQ/embookshelf/internal/model"
	"github.com/BlackForgeHQ/embookshelf/internal/repo"
	"github.com/BlackForgeHQ/embookshelf/internal/service"
	"github.com/BlackForgeHQ/embookshelf/internal/storage"
)
```

The `ReadingGuideArgs` type and its `Kind` method are unchanged. Replace `ReadingGuideDeps` (lines 26-38):

```go
// guideStore is the one write a guide job makes.
type guideStore interface {
	Upsert(ctx context.Context, g model.ReadingGuide) error
}

// ReadingGuideDeps groups the seams the worker needs.
//
// Config is read per job rather than captured at boot, so an admin
// changing the model, the language or the cap takes effect on the next
// job instead of at the next restart — the same hot-reload behaviour
// Notifier gives the email subsystem.
type ReadingGuideDeps struct {
	Config    func(context.Context) (repo.ReadingGuideConfig, error)
	Completer func(repo.ReadingGuideConfig) (service.GuideCompleter, error)
	Guides    guideStore
	Books     bookReader
	// Open yields the book's bytes through the library handle, which is
	// what keeps guide generation working on S3-backed libraries.
	Open    func(context.Context, model.Book) (storage.Source, error)
	Publish func(bookID string)
}

func (d ReadingGuideDeps) publish(bookID string) {
	if d.Publish != nil {
		d.Publish(bookID)
	}
}

// bookOpenerFunc adapts the Open seam to the opener interface the
// generator service takes.
type bookOpenerFunc func(context.Context, model.Book) (storage.Source, error)

func (f bookOpenerFunc) Open(ctx context.Context, book model.Book) (storage.Source, error) {
	return f(ctx, book)
}
```

`ErrReadingGuidesDisabled` and its comment are unchanged. Replace the body of `ReadingGuide`:

```go
// ReadingGuide generates and stores one book's guide.
func ReadingGuide(ctx context.Context, a ReadingGuideArgs, deps ReadingGuideDeps) error {
	cfg, err := deps.Config(ctx)
	if err != nil {
		return fmt.Errorf("read guide settings: %w", err)
	}
	if !cfg.Enabled {
		slog.Debug("reading guide skipped: disabled", "book", a.BookID)
		return ErrReadingGuidesDisabled
	}

	completer, err := deps.Completer(cfg)
	if err != nil {
		return fmt.Errorf("configure model: %w", err)
	}

	// Re-read the book: a title or blurb edited since the job was queued
	// should reach the prompt, and a deleted book should not generate.
	book, err := deps.Books.GetByID(ctx, "", a.BookID)
	if err != nil {
		return fmt.Errorf("load book %s: %w", a.BookID, err)
	}

	svc := service.NewReadingGuideService(
		deps.Guides,
		bookOpenerFunc(deps.Open),
		completer,
		service.ReadingGuideOptions{
			Language: cfg.Language,
			TextCap:  cfg.TextCap,
			Model:    cfg.Model,
		},
	)
	if _, err := svc.Generate(ctx, book); err != nil {
		return fmt.Errorf("generate guide for %s: %w", a.BookID, err)
	}

	deps.publish(a.BookID)
	return nil
}
```

`errors` is still imported for `ErrReadingGuidesDisabled`'s `errors.New`.

- [ ] **Step 5: Rewire the registry**

In `internal/queue/registry.go`, replace the `readingGuide := task.ReadingGuideDeps{...}` block (lines 82-89):

```go
	// Settings is read per job so an admin can change model, language or
	// cap without a restart. Registered unconditionally, like the email
	// jobs: the worker itself refuses when the feature is disabled.
	readingGuide := task.ReadingGuideDeps{
		Config: deps.AppSettings.GetReadingGuide,
		Completer: func(c repo.ReadingGuideConfig) (service.GuideCompleter, error) {
			// Explicit rather than returning c.Client() directly: on
			// error that would box a nil *llm.Client into a non-nil
			// interface, and the caller's nil check would miss it.
			cl, err := c.Client()
			if err != nil {
				return nil, err
			}
			return cl, nil
		},
		Guides: deps.Guides,
		Books:  deps.Books,
		Open:   service.NewLibraryBookOpener(deps.LibStore).Open,
	}
	if deps.Hub != nil {
		readingGuide.Publish = func(bookID string) {
			_ = deps.Hub.Publish(sse.ReadingGuideUpdated{BookID: bookID})
		}
	}
```

- [ ] **Step 6: Run the tests and the linter**

```bash
go build ./... && go test ./internal/task/ -run TestReadingGuide -v && go test ./internal/service/ && make go-lint
```

Expected: four `TestReadingGuide*` PASS, the service suite still passes, lint clean.

- [ ] **Step 7: Commit**

```bash
git add internal/service/reading_guide.go internal/task/reading_guide.go internal/task/reading_guide_test.go internal/queue/registry.go
git commit -m "refactor(task): the guide worker declares what it calls

The worker turned a settings row into a live llm.Client itself, so there
was no point to substitute and no test. Config and Completer are seams
now, and the three branches that matter are characterised: disabled is
permanent, a deleted book never writes a guide, a terminal publish fires
once."
```

---

## Task 5: `FinalizeDeps` replaces the last of `AudiobookDeps`

**Files:**
- Modify: `internal/task/audiobook_finalize.go` (whole file)
- Modify: `internal/queue/registry.go` (the interim `audiobook` block)
- Create: `internal/task/audiobook_finalize_test.go`

**Interfaces:**
- Consumes: `bookReader` (Task 3), `mp3Frames`, `fakeBooks` (Task 2).
- Produces:
  - `type FinalizeDeps struct { Runs finalizeStore; Books bookAudioWriter; Files narrationFiles; Place func(context.Context, model.Book, string) (service.PlaceResult, error); Cover func(model.Book) (io.ReadCloser, error); Publish func(bookID string); DataPath string }`
  - `type finalizeStore interface { GetByBookID(ctx context.Context, bookID string) (model.Audiobook, error); ListSegments(ctx context.Context, bookID string) ([]model.AudiobookSegment, error); SetSegmentStart(ctx context.Context, bookID string, seq int, startMS int64) error; SetState(ctx context.Context, bookID string, state model.AudiobookState, msg string) error; SetReady(ctx context.Context, bookID, fileID string, durationMS int64) error }`
  - `type bookAudioWriter interface { bookReader; UpdateAudio(ctx context.Context, id string, durationSeconds *int, narrator string, chapters []model.Chapter) error }`
  - `type narrationFiles interface { GetByLocation(ctx context.Context, libraryID, location string) (model.File, error); SetContentHash(ctx context.Context, fileID string, hash []byte, size int64, mtime time.Time) error; Insert(ctx context.Context, f model.File) (model.File, error) }`
  - `AudiobookDeps` is deleted.

- [ ] **Step 1: Write the failing tests**

Create `internal/task/audiobook_finalize_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/BlackForgeHQ/embookshelf/internal/model"
	"github.com/BlackForgeHQ/embookshelf/internal/repo"
	"github.com/BlackForgeHQ/embookshelf/internal/service"
	"github.com/BlackForgeHQ/embookshelf/internal/task"
)

// ---------------------------------------------------------------------------
// Fakes local to finalize
// ---------------------------------------------------------------------------

type stateWrite struct {
	State model.AudiobookState
	Msg   string
}

type fakeFinalizeRuns struct {
	run      model.Audiobook
	segments []model.AudiobookSegment
	states   []stateWrite
	starts   map[int]int64
	ready    *readyWrite
}

type readyWrite struct {
	FileID  string
	TotalMS int64
}

func (f *fakeFinalizeRuns) GetByBookID(context.Context, string) (model.Audiobook, error) {
	return f.run, nil
}

func (f *fakeFinalizeRuns) ListSegments(context.Context, string) ([]model.AudiobookSegment, error) {
	return f.segments, nil
}

func (f *fakeFinalizeRuns) SetSegmentStart(_ context.Context, _ string, seq int, startMS int64) error {
	if f.starts == nil {
		f.starts = map[int]int64{}
	}
	f.starts[seq] = startMS
	return nil
}

func (f *fakeFinalizeRuns) SetState(_ context.Context, _ string, state model.AudiobookState, msg string) error {
	f.states = append(f.states, stateWrite{State: state, Msg: msg})
	return nil
}

func (f *fakeFinalizeRuns) SetReady(_ context.Context, _, fileID string, totalMS int64) error {
	f.ready = &readyWrite{FileID: fileID, TotalMS: totalMS}
	return nil
}

type fakeFiles struct {
	existing  *model.File
	inserted  []model.File
	hashSets  int
	nextID    string
	lookupErr error
}

func (f *fakeFiles) GetByLocation(context.Context, string, string) (model.File, error) {
	if f.existing != nil {
		return *f.existing, nil
	}
	if f.lookupErr != nil {
		return model.File{}, f.lookupErr
	}
	return model.File{}, repo.ErrNotFound
}

func (f *fakeFiles) SetContentHash(context.Context, string, []byte, int64, time.Time) error {
	f.hashSets++
	return nil
}

func (f *fakeFiles) Insert(_ context.Context, file model.File) (model.File, error) {
	if f.nextID != "" {
		file.ID = f.nextID
	}
	f.inserted = append(f.inserted, file)
	return file, nil
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type finalizeHarness struct {
	deps      task.FinalizeDeps
	runs      *fakeFinalizeRuns
	books     *fakeBooks
	files     *fakeFiles
	dataPath  string
	published int
	placed    int
	placeErr  error
}

// newFinalizeHarness stages two done segments on disk, which is the
// state a run is in when the last segment dispatches finalize.
func newFinalizeHarness(t *testing.T) *finalizeHarness {
	t.Helper()
	h := &finalizeHarness{
		runs: &fakeFinalizeRuns{
			run: model.Audiobook{BookID: "b1", State: model.AudiobookRunning, Engine: "openai"},
		},
		books: &fakeBooks{book: model.Book{
			ID: "b1", LibraryID: "lib1", Title: "Dune", Author: "Frank Herbert", Format: "EPUB",
		}},
		files:    &fakeFiles{nextID: "file-1"},
		dataPath: t.TempDir(),
	}

	dir := task.StagingDir(h.dataPath, "b1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create staging: %v", err)
	}
	for seq := range 2 {
		path := filepath.Join(dir, "seg-"+strconv.Itoa(seq)+".mp3")
		if err := os.WriteFile(path, mp3Frames(4), 0o600); err != nil {
			t.Fatalf("stage segment %d: %v", seq, err)
		}
		h.runs.segments = append(h.runs.segments, model.AudiobookSegment{
			BookID: "b1", Seq: seq, ChapterIndex: seq, ChapterTitle: "Chapter",
			State: model.SegmentDone, StagedPath: path, DurationMS: 100,
		})
	}

	h.deps = task.FinalizeDeps{
		Runs:  h.runs,
		Books: h.books,
		Files: h.files,
		Place: func(_ context.Context, _ model.Book, _ string) (service.PlaceResult, error) {
			h.placed++
			if h.placeErr != nil {
				return service.PlaceResult{}, h.placeErr
			}
			return service.PlaceResult{
				Location: "Dune/Dune.mp3", Size: 1234, Mtime: time.Unix(0, 0).UTC(),
			}, nil
		},
		Publish:  func(string) { h.published++ },
		DataPath: h.dataPath,
	}
	return h
}

func (h *finalizeHarness) run() error {
	return task.AudiobookFinalize(context.Background(), task.AudiobookFinalizeArgs{BookID: "b1"}, h.deps)
}

func (h *finalizeHarness) stagingExists() bool {
	_, err := os.Stat(task.StagingDir(h.dataPath, "b1"))
	return err == nil
}

// ---------------------------------------------------------------------------
// Refusals
// ---------------------------------------------------------------------------

// Cancelled between the last segment landing and this job being picked
// up. Sweep rather than publish — a cancel means the user does not want
// the partial, and here the partial happens to be complete.
func TestFinalizeSweepsACanceledRunWithoutPublishing(t *testing.T) {
	h := newFinalizeHarness(t)
	h.runs.run.State = model.AudiobookCanceled

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize: %v", err)
	}
	if h.stagingExists() {
		t.Error("a canceled run's staging survived")
	}
	if h.published != 0 {
		t.Errorf("published %d times for a canceled run", h.published)
	}
	if len(h.files.inserted) != 0 {
		t.Errorf("inserted %d files rows for a canceled run", len(h.files.inserted))
	}
}

func TestFinalizeIsANoOpForAReadyRun(t *testing.T) {
	h := newFinalizeHarness(t)
	h.runs.run.State = model.AudiobookReady

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize: %v", err)
	}
	if h.placed != 0 || h.published != 0 {
		t.Errorf("placed=%d published=%d, want neither on a finished run", h.placed, h.published)
	}
}

// Reached before the run actually finished. Not an error — the segment
// that completes last dispatches this again.
func TestFinalizeDefersWhileASegmentIsOutstanding(t *testing.T) {
	h := newFinalizeHarness(t)
	h.runs.segments[1].State = model.SegmentPending

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize: %v", err)
	}
	if h.placed != 0 {
		t.Errorf("placed %d times with a segment still outstanding", h.placed)
	}
	if h.published != 0 {
		t.Errorf("published %d times before the run finished", h.published)
	}
	if !h.stagingExists() {
		t.Error("staging was swept while a segment was still outstanding")
	}
}

func TestFinalizeFailsARunWithNoSegments(t *testing.T) {
	h := newFinalizeHarness(t)
	h.runs.segments = nil

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize returned %v, want nil so River stops", err)
	}
	if len(h.runs.states) != 1 || h.runs.states[0].State != model.AudiobookFailed {
		t.Fatalf("states = %+v, want one failed write", h.runs.states)
	}
}

// ---------------------------------------------------------------------------
// ADR-0028 §6 — failure keeps the paid-for work
// ---------------------------------------------------------------------------

// Retry re-enqueues only the segments that never finished, so every
// paid-for segment has to survive a failed finalize.
func TestFinalizeRetainsStagingWhenPlacementFails(t *testing.T) {
	h := newFinalizeHarness(t)
	h.placeErr = errors.New("backend refused the upload")

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize returned %v, want nil so River stops", err)
	}
	if len(h.runs.states) != 1 || h.runs.states[0].State != model.AudiobookFailed {
		t.Fatalf("states = %+v, want one failed write", h.runs.states)
	}
	if !h.stagingExists() {
		t.Error("a failed finalize reclaimed the staging — Retry would have to buy it again")
	}
	if h.published != 1 {
		t.Errorf("published %d times, want exactly one terminal event", h.published)
	}
}

// ---------------------------------------------------------------------------
// Completion
// ---------------------------------------------------------------------------

func TestFinalizePublishesTheNarrationAndSweeps(t *testing.T) {
	h := newFinalizeHarness(t)

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize: %v", err)
	}
	if len(h.files.inserted) != 1 {
		t.Fatalf("inserted %d files rows, want 1", len(h.files.inserted))
	}
	got := h.files.inserted[0]
	if got.Format != "MP3" {
		t.Errorf("format = %q, want MP3", got.Format)
	}
	if len(got.ContentHash) == 0 {
		t.Error("content_hash is empty — the rename safety net a library scan runs is blind to it")
	}
	if h.runs.ready == nil {
		t.Fatal("the run was never marked ready")
	}
	if h.runs.ready.FileID != "file-1" {
		t.Errorf("file id = %q, want the inserted row's", h.runs.ready.FileID)
	}
	if h.runs.ready.TotalMS <= 0 {
		t.Errorf("total = %dms, want it measured from the frames", h.runs.ready.TotalMS)
	}
	if h.books.audio == nil || h.books.audio.DurationSeconds == nil {
		t.Fatal("the book row's duration was never written")
	}
	if len(h.books.audio.Chapters) != 2 {
		t.Errorf("wrote %d chapters, want one per chapter index", len(h.books.audio.Chapters))
	}
	if h.books.audio.Narrator != "" {
		t.Errorf("narrator = %q — it means what the file's tags said, and a synthesized voice is not that",
			h.books.audio.Narrator)
	}
	if h.stagingExists() {
		t.Error("staging survived a successful finalize")
	}
	if h.published != 1 {
		t.Errorf("published %d times, want exactly 1", h.published)
	}
	if len(h.runs.starts) != 2 {
		t.Errorf("recorded %d segment starts, want the alignment map completed", len(h.runs.starts))
	}
}

// Regeneration lands on the same key. Inserting unconditionally would
// violate files' UNIQUE(library_id, location), and on a backend it would
// leave the old row pointing at bytes the new upload overwrote.
func TestFinalizeUpdatesTheRowAPreviousRenditionLeft(t *testing.T) {
	h := newFinalizeHarness(t)
	h.files.existing = &model.File{ID: "old-file", LibraryID: "lib1", BookID: "b1", Location: "Dune/Dune.mp3"}

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize: %v", err)
	}
	if len(h.files.inserted) != 0 {
		t.Errorf("inserted %d rows, want the existing one reused", len(h.files.inserted))
	}
	if h.files.hashSets != 1 {
		t.Errorf("set the hash %d times, want 1", h.files.hashSets)
	}
	if h.runs.ready == nil || h.runs.ready.FileID != "old-file" {
		t.Errorf("ready points at %+v, want the reused row", h.runs.ready)
	}
}

// A narration without embedded art is still a good narration.
func TestFinalizeSucceedsWithNoCoverSource(t *testing.T) {
	h := newFinalizeHarness(t)
	h.deps.Cover = nil
	h.books.book.HasCover = true

	if err := h.run(); err != nil {
		t.Fatalf("AudiobookFinalize: %v", err)
	}
	if h.runs.ready == nil {
		t.Error("the run was not marked ready without a cover source")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
go test ./internal/task/ -run TestFinalize
```

Expected: FAIL to compile — `undefined: task.FinalizeDeps`.

- [ ] **Step 3: Replace `AudiobookDeps` with `FinalizeDeps`**

In `internal/task/audiobook_finalize.go`, set the imports:

```go
import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/BlackForgeHQ/embookshelf/internal/audio"
	"github.com/BlackForgeHQ/embookshelf/internal/model"
	"github.com/BlackForgeHQ/embookshelf/internal/repo"
	"github.com/BlackForgeHQ/embookshelf/internal/service"
)
```

Delete the `AudiobookDeps` struct and the `publishAudiobook` function that Task 3 moved here. Add, immediately below the `audiobookGenre` const:

```go
// finalizeStore is the slice of BookAudiobookRepo finalize touches.
type finalizeStore interface {
	GetByBookID(ctx context.Context, bookID string) (model.Audiobook, error)
	ListSegments(ctx context.Context, bookID string) ([]model.AudiobookSegment, error)
	SetSegmentStart(ctx context.Context, bookID string, seq int, startMS int64) error
	SetState(ctx context.Context, bookID string, state model.AudiobookState, msg string) error
	SetReady(ctx context.Context, bookID, fileID string, durationMS int64) error
}

// bookAudioWriter reads the book and writes back what only a finished
// narration knows: its duration and its chapter marks.
type bookAudioWriter interface {
	bookReader
	UpdateAudio(ctx context.Context, id string, durationSeconds *int, narrator string, chapters []model.Chapter) error
}

// narrationFiles is the files-table slice finalize needs to record the
// placed audio, reusing the row a previous rendition left behind.
type narrationFiles interface {
	GetByLocation(ctx context.Context, libraryID, location string) (model.File, error)
	SetContentHash(ctx context.Context, fileID string, hash []byte, size int64, mtime time.Time) error
	Insert(ctx context.Context, f model.File) (model.File, error)
}

// FinalizeDeps groups the seams the finalize worker needs.
type FinalizeDeps struct {
	Runs  finalizeStore
	Books bookAudioWriter
	Files narrationFiles
	// Place moves the assembled file into the book's own folder.
	// Deliberately narrower than a LibraryStore: the book's folder
	// already exists, and the generic Placer would answer that with a
	// "Title (2)" sibling — a second leaf that scan reads as a second
	// book. Handing over the whole store would let a later edit reach it.
	Place func(ctx context.Context, book model.Book, srcPath string) (service.PlaceResult, error)
	// Cover supplies the art embedded in the finished file. Nil-able and
	// best effort: a narration without embedded art is still a good
	// narration.
	Cover    func(book model.Book) (io.ReadCloser, error)
	Publish  func(bookID string)
	DataPath string
}

func (d FinalizeDeps) publish(bookID string) {
	if d.Publish != nil {
		d.Publish(bookID)
	}
}
```

Now rethread every function in the file. `AudiobookFinalize(ctx, a, deps FinalizeDeps)`:

- `deps.Audiobooks.GetByBookID` → `deps.Runs.GetByBookID`
- `deps.Audiobooks.ListSegments` → `deps.Runs.ListSegments`
- `deps.Audiobooks.SetReady` → `deps.Runs.SetReady`
- `publishAudiobook(deps, a.BookID)` → `deps.publish(a.BookID)`
- replace the `handle, err := deps.LibStore.For(...)` block and the `placed, err := handle.PlaceNarration(...)` block with one call, keeping the `PlaceNarration` comment on `FinalizeDeps.Place` where it now lives:

```go
	placed, err := deps.Place(ctx, book, assembled)
	if err != nil {
		return fail(ctx, deps, a.BookID, fmt.Errorf("place narration: %w", err))
	}
```

The hash is still taken before placement, with its comment, because placement consumes the file.

`upsertNarrationFile(ctx, deps FinalizeDeps, ...)` — `deps.Files` calls are already named that way; only the parameter type changes.

`assemble(ctx, deps FinalizeDeps, ...)` — `deps.Audiobooks.SetSegmentStart` → `deps.Runs.SetSegmentStart`.

`loadCover(deps FinalizeDeps, book model.Book) ([]byte, string)`:

```go
func loadCover(deps FinalizeDeps, book model.Book) ([]byte, string) {
	if deps.Cover == nil || !book.HasCover {
		return nil, ""
	}
	rc, err := deps.Cover(book)
	if err != nil {
		return nil, ""
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		return nil, ""
	}
	return b, book.CoverMime
}
```

The doc comment above it is unchanged.

`fail(ctx, deps FinalizeDeps, ...)` — `deps.Audiobooks.SetState` → `deps.Runs.SetState`, `publishAudiobook(deps, bookID)` → `deps.publish(bookID)`.

- [ ] **Step 4: Rewire the registry**

In `internal/queue/registry.go`, replace the interim `audiobook := task.AudiobookDeps{...}` block:

```go
	finalize := task.FinalizeDeps{
		Runs:     deps.Audiobooks,
		Books:    deps.Books,
		Files:    deps.FileRepo,
		DataPath: deps.DataPath,
		Place: func(ctx context.Context, book model.Book, srcPath string) (service.PlaceResult, error) {
			handle, err := deps.LibStore.For(ctx, book.LibraryID)
			if err != nil {
				return service.PlaceResult{}, fmt.Errorf("resolve library: %w", err)
			}
			return handle.PlaceNarration(ctx, book, srcPath)
		},
	}
	if deps.Covers != nil {
		finalize.Cover = deps.Covers.Open
	}
	if deps.Hub != nil {
		finalize.Publish = func(bookID string) {
			_ = deps.Hub.Publish(sse.AudiobookUpdated{BookID: bookID})
		}
	}
```

Change the finalize registration:

```go
		register(func(ctx context.Context, a task.AudiobookFinalizeArgs) error {
			return task.AudiobookFinalize(ctx, a, finalize)
		}),
```

Add `"fmt"` and `"github.com/BlackForgeHQ/embookshelf/internal/model"` to the imports.

- [ ] **Step 5: Run the tests**

```bash
go build ./... && go test ./internal/task/ -run TestFinalize -v
```

Expected: all nine `TestFinalize*` PASS.

- [ ] **Step 6: Full suite and linter**

```bash
make test && make go-lint
```

Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add internal/task/audiobook_finalize.go internal/task/audiobook_finalize_test.go internal/queue/registry.go
git commit -m "refactor(task): the finalize worker declares what it calls

AudiobookDeps is gone. FinalizeDeps names four narrow interfaces and
three function seams, and ADR-0028 §6 is now pinned by a test rather
than by a comment: a failed placement keeps every paid-for segment on
disk, a cancel sweeps them."
```

---

## Task 6: Record the decision

**Files:**
- Modify: `CONTEXT.md` (audiobook / task entries, if they name `AudiobookDeps`)
- External: GitHub issue #177

- [ ] **Step 1: Check whether CONTEXT.md went stale**

```bash
grep -n "AudiobookDeps\|ReadingGuideDeps\|LoopAudiobookStagingSweep\|SweepAudiobookStaging" CONTEXT.md
```

If any hit names a type or signature this plan changed, update the line to match. If there are no hits, skip to Step 2 with no edit.

- [ ] **Step 2: Replace the issue's acceptance criteria**

```bash
gh issue comment 177 --body "Design agreed and implemented; the shared generation module was rejected. The guide job and the audiobook segment job diverge at three of the nine steps — one remote call versus N chunked cancel-checked ones, one upsert versus claim/stage/record/dispatch, one terminal publish versus three branch-local ones. A module owning the shape with those parameterised needs hooks for chunking, cancellation, claiming and staging: a template method with six holes for two callers, one of which uses four.

What was actually broken is dependency width, which is the second half of this issue. See \`docs/spec/generation-worker-seams.spec.md\` for the full reasoning and the replacement acceptance criteria."
```

Then edit the issue body's checklist to the criteria in §6 of the spec.

- [ ] **Step 3: Final verification**

```bash
make ci-local
```

Expected: every check passes.

- [ ] **Step 4: Commit any CONTEXT.md change**

```bash
git add CONTEXT.md
git commit -m "docs(context): the generation workers no longer share a deps struct"
```

Skip this commit if Step 1 found nothing to change.

---

## Self-Review Notes

**Spec coverage.** §2.1 → Task 3. §2.2 → Task 5. §2.3 → Task 4. §2.4 → Task 1. §2.5 → the `publish` methods in Tasks 3, 4 and 5, plus the annotated `deps.Finalize == nil` guard in Task 3 Step 3. §3 → enforced by the Global Constraints and by the tests characterising existing behaviour. §4.1 → Task 3's fourteen tests. §4.2 → Task 5's nine. §4.3 → Task 4's four. §4.4 → Task 1. §5 → the registry and main.go edits inside Tasks 1, 3, 4 and 5. §6 → Task 6.

**One deviation from the spec, deliberate.** §2.5 says the worker's nil guard "stays" because the enqueue seam cannot be a plain value while the import cycle exists. Task 3 keeps the guard but moves the late-binding dereference into a registry closure that returns a descriptive error. The guard is therefore unreachable in production and defensive only for a zero-value `SegmentDeps`; the closure is what #184 deletes. This is strictly better than a nil `Finalize` field, and the comment says so.

**Interim state.** Task 3 leaves `AudiobookDeps` alive for the finalize worker and moves it, with `publishAudiobook`, into `audiobook_finalize.go`. Task 5 deletes both. The build is green at every commit.
