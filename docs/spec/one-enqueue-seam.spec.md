# One Enqueue Seam — Refactor Specification

> Four modules each re-derive the same workaround for a dependency the import graph will not allow, and the workaround has a data race and a live failure window in it. This replaces all four with one enqueue seam in a leaf package, so every service takes an ordinary constructor argument. One late-bound indirection survives, named once and documented as irreducible.

- **Status:** Approved
- **Issue:** [#184](https://github.com/BlackForgeHQ/embookshelf/issues/184)
- **Scope:** new `internal/jobs`; `internal/queue`, `internal/service`, `internal/task`, `cmd/embookshelf/main.go`
- **Depends on:** [#177](https://github.com/BlackForgeHQ/embookshelf/issues/177) (`refactor/generation-worker-seams`) — this branch stacks on it
- **Companion artifacts:** `docs/adr/0028-audiobook-generation-pipeline.md` §3, §6

---

## 1. What is actually there

`internal/queue` imports `internal/service` (for `queue.Deps`' concrete service types and for `registry.go`'s worker wiring), and `internal/task` imports `internal/service` too. `internal/service` imports neither. **There is no import cycle today** — it is avoided by the service tier declaring `func` types instead of naming the queue:

| Seam | Declared | Wired |
| --- | --- | --- |
| `IngestDispatcher` | `service/bookdrop_intake.go:44` | `WithIngestDispatcher`, `main.go:435` |
| `EnrichDispatcher` | `service/bookdrop.go:76` | `WithAutoEnrich`, `main.go:442` |
| `GuideDispatcher` | `service/reading_guide_run.go:23` | constructor arg, `main.go:481` |
| `SegmentDispatcher` / `FinalizeDispatcher` | `service/audiobook.go:47,50` | via `AudiobookDispatch`, `main.go:449-459` |

Four files carry a variant of the same comment — "A function rather than a `queue.Client` because `internal/queue` imports this package". The issue says three; it is four.

Only one of these is a *mutable holder*: `AudiobookDispatch` (`service/audiobook.go:61`), a pointer-to-struct of function fields passed into `queue.New` empty and filled by the composition root afterwards. The rest are plain function values. The issue's "three mutable dispatch holders" over-counts the holders and under-counts the duplication.

## 2. Why this is a bug fix, not a tidy-up

`river.Client.Start(ctx)` is called **inside** `queue.New` (`internal/queue/queue.go:157`), before it returns. `main.go` assigns `audiobookDispatch.Segment` and `.Finalize` at lines 449-455, *after* that call. Two consequences, both real at every process start:

**A data race.** The main goroutine writes `audiobookDispatch.Finalize`; River's worker goroutines read it through the closure the registry captured. There is no synchronisation on either side. Nothing in the suite currently exercises it under `-race`, which is why it has not been caught.

**A live abandonment window.** River begins draining leftover jobs the moment it starts. An `audiobook.segment` job requeued from a previous process that completes the run inside that window reaches `recordSegment`'s `AudiobookNextFinalize` branch, finds no dispatcher, logs a warning and returns. The run is left at `running` with every segment `done` and no finalize job — precisely the stranded-run failure #157 fixed elsewhere and #184 names here. `AudiobookService.Status` re-derives the transition on the next read, so it recovers, but only once somebody looks.

## 3. What is achievable, and what is not

The issue asks to "delete the dispatch holders". Moving the enqueue seam to a leaf package fixes the *import* direction, and that alone is enough for every service to take an ordinary constructor argument. It does **not** dissolve the *construction* cycle:

- `queue.New` builds the worker registry from `deps.BookDropSvc`, `deps.Enrich`, `deps.LibSvc` — the workers call back into the service tier.
- Those services would take the enqueuer that `queue.New` returns.

Neither can be built first. This is a cycle in the object graph, not in the imports, and no interface placement removes it. Something must be late-bound.

Reaching literally zero late binding requires the workers to stop calling services at all — inverting `BookDropSvc`, `Enrich` and `LibSvc` out of the worker deps down to repos, across bookdrop, enrichment and library scan. That is a much larger change and is explicitly **out of scope** here.

So the deliverable is: **four re-derivations collapse into one**, and the one that survives is correct where the current one is not.

## 4. Design

### 4.1 `internal/jobs` — a leaf both tiers can depend on

New package. Imports nothing from this repo, and nothing beyond `context`, `errors` and `sync`.

```go
// Args is one job's payload: a JSON-serializable struct that names its
// own kind.
type Args interface{ Kind() string }

// Queued is the optional half of Args: a job that names a queue runs
// there instead of the default one.
type Queued interface{ Queue() string }

// Enqueuer hands a job to the worker pool.
type Enqueuer interface {
	Enqueue(ctx context.Context, args Args) error
}
```

Every `*Args` payload type moves here from `internal/task`, unchanged: `BookDropIngestArgs`, `BookDropAutoEnrichArgs`, `LibraryScanArgs`, `SendToKindleArgs`, `ReadingGuideArgs`, `AudiobookSegmentArgs`, `AudiobookFinalizeArgs`, plus the `AudiobookQueue` constant.

They are pure payload — a struct, a `Kind()`, sometimes a `Queue()`. The Go type names keep their `Args` suffix so a call site reads `jobs.LibraryScanArgs{…}`.

**Hard constraint: every `Kind()` string and every `json` tag is byte-identical after the move.** River stores the kind and the encoded args, not the Go type name, so renaming the type is safe — changing a kind or a tag orphans every in-flight job.

`internal/queue`'s own `JobArgs` and `Queued` interfaces are deleted; `queue.Client.Enqueue` takes `jobs.Args`, which makes `*RiverClient` a `jobs.Enqueuer` with no adapter.

### 4.2 `jobs.Deferred` — the one surviving knot

```go
// ErrNoQueue is returned by a Deferred that has not been resolved.
var ErrNoQueue = errors.New("no queue configured")

// Deferred is an Enqueuer whose backing client arrives after the
// services holding it are constructed.
//
// This is the one irreducible knot in the composition root: the queue's
// worker registry is assembled inside queue.New out of the very
// services that need to enqueue, so neither can be built first. Rather
// than four modules each re-deriving a workaround, the knot is named
// once, here, and every service takes an ordinary Enqueuer.
//
// The mutex is not decoration. queue.New starts River before it
// returns, so worker goroutines can read this while the composition
// root is still writing it.
type Deferred struct {
	mu    sync.RWMutex
	inner Enqueuer
}

func (d *Deferred) Resolve(e Enqueuer)
func (d *Deferred) Enqueue(ctx context.Context, args Args) error
```

`Enqueue` before `Resolve` returns `ErrNoQueue`. It does not panic and does not silently drop: a job that cannot be queued is a real failure the caller must surface. This is the behavioural change that closes §2's abandonment window — the segment worker's `AudiobookNextFinalize` branch now gets an error it can log with a reason, and `AudiobookService.Start` fails the run rather than leaving it invisible at 0%.

### 4.3 The service tier takes an ordinary argument

Deleted outright: `SegmentDispatcher`, `FinalizeDispatcher`, `AudiobookDispatch`, `IngestDispatcher`, `EnrichDispatcher`, `GuideDispatcher`, `WithIngestDispatcher`, `WithFinalizeDispatcher`. With them go all four "because internal/queue imports this package" comments.

| Before | After |
| --- | --- |
| `NewBookDropService(…)` + `WithIngestDispatcher(d)` | `NewBookDropService(…, enq jobs.Enqueuer)` |
| `WithAutoEnrich(policy, dispatcher)` | `WithAutoEnrichPolicy(policy)` — the enqueuer already arrived via the constructor |
| `NewGuideRunner(c, GuideDispatcher, cap)` | `NewGuideRunner(c, enq jobs.Enqueuer, cap)` |
| `NewAudiobookService(store, books, SegmentDispatcher)` + `WithFinalizeDispatcher(f)` | `NewAudiobookService(store, books, enq jobs.Enqueuer)` |
| `task.SegmentDeps.Finalize service.FinalizeDispatcher` | `task.SegmentDeps.Enqueue jobs.Enqueuer` |

Each service builds its own payload at the point of use — `s.enq.Enqueue(ctx, jobs.AudiobookSegmentArgs{BookID: bookID, Seq: seq})` — instead of calling a closure that does it on their behalf. The seam stops being a cycle-breaker and becomes what a substitution point should be: a test passes a recording `jobs.Enqueuer` and asserts on the payloads, not on positional arguments to an anonymous function.

### 4.4 The nil guards go

Every `if s.dispatch == nil` / `if deps.Finalize == nil` branch is deleted. A `jobs.Enqueuer` is a required constructor argument, so it is never nil; an unresolved `Deferred` is a non-nil value that returns `ErrNoQueue`. Specifically:

- `service/audiobook.go:190-193` (`dispatchFinalize`) and `:268-270` (`dispatchAll`) — the two that returned "no queue configured for audiobook generation" — collapse into the error `Deferred` already returns.
- `task/audiobook.go`'s `deps.Finalize == nil` branch, annotated in #177 as this issue's to remove, is removed. Its failure now surfaces through the existing `slog.Warn("audiobook: dispatch finalize", …)` with `ErrNoQueue` as the cause.
- `service/bookdrop.go`'s `enrichDispatch == nil` reading as "no worker pool" is deliberately **kept in spirit**: Auto-enrich is genuinely optional and a book must still import without it. It becomes a check on the policy, not on the dispatcher.

## 5. Testing

The point of the refactor is that these become testable without a queue.

**`internal/jobs`** — `Deferred` is the only thing with behaviour:
- unresolved → `Enqueue` returns `ErrNoQueue`, and nothing is dropped silently
- resolved → the payload reaches the inner enqueuer unchanged
- `Resolve` during concurrent `Enqueue` is race-free, asserted under `go test -race` with a goroutine on each side. **This test is the regression pin for §2's data race** and must fail against a mutex-free implementation.
- every payload type round-trips through `encoding/json` with its tags intact, and `Kind()` returns the exact historical string — a table test, one row per job type, so a rename that would orphan in-flight jobs fails loudly

**`internal/service`** — existing tests that substitute a recording dispatcher are rewritten to substitute a recording `jobs.Enqueuer` and assert on payloads. The audiobook suite's eleven tests must keep passing.

**`internal/task`** — `SegmentDeps.Enqueue` replaces `Finalize`; the existing "finalize dispatched exactly once" test asserts on the enqueued `jobs.AudiobookFinalizeArgs` instead of a call count.

**New behavioural test:** a run whose enqueuer is an unresolved `Deferred` fails visibly rather than sitting at 0% forever — `AudiobookService.Start` marks the run failed with the queue error, which is what `dispatchAll` already promises and could not previously be tested.

## 6. Acceptance criteria

These replace the criteria on #184, which were written before the construction cycle and the race were identified.

- [ ] `internal/jobs` exists as a leaf package: no repo imports, holding `Args`, `Queued`, `Enqueuer`, `Deferred` and every job payload type
- [ ] Every `Kind()` string and `json` tag is unchanged — pinned by a table test
- [ ] `internal/service` imports `internal/jobs` directly; the four dispatcher func types and the `AudiobookDispatch` holder are deleted, along with all four "because internal/queue imports this package" comments
- [ ] Exactly one late-bound indirection remains, in `internal/jobs`, documented as irreducible with the reason
- [ ] The write/read race on the dispatch holder is gone, pinned by a `-race` test that fails without the mutex
- [ ] No remaining nil guard whose branch abandons work; an unresolved queue returns `ErrNoQueue` and the caller surfaces it
- [ ] A run that cannot be queued is marked failed with the reason, rather than left invisible
- [ ] Existing service tests that substituted a recording dispatcher pass against a recording `jobs.Enqueuer`
- [ ] `make test`, `go test -race ./internal/jobs/ ./internal/service/ ./internal/task/`, and `make go-lint` pass
