import { describe, expect, it } from "vitest"

import type { Audiobook } from "@/api/audiobooks"
import { renditionsFor } from "@/lib/rendition"

function narration(over: Partial<Audiobook> = {}): Audiobook {
  return {
    bookId: "b1",
    state: "ready",
    engine: "openai",
    voice: "alloy",
    segmentsTotal: 10,
    segmentsDone: 10,
    segmentsFailed: 0,
    durationSeconds: 3600,
    stale: false,
    ...over,
  } as Audiobook
}

describe("renditionsFor", () => {
  // The primary rendition is the book's own file, whatever it is. A CBZ
  // has one and it is a comic; an MP3 has one and it is audio. This is
  // the axis books.format answers, and it is not the axis Listen
  // answers (ADR-0025 §3).
  it("always offers the book's own file", () => {
    const state = renditionsFor("EPUB", null, "primary")

    expect(state.available).toEqual(["primary"])
    expect(state.selected).toBe("primary")
    expect(state.canSwitch).toBe(false)
  })

  it("offers narration once one is ready", () => {
    const state = renditionsFor("EPUB", narration(), "primary")

    expect(state.available).toEqual(["primary", "narration"])
    expect(state.canSwitch).toBe(true)
  })

  // A run still going has no file behind it. Offering Listen would open
  // a player pointed at nothing — which is why the panel's own gate is
  // on ready rather than on the run existing.
  it("withholds narration until the run is ready", () => {
    for (const state of ["pending", "running", "failed", "canceled"] as const) {
      const got = renditionsFor("EPUB", narration({ state }), "primary")

      expect(got.available).toEqual(["primary"])
      expect(got.canSwitch).toBe(false)
    }
  })

  it("selects the narration when it is asked for and ready", () => {
    expect(renditionsFor("EPUB", narration(), "narration").selected).toBe(
      "narration",
    )
  })

  // The fallback that matters on a reload mid-run: a reader who was
  // listening comes back to a run that is no longer ready and gets the
  // text, not a player with nothing behind it.
  it("falls back to the book's own file when the narration is not ready", () => {
    const state = renditionsFor("EPUB", narration({ state: "running" }), "narration")

    expect(state.selected).toBe("primary")
  })

  it("falls back when there is no narration at all", () => {
    expect(renditionsFor("EPUB", null, "narration").selected).toBe("primary")
  })

  // A stale narration is still playable — ADR-0025 §2 surfaces the
  // staleness rather than acting on it, because discarding hours of
  // audio over a re-upload would be worse.
  it("keeps a stale narration selectable", () => {
    const state = renditionsFor("EPUB", narration({ stale: true }), "narration")

    expect(state.available).toContain("narration")
    expect(state.selected).toBe("narration")
  })

  // Narration is only ever generated from a narratable format, but the
  // run outlives the book's format: a re-import can turn a narrated EPUB
  // into something else, and the audio that was already paid for is
  // still there and still playable.
  it("still offers a narration whose book is no longer narratable", () => {
    const state = renditionsFor("CBZ", narration(), "narration")

    expect(state.available).toContain("narration")
    expect(state.selected).toBe("narration")
  })

  // A book whose own file nothing can open still has its narration.
  // Without this the reader would refuse a book it can perfectly well
  // read aloud.
  it("offers narration for a format with no reader of its own", () => {
    const state = renditionsFor("MOBI", narration(), "primary")

    expect(state.available).toEqual(["narration"])
    expect(state.selected).toBe("narration")
    expect(state.canSwitch).toBe(false)
  })

  it("has nothing to offer for an unreadable book with no narration", () => {
    const state = renditionsFor("MOBI", null, "primary")

    expect(state.available).toEqual([])
    expect(state.selected).toBeNull()
  })
})
