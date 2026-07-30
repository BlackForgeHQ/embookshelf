# One Enqueue Seam Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace four re-derivations of the same enqueue workaround with one seam in a leaf package, so every service takes an ordinary constructor argument — and close the data race and abandonment window the current dispatch holder carries.

**Architecture:** A new leaf package `internal/jobs` owns the job payload types, an `Args`/`Queued`/`Enqueuer` vocabulary, and `Deferred` — a mutex-guarded `Enqueuer` resolved after `queue.New` returns. `internal/service` imports it directly and drops all four dispatcher function types plus the `AudiobookDispatch` holder. `internal/queue` implements `jobs.Enqueuer`. Exactly one late-bound indirection survives, because `queue.New` builds its worker registry out of the very services that need to enqueue.

**Tech Stack:** Go, standard library only. No new dependencies.

**Spec:** `docs/spec/one-enqueue-seam.spec.md`
**Depends on:** #177 — this branch (`refactor/one-enqueue-seam`) stacks on `refactor/generation-worker-seams`.

## Global Constraints

- **Every `Kind()` string and every `json` tag on a moved payload type is byte-identical after the move.** River stores the kind and the encoded args, so renaming a Go type is safe but changing a kind or a tag orphans every in-flight job. This is the single highest-risk constraint in the plan.
- **Postgres only (ADR-0023).** No dialect branches, no SQLite migrations.
- **No behaviour changes except the three the spec names:** an unresolved queue returns `jobs.ErrNoQueue` instead of a silent nil-guard return; the dispatch holder's race is synchronised; a run that cannot be queued is marked failed with the reason. Everything else — every branch, error wrap, ordering decision and explanatory comment — survives verbatim.
- **Error swallowing is deliberate and must be preserved.** `BookDropService.requestAutoEnrich` and `Intake` both log-and-continue on a dispatch failure, because the row is already committed and losing the job only delays processing. Removing a nil guard must not turn either into a hard failure.
- **Every file starts with** `// SPDX-License-Identifier: AGPL-3.0-or-later` followed by a blank line.
- **`internal/jobs` imports nothing from this repo** — only `context`, `errors`, `sync`. A repo import in that package is a design failure, not a detail.
- **Comments explain why, not what.** This codebase comments densely in that register; match it, and carry existing comments across when moving code.
- **Gates:** `go build ./...`, `make test`, `go test -race ./internal/jobs/ ./internal/service/ ./internal/task/`, and `make go-lint` pass at every commit.
- Commit messages carry **no** `Co-Authored-By` or `Claude-Session` trailers.
- Postgres is running; export `TEST_DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable'` before running Go tests.

---

## File Structure

**Created:**
- `internal/jobs/jobs.go` — `Args`, `Queued`, `Enqueuer`, and every job payload type
- `internal/jobs/deferred.go` — `Deferred`, `ErrNoQueue`
- `internal/jobs/jobs_test.go` — the kind/tag table test
- `internal/jobs/deferred_test.go` — including the `-race` regression pin

**Modified:**
- `internal/queue/queue.go` — `JobArgs`/`Queued` deleted, `Enqueue` takes `jobs.Args`, `Deps` gains `Enqueue`, loses `AudiobookDispatch`
- `internal/queue/registry.go` — payload types come from `jobs`
- `internal/task/{audiobook,bookdrop,library_scan,send_to_kindle,reading_guide}.go` — payload types leave; `SegmentDeps.Finalize` becomes `Enqueue jobs.Enqueuer`
- `internal/service/{audiobook,bookdrop,bookdrop_intake,reading_guide_run}.go` — dispatcher types deleted, `jobs.Enqueuer` taken as a constructor argument
- `internal/service/{audiobook,bookdrop_intake,bookdrop_approve}_test.go`, `internal/task/audiobook_segment_test.go` — recording dispatchers become recording enqueuers
- `cmd/embookshelf/main.go` — one `jobs.Deferred`, resolved once

---

## Task 1: The leaf package

Nothing imports it yet, so it lands on its own with its own tests. This is where the race regression pin lives.

**Files:**
- Create: `internal/jobs/jobs.go`, `internal/jobs/deferred.go`, `internal/jobs/deferred_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `jobs.Args` (`Kind() string`), `jobs.Queued` (`Queue() string`), `jobs.Enqueuer` (`Enqueue(context.Context, Args) error`), `jobs.ErrNoQueue`, `jobs.Deferred` with `Resolve(Enqueuer)` and `Enqueue(context.Context, Args) error`.

- [ ] **Step 1: Write the vocabulary**

Create `internal/jobs/jobs.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package jobs is the vocabulary both the service tier and the queue
// tier speak: what a job payload is, and what it means to enqueue one.
//
// It exists to be a leaf. internal/queue imports internal/service, so
// the service tier cannot name a queue client — which is why four
// separate modules each grew their own function-typed dispatcher and
// the same comment explaining it. Declaring the seam somewhere both
// tiers can depend on turns those into one ordinary argument.
package jobs

