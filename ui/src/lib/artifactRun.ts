import { LIVE_POLL_MS } from "@/api/query"

// The one reader of every derived artifact's status vocabulary (#350):
// the Markdown rendition and the generated EPUB share a five-state
// union, the narration adds "canceled", and five surfaces used to
// re-derive "is it moving / did it fail / is it stale" from the raw
// strings — one of them forgot the poll entirely and hung on a
// transient state forever. The UI twin of the Go tier's
// renditionStatusSpec (#339) and Staleness (#340) moves, and the
// generalisation of lib/audiobookRun's runView, which proved the shape
// for one artifact of four (that module keeps the run-specific facts —
// percent, canCancel — and shares this vocabulary).

/** The wire states every rendition-shaped artifact speaks. */
export type ArtifactState =
  | "none"
  | "pending"
  | "running"
  | "ready"
  | "failed"
  | "canceled"

/** What phase of its life an artifact is in, as a surface sees it. */
export type ArtifactPhase = "none" | "moving" | "ready" | "stopped"

/**
 * An artifact's status as derived facts, not state strings. Undefined
 * input is a query still loading (or gated off): everything answers
 * false — surfaces gate their loading arms separately.
 */
export type ArtifactView = {
  phase: ArtifactPhase
  /** Work is in flight: the surface should poll and say so. */
  moving: boolean
  ready: boolean
  failed: boolean
  /** Labelled, never auto-invalidated (ADR-0025 §2 applied to renditions). */
  stale: boolean
  /** The worker's message verbatim — the thing the admin acts on (ADR-0033 §5). */
  error?: string
  /** Offer the generate button: nothing exists, it failed, or it is stale. */
  canGenerate: boolean
  canRetry: boolean
}

/** movingStates: enqueued or converting — the difference is the queue's. */
const movingStates: ReadonlySet<string> = new Set(["pending", "running"])

export function isMovingState(state: string | undefined): boolean {
  return state !== undefined && movingStates.has(state)
}

export function artifactView(
  status: { state: ArtifactState; stale?: boolean; error?: string } | undefined
): ArtifactView {
  if (!status) {
    return {
      phase: "none",
      moving: false,
      ready: false,
      failed: false,
      stale: false,
      canGenerate: false,
      canRetry: false,
    }
  }
  const moving = isMovingState(status.state)
  const ready = status.state === "ready"
  const failed = status.state === "failed"
  const stale = status.stale === true
  const phase: ArtifactPhase =
    moving ? "moving" : ready ? "ready" : failed || status.state === "canceled" ? "stopped" : "none"
  return {
    phase,
    moving,
    ready,
    failed,
    stale,
    error: status.error,
    canGenerate: status.state === "none" || failed || stale,
    canRetry: failed,
  }
}

/**
 * pollWhile is the poll predicate as part of the module: LIVE_POLL_MS
 * while the caller's "still working" test holds, off otherwise. Spread
 * into useApiQuery opts — nobody writes a refetchInterval by hand.
 */
export function pollWhile<TData>(moving: (data: TData | undefined) => boolean): {
  refetchInterval: (query: { state: { data?: TData } }) => number | false
} {
  return {
    refetchInterval: (query) => (moving(query.state.data) ? LIVE_POLL_MS : false),
  }
}

/** The rendition-shaped default: poll while the artifact's state is moving. */
export function pollWhileMoving<TData extends { state?: string }>(): {
  refetchInterval: (query: { state: { data?: TData } }) => number | false
} {
  return pollWhile<TData>((data) => isMovingState(data?.state))
}
