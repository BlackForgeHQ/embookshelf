import { describe, expect, it } from "vitest"

import type { Audiobook } from "@/api/audiobooks"
import { runView } from "@/lib/audiobookRun"

function run(over: Partial<Audiobook> = {}): Audiobook {
  return {
    bookId: "b1",
    state: "running",
    engine: "openai",
    voice: "alloy",
    model: "tts-1",
    error: "",
    segmentsTotal: 10,
    segmentsDone: 4,
    segmentsFailed: 0,
    durationSeconds: 0,
    stale: false,
    ...over,
  } as Audiobook
}

describe("runView phases", () => {
  // Pending and running are one phase to a reader: something is
  // happening and there is nothing to do but wait. The distinction
  // matters to the server, not to the panel.
  it("treats pending and running as one moving phase", () => {
    expect(runView(run({ state: "pending" })).phase).toBe("running")
    expect(runView(run({ state: "running" })).phase).toBe("running")
  })

  it("separates a finished narration from a stopped one", () => {
    expect(runView(run({ state: "ready" })).phase).toBe("ready")
    expect(runView(run({ state: "failed" })).phase).toBe("stopped")
    expect(runView(run({ state: "canceled" })).phase).toBe("stopped")
  })
})

describe("runView playable", () => {
  // Whether the audio Rendition exists — the question `renditionsFor`
  // used to answer for itself by comparing the wire `state` (#243), which
  // is exactly the two-places-to-look failure this view-model was built
  // to close (#197).
  //
  // Keyed by state rather than looped over a literal list: a state added
  // to the wire type fails to compile here until this table says whether
  // it has bytes behind it.
  const playableByState: Record<Audiobook["state"], boolean> = {
    // Nothing has been written yet.
    pending: false,
    // Partial audio is not a file anyone can open.
    running: false,
    ready: true,
    // Both terminal and both empty-handed: a failed run may have written
    // some segments and a cancelled one certainly did, but neither ever
    // produced the stitched file the player would ask for.
    failed: false,
    canceled: false,
  }

  it("answers whether the narration has bytes to play, for every state", () => {
    for (const [state, want] of Object.entries(playableByState)) {
      expect(runView(run({ state: state as Audiobook["state"] })).playable).toBe(
        want,
      )
    }
  })

  // ADR-0025 §2 surfaces staleness rather than acting on it: discarding
  // hours of audio because someone re-uploaded a better EPUB would be
  // worse. Asserted here rather than in `formats.test.ts`, where it used
  // to live, because staleness no longer reaches the Rendition module.
  it("keeps a stale narration playable", () => {
    expect(runView(run({ state: "ready", stale: true })).playable).toBe(true)
  })
})

describe("runView percent", () => {
  // Done over total on persisted rows, not job state: that is what
  // survives a reload on a run measured in tens of minutes (ADR-0028 §7).
  it("is coverage, done over total", () => {
    expect(runView(run({ segmentsDone: 4, segmentsTotal: 10 })).percent).toBe(40)
  })

  it("counts a failed section as finished, because it will not move again", () => {
    expect(
      runView(run({ segmentsDone: 3, segmentsFailed: 1, segmentsTotal: 4 })).percent,
    ).toBe(100)
  })

  // A plan with no segments would otherwise divide by zero and render
  // NaN% in the progress bar.
  it("is zero before a plan exists", () => {
    expect(runView(run({ segmentsDone: 0, segmentsTotal: 0 })).percent).toBe(0)
  })
})

describe("runView affordances", () => {
  // Cancel is the only stop-loss on a run that may cost $170, so it is
  // offered for as long as the run is not terminal.
  it("offers cancel while the run is moving and never after", () => {
    expect(runView(run({ state: "running" })).canCancel).toBe(true)
    expect(runView(run({ state: "pending" })).canCancel).toBe(true)
    expect(runView(run({ state: "ready" })).canCancel).toBe(false)
    expect(runView(run({ state: "failed" })).canCancel).toBe(false)
  })

  // Retry re-enqueues only what never finished, so it is meaningless on
  // a cancelled run — the user asked for it to stop.
  it("offers retry on a failure and not on a cancellation", () => {
    expect(runView(run({ state: "failed" })).canRetry).toBe(true)
    expect(runView(run({ state: "canceled" })).canRetry).toBe(false)
    expect(runView(run({ state: "ready" })).canRetry).toBe(false)
  })

  it("offers regeneration only once nothing is moving", () => {
    expect(runView(run({ state: "running" })).canRegenerate).toBe(false)
    expect(runView(run({ state: "ready" })).canRegenerate).toBe(true)
    expect(runView(run({ state: "failed" })).canRegenerate).toBe(true)
  })

  // A pending run has no engine result to describe yet.
  it("hides provenance until the run has started producing", () => {
    expect(runView(run({ state: "pending" })).showProvenance).toBe(false)
    expect(runView(run({ state: "running" })).showProvenance).toBe(true)
  })
})