import "context"

// Args is one job's payload: a JSON-serializable struct that names its
// own kind.
//
// The kind is a stored value, not a derived one — River persists it
// alongside the encoded args — so renaming a Go type here is safe and
// changing a Kind() string orphans every in-flight job of that type.
type Args interface {
	Kind() string
}

// Queued is the optional half of Args: a job that names a queue runs
// there instead of the default one. Declared as an interface rather
// than a field so the name travels with the payload exactly as Kind
// does.
type Queued interface {
	Queue() string
}

// Enqueuer hands a job to the worker pool.
//
// One method for every job type: the kind travels with the payload, so
// adding a job does not widen this interface.
type Enqueuer interface {
	Enqueue(ctx context.Context, args Args) error
}
```

- [ ] **Step 2: Write the failing test for `Deferred`**

Create `internal/jobs/deferred_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package jobs_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/BlackForgeHQ/embookshelf/internal/jobs"
)

// probeArgs is a payload for exercising the seam itself.
type probeArgs struct {
	ID string `json:"id"`
}

func (probeArgs) Kind() string { return "test.probe" }

// recorder is an Enqueuer that keeps what it was handed.
type recorder struct {
	mu   sync.Mutex
	got  []jobs.Args
	err  error
}

func (r *recorder) Enqueue(_ context.Context, args jobs.Args) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.got = append(r.got, args)
	return nil
}

func (r *recorder) seen() []jobs.Args {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]jobs.Args(nil), r.got...)
}

// An unresolved Deferred refuses rather than dropping. A job that
// cannot be queued is a real failure the caller has to see — the old
// nil-holder returned silently, which is how a finished audiobook run
// lost its finalize job and sat at running forever.
func TestDeferredRefusesBeforeItIsResolved(t *testing.T) {
	var d jobs.Deferred

	err := d.Enqueue(context.Background(), probeArgs{ID: "a"})

	if !errors.Is(err, jobs.ErrNoQueue) {
		t.Fatalf("err = %v, want ErrNoQueue", err)
	}
}

func TestDeferredPassesThePayloadThroughOnceResolved(t *testing.T) {
	var d jobs.Deferred
	rec := &recorder{}
	d.Resolve(rec)

	if err := d.Enqueue(context.Background(), probeArgs{ID: "a"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	got := rec.seen()
	if len(got) != 1 {
		t.Fatalf("enqueued %d jobs, want 1", len(got))
	}
	p, ok := got[0].(probeArgs)
	if !ok {
		t.Fatalf("got %T, want the payload unchanged", got[0])
	}
	if p.ID != "a" {
		t.Errorf("id = %q, want a", p.ID)
	}
}

func TestDeferredSurfacesTheInnerError(t *testing.T) {
	var d jobs.Deferred
	sentinel := errors.New("queue down")
	d.Resolve(&recorder{err: sentinel})

	if err := d.Enqueue(context.Background(), probeArgs{}); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the inner error surfaced", err)
	}
}

// The regression pin for the race this refactor exists to close.
//
// queue.New starts River before it returns, so worker goroutines are
// already draining jobs while the composition root is still wiring the
// enqueuer. The old holder was a plain struct field written on one
// goroutine and read on others with no synchronization at all.
//
// This test must fail under -race against a mutex-free Deferred.
func TestDeferredResolvesSafelyWhileEnqueuesAreInFlight(t *testing.T) {
	var d jobs.Deferred
	rec := &recorder{}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for range 100 {
			// Either outcome is legitimate — before Resolve it refuses,
			// after it succeeds. What must not happen is a data race.
			_ = d.Enqueue(context.Background(), probeArgs{ID: "a"})
		}
	}()
	go func() {
		defer wg.Done()
		d.Resolve(rec)
	}()

	wg.Wait()
}
```

- [ ] **Step 3: Run it to verify it fails**

```bash
go test ./internal/jobs/
```

Expected: FAIL to compile — `undefined: jobs.Deferred`, `undefined: jobs.ErrNoQueue`.

- [ ] **Step 4: Write `Deferred`**

Create `internal/jobs/deferred.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package jobs

import (
	"context"
	"errors"
	"sync"
)

// ErrNoQueue is what a Deferred returns before it has been resolved.
var ErrNoQueue = errors.New("no queue configured")

// Deferred is an Enqueuer whose backing client arrives after the
// services holding it are constructed.
//
// This is the one irreducible knot in the composition root, and naming
// it once is the whole point. The queue's worker registry is assembled
// inside queue.New out of the very services that need to enqueue, so
// neither can be built first. Four modules used to each re-derive a
// workaround for that; now they take an ordinary Enqueuer and this
// holds the knot alone.
//
// The mutex is not decoration. queue.New calls river.Client.Start
// before it returns, so worker goroutines are already draining jobs
// while the composition root is still calling Resolve.
type Deferred struct {
	mu    sync.RWMutex
	inner Enqueuer
}

