# A segment awaiting a retry is outstanding, not failed

The segment worker recorded `model.SegmentFailed` for both kinds of failure and then said which kind it was to River alone: `return nil` for a permanent one, so River stops, and `return err` for a transient one, so River retries. The row could not tell the two apart.

`AudiobookCoverage` counts `failed` as settled, and `NextForRun` concludes a run whose coverage is settled with a failure in it. So a sibling segment landing while a retry was still outstanding read one settled failure, concluded the run `failed`, and the retry that then succeeded was a no-op — the disposition deliberately refuses to act on a failed run (ADR-0028 §7, #206). The result was a run sitting at `failed` with every segment done and audio on disk for all of them, recoverable only by an explicit Retry.

Which of the two interleavings happened was a timing coincidence. The cross-tier pipeline test (#248) asserted the lucky one and its own comment described the unlucky one as expected behaviour.

## Status

accepted (2026-07-30)

## Decisions bundled here

### 1. `retrying` is a segment state

`book_audiobook_segments.state` gains `retrying` (migration 000044, Postgres only — SQLite parity is frozen at 38 per ADR-0023). Coverage counts it in `Total` and in neither `Done` nor `Failed`, so a run holding one is neither complete nor settled and does not conclude.

A state rather than a `retry_at` timestamp or an attempt counter on the row: the question the disposition asks is not *when* the retry is or *how many* have happened, it is whether this segment is still outstanding. That is what a state answers, and it is the same distinction `canceled` already draws on the run — two outcomes that would both be "failure" if the schema only had one word for it.

The alternative considered and rejected was deferring the *conclusion* rather than changing what a state means: leaving the row `failed` and having `NextForRun` withhold `fail` while any segment might still be retried. It cannot be written. The rule reads segment rows and a state column, and nothing in either says whether River holds an attempt — the fact only exists in the queue's own tables, which the model tier does not and should not read. Encoding it in the row the worker already writes is the only place the information is available at the moment it is known.

### 2. The worker learns which attempt it is, as a plain value

`jobs.Attempt{Number, Max}`, filled by the registry's River adapter from `job.Attempt` / `job.MaxAttempts` and passed to every registered work function. The three failure branches are then:

- permanent (`tts.ErrPermanent`, `service.ErrNotNarratable`) → `failed`, return nil,
- transient with attempts remaining → `retrying`, return the error so River retries,
- transient on the last attempt → `failed`, return the error.

A plain value rather than the `*river.Job`, because `internal/task` does not import River and this change is not the reason to start (CONTEXT.md, Job registry). That confinement is what lets every worker be exercised without a queue, and it is why `jobs.ErrDoNotRetry` exists at all: the task tier states a retry verdict in a vocabulary the queue tier translates.

The signature widened for all seven registered jobs rather than for the segment worker alone. The adapter that fills the attempt in is generic over the args type, so the alternative was a second `register` overload existing only to keep six closures one parameter shorter.

`Attempt.Last()` is true for the zero value, which is the safe reading rather than an accident: a zero `Attempt` means no queue told this worker anything, so there is no retry to wait for, and recording a settled failure concludes the run where `retrying` would leave a row nothing is ever going to move.

### 3. Nothing else changes what it means by "settled"

Everything downstream already reads `retrying` correctly, and this is the audit rather than a claim:

- `scanCoverage` filters `state = 'failed'` for the failed column, so `retrying` was never in it. Widening that filter is the one edit that reintroduces the bug, and it says so.
- `ListUnfinishedSegments` (`state <> 'done'`) re-enqueues a `retrying` segment on Retry, which is the route back if the queue itself gives up on the job.
- `MarkSegmentRunning` (`state <> 'done'`) lets the retry claim the row.
- `AudiobookFinalize` defers on any segment that is not `done`.
- `ListStaleStaging` (`state <> 'done'`) still reclaims a run that has sat with a `retrying` segment past the TTL.
- Nothing renders a per-segment state. The handler's `audiobookDTO` exposes `segmentsTotal/Done/Failed` — the Coverage counts — and the UI's progress bar is `(done + failed) / total`, so a `retrying` segment reads as outstanding there too. There is no exhaustive switch over `SegmentState` anywhere in the tree.

## Consequences

- A run whose engine is down now stays `running` for as long as River's backoff takes to exhaust ~25 attempts, where it used to fail on the first one. That is the point — the run is genuinely still being attempted — but it is a visible change in how long a broken engine takes to surface, and the progress bar sits still while it happens.
- `book_audiobook_segments.state` is no longer a four-value column. The down migration moves `retrying` rows to `failed` before narrowing the CHECK, which is the reading the older code puts on them.
- The pipeline test's ordering caveat is gone: both interleavings are now tested and both reach `ready`, plus a third test for the transient failure that exhausts its attempts and does conclude the run — deferring a conclusion is only honest while something is still going to run.

## Companion artifacts

- ADR-0028 §3 (one job per chapter, the retry unit) and §7 (Coverage as the run's authority on its own lifecycle).
- ADR-0031 — the other amendment to the same write path; its generation guard and this state are both about a result arriving when the run has moved on.
- `CONTEXT.md` — Segment, Coverage.
- Issue #263, surfaced by the cross-tier pipeline test of #248.
