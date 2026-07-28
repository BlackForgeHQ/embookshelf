# Chunking In The Adapter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the split, the per-chunk cancel check and the frame join inside the TTS engine adapter, so the per-request cap becomes an implementation detail and a caller can use `tts.Engine` correctly knowing only `Synthesize`.

**Architecture:** The sentence splitter becomes `internal/textsplit`, a leaf both `fileproc` and `tts` import. Each TTS adapter implements an unexported per-request `speaker`; one `chunked` wrapper written once owns the loop. `tts.Info` carries its own constructor so `tts.New`'s switch disappears. The settings row's per-engine slot stays a struct and is guarded by a test instead.

**Tech Stack:** Go, standard library only. No new dependencies.

**Spec:** `docs/spec/chunking-in-the-adapter.spec.md`
**Depends on:** #177 and #184 — this branch (`refactor/chunking-in-the-adapter`) stacks on `refactor/one-enqueue-seam`.

## Global Constraints

- **No behaviour changes. None.** This is a pure relocation of responsibility. Every branch, error wrap, ordering decision and explanatory comment survives verbatim; only which package holds them changes.
- **[#185](https://github.com/BlackForgeHQ/embookshelf/issues/185)'s asymmetry moves without being fixed.** Unusable audio is a permanent failure at one chunk and an untagged retryable error at several, because the multi-chunk join wraps with `fmt.Errorf("chunk %d: %w", i, err)`. Preserve that wrap byte-for-byte. Fixing it here would smuggle a behaviour change into a refactor and silently invalidate the tests that pin it.
- **Postgres only (ADR-0023).** No dialect branches.
- **Every file starts with** `// SPDX-License-Identifier: AGPL-3.0-or-later` followed by a blank line.
- **`internal/textsplit` imports only `strings`.** A repo import there is a design failure.
- **Comments explain why, not what** — and not what the code used to be. Carry existing comments with the code they explain.
- **Gates:** `go build ./...`, `make test`, and `make go-lint` pass at every commit. `go test -race ./internal/task/` has a known pre-existing flake in `drain_test.go` (issue #186) — filter it rather than chasing it.
- Commit messages carry **no** `Co-Authored-By` or `Claude-Session` trailers.
- Postgres is running; export `TEST_DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable'` before running Go tests.

---

## File Structure

**Created:**
- `internal/textsplit/textsplit.go`, `textsplit_test.go` — the sentence splitter, moved
- `internal/audio/audiotest/audiotest.go` — shared MPEG frame fixtures
- `internal/tts/chunking.go`, `chunking_test.go` — the `speaker`/`chunked` pair and its tests

**Modified:**
- `internal/fileproc/epub_segments.go` — `SplitForSynthesis` leaves; the call site uses `textsplit`
- `internal/tts/tts.go` — `Request.BeforeChunk`, `Info.newSpeaker`, `New`'s switch deleted
- `internal/tts/engines.go` — three `Synthesize` methods become `speak`
- `internal/task/audiobook.go` — hands over a whole segment; `joinParts` deleted
- `internal/task/audiobook_segment_test.go`, `generation_fakes_test.go` — fake engine takes a segment; frame builder replaced
- `internal/repo/audiobook_settings_test.go` — the dispatch-site guard

---

## Task 1: The splitter becomes a leaf

**Files:**
- Create: `internal/textsplit/textsplit.go`, `internal/textsplit/textsplit_test.go`
- Modify: `internal/fileproc/epub_segments.go`, `internal/task/audiobook.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `textsplit.OnSentences(text string, maxChars int) []string`. `fileproc.SplitForSynthesis` no longer exists.

- [ ] **Step 1: Move the function and its helpers**

Create `internal/textsplit/textsplit.go`. Take `SplitForSynthesis`, `sentenceEnders` and `sentenceBoundaryBefore` from `internal/fileproc/epub_segments.go` **with every comment verbatim** — read them with `git show HEAD:internal/fileproc/epub_segments.go` rather than retyping. Rename only the exported function:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package textsplit cuts prose into budgeted pieces on sentence
// boundaries.
//
// Two callers at two granularities: the segment planner splits a chapter
// into River jobs, and a TTS adapter splits a segment into engine calls.
// Same algorithm, different budgets — which is why this is neither the
// EPUB extractor's nor the speech package's to own.
package textsplit

import "strings"

// OnSentences cuts text into pieces of at most maxChars characters.
//
// <carry the rest of SplitForSynthesis's doc comment verbatim>
func OnSentences(text string, maxChars int) []string {
	// <body unchanged>
}
```

`sentenceEnders` and `sentenceBoundaryBefore` move unexported, comments intact.

- [ ] **Step 2: Move the tests**

Find the existing tests for `SplitForSynthesis` (`grep -n "SplitForSynthesis" internal/fileproc/*_test.go`). Move every one to `internal/textsplit/textsplit_test.go`, package `textsplit_test`, renaming only the call. Do not weaken or drop any case — they encode the sentence-boundary, rune-safety and window behaviour.

- [ ] **Step 3: Update both call sites, delete the original**

`internal/fileproc/epub_segments.go:122` becomes `for _, chunk := range textsplit.OnSentences(text, opts.maxChars())`. Add the import; delete `SplitForSynthesis`, `sentenceEnders` and `sentenceBoundaryBefore` from that file.

`internal/task/audiobook.go:259` becomes `textsplit.OnSentences(text, sel.Info.MaxRequestChars)`. This line is deleted entirely in Task 5; retype it now so the tree builds.

- [ ] **Step 4: Confirm the leaf holds and nothing was lost**

```bash
go list -f '{{.Imports}}' ./internal/textsplit    # expect [strings] only
grep -rn "SplitForSynthesis" --include='*.go' .   # expect no hits
go build ./... && go test ./internal/textsplit/ ./internal/fileproc/ ./internal/task/ -v 2>&1 | tail -30
```

- [ ] **Step 5: Full gates and commit**

```bash
make test && make go-lint
git add internal/textsplit/ internal/fileproc/ internal/task/
git commit -m "refactor(textsplit): the sentence splitter becomes a leaf

Two callers at two granularities -- the segment planner splitting a
chapter into jobs, and shortly a TTS adapter splitting a segment into
engine calls. It belongs to neither the EPUB extractor nor the speech
package, and tts importing fileproc for forty lines would drag model,
storage and sidecar into a speech adapter.

Renamed from SplitForSynthesis because only one of the two callers is
synthesising."
```

---

## Task 2: Shared frame fixtures

Three packages need MPEG-1 Layer III frames once `internal/tts` starts joining audio. Two copies was defensible; three is not.

**Files:**
- Create: `internal/audio/audiotest/audiotest.go`
- Modify: `internal/task/generation_fakes_test.go`

**Interfaces:**
- Produces: `audiotest.Frames(n int) []byte` — n back-to-back MPEG-1 Layer III frames of silence; `audiotest.FrameBytes` — the size of one frame.

- [ ] **Step 1: Write the package**

Create `internal/audio/audiotest/audiotest.go`. The frame shape must be identical to the one `internal/audio/mp3_test.go` already validates — read it from there, do not invent it.

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package audiotest builds MPEG audio fixtures for tests in other
// packages.
//
// Its own package rather than an export from internal/audio because a
// frame builder is test support, and audio's job is parsing real files —
// putting a fixture in its production surface would invite production
// use. Same shape as repotest and storagetest.
package audiotest

import "bytes"

// frameHeader is one MPEG-1 Layer III frame header: 128 kbit/s,
// 44.1 kHz, stereo, no CRC. Every engine in the catalog emits exactly
// this shape, which is what makes byte-wise concatenation legal
// (ADR-0027).
var frameHeader = []byte{0xFF, 0xFB, 0x90, 0x00}

// FrameBytes is the size of the frame above: 144 * 128000 / 44100.
const FrameBytes = 417

// Frames builds n back-to-back frames of silence.
func Frames(n int) []byte {
	var b bytes.Buffer
	for range n {
		b.Write(frameHeader)
		b.Write(make([]byte, FrameBytes-len(frameHeader)))
	}
	return b.Bytes()
}
```

- [ ] **Step 2: Prove it against the real parser**

Add `internal/audio/audiotest/audiotest_test.go`, package `audiotest_test`, importing both `audio` and `audiotest`: assert `audio.Payload(audiotest.Frames(4))` returns `4 * audiotest.FrameBytes` of frames, a positive duration, and no error. A fixture the production parser rejects would fail every downstream test with a message about the fixture rather than the code.

- [ ] **Step 3: Delete `internal/task`'s copy**

In `internal/task/generation_fakes_test.go`, delete `mpeg1FrameHeader`, `mp3FrameBytes` and `mp3Frames`, and the comment explaining the duplication (it is no longer true). Replace every use across `internal/task`'s tests with `audiotest.Frames(...)` and `audiotest.FrameBytes`.

`internal/audio`'s own `mp3_test.go` keeps its copy — importing your own test-support package from your own tests is a cycle, and an in-package fixture the package under test also validates is a different thing.

- [ ] **Step 4: Gates and commit**

```bash
go build ./... && make test && make go-lint
git add internal/audio/ internal/task/
git commit -m "test(audiotest): one frame fixture for three packages

internal/tts is about to need MPEG frames to test joining, which would
have been the third copy. A test-support package, following repotest
and storagetest, rather than an export from internal/audio -- a frame
builder in the production surface of a package whose job is parsing
real files invites production use."
```

---

## Task 3: The catalog entry carries its adapter

**Files:**
- Modify: `internal/tts/tts.go`
- Create: `internal/repo/audiobook_settings_test.go` (or extend, if one exists)

**Interfaces:**
- Consumes: nothing from Tasks 1–2.
- Produces: `Info.newSpeaker func(Config) speaker` (unexported); `tts.New` with no `switch`. At this point `speaker` is satisfied by the existing adapters' `Synthesize` methods — Task 4 renames them.

- [ ] **Step 1: Write the dispatch-site guard first**

`internal/repo`'s test asserting every catalog engine has a settings slot. Check whether `internal/repo/audiobook_settings_test.go` exists and extend it if so.

```go
// Adding a TTS engine means touching the catalog, its adapter, and this
// settings row's per-engine slot. The first two are one declaration
// after #183; this is the third site, and it cannot join them without
// changing the shape of persisted settings. So it fails loudly instead.
func TestEveryCatalogEngineHasASettingsSlot(t *testing.T) {
	var cfg AudiobookConfig
	for _, info := range tts.Catalog {
		if (&cfg).EngineSlot(info.ID) == nil {
			t.Errorf("engine %q is in the TTS catalog but has no settings slot — "+
				"add one to AudiobookConfig and to EngineSlot's switch", info.ID)
		}
	}
}
```

Match the package of the existing repo tests (`repo` or `repo_test`); `EngineSlot` has a pointer receiver on an unexported-field struct, so an in-package test may be required — check before writing.

- [ ] **Step 2: Run it — it should PASS**

```bash
go test ./internal/repo/ -run TestEveryCatalogEngineHasASettingsSlot -v
```

Expected: PASS. This guard describes an invariant that already holds; it exists to fail on the *next* engine. Prove it can fail: temporarily add a fourth entry to `tts.Catalog` with a new `EngineID`, re-run, confirm FAIL, then remove it. Record both outputs — a guard that cannot fail guards nothing.

- [ ] **Step 3: Move the constructors onto the catalog**

In `internal/tts/tts.go`, add to `Info`:

```go
	// newSpeaker builds this engine's adapter. Declared on the catalog
	// entry so adding an engine is one entry rather than an entry plus a
	// switch case somewhere else that nothing forces to agree with it.
	//
	// Unexported because only this package declares engines. The
	// settings handler reads Info's named fields and never marshals it.
	newSpeaker func(Config) speaker
```

Declare the `speaker` interface in `tts.go` for now (Task 4 moves it to `chunking.go` and gives it its final shape):

```go
// speaker is one engine's adapter.
type speaker interface {
	Synthesize(ctx context.Context, r Request) ([]byte, error)
	ListVoices(ctx context.Context) ([]Voice, error)
}
```

Set `newSpeaker` on each of the three `Catalog` entries — e.g. `newSpeaker: func(c Config) speaker { return &openAIEngine{cfg: c} }`.

Replace `New`'s switch with:

```go
	if info.newSpeaker == nil {
		return nil, fmt.Errorf("tts: engine %q is in the catalog but has no adapter", id)
	}
	return info.newSpeaker(cfg), nil
```

Base-URL resolution above it is unchanged.

- [ ] **Step 4: Gates and commit**

```bash
go build ./... && go test ./internal/tts/ ./internal/repo/ -v 2>&1 | tail -20 && make test && make go-lint
git add internal/tts/ internal/repo/
git commit -m "refactor(tts): the catalog entry carries its own adapter

Adding an engine meant editing the catalog, a switch in New, and the
settings row's per-engine slot, with nothing forcing the three to agree
-- the slot accessor's own comment warned about it.

The first two are now one declaration. The third is a persisted JSON
shape and cannot join them without a migration, so a test fails loudly
when a catalog engine has no slot."
```

---

## Task 4: Chunking moves inside

The substance of the issue.

**Files:**
- Create: `internal/tts/chunking.go`, `internal/tts/chunking_test.go`
- Modify: `internal/tts/tts.go`, `internal/tts/engines.go`

**Interfaces:**
- Consumes: `textsplit.OnSentences` (Task 1), `audiotest.Frames` (Task 2), `Info.newSpeaker` (Task 3).
- Produces: `Request` gains `BeforeChunk func(context.Context) error`; `speaker` has `speak` instead of `Synthesize`; `chunked` implements `Engine`. `tts.New` returns a `chunked`.

- [ ] **Step 1: Write the failing tests**

Create `internal/tts/chunking_test.go`, package `tts` (in-package — it exercises `speaker`, `chunked` and `newSpeaker`, all unexported).

Cover, with a fake `speaker` recording the texts it was handed:
- a segment at or below the cap → exactly one call, bytes returned **unchanged** (the engine's own tag survives)
- a segment above the cap → more than one call, every text within the cap, splits on sentence boundaries, pieces in order
- multi-piece audio is concatenated: total frames equal the sum of the parts, using `audiotest.Frames`
- `BeforeChunk` is called before every `speak`, and one call earlier than `speak` in total ordering
- `BeforeChunk` returning an error aborts, returns that error **unwrapped** (`errors.Is` against the caller's own sentinel must match), and makes no further `speak` calls
- a cancelled `ctx` stops the loop before the first call
- unusable multi-chunk audio produces `chunk N: ...`, untagged — with a comment naming #185 so a reader knows the asymmetry is pinned deliberately, not endorsed

Then the per-adapter cap tests, which are what make the cap an implementation detail. For each of `EngineOpenAI` (4096), `EngineElevenLabs` (5000) and `EngineAzure` (8000): build via `New`, point it at an `httptest.Server` returning `audiotest.Frames(2)`, hand it a segment several times that engine's cap, and assert **every request body's text is within that engine's own cap** and that more than one request arrived. Read each adapter's request encoding from `engines.go` — OpenAI sends JSON `input`, ElevenLabs JSON `text`, Azure SSML — do not assume.

Deriving the cap from `Lookup(id).MaxRequestChars` in the test is correct here; the point is that the three values differ, so a shared constant would not satisfy all three.

- [ ] **Step 2: Run to verify failure**

```bash
go test ./internal/tts/ -run 'TestChunk|TestSegment' 2>&1 | head -20
```

Expected: FAIL to compile — `chunked` and `BeforeChunk` do not exist.

- [ ] **Step 3: Add `BeforeChunk` to `Request`**

In `internal/tts/tts.go`, replace the `Request` doc comment and add the field, exactly as spec §4.1 gives it. Note in the comment that `Text` is a whole segment.

- [ ] **Step 4: Write `chunking.go`**

```go
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
// homework. ADR-0026 §1 settled that chunking is engine-specific — the
// catalog's caps differ by a factor of two — so the interface should
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
```

Delete the `speaker` declaration Task 3 put in `tts.go`.

- [ ] **Step 5: Rename the adapters' methods and wrap in `New`**

In `internal/tts/engines.go`, rename all three `Synthesize` methods to `speak` — signature and body otherwise untouched. `ListVoices` is unchanged.

In `New`, wrap:

```go
	return chunked{speaker: info.newSpeaker(cfg), maxChars: info.MaxRequestChars}, nil
```

- [ ] **Step 6: Run**

```bash
go build ./... && go test ./internal/tts/ -v 2>&1 | tail -40
```

Expected: the new chunking tests pass and every pre-existing `internal/tts` test still passes. The existing adapter tests call `Synthesize` with short text, which now goes through `chunked` as a single piece and returns unchanged — they should need no edits. If one does, say why in your report.

- [ ] **Step 7: Gates and commit**

```bash
make test && make go-lint
git add internal/tts/
git commit -m "refactor(tts): the adapter owns its own chunking

Using Synthesize correctly took four pieces of caller-side knowledge:
look the per-request cap up in the catalog, split the segment against
it, cancel-check per chunk, rejoin the frames. Roughly forty lines of
this package's contract enforced in internal/task.

ADR-0026 §1 already said chunking is engine-specific -- the caps differ
by a factor of two. Now the interface admits it: Synthesize takes a
whole segment, and the cap is nobody else's business."
```

---

## Task 5: The worker hands over a segment

**Files:**
- Modify: `internal/task/audiobook.go`, `internal/task/audiobook_segment_test.go`

**Interfaces:**
- Consumes: `tts.Request.BeforeChunk`, `chunked` via `tts.New`.
- Produces: `synthesizeSegment` makes one `Synthesize` call. `joinParts` no longer exists.

- [ ] **Step 1: Rewrite the worker's tail**

In `internal/task/audiobook.go`, `synthesizeSegment`'s chunking loop becomes one call. Everything above it — the book re-read, the `Narratable` gate, `deps.Engine(cfg)`, the run-vs-current engine pin, `segmentText` — is unchanged:

```go
	// The cancel check travels with the request: the adapter splits this
	// segment into as many engine calls as its cap needs, and runs this
	// before each one. It is the only thing between a user pressing stop
	// and the rest of a $170 run being billed anyway (ADR-0028 §6).
	return sel.Engine.Synthesize(ctx, tts.Request{
		Text:  text,
		Voice: run.Voice,
		Model: run.Model,
		BeforeChunk: func(ctx context.Context) error {
			if canceled(ctx, a.BookID, deps) {
				return errCanceled
			}
			return nil
		},
	})
```

Delete `joinParts`. Keep `canceled` and `errCanceled` — both still used. Drop the `textsplit` and `audio` imports if nothing else in the file needs them (`audio.Payload` is still called in `AudiobookSegment`, so `audio` stays).

- [ ] **Step 2: Move the fake engine's chunking into the fake**

`internal/task`'s `fakeEngine` now receives whole segments. Two existing tests depend on chunking happening, and they must keep their meaning at the worker level:

- `TestSegmentStopsSpendingWhenCancelLandsBetweenChunks` — the worker's job here is that its `BeforeChunk` closure reports a mid-run cancel and the segment is abandoned without a recorded result. Give `fakeEngine` a `chunks int` field; its `Synthesize` calls `r.BeforeChunk(ctx)` before each simulated piece and returns its error unwrapped. Set `chunks: 3` and keep every existing assertion — engine calls made, nothing recorded, nothing staged.
- `TestSegmentTreatsMultiChunkUnusableAudioAsRetryableUnlikeTheSingleChunkCase` — the *asymmetry* is now `internal/tts`'s (Task 4 pins it). What survives here is the worker's half: an untagged engine error is returned for River to retry, and one failed segment is recorded. Rewrite it to have the fake return an untagged error directly, rename it to say that, and leave a comment pointing at the `internal/tts` test that now owns the chunk-count asymmetry and at #185.

Every other segment test keeps its current shape.

- [ ] **Step 3: Run**

```bash
go test ./internal/task/ -run TestSegment -v 2>&1 | tail -40
```

Expected: all pass. If an assertion has to change, say exactly which and why in your report — the constraint is no behaviour change, so a changed assertion needs a reason that is about the fake, not about the worker.

- [ ] **Step 4: Confirm the caller-side knowledge is gone**

```bash
grep -n "MaxRequestChars\|joinParts\|OnSentences" internal/task/*.go
```

Expected: no hits. The settings handler's `MaxRequestChars` use is display and stays.

- [ ] **Step 5: Gates and commit**

```bash
make test && make go-lint
git add internal/task/
git commit -m "refactor(task): the segment worker hands over a whole segment

It no longer reads the engine's per-request cap, splits against it,
loops cancel checks, or rejoins frames. One Synthesize call, with the
cancel check travelling as a callback because the only real
implementation re-reads Postgres and internal/tts must not know that."
```

---

## Task 6: Record the decision

**Files:**
- Modify: `CONTEXT.md`, `docs/architecture.md` if either names what moved; GitHub issues #183 and #185

- [ ] **Step 1: Check the docs**

```bash
grep -n "SplitForSynthesis\|MaxRequestChars\|joinParts" CONTEXT.md docs/architecture.md
```

Correct any hit. Add `internal/textsplit` where the architecture doc lists packages, if it lists them.

- [ ] **Step 2: Update the issues**

#183: comment recording that the splitter became a leaf rather than moving into either caller and why, that the third dispatch site is guarded by a test rather than a migration because it is a persisted JSON shape, and that #185's asymmetry moved without being fixed. Replace the acceptance criteria with §6 of `docs/spec/chunking-in-the-adapter.spec.md`, preserving the originals below.

#185: comment noting the code moved — the join is now `internal/tts`'s `join`, and the asymmetry is pinned by a test there as well as in `internal/task`.

- [ ] **Step 3: Final verification**

```bash
make ci-local
```

- [ ] **Step 4: Commit any doc change**

```bash
git add CONTEXT.md docs/architecture.md
git commit -m "docs: chunking is the adapter's, and the splitter is a leaf"
```

Skip if Step 1 found nothing.

---

## Self-Review Notes

**Spec coverage.** §3 → Task 1. §4.4 → Task 2. §4.3 and §2 → Task 3. §4.1, §4.2 and §5's tts cases → Task 4. §5's task cases → Task 5. §6 → Task 6.

**Highest risk.** Task 5's test migration. Two `internal/task` tests currently depend on the worker doing the chunking; after this they depend on a fake that simulates it. The danger is quietly weakening them into tests that pass without exercising anything. Task 5 Step 2 names what each must still prove.

**Second risk.** Preserving #185's asymmetry while moving the code that causes it. Task 4's `join` keeps the untagged `chunk %d:` wrap and the single-piece passthrough verbatim; the comment says why, so a future reader does not "fix" it in passing.

**Ordering.** Tasks 1, 2 and 3 are independent and could run in any order. Task 4 needs all three. Task 5 needs Task 4.

**Deliberately not done.** `repo.AudiobookConfig`'s per-engine slots stay named struct fields — making them a map keyed by `EngineID` would change the persisted settings shape and need a migration, which is out of scope per spec §2.