// Resolve supplies the real enqueuer. Called once, by the composition
// root, as soon as the queue exists.
func (d *Deferred) Resolve(e Enqueuer) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.inner = e
}

// Enqueue hands the job on, or refuses if the queue is not up yet.
//
// Refusing rather than dropping is deliberate: the caller decides what
// an unqueueable job means. A bookdrop intake logs it and lets the
// watcher retry; an audiobook run fails outright, because a run with no
// jobs shows 0% forever with no error to explain it.
func (d *Deferred) Enqueue(ctx context.Context, args Args) error {
	d.mu.RLock()
	inner := d.inner
	d.mu.RUnlock()
	if inner == nil {
		return ErrNoQueue
	}
	return inner.Enqueue(ctx, args)
}
```

- [ ] **Step 5: Run the tests, including under `-race`**

```bash
go test ./internal/jobs/ -v && go test -race ./internal/jobs/
```

Expected: all four PASS, and clean under `-race`.

- [ ] **Step 6: Prove the race pin actually pins**

Temporarily delete the mutex from `Deferred` (make `inner` a bare field, drop the lock/unlock calls), then:

```bash
go test -race ./internal/jobs/ -run TestDeferredResolvesSafelyWhileEnqueuesAreInFlight
```

Expected: FAIL with `DATA RACE`. Restore the mutex and confirm it passes again. Record both outputs in your report — a regression pin that does not fail against the bug is not a pin.

- [ ] **Step 7: Lint and commit**

```bash
make go-lint
git add internal/jobs/
git commit -m "feat(jobs): one enqueue seam, in a package both tiers can import

internal/queue imports internal/service, so the service tier cannot
name a queue client. Four modules each grew a function-typed dispatcher
and the same comment explaining why. This is the seam they were
approximating.

Deferred holds the one knot that cannot be removed: queue.New builds
its worker registry out of the services that need to enqueue. It is
mutex-guarded because queue.New starts River before it returns, so
workers read the enqueuer while the composition root is still writing
it -- an unsynchronized write in the holder this replaces."
```

---

## Task 2: The payloads move

Mechanical but wide, and the highest-risk step in the plan: a changed kind string or json tag orphans in-flight jobs.

**Files:**
- Modify: `internal/jobs/jobs.go` (append payloads), `internal/task/{audiobook,bookdrop,library_scan,send_to_kindle,reading_guide}.go`, `internal/queue/{queue,registry}.go`, `cmd/embookshelf/main.go`
- Create: `internal/jobs/jobs_test.go`

**Interfaces:**
- Consumes: `jobs.Args`, `jobs.Queued` from Task 1.
- Produces: `jobs.BookDropIngestArgs{ItemID}`, `jobs.BookDropAutoEnrichArgs{BookID}`, `jobs.LibraryScanArgs{LibraryID}`, `jobs.SendToKindleArgs{BookID,UserID}`, `jobs.ReadingGuideArgs{BookID}`, `jobs.AudiobookSegmentArgs{BookID,Seq}`, `jobs.AudiobookFinalizeArgs{BookID}`, `jobs.AudiobookQueue`. `queue.Client.Enqueue` takes `jobs.Args`; `queue.JobArgs` and `queue.Queued` are deleted.

- [ ] **Step 1: Write the table test that pins the wire format**

Create `internal/jobs/jobs_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package jobs_test

import (
	"encoding/json"
	"testing"

	"github.com/BlackForgeHQ/embookshelf/internal/jobs"
)

// Kind strings and json tags are stored values: River persists the kind
// alongside the encoded args, and a running deployment has rows in
// river_job carrying both. Renaming a Go type is free; changing either
// of these orphans every in-flight job of that type, silently, with no
// error anywhere.
//
// So they are pinned here as literals rather than derived from the
// types — a test that asks the type what its kind is would agree with
// any rename.
func TestJobKindsAndPayloadsAreTheStoredOnes(t *testing.T) {
	cases := []struct {
		args jobs.Args
		kind string
		json string
	}{
		{jobs.BookDropIngestArgs{ItemID: "i1"}, "bookdrop.ingest", `{"item_id":"i1"}`},
		{jobs.BookDropAutoEnrichArgs{BookID: "b1"}, "bookdrop.auto_enrich", `{"book_id":"b1"}`},
		{jobs.LibraryScanArgs{LibraryID: "l1"}, "library.scan", `{"library_id":"l1"}`},
		{jobs.SendToKindleArgs{BookID: "b1", UserID: "u1"}, "kindle.send", `{"book_id":"b1","user_id":"u1"}`},
		{jobs.ReadingGuideArgs{BookID: "b1"}, "guide.generate", `{"book_id":"b1"}`},
		{jobs.AudiobookSegmentArgs{BookID: "b1", Seq: 7}, "audiobook.segment", `{"book_id":"b1","seq":7}`},
		{jobs.AudiobookFinalizeArgs{BookID: "b1"}, "audiobook.finalize", `{"book_id":"b1"}`},
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			if got := tc.args.Kind(); got != tc.kind {
				t.Errorf("kind = %q, want %q — this orphans in-flight jobs", got, tc.kind)
			}
			b, err := json.Marshal(tc.args)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(b) != tc.json {
				t.Errorf("payload = %s, want %s — this orphans in-flight jobs", b, tc.json)
			}
		})
	}
}

