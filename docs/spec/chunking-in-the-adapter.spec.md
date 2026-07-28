# Chunking Moves Inside the Engine Adapter — Refactor Specification

> The TTS engine interface looks small — synthesis plus voice listing — but a caller cannot use it correctly without four further pieces of caller-side knowledge. ADR-0026 §1 already says chunking is engine-specific; the interface does not admit it. This moves the split, the cancel loop and the frame join inside the adapter, and makes the per-request cap an implementation detail.

- **Status:** Approved
- **Issue:** [#183](https://github.com/BlackForgeHQ/embookshelf/issues/183)
- **Scope:** new `internal/textsplit`, new `internal/audio/audiotest`; `internal/tts`, `internal/fileproc`, `internal/task`, `internal/repo`
- **Depends on:** [#177](https://github.com/BlackForgeHQ/embookshelf/issues/177) and [#184](https://github.com/BlackForgeHQ/embookshelf/issues/184) — this branch stacks on `refactor/one-enqueue-seam`
- **Companion artifacts:** `docs/adr/0026-tts-engine-catalog.md` §1, `docs/adr/0028-audiobook-generation-pipeline.md` §6, §8

---

## 1. What the caller currently has to know

`tts.Engine` is two methods. Using `Synthesize` correctly today requires all of this, outside the package:

```go
chunks := fileproc.SplitForSynthesis(text, sel.Info.MaxRequestChars)   // 1. read the cap, 2. split
parts := make([][]byte, 0, len(chunks))
for _, chunk := range chunks {
    if err := ctx.Err(); err != nil { return nil, err }
    if canceled(ctx, a.BookID, deps) { return nil, errCanceled }      // 3. cancel per chunk
    part, err := sel.Engine.Synthesize(ctx, tts.Request{...})
    if err != nil { return nil, err }
    parts = append(parts, part)
}
return joinParts(parts)                                               // 4. rejoin frames
```

Roughly forty lines of `internal/tts`'s contract, enforced in `internal/task`. ADR-0026 §1 states the rationale for closing this outright: per-request caps varying by 25% across the catalog mean chunking is engine-specific whether or not the interface admits it. It currently does not.

`sel.Info.MaxRequestChars` is read in exactly one place outside the package for this purpose (`internal/task/audiobook.go:259`). The settings UI reads it too (`internal/handler/audiobook_settings.go:209`), which is a display concern and stays.

## 2. The three dispatch sites

Adding an engine today means editing three places that nothing forces to agree:

1. `tts.Catalog` — the entry (`internal/tts/tts.go`)
2. `tts.New`'s `switch id` — the adapter (`internal/tts/tts.go`)
3. `repo.AudiobookConfig.EngineSlot`'s `switch id` — the settings row's per-engine slot (`internal/repo/audiobook_settings.go`)

Site 3's own doc comment already warns about this.

Sites 1 and 2 collapse: the catalog entry carries its own constructor, and the switch disappears.

Site 3 does not collapse cheaply. `OpenAI`, `ElevenLabs` and `Azure` are named struct fields with `json` tags, persisted in the settings row; replacing them with a map keyed by `EngineID` changes the stored shape and needs a migration and a compatibility path. The issue's acceptance criteria allow the alternative — "the three dispatch sites are made to fail loudly when they disagree" — so site 3 is covered by a test asserting every catalog entry resolves to a non-nil slot. A new engine fails the suite immediately, with no change to persisted data.

## 3. Where the splitter lives

`fileproc.SplitForSynthesis` has two callers at two granularities:

- `fileproc.ExtractEPUBSegments` splits a chapter into **segments** — one River job each
- the worker splits a segment into **chunks** — one engine call each

Same sentence-boundary algorithm, different budgets. Once chunking moves into the adapter, `internal/tts` needs it too.

`tts` importing `fileproc` was rejected: it would pull `model`, `storage` and `sidecar` into a speech adapter for forty lines, and a TTS package depending on the EPUB extractor reads backwards. Moving it into `tts` and having `fileproc` import that was also rejected — `fileproc`'s use is segment planning, not synthesis, so the name would be wrong at one of the two call sites.

It becomes **`internal/textsplit`**, a leaf importing only `strings`, with one exported function:

```go
// OnSentences cuts text into pieces of at most maxChars characters.
func OnSentences(text string, maxChars int) []string
```

Renamed from `SplitForSynthesis` because only one of its two callers is synthesising. The repo already carries small focused packages of this shape — `internal/audio`, `internal/hashing`, `internal/layout`, `internal/tagging`.

The algorithm, and every comment explaining it, moves verbatim: sentence boundaries because a mid-sentence cut is audible at every seam and a book is ~180 of them (ADR-0028 §8); runes not bytes because a byte index can split a multi-byte rune and because every engine's cap is quoted in characters; the look-back window because scanning the whole prefix would waste most of the cap.

## 4. Design

### 4.1 `Request` carries a whole segment

```go
// Request is one segment's worth of narration. Text is the whole
// segment — the adapter splits it against its own per-request cap.
type Request struct {
	Text  string
	Voice string
	Model string
	// BeforeChunk runs before each engine call and aborts the segment if
	// it returns an error, which is returned unwrapped so the caller can
	// match its own sentinel.
	//
	// A callback rather than a flag because the only real implementation
	// re-reads the run's state from Postgres, which this package must
	// not know about. It is the stop-loss on a run that may be $170
	// (ADR-0028 §6): a cancel that only took effect between segments
	// would keep spending for most of a dozen engine calls.
	BeforeChunk func(context.Context) error
}
```

`Engine` keeps its shape — `Synthesize(ctx, Request) ([]byte, error)` plus `ListVoices` — but `Synthesize` now accepts a whole segment and returns the whole segment's audio.

### 4.2 One chunking implementation, wrapping a per-request adapter

```go
// speaker is the per-request primitive: one engine call, one piece of
// audio. Each adapter implements this; chunking is written once.
type speaker interface {
	speak(ctx context.Context, r Request) ([]byte, error)
	ListVoices(ctx context.Context) ([]Voice, error)
}

// chunked turns a per-request speaker into an Engine that takes a whole
// segment, owning the split, the cancel check and the join.
type chunked struct {
	speaker
	maxChars int
}
```

`chunked.Synthesize` reproduces today's loop exactly: `ctx.Err()` then `BeforeChunk` before every call, `speak` per piece, join at the end.

**A single piece is returned unchanged**, as `joinParts` does today — it still carries the engine's own ID3 tag, and the worker's `audio.Payload` call strips it. Only multi-piece results are decoded and concatenated.

**The multi-chunk join keeps its current error shape verbatim**: `fmt.Errorf("chunk %d: %w", i, err)`, untagged. That untagged wrap is precisely the defect [#185](https://github.com/BlackForgeHQ/embookshelf/issues/185) tracks — unusable audio is permanent at one chunk and retryable at several. Fixing it here would be a behaviour change smuggled into a refactor, and would silently invalidate the two tests in `internal/task/audiobook_segment_test.go` that pin the current asymmetry. #185 moves; it does not get fixed. Its issue text is updated to say the code now lives in `internal/tts`.

### 4.3 The catalog entry carries its adapter

`Info` gains an unexported field:

```go
	// newSpeaker builds this engine's adapter. Declared on the catalog
	// entry so adding an engine is one entry rather than an entry plus a
	// switch case somewhere else that nothing forces to agree with it.
	newSpeaker func(Config) speaker
```

Unexported deliberately: only `internal/tts` declares engines, and `Info` is read by the settings handler, which copies named fields into its own DTO and never marshals `Info` directly.

`tts.New` loses its `switch` entirely. Base-URL resolution is unchanged; the "in the catalog but has no adapter" error survives as a nil check on `newSpeaker`.

### 4.4 `internal/audio/audiotest`

Three packages now need MPEG-1 Layer III frame fixtures: `internal/audio`'s own tests, `internal/task`'s (added under #177), and `internal/tts`'s. Two copies was defensible; three is not.

A test-support package, matching the existing `internal/repo/repotest` and `internal/storage/storagetest` precedent:

```go
// Frames builds n back-to-back MPEG-1 Layer III frames of silence.
func Frames(n int) []byte
```

`internal/task`'s copy is deleted in favour of it. `internal/audio`'s own tests keep theirs — an in-package fixture that the package under test also validates is not the same thing as shared support, and importing your own test-support package from your own tests is a cycle.

## 5. Testing

**`internal/textsplit`** — the existing `SplitForSynthesis` tests move with the function, unchanged in substance.

**`internal/tts`** — chunking is tested per adapter, which is what makes the cap an implementation detail rather than a convention:

- for each of the three engines, a segment larger than that engine's own `MaxRequestChars` produces more than one request, and **every request's text is within that engine's cap** — the caps differ (4096 / 5000 / 8000), so a test that passed against a shared constant would not pass here
- splits land on sentence boundaries
- a segment at or below the cap produces exactly one request, and its bytes come back unchanged including the engine's own tag
- `BeforeChunk` runs before every call, and its error aborts the segment, is returned unwrapped, and stops further calls
- a cancelled `ctx` stops the loop
- multi-chunk audio is concatenated in order and the total frame count is the sum of the parts
- the `chunk %d:` wrap on unusable multi-chunk audio is asserted as-is, with a comment pointing at #185 so a future reader knows it is pinned deliberately

**`internal/repo`** — every `tts.Catalog` entry resolves to a non-nil `EngineSlot`. This is the "fail loudly" guard for dispatch site 3.

**`internal/task`** — the segment worker's tests survive with their meaning intact. The two that pin #185's asymmetry keep pinning it. What changes is that the worker's fake engine now receives a whole segment rather than a chunk, so the harness's `MaxRequestChars` plumbing moves into the fake.

## 6. Acceptance criteria

These replace the criteria on #183, which predate the splitter and dispatch-site decisions.

- [ ] `tts.Engine.Synthesize` accepts a whole segment; no caller reads `MaxRequestChars` to split, and none rejoins frames
- [ ] Splitting still lands on sentence boundaries; cancellation is still checked before every engine call
- [ ] Chunking is tested per adapter: a large segment chunks to each engine's own cap, and the three caps are genuinely different in the assertions
- [ ] Adding an engine is one catalog entry; `tts.New`'s switch is gone
- [ ] A test fails loudly when a catalog engine has no settings slot
- [ ] `internal/textsplit` is a leaf; `fileproc.SplitForSynthesis` is gone
- [ ] `internal/audio/audiotest` replaces `internal/task`'s frame-builder copy
- [ ] Behaviour is unchanged, including #185's asymmetry, which moves without being fixed
- [ ] `make test` and `make go-lint` pass
