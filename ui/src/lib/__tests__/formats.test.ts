import { describe, expect, it } from "vitest"

import type { Audiobook } from "@/api/audiobooks"
import type { RunView } from "@/lib/audiobookRun"
import { runView } from "@/lib/audiobookRun"
import {
  NARRATABLE_FORMATS,
  isNarratableFormat,
  narratableFormatList,
  readerKindForFormat,
  renditionsFor,
} from "@/lib/formats"

describe("isNarratableFormat", () => {
  // The format reaches this from an API payload that got it from a
  // database column that got it from a filename. Only the first of those
  // is canonical, so a case-sensitive comparison would hide the Generate
  // button for a book whose row happens to say "epub".
  it("ignores case and surrounding space", () => {
    for (const format of ["EPUB", "epub", "ePub", "  EPUB  "]) {
      expect(isNarratableFormat(format)).toBe(true)
    }
  })

  it("refuses formats with no text to read", () => {
    for (const format of ["PDF", "CBZ", "M4B", "", "EPUB3"]) {
      expect(isNarratableFormat(format)).toBe(false)
    }
  })

  // Guards the Go parity test's parser as much as the behaviour: it
  // reads this file by exact anchor, and an empty list would make it
  // fail loudly rather than pass vacuously.
  it("has at least one format", () => {
    expect(NARRATABLE_FORMATS.length).toBeGreaterThan(0)
  })
})

describe("readerKindForFormat", () => {
  it("routes each format to its surface", () => {
    expect(readerKindForFormat("EPUB")).toBe("text")
    expect(readerKindForFormat("PDF")).toBe("text")
    expect(readerKindForFormat("CBZ")).toBe("comic")
    expect(readerKindForFormat("M4B")).toBe("audio")
  })

  // null is what the route turns into "reader not implemented". A
  // default of "text" would have opened the EPUB reader on a .mobi and
  // failed somewhere deep inside epub.js instead.
  it("returns null for a format nothing reads", () => {
    expect(readerKindForFormat("MOBI")).toBeNull()
    expect(readerKindForFormat("DJVU")).toBeNull()
    expect(readerKindForFormat("")).toBeNull()
  })

  it("ignores case, like the rest of the table", () => {
    expect(readerKindForFormat("epub")).toBe("text")
  })

  // The reader kind is not the Rendition. An EPUB with a finished
  // narration still opens the text surface; the Listen toggle lives
  // inside it (ADR-0025 §3).
  it("does not answer the rendition question", () => {
    expect(readerKindForFormat("EPUB")).toBe("text")
    expect(isNarratableFormat("EPUB")).toBe(true)
  })
})

describe("narratableFormatList", () => {
  it("reads as a sentence fragment", () => {
    const list = narratableFormatList()
    expect(list).toBe("EPUB")
    expect(list).not.toContain('"')
    expect(list).not.toContain("[")
  })
})

// A run as this module now receives it: the view, not the wire row.
// Built by `runView` rather than hand-rolled so the pair cannot drift
// into a combination the real view-model never produces (#243).
function narration(over: Partial<Audiobook> = {}): RunView {
  return runView({
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
  } as Audiobook)
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

  it("offers narration once one is playable", () => {
    const state = renditionsFor("EPUB", narration(), "primary")

    expect(state.available).toEqual(["primary", "narration"])
    expect(state.canSwitch).toBe(true)
  })

  // Offering Listen for a run with nothing behind it would open a player
  // pointed at silence. Which states have bytes is not asked here — that
  // table lives in `audiobookRun`, and this asserts only that the answer
  // is obeyed.
  it("withholds narration while the run is not playable", () => {
    for (const state of ["pending", "running", "failed", "canceled"] as const) {
      const got = renditionsFor("EPUB", narration({ state }), "primary")

      expect(got.available).toEqual(["primary"])
      expect(got.canSwitch).toBe(false)
    }
  })

  it("selects the narration when it is asked for and playable", () => {
    expect(renditionsFor("EPUB", narration(), "narration").selected).toBe(
      "narration",
    )
  })

  // The fallback that matters on a reload mid-run: a reader who was
  // listening comes back to a run that is no longer ready and gets the
  // text, not a player with nothing behind it.
  it("falls back to the book's own file when the narration is not playable", () => {
    const state = renditionsFor("EPUB", narration({ state: "running" }), "narration")

    expect(state.selected).toBe("primary")
  })

  it("falls back when there is no narration at all", () => {
    expect(renditionsFor("EPUB", null, "narration").selected).toBe("primary")
  })

  // Staleness no longer reaches this module at all — it is not on the
  // view — so the assertion that it does not withhold a stale narration
  // moved to `audiobookRun.test.ts`, next to the field that decides.

  it("has nothing to offer for an unreadable book with no narration", () => {
    const state = renditionsFor("MOBI", null, "primary")

    expect(state.available).toEqual([])
    expect(state.selected).toBeNull()
  })
})
