# A narration run carries a generation, and a start refuses over a live one

A narration run had no identity. `BookAudiobookRepo.Start` upserted the run row to `pending` unconditionally and deleted the segment plan, while River could still hold in-flight segment jobs for the plan that had just gone. Those jobs address a unit of work as `(book_id, seq)` — deliberately, because a row id would let a retry address a row a regeneration had replaced (`jobs.AudiobookSegmentArgs`) — but `(book, seq)` has the same property one level up: sequence 12 of the run that went and sequence 12 of the run that replaced it are one address.

Two outcomes, both silent. A stale job for a sequence beyond the new plan failed a segment permanently. A stale job for a sequence *inside* it committed a result into the live plan's row, and with it a coverage read for a plan the result never entered — which can dispatch finalize and publish a book assembled partly from audio nobody asked for.

The question this ADR settles is what gives a run its identity, and what a start does when it finds one still working.

## Status

accepted (2026-07-30)

## Decisions bundled here

### 1. Identity is a counter on the run, not a run-identity row

`book_audiobooks` gains `generation INTEGER NOT NULL DEFAULT 0`, bumped by every `Start`. The segment job args carry it, and the two writes that touch a segment refuse a mismatch.

The alternative was a run-identity row — a `book_audiobook_runs` table with its own primary key, segments and jobs keyed on it. It was rejected on what the existing table *means*: one row per book, `book_id` as the primary key, mirroring `book_reading_guides` (ADR-0028 §5). A run-identity row makes that one-to-many and buys a run history, and nothing asks for a run history. What is actually needed is the ability to say "this job is not from the run that exists", which is a comparison, not a record.

The counter does not go on the segments. Segments are wiped and re-planned on every start, so a segment row can never disagree with its run; a generation column there would be a second place to be wrong, kept in step by hand. Both guards therefore read the parent row — `MarkSegmentRunning` joins `book_audiobooks` in the same statement as the claim, so there is no read-then-check for a regeneration to slip between.

### 2. Both writes carry the guard, because the claim is not the window

`MarkSegmentRunning` refuses a mismatched generation, and so does `RecordSegment`. The second is correctness, not belt-and-braces.

A segment is claimed *before* synthesis, and synthesis runs for minutes. So a job can claim under generation 1 and arrive at the write after generation 2 has started — that window is the whole engine call, and it is the expensive one, because by then the audio exists and would land in the live plan's row. Guarding the claim alone closes the cheap window and leaves the costly one open.

`RecordSegment` already refused a zero-row segment write (#220): a write matching no row is a result addressed to a plan that does not exist, and the call is refused with `ErrNotFound` rather than committing a transition derived from coverage the result never entered. The generation goes into that same `WHERE`, so there is one refusal path and one sentinel, not two.

### 3. A start refuses over a run that has not concluded

`Start`'s upsert takes a row only when it is absent or terminal (`ready`, `failed`, `canceled`). Over `pending` or `running` it refuses with `repo.ErrRunInProgress`.

This is the first already-running refusal on the `Generate → Preflight → Start` path; there was none before. Clobbering and relying on the generation alone was considered and rejected on money rather than on correctness: the generation makes the *stale jobs* harmless, but the segments the start deletes are audio that has already been bought, and the jobs still working through that plan carry on spending until each one discovers it has been superseded. ADR-0028 §6 makes cancel the stop-loss on a run that can cost $170, and the refusal is what makes a user take it deliberately instead of losing the same money by pressing Generate twice.

It surfaces as a **409 through `writeAudiobookError`'s existing default arm** — no new error code, no `AllErrorCodes` entry, no client change. It joins cancel-a-finished-run and retry-with-nothing-outstanding, which are the same category: the caller's view of this run is stale, and the sentence is the whole answer.

### 4. Staging is scoped by generation

A segment stages to `${DATA_PATH}/audiobooks/{book_id}/{generation}/seg-{seq}.mp3`. A superseded worker then physically cannot touch the live run's bytes.

Two reasons it is worth a directory level. `Staging.WriteSegment` is `os.WriteFile`, which is not atomic, so a stale write over a live segment can leave a truncated file that finalize concatenates without complaint. And the plans can genuinely differ — a regeneration may pick another voice, engine or segmentation cap — so the same `seq` of two generations is not the same audio, and generation-1 bytes landing at generation-2's path would ship the wrong voice with nothing anywhere to say so.

Nothing downstream had to change. Segment rows carry their own `staged_path`, so finalize never derives a path; `Clean(bookID)` and the hourly `Sweep` operate on the whole book directory and keep working unchanged.

### 5. The column defaults to 0, and that is load-bearing at deploy time

A segment job enqueued before this column existed has no `generation` key in its stored args, so Go decodes the zero value. Zero is also what the migration leaves on every existing row — and a row is only still at 0 if nothing has restarted it since the deploy, so a pre-deploy job addressing it genuinely is the current one. It claims, it records, it finishes. The first start after the deploy bumps that row to 1, and every genuinely stale job goes quiet from then on.

This rests entirely on a zero value, which is exactly the kind of thing that reads as an oversight and gets "tidied" into a pointer or a required field — either of which would fail every in-flight job of a deployment upgraded mid-run. It is pinned by two tests that say so: `TestAudiobookSegmentArgsFromBeforeGenerationsDecodeToZero` (`internal/jobs`) and `TestAJobEnqueuedBeforeGenerationsExistedStillClaimsItsSegment` (`internal/task`).

## Consequences

- Regenerating now takes two actions where it took one: cancel, then generate. The UI's type-to-confirm regenerate dialog is the place a user meets this, and it currently discovers the refusal from a 409 rather than anticipating it.
- `BookAudiobookRepo.Start` returns the generation it assigned, and `AudiobookService` carries that value into every job it dispatches. `Retry` deliberately dispatches under the run's *existing* generation: a retry re-enters the plan that is there rather than replacing it, which is the whole reason it is cheap (ADR-0028 §6).
- Staging paths gained a level. Anything reading them by convention rather than from `staged_path` would break; nothing does.
- The repo's claim to be the only writer of run state is now true on the start path too: the unguarded upsert-to-`pending` is gone.

## Companion artifacts

- ADR-0028 — the pipeline this amends, in particular §3 (one job per chapter) and §6 (cancel as the stop-loss).
- ADR-0025 §4 — one audiobook per Book, and regeneration as a destructive overwrite.
- `CONTEXT.md` — Audiobook run, Segment, Staging area.
- Issue #253, and #220, which established the refuse-a-zero-row-write mechanism §2 extends.