// Only the audiobook jobs declare a queue. A run is tens of long jobs
// per book, and sharing the default pool would stall BookDrop ingest
// and Library scan for as long as the run lasts (ADR-0028 §3).
func TestOnlyAudiobookJobsDeclareTheirOwnQueue(t *testing.T) {
	queued := map[string]string{
		"audiobook.segment":  jobs.AudiobookQueue,
		"audiobook.finalize": jobs.AudiobookQueue,
	}
	all := []jobs.Args{
		jobs.BookDropIngestArgs{}, jobs.BookDropAutoEnrichArgs{}, jobs.LibraryScanArgs{},
		jobs.SendToKindleArgs{}, jobs.ReadingGuideArgs{},
		jobs.AudiobookSegmentArgs{}, jobs.AudiobookFinalizeArgs{},
	}
	for _, a := range all {
		q, declares := a.(jobs.Queued)
		want, shouldDeclare := queued[a.Kind()]
		if declares != shouldDeclare {
			t.Errorf("%s declares a queue = %v, want %v", a.Kind(), declares, shouldDeclare)
			continue
		}
		if declares && q.Queue() != want {
			t.Errorf("%s queue = %q, want %q", a.Kind(), q.Queue(), want)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/jobs/ -run TestJobKinds
```

Expected: FAIL to compile — the payload types do not exist in `jobs` yet.

- [ ] **Step 3: Move the payloads**

Append to `internal/jobs/jobs.go`. Copy each type, its `Kind()`, its `Queue()` where present, and **its existing doc comments verbatim** from `internal/task`:

```go
// AudiobookQueue is the River queue narration runs on.
//
// Its own queue rather than the default one because a run is tens of
// long jobs per book: sharing the four default workers would stall
// BookDrop ingest and Library scan for as long as the run lasts
// (ADR-0028 §3).
const AudiobookQueue = "audiobook"

// BookDropIngestArgs addresses one staged upload.
type BookDropIngestArgs struct {
	ItemID string `json:"item_id"`
}

// Kind is the job name. Must be stable — changing it orphans in-flight
// jobs.
func (BookDropIngestArgs) Kind() string { return "bookdrop.ingest" }

// BookDropAutoEnrichArgs addresses the gap-fill for a freshly approved
// book.
type BookDropAutoEnrichArgs struct {
	BookID string `json:"book_id"`
}

// Kind is the stable job name. Must not change — renaming it orphans
// in-flight jobs.
func (BookDropAutoEnrichArgs) Kind() string { return "bookdrop.auto_enrich" }

// LibraryScanArgs addresses one library's rescan.
type LibraryScanArgs struct {
	LibraryID string `json:"library_id"`
}

func (LibraryScanArgs) Kind() string { return "library.scan" }

// SendToKindleArgs addresses one delivery.
type SendToKindleArgs struct {
	BookID string `json:"book_id"`
	UserID string `json:"user_id"`
}

// Kind is the stable job name.
func (SendToKindleArgs) Kind() string { return "kindle.send" }

// ReadingGuideArgs is the payload for generating one book's guide.
// BookID only — the worker re-reads the row, so a metadata edit between
// enqueue and dispatch is reflected rather than baked into the payload.
type ReadingGuideArgs struct {
	BookID string `json:"book_id"`
}

// Kind is the stable job name.
func (ReadingGuideArgs) Kind() string { return "guide.generate" }

// AudiobookSegmentArgs addresses one unit of synthesis.
//
// Book and seq rather than the segment's own id, because that pair is
// what the plan is keyed on and what a Retry re-enqueues; carrying a row
// id would mean a retry could address a row a regeneration has replaced.
type AudiobookSegmentArgs struct {
	BookID string `json:"book_id"`
	Seq    int    `json:"seq"`
}

func (AudiobookSegmentArgs) Kind() string  { return "audiobook.segment" }
func (AudiobookSegmentArgs) Queue() string { return AudiobookQueue }

// AudiobookFinalizeArgs addresses the concatenation of a finished run.
type AudiobookFinalizeArgs struct {
	BookID string `json:"book_id"`
}

func (AudiobookFinalizeArgs) Kind() string  { return "audiobook.finalize" }
func (AudiobookFinalizeArgs) Queue() string { return AudiobookQueue }
```

Then delete the seven payload types, their `Kind()`/`Queue()` methods and the `AudiobookQueue` const from `internal/task`, leaving each file's `*Deps` struct and worker function in place.

- [ ] **Step 4: Retype the worker signatures and the queue**

In `internal/task`, every worker's second parameter changes from `task.XArgs` to `jobs.XArgs`. Add the `jobs` import to each of the five files; drop it from any that no longer need another import as a result.

In `internal/queue/queue.go`: delete the `JobArgs` and `Queued` interface declarations and the `queueOf` helper's dependence on them — `queueOf` now takes `jobs.Args` and asserts `jobs.Queued`. `Client.Enqueue` and `RiverClient.Enqueue` take `jobs.Args`. Add a `Enqueue jobs.Enqueuer` field to `Deps` with this comment:

```go
	// Enqueue is the seam workers use to dispatch follow-on jobs. It is
	// the same *jobs.Deferred the service tier holds: the registry below
	// is built out of services that need it, so it cannot be the client
	// this call is constructing.
	Enqueue jobs.Enqueuer
```

Leave `Deps.AudiobookDispatch` in place for now — Task 3 removes it.

In `internal/queue/registry.go`: `register[T jobs.Args]` and every `task.XArgs` reference becomes `jobs.XArgs`.

In `cmd/embookshelf/main.go`: every `task.XArgs{…}` literal becomes `jobs.XArgs{…}`.

- [ ] **Step 5: Verify no kind or tag moved**

```bash
git show HEAD:internal/task/audiobook.go | grep -oE '"(audiobook|bookdrop|library|kindle|guide)\.[a-z_]+"' | sort > /tmp/kinds-before.txt
git show HEAD:internal/task/bookdrop.go internal/task/library_scan.go internal/task/send_to_kindle.go internal/task/reading_guide.go 2>/dev/null | grep -oE '"(audiobook|bookdrop|library|kindle|guide)\.[a-z_]+"' | sort >> /tmp/kinds-before.txt
grep -oE '"(audiobook|bookdrop|library|kindle|guide)\.[a-z_]+"' internal/jobs/jobs.go | sort > /tmp/kinds-after.txt
diff <(sort -u /tmp/kinds-before.txt) <(sort -u /tmp/kinds-after.txt) && echo "kinds identical"
```

Expected: `kinds identical`. If it differs, the table test in Step 1 will also fail — trust the test over this check, and fix the payload rather than the test.

- [ ] **Step 6: Run everything**

```bash
go build ./... && go test ./internal/jobs/ -v && make test && make go-lint
```

Expected: the two new `jobs` tests PASS, the whole suite still green.

- [ ] **Step 7: Commit**

```bash
git add internal/jobs/ internal/task/ internal/queue/ cmd/embookshelf/main.go
git commit -m "refactor(jobs): the payload types move to the leaf package

They are pure payload -- a struct, a Kind, sometimes a Queue -- and
having them in internal/task is what stopped the service tier naming
what it enqueues. The kinds and json tags are unchanged and now pinned
by a table test as literals, because River stores both and a rename
would orphan in-flight jobs with no error anywhere."
```

---

## Task 3: The audiobook tier takes an enqueuer

This is where the holder and its nil guards die.

**Files:**
- Modify: `internal/service/audiobook.go`, `internal/service/audiobook_test.go`, `internal/task/audiobook.go`, `internal/task/audiobook_segment_test.go`, `internal/queue/{queue,registry}.go`, `cmd/embookshelf/main.go`

**Interfaces:**
- Consumes: `jobs.Enqueuer`, `jobs.Deferred`, `jobs.ErrNoQueue`, `jobs.AudiobookSegmentArgs`, `jobs.AudiobookFinalizeArgs`.
- Produces: `service.NewAudiobookService(store audiobookStore, books bookSourceOpener, enq jobs.Enqueuer) *AudiobookService`; `task.SegmentDeps.Enqueue jobs.Enqueuer` replacing `Finalize`. `service.SegmentDispatcher`, `service.FinalizeDispatcher`, `service.AudiobookDispatch`, `WithFinalizeDispatcher` and `queue.Deps.AudiobookDispatch` no longer exist.

- [ ] **Step 1: Rewrite the service tests against a recording enqueuer**

In `internal/service/audiobook_test.go`, replace `recordingDispatcher` and `recordingFinalizer` with one recorder:

```go
// recordingEnqueuer captures the payloads a run hands to the pool, so a
// test can assert on what was queued rather than on the arguments of an
// anonymous function.
type recordingEnqueuer struct {
	mu   sync.Mutex
	args []jobs.Args
	err  error
}

func (r *recordingEnqueuer) Enqueue(_ context.Context, a jobs.Args) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.args = append(r.args, a)
	return nil
}

// segments returns the seq of every segment job queued, in order.
func (r *recordingEnqueuer) segments() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []int
	for _, a := range r.args {
		if s, ok := a.(jobs.AudiobookSegmentArgs); ok {
			out = append(out, s.Seq)
		}
	}
	return out
}

// finalizes counts the finalize jobs queued.
func (r *recordingEnqueuer) finalizes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, a := range r.args {
		if _, ok := a.(jobs.AudiobookFinalizeArgs); ok {
			n++
		}
	}
	return n
}
```

Every `NewAudiobookService(store, opener, nil)` becomes `NewAudiobookService(store, opener, &jobs.Deferred{})` — an unresolved `Deferred` is exactly what those tests were expressing with `nil`, and now it refuses with a reason instead of tripping a guard. Every `disp.dispatch` becomes the recorder; every `.WithFinalizeDispatcher(fin.dispatch)` is deleted and the recorder passed to the constructor instead. Assertions on dispatched seqs use `rec.segments()`; assertions on finalize use `rec.finalizes()`.

Add one test the old shape could not express:

```go
// A run that cannot be queued must fail loudly. Left pending with no
// jobs it shows 0% forever and no error explains why — the failure the
// nil-dispatcher guard used to produce silently.
func TestStartFailsTheRunWhenTheQueueIsNotUp(t *testing.T) {
	store := &fakeAudiobookStore{}
	svc := NewAudiobookService(store, &epubOpener{src: buildTestEPUB(t, strings.Repeat("abcdefghij", 100))}, &jobs.Deferred{})

	err := svc.Start(context.Background(), model.Book{ID: "b1", Format: "EPUB"}, AudiobookOptions{SegmentChars: 400})

	if err == nil {
		t.Fatal("Start returned nil with no queue configured")
	}
	if store.state != model.AudiobookFailed {
		t.Errorf("run state = %q, want failed so the UI can say why", store.state)
	}
}
```

Adjust the assertion to whatever `fakeAudiobookStore` actually records for state — read it before writing this test rather than assuming the field name.

- [ ] **Step 2: Run to verify failure**

```bash
go test ./internal/service/ -run TestStart
```

Expected: FAIL to compile.

- [ ] **Step 3: Rewrite the service**

In `internal/service/audiobook.go`:

Delete `SegmentDispatcher`, `FinalizeDispatcher`, `AudiobookDispatch` and their comments, and `WithFinalizeDispatcher`. Replace the struct fields and constructor:

```go
type AudiobookService struct {
	store audiobookStore
	books bookSourceOpener
	// enq is an ordinary dependency now. It used to be two function
	// fields plus a mutable holder, because internal/queue imports this
	// package and the seam had to dodge that.
	enq   jobs.Enqueuer
	sweep StagingSweeper
}

