import { describe, expect, it } from "vitest"

import { LIVE_POLL_MS, pollWhile } from "@/lib/poll"

describe("pollWhile", () => {
  // Self-terminating: an idle instance is never polled, which is the
  // whole reason this is a predicate rather than an interval.
  it("polls at the shared cadence while the predicate holds", () => {
    const interval = pollWhile((d: { live: boolean }) => d.live)
    expect(interval({ state: { data: { live: true } } })).toBe(LIVE_POLL_MS)
  })

  it("stops once the predicate goes false", () => {
    const interval = pollWhile((d: { live: boolean }) => d.live)
    expect(interval({ state: { data: { live: false } } })).toBe(false)
  })

  // No data yet means the first fetch is still in flight; polling on an
  // undefined payload would call the predicate with nothing.
  it("stops when there is no data to judge", () => {
    const interval = pollWhile((d: { live: boolean }) => d.live)
    expect(interval({ state: { data: undefined } })).toBe(false)
  })
})
