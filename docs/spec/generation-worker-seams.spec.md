# Generation Workers Get Narrow Seams — Refactor Specification

> The Reading guide job and the two Audiobook workers depend on nine concrete types between them, which is why 685 lines of claim, cancel and permanent-failure branching have no test file. This replaces those dependencies with narrow interfaces and function seams, so the branching is exercisable without Postgres, River, or a live TTS endpoint. It deliberately does **not** merge the jobs into a shared generation module.

- **Status:** Approved
- **Issue:** [#177](https://github.com/BlackForgeHQ/embookshelf/issues/177) — HITL; this document is the agreed shape
- **Scope:** `internal/task` (`audiobook.go`, `audiobook_finalize.go`, `reading_guide.go`), `internal/queue/registry.go`, `cmd/embookshelf/main.go`
- **Unblocks:** [#183](https://github.com/BlackForgeHQ/embookshelf/issues/183) (chunking into the engine adapter), [#184](https://github.com/BlackForgeHQ/embookshelf/issues/184) (service/queue cycle)
- **Companion artifacts:** `docs/adr/0024-reading-guides.md`, `docs/adr/0028-audiobook-generation-pipeline.md` §6

---

## 1. Why the ticket's premise was rejected

Issue #177 opens by asserting that the guide job and the audiobook run job are "the same pipeline, copy-pasted," and asks for one module that owns the shape while each job supplies only its extractor, its remote call, and its row to upsert.

Reading both, the sequences are not the same:

| Step | `ReadingGuide` | `AudiobookSegment` |
| --- | --- | --- |
| read settings row | yes | yes |
| disabled → permanent sentinel | yes | yes |
| build remote client | `cfg.Client()` → one `llm.Client` | `cfg.SelectEngine()`, plus a run-vs-current engine pin |
| re-read book row | yes | yes, inside `synthesizeSegment` |
| open bytes through the library handle | yes | yes |
| extract from the EPUB | `ExtractEPUBText`, whole book, capped | `ExtractEPUBSegments`, indexed by seq, with a plan-drift guard |
| call the remote | one call | N chunked calls, cancel-checked before each, frames rejoined |
| persist | one upsert | claim → stage MP3 to disk → `RecordSegment` → possibly dispatch finalize |
| publish SSE | once, at the end | on three separate branches, not at the end |

`AudiobookFinalize` shares none of the first eight steps at all — it is concatenation, ID3 tagging, placement and a `files` upsert.

The honest overlap is: settings-plus-sentinel, re-read the book, open, extract, publish behind a nil guard. Steps 3, 7 and 8 differ structurally rather than cosmetically. A module owning "the shape" with those parameterised also needs hooks for chunking, cancellation, claiming and staging — a template method with six holes serving two callers, one of which uses four of them. That trades duplication for indirection and reads worse than what is there now.

**What is actually broken is the second half of the ticket.** `AudiobookDeps` is nine concrete pointers (`*repo.BookAudiobookRepo`, `*repo.BookRepo`, `*repo.FileRepo`, `*coverstore.Store`, `*sse.Hub`, `service.LibraryStore`, `*service.AudiobookDispatch`). The only test file in the package that touches audiobooks exercises the *staging sweeper*, through `repotest.New(t)` and a live Postgres. The claim path, the cancel-mid-chunk path, the `tts.ErrPermanent` path and the finalize-deferred path have no coverage. One package over, `AudiobookService` declares a four-method interface described as "narrow so the lifecycle is exercisable without a database" and carries eleven tests. That contrast is the whole argument, and it is about dependency width, not about duplication.

So this specification keeps #177's deepening goal and drops its proposed mechanism.

---

## 2. Seams

Repos become consumer-side unexported interfaces in `internal/task`. Single-purpose collaborators become function fields, matching the idiom `internal/service` already uses for `SegmentDispatcher`, `FinalizeDispatcher` and `StagingSweeper`.

`AudiobookDeps` is deleted. Three structs replace it and `ReadingGuideDeps`.

### 2.1 `SegmentDeps` — `internal/task/audiobook.go`

```go
type SegmentDeps struct {
	Config   func(context.Context) (repo.AudiobookConfig, error)
	Engine   func(repo.AudiobookConfig) (repo.ConfiguredEngine, error)
	Runs     segmentStore
	Books    bookReader
	Open     func(context.Context, model.Book) (storage.Source, error)
	Finalize service.FinalizeDispatcher
	Publish  func(bookID string)
	DataPath string
}

type segmentStore interface {
	GetByBookID(ctx context.Context, bookID string) (model.Audiobook, error)
	MarkSegmentRunning(ctx context.Context, bookID string, seq int) (bool, error)
	RecordSegment(ctx context.Context, bookID string, seq int, res model.SegmentResult) (model.AudiobookOutcome, error)
}

type bookReader interface {
	GetByID(ctx context.Context, userID, id string) (model.Book, error)
}
```

`Config` and `Engine` are separate fields rather than one resolve step. The disabled check and the permanent sentinel stay visible in the worker body, where a reader looking for why River does not retry will find them; a test supplies a disabled config, or a fake engine, without either an HTTP server or a settings row.

### 2.2 `FinalizeDeps` — `internal/task/audiobook_finalize.go`

```go
type FinalizeDeps struct {
	Runs     finalizeStore
	Books    bookAudioWriter
	Files    narrationFiles
	Place    func(context.Context, model.Book, string) (service.PlaceResult, error)
	Cover    func(model.Book) (io.ReadCloser, error)
	Publish  func(bookID string)
	DataPath string
}

type finalizeStore interface {
	GetByBookID(ctx context.Context, bookID string) (model.Audiobook, error)
	ListSegments(ctx context.Context, bookID string) ([]model.AudiobookSegment, error)
	SetSegmentStart(ctx context.Context, bookID string, seq int, startMS int64) error
	SetState(ctx context.Context, bookID string, state model.AudiobookState, msg string) error
	SetReady(ctx context.Context, bookID, fileID string, durationMS int64) error
}

type bookAudioWriter interface {
	bookReader
	UpdateAudio(ctx context.Context, id string, durationSeconds *int, narrator string, chapters []model.Chapter) error
}

type narrationFiles interface {
	GetByLocation(ctx context.Context, libraryID, location string) (model.File, error)
	SetContentHash(ctx context.Context, fileID string, hash []byte, size int64, mtime time.Time) error
	Insert(ctx context.Context, f model.File) (model.File, error)
}
```

`Place` is a function rather than a `LibraryStore`, because finalize resolves a handle for exactly one purpose: `PlaceNarration`. Handing it the whole store would let a future edit reach `OpenBookSource` or `Placer`, and the comment at the current call site exists precisely to say the generic `Placer` is wrong here.

`Cover` stays nil-able. A narration without embedded art is still a good narration; that is existing, deliberate behaviour and the nil is the encoding of it.

### 2.3 `ReadingGuideDeps` — `internal/task/reading_guide.go`

Same name, narrowed contents.

```go
type ReadingGuideDeps struct {
	Config    func(context.Context) (repo.ReadingGuideConfig, error)
	Completer func(repo.ReadingGuideConfig) (service.GuideCompleter, error)
	Guides    guideStore
	Books     bookReader
	Open      func(context.Context, model.Book) (storage.Source, error)
	Publish   func(bookID string)
}

type guideStore interface {
	Upsert(ctx context.Context, g model.ReadingGuide) error
}
```

`service.guideCompleter` is currently unexported and satisfied by `*llm.Client`. It becomes `service.GuideCompleter` so the `Completer` seam has a type to name; `NewReadingGuideService` takes the exported name and its behaviour is unchanged.

### 2.4 The sweeper

`SweepAudiobookStaging` and `LoopAudiobookStagingSweep` stop taking a deps struct. They read `ListStaleStaging` and nothing else:

```go
type stagingLister interface {
	ListStaleStaging(ctx context.Context, olderThanDays int) ([]string, error)
}

func SweepAudiobookStaging(ctx context.Context, runs stagingLister, dataPath string) (int, error)
func LoopAudiobookStagingSweep(ctx context.Context, runs stagingLister, dataPath string)
```

### 2.5 Publishing

Each deps struct grows a nil-safe method:

```go
func (d SegmentDeps) publish(bookID string) {
	if d.Publish != nil {
		d.Publish(bookID)
	}
}
```

which removes `publishAudiobook`'s `deps.Hub != nil` and the guide worker's inline equivalent. The registry supplies a closure over `sse.Hub`; a test supplies a counter.

The `deps.Dispatch == nil || deps.Dispatch.Finalize == nil` guard at `internal/task/audiobook.go:206` — the one whose branch abandons a run's completion — **stays**. Removing it requires the enqueue seam to be a non-optional constructor argument, which requires the service/queue import cycle to be broken. That is #184's work and this refactor must not pre-empt it. #184's acceptance criterion "no remaining nil guard whose branch abandons work" refers to this line.

---

## 3. What does not change

No code moves between files. `synthesizeSegment`, `segmentText`, `joinParts` and `canceled` stay in `audiobook.go`; `assemble`, `chapterMarks`, `upsertNarrationFile` and `loadCover` stay in `audiobook_finalize.go`; the guide worker's forty-line body is untouched apart from reading its collaborators off the new fields.

No shared generation module is introduced. The one genuinely common piece, `service.LibraryBookOpener`, already exists and is already used by both jobs — it simply stops being constructed inside each worker and becomes the `Open` field the registry wires once.

No behaviour changes. Every branch, every error wrap, every ordering decision and every explanatory comment survives; only the types through which the branches reach their collaborators differ. This is what makes the new tests meaningful: they characterise what ships today.

---

## 4. Tests

All new tests live in `package task_test` with in-memory fakes. No Postgres, no River, no HTTP, no TTS endpoint.

Fake engines return canned MP3 bytes, because `audio.Payload` rejects anything else and the staging and assembly paths both run through it. `internal/audio` has `mp3Frames(n int)` but it is unexported test support, so `internal/task`'s tests get their own minimal frame builder in a `_test.go` file. Duplicated on purpose: exporting a frame builder from `internal/audio` would put a test fixture in the production surface of a package whose job is to parse real files.

### 4.1 `audiobook_segment_test.go`

| Case | Asserted outcome |
| --- | --- |
| Feature disabled | returns `ErrAudiobooksDisabled`; the engine is never built |
| Run already cancelled | returns nil; no claim, no engine call |
| Run already ready | returns nil; no claim, no engine call |
| `MarkSegmentRunning` reports not-claimed | returns nil; no engine call — the same audio is never bought twice |
| Cancel observed between chunks | returns nil; **no** segment result recorded, because the run is already in its final state |
| Engine returns `tts.ErrPermanent` | segment recorded failed with the reason; publish fires once; returns nil so River stops |
| Engine returns a transient error | segment recorded failed; the error is returned so River retries |
| Book row missing | the error is surfaced wrapped; nothing is staged |
| Book format is not EPUB | fails through `service.ErrNotNarratable`, treated as permanent |
| Run engine differs from the currently selected one | permanent failure; no engine call |
| Engine returns unusable audio | recorded failed with "engine returned unusable audio"; publish once; returns nil |
| Last outstanding segment completes | `AudiobookNextFinalize` → finalize dispatched **exactly once** |

### 4.2 `audiobook_finalize_test.go`

| Case | Asserted outcome |
| --- | --- |
| Run cancelled between the last segment and this job | staging swept; nothing published; no `files` row |
| Run already ready | no-op |
| A segment is not yet done | returns nil, publishes nothing — the last segment will dispatch this again |
| No segments at all | run marked failed; returns nil |
| Every segment done | assembles, places, upserts the `files` row, `SetReady`, sweeps staging, publishes **once** |
| Assemble fails | `SetState(failed)` with the cause; **staging retained** (ADR-0028 §6); returns nil |
| A `files` row already exists at the location | the row is updated, not duplicated |

### 4.3 `reading_guide_test.go`

| Case | Asserted outcome |
| --- | --- |
| Feature disabled | returns `ErrReadingGuidesDisabled`; the completer is never built |
| Book row missing | the error is surfaced wrapped; nothing is upserted |
| Success | the guide is upserted and publish fires once |

### 4.4 Existing tests

`TestSweepAudiobookStagingReclaimsAStrandedRun` and `TestSweepAudiobookStagingRetainsAFreshlyFailedRun` keep `repotest`. They assert what a SQL predicate selects, which is exactly the thing a fake would stop testing. Only their call signature changes.

---

## 5. Blast radius

- `internal/queue/registry.go` — builds `SegmentDeps`, `FinalizeDeps` and the narrowed `ReadingGuideDeps` from the same concrete repos it already holds. `deps.AudiobookDispatch` continues to reach the segment worker unchanged.
- `cmd/embookshelf/main.go` — the `LoopAudiobookStagingSweep` call site loses its struct.
- `internal/task/audiobook_test.go` — two `SweepAudiobookStaging` calls.
- `internal/service/reading_guide.go` — `guideCompleter` is exported as `GuideCompleter`.

No handler change, no UI change, no migration, no API change.

---

## 6. Acceptance criteria

These replace the criteria on #177, which were written against the rejected premise. The issue is updated to carry them, with a comment recording why the shared module was dropped.

- [ ] `AudiobookDeps` is gone; each worker declares only what it calls, as narrow interfaces and function seams
- [ ] Both audiobook workers and the guide worker are exercisable with no Postgres, no River and no live remote
- [ ] The segment worker's claim, cancel, permanent-failure and transient-failure branches are each covered by a test
- [ ] ADR-0028 §6 is pinned by tests: a failed run retains its staging and its completed segments; a cancelled run's staging is swept
- [ ] A terminal publish fires exactly once on each path that reaches one
- [ ] Behaviour is unchanged — the new tests characterise what ships today
- [ ] The nil guard at `audiobook.go:206` is left in place and annotated as #184's
- [ ] `make test` and `make go-lint` pass