func NewAudiobookService(
	store audiobookStore,
	books bookSourceOpener,
	enq jobs.Enqueuer,
) *AudiobookService {
	return &AudiobookService{store: store, books: books, enq: enq}
}
```

`dispatchFinalize` loses its nil guard and becomes the enqueue:

```go
func (s *AudiobookService) dispatchFinalize(ctx context.Context, bookID string) error {
	return s.enq.Enqueue(ctx, jobs.AudiobookFinalizeArgs{BookID: bookID})
}
```

`dispatchAll` loses its nil guard; the body is otherwise unchanged, with the enqueue swapped in:

```go
func (s *AudiobookService) dispatchAll(ctx context.Context, bookID string, segments []model.AudiobookSegment) error {
	for _, seg := range segments {
		if err := s.enq.Enqueue(ctx, jobs.AudiobookSegmentArgs{BookID: bookID, Seq: seg.Seq}); err != nil {
			msg := fmt.Sprintf("could not queue segment %d: %v", seg.Seq, err)
			if serr := s.store.SetState(ctx, bookID, model.AudiobookFailed, msg); serr != nil {
				slog.Warn("audiobook: mark failed after dispatch error", "book", bookID, "err", serr)
			}
			return errors.New(msg)
		}
	}
	return nil
}
```

The `dispatchAll` doc comment ("A run left at pending with no jobs is invisible…") survives verbatim — it is now the reason `ErrNoQueue` is an error rather than a silent return.

- [ ] **Step 4: Retype the segment worker**

In `internal/task/audiobook.go`, `SegmentDeps.Finalize service.FinalizeDispatcher` becomes:

```go
	// Enqueue dispatches the finalize job when this segment completes
	// the run. An ordinary dependency: the holder it replaces was passed
	// in empty and filled after queue.New returned, which left a window
	// where a completing run silently lost its finalize job.
	Enqueue jobs.Enqueuer
