import type { Audiobook } from "@/api/audiobooks"

/** What phase of its life a run is in, as a reader sees it. */
export type RunPhase = "running" | "ready" | "stopped"

/**
 * A run as the panel needs it: derived facts, not state strings.
 *
 * The panel branched on `state` in four separate places — a predicate, a
 * three-way render switch, a failed check for the retry button, and a
 * pending check for the provenance line — so a new state meant finding
 * all four (#197).
 */
export type RunView = {
  phase: RunPhase
  /** Coverage, done over total, 0–100 and already rounded. */
  percent: number
  canCancel: boolean
  canRetry: boolean
  canRegenerate: boolean
  showProvenance: boolean
}

export function runView(a: Audiobook): RunView {
  // Pending and running are one phase here. The difference is whether
  // the segment jobs have been picked up, which matters to the queue and
  // not at all to someone watching a progress bar.
  const moving = a.state === "pending" || a.state === "running"
  const phase: RunPhase = moving ? "running" : a.state === "ready" ? "ready" : "stopped"

  return {
    phase,
    percent: percentComplete(a),
    // The only stop-loss on a run that may cost $170, so it is offered
    // for as long as the run is not terminal.
    canCancel: moving,
    // Retry re-enqueues only the segments that never finished. On a
    // cancelled run that would resume something the user stopped.
    canRetry: a.state === "failed",
    canRegenerate: !moving,
    // A pending run has produced nothing to describe yet.
    showProvenance: a.state !== "pending",
  }
}

/**
 * Progress is coverage over persisted rows rather than job state,
 * because that is what survives a reload and a restart on a run measured
 * in tens of minutes (ADR-0028 §7).
 *
 * Failed sections count as finished: they will not move again, and a bar
 * that stops short of the end on a run that has stopped reads as a run
 * still going.
 */
function percentComplete(a: Audiobook): number {
  if (a.segmentsTotal <= 0) return 0
  const settled = a.segmentsDone + a.segmentsFailed
  return Math.round((settled / a.segmentsTotal) * 100)
}
