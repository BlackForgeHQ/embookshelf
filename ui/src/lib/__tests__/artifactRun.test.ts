import { describe, expect, it } from "vitest"

import { LIVE_POLL_MS } from "@/api/query"
import { artifactView, isMovingState, pollWhile, pollWhileMoving } from "@/lib/artifactRun"

describe("artifactView", () => {
  it("derives the facts per state, so no surface reads raw strings", () => {
    expect(artifactView({ state: "none", stale: false })).toMatchObject({
      phase: "none", moving: false, canGenerate: true, canRetry: false,
    })
    expect(artifactView({ state: "pending", stale: false })).toMatchObject({
      phase: "moving", moving: true, canGenerate: false,
    })
    expect(artifactView({ state: "running", stale: false })).toMatchObject({
      phase: "moving", moving: true,
    })
    expect(artifactView({ state: "ready", stale: false })).toMatchObject({
      phase: "ready", ready: true, canGenerate: false,
    })
    expect(artifactView({ state: "failed", stale: false, error: "boom" })).toMatchObject({
      phase: "stopped", failed: true, canGenerate: true, canRetry: true, error: "boom",
    })
    expect(artifactView({ state: "canceled", stale: false })).toMatchObject({
      phase: "stopped", moving: false, canRetry: false,
    })
  })

  it("stale is labelled and re-offers generation, never auto-invalidates", () => {
    const v = artifactView({ state: "ready", stale: true })
    expect(v.ready).toBe(true)
    expect(v.stale).toBe(true)
    expect(v.canGenerate).toBe(true)
  })

  it("a query still loading answers all-false rather than lying", () => {
    const v = artifactView(undefined)
    expect(v).toMatchObject({ moving: false, ready: false, failed: false, canGenerate: false })
  })
})

describe("the poll predicate", () => {
  const q = (data: unknown) => ({ state: { data } }) as { state: { data?: { state?: string } } }

  it("polls at the shared cadence while moving, stops on terminal states", () => {
    const { refetchInterval } = pollWhileMoving()
    expect(refetchInterval(q({ state: "pending" }))).toBe(LIVE_POLL_MS)
    expect(refetchInterval(q({ state: "running" }))).toBe(LIVE_POLL_MS)
    expect(refetchInterval(q({ state: "ready" }))).toBe(false)
    expect(refetchInterval(q({ state: "failed" }))).toBe(false)
    expect(refetchInterval(q({ state: "none" }))).toBe(false)
    // A first fetch still in flight is not something to poll about.
    expect(refetchInterval(q(undefined))).toBe(false)
  })

  it("pollWhile takes any moving test — the coverage-count shape", () => {
    const { refetchInterval } = pollWhile<{ converting: number }>(
      (d) => (d?.converting ?? 0) > 0
    )
    expect(refetchInterval({ state: { data: { converting: 3 } } })).toBe(LIVE_POLL_MS)
    expect(refetchInterval({ state: { data: { converting: 0 } } })).toBe(false)
  })

  it("isMovingState is the one spelling of the moving set", () => {
    expect(isMovingState("pending")).toBe(true)
    expect(isMovingState("running")).toBe(true)
    expect(isMovingState("ready")).toBe(false)
    expect(isMovingState(undefined)).toBe(false)
  })
})