```

In `recordSegment`, the `AudiobookNextFinalize` branch loses its nil guard entirely:

```go
	case model.AudiobookNextFinalize:
		if err := deps.Enqueue.Enqueue(ctx, jobs.AudiobookFinalizeArgs{BookID: a.BookID}); err != nil {
			slog.Warn("audiobook: dispatch finalize", "book", a.BookID, "err", err)
		}
```

In `internal/task/audiobook_segment_test.go`, the harness's `Finalize: func(...)` becomes an `Enqueue:` holding a small recording enqueuer, and `h.finalized` counts `jobs.AudiobookFinalizeArgs` payloads. Keep every existing assertion's meaning.

- [ ] **Step 5: Rewire queue and main**

In `internal/queue/queue.go`, delete the `AudiobookDispatch` field and its comment from `Deps`.

In `internal/queue/registry.go`, the segment deps' `Finalize:` closure — the one that dereferenced the holder late — is deleted outright and replaced with `Enqueue: deps.Enqueue`. Remove the now-unused `errors` import if nothing else needs it.

In `cmd/embookshelf/main.go`, replace the `audiobookDispatch := &service.AudiobookDispatch{}` declaration and the whole post-`queue.New` wiring block:

```go
	// One late-bound enqueuer for the whole composition root. The queue's
	// worker registry is built out of the services that need to enqueue,
	// so neither can exist first; jobs.Deferred holds that knot alone and
	// every service takes it as an ordinary argument.
	enq := &jobs.Deferred{}
```

declared before the services that need it. `audiobookSvc` is constructed with `enq` and keeps `WithStagingSweeper`. `queue.Deps` gains `Enqueue: enq` and loses `AudiobookDispatch`. After `queue.New` returns:

```go
	// The queue exists; everything holding the deferred enqueuer can now
	// reach it.
	enq.Resolve(q)
```

- [ ] **Step 6: Run everything**

```bash
go build ./... && go test ./internal/service/ ./internal/task/ -v 2>&1 | tail -40 && make test && go test -race ./internal/jobs/ ./internal/service/ ./internal/task/ && make go-lint
```

Expected: all green. The audiobook service's eleven tests still pass.

- [ ] **Step 7: Commit**

```bash
git add internal/service/ internal/task/ internal/queue/ cmd/embookshelf/main.go
git commit -m "refactor(audiobook): the run tier takes an enqueuer, not a holder

AudiobookDispatch was a pointer to a struct of function fields, passed
into queue.New empty and filled afterwards. queue.New starts River
before it returns, so a leftover segment job completing in that window
found no dispatcher and stranded its run.

Both dispatcher types and the holder are gone; the service and the
segment worker take a jobs.Enqueuer. The nil guards go with them -- an
unresolved queue now returns ErrNoQueue and Start fails the run with
the reason, which is what the dispatchAll comment always promised."
```

---

## Task 4: The bookdrop and guide tiers

The remaining three function types, and the last two "because internal/queue imports this package" comments.

**Files:**
- Modify: `internal/service/{bookdrop,bookdrop_intake,reading_guide_run}.go`, `internal/service/{bookdrop_intake,bookdrop_approve,reading_guide_run}_test.go`, `cmd/embookshelf/main.go`

**Interfaces:**
- Consumes: `jobs.Enqueuer`, `jobs.BookDropIngestArgs`, `jobs.BookDropAutoEnrichArgs`, `jobs.ReadingGuideArgs`.
- Produces: `NewBookDropService(bdrop, libs, books, covers, hub, files, enq jobs.Enqueuer)`; `WithAutoEnrichPolicy(p autoEnrichPolicy)` replacing `WithAutoEnrich`; `NewGuideRunner(c guideCandidateLister, enq jobs.Enqueuer, textCap int64)`. `IngestDispatcher`, `EnrichDispatcher`, `GuideDispatcher` and `WithIngestDispatcher` no longer exist.

- [ ] **Step 1: Rewrite the tests**

In `internal/service/bookdrop_intake_test.go`, `fakeDispatcher` becomes a `jobs.Enqueuer` recording `jobs.BookDropIngestArgs.ItemID`; `h.svc.dispatch = h.disp.dispatch` becomes construction with the recorder. In `bookdrop_approve_test.go`, `fakeEnrichDispatcher` records `jobs.BookDropAutoEnrichArgs.BookID`, and `WithAutoEnrich(policy, dispatch.dispatch)` becomes `WithAutoEnrichPolicy(policy)` with the recorder passed to the constructor.

`TestApproveWithoutAnEnrichDispatcherStillImports` keeps its name and its point — a binary with no worker pool still imports books — but expresses it with an unresolved `&jobs.Deferred{}` instead of a nil dispatcher. Update its comment to say so.

Apply the same treatment to `reading_guide_run_test.go`'s dispatcher, whatever shape it has — read it first.

- [ ] **Step 2: Run to verify failure**

```bash
go test ./internal/service/ -run 'TestIntake|TestApprove|TestGuide'
```

Expected: FAIL to compile.

- [ ] **Step 3: Rewrite the services**

`internal/service/bookdrop_intake.go`: delete `IngestDispatcher` and `WithIngestDispatcher`. The intake call site loses its nil guard but keeps its swallow — the row is committed and the watcher re-dispatches:

```go
	// The row is committed. Losing the job only delays processing; the
	// watcher re-dispatches on its next tick, and DiscoverOnStartup
	// catches anything still stranded at boot.
	if err := s.enq.Enqueue(ctx, jobs.BookDropIngestArgs{ItemID: item.ID}); err != nil {
		slog.Error("dispatch bookdrop ingest", "item_id", item.ID, "err", err)
	}
```

`internal/service/bookdrop.go`: delete `EnrichDispatcher` and its comment; the `enrichDispatch` field becomes nothing (the service already holds `enq`). `WithAutoEnrich` becomes `WithAutoEnrichPolicy` taking only the policy, with its comment rewritten — the two no longer travel together because only one of them is still optional. `requestAutoEnrich`'s guard drops the dispatcher half:

```go
	if s.enrichPolicy == nil {
		return
	}
```

and its dispatch becomes `s.enq.Enqueue(ctx, jobs.BookDropAutoEnrichArgs{BookID: bookID})`, still logged-and-swallowed. Add `enq jobs.Enqueuer` to `NewBookDropService`'s parameters and the struct.

`internal/service/reading_guide_run.go`: delete `GuideDispatcher`; `NewGuideRunner` takes `enq jobs.Enqueuer`; `r.dispatch(ctx, bookID)` becomes `r.enq.Enqueue(ctx, jobs.ReadingGuideArgs{BookID: bookID})`.

- [ ] **Step 4: Rewire main**

In `cmd/embookshelf/main.go`: `bdropSvc` is constructed with `enq` and its chain becomes `.WithLibraryStore(libStore).WithBookDropPath(cfg.BookDropPath).WithAutoEnrichPolicy(appSettingsRepo)`. Delete the two post-`queue.New` `bdropSvc.With…` blocks and their comments. `guideRunner` is constructed with `enq`; move its construction and the `guideCfg` read above `queue.New` — nothing in either depends on the queue any more.

- [ ] **Step 5: Confirm the comments are gone**

```bash
grep -rn "internal/queue imports this package" internal/ && echo "STILL PRESENT" || echo "all four gone"
grep -rn "Dispatcher\b" internal/service/ internal/task/ --include='*.go' | grep -v _test
```

Expected: `all four gone`, and no dispatcher types remaining outside tests.

- [ ] **Step 6: Run everything**

```bash
go build ./... && make test && go test -race ./internal/jobs/ ./internal/service/ ./internal/task/ && make go-lint
```

- [ ] **Step 7: Commit**

```bash
git add internal/service/ cmd/embookshelf/main.go
git commit -m "refactor(service): bookdrop and guide take the enqueuer too

The last three function-typed dispatchers and the last two copies of
the comment explaining why they existed. Auto-enrich keeps its optional
half -- a binary with no worker pool still imports books -- but that is
now a property of the policy, not of a nil dispatcher."
```

---

## Task 5: Record the decision

**Files:**
- Modify: `CONTEXT.md` if it names any deleted type; GitHub issue #184

- [ ] **Step 1: Check CONTEXT.md**

```bash
grep -n "AudiobookDispatch\|IngestDispatcher\|EnrichDispatcher\|GuideDispatcher\|SegmentDispatcher\|FinalizeDispatcher\|JobArgs" CONTEXT.md
```

Update any hit to match. If none, skip the commit at Step 3.

- [ ] **Step 2: Update issue #184**

Post a comment recording: that there was one holder and four re-derivations rather than three holders; that `queue.New` starts River before returning, making the holder both a data race and a live abandonment window; that the construction cycle cannot be dissolved by interface placement, so one late-bound indirection survives in `jobs.Deferred`; and that reaching zero would require inverting `BookDropSvc`, `Enrich` and `LibSvc` out of the worker deps, which is out of scope. Replace the issue's acceptance criteria with §6 of `docs/spec/one-enqueue-seam.spec.md`, preserving the originals below them.

- [ ] **Step 3: Final verification**

```bash
make ci-local && go test -race ./internal/jobs/ ./internal/service/ ./internal/task/
```

- [ ] **Step 4: Commit any CONTEXT.md change**

```bash
git add CONTEXT.md
git commit -m "docs(context): one enqueue seam replaces the dispatcher types"
```

---

## Self-Review Notes

**Spec coverage.** §4.1 → Tasks 1 and 2. §4.2 → Task 1. §4.3 → Tasks 3 and 4. §4.4 → Tasks 3 and 4. §5 → the tests in every task, with the `-race` pin proved against a mutex-free implementation in Task 1 Step 6. §6 → Task 5.

**Highest risk.** Task 2's payload move. A changed kind string or json tag orphans in-flight jobs silently. It is pinned two ways: a table test asserting literals (not values derived from the types, which would agree with any rename), and a mechanical `diff` of the kind strings before and after.

**Ordering.** Task 2 must precede Tasks 3 and 4 — the services cannot name what they enqueue until the payloads leave `internal/task`. Tasks 3 and 4 are independent of each other and could swap; 3 is first because it carries the race and the holder.

**Deliberately not done.** Workers still call back into services (`BookDropSvc`, `Enrich`, `LibSvc`), which is what keeps the construction cycle alive and forces `Deferred` to exist. Inverting that is a larger change and is out of scope per spec §3.
