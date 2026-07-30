import { describe, expect, it } from "vitest"

import type { Locator } from "@/lib/locator"
import type { ResumePositions } from "@/lib/resume"
import { encodeLocator } from "@/lib/locator"
import {
  renditionForLocator,
  resumeCfi,
  resumeFieldFor,
  resumeLocator,
  resumePage,
  resumeSeconds,
} from "@/lib/resume"

const CFI = "epubcfi(/6/14!/4/2)"

// The write side, as `internal/repo/progress.go` performs it: an
// incoming locator is routed by its own kind, and an absent one leaves
// both stored positions alone.
//
// Composed from the shipped rule rather than a copy of it. This helper
// used to carry its own `Stored` type and its own `startsWith("time:")`
// branch, which meant the round-trip tests below proved a rule that
// existed nowhere but in this file (#245).
function store(prev: ResumePositions, locator: Locator | null): ResumePositions {
  if (!locator) return prev
  const field = resumeFieldFor(renditionForLocator(locator))
  return { ...prev, [field]: encodeLocator(locator) }
}

describe("resumeLocator", () => {
  const cases: Array<{
    name: string
    stored: ResumePositions
    primary: Locator | null
    narration: Locator | null
  }> = [
    {
      name: "no position at all",
      stored: {},
      primary: null,
      narration: null,
    },
    {
      name: "text position only",
      stored: { resumeCfi: CFI },
      primary: { kind: "cfi", cfi: CFI },
      narration: null,
    },
    {
      name: "audio position only",
      stored: { resumeAudio: "time:90.50" },
      primary: null,
      narration: { kind: "time", seconds: 90.5 },
    },
    {
      name: "both positions, kept apart",
      stored: { resumeCfi: "page:12", resumeAudio: "time:3661.00" },
      primary: { kind: "page", page: 12 },
      narration: { kind: "time", seconds: 3661 },
    },
    // Unreadable rather than absent: the text position survives as
    // `unknown` (a locator no shell resumes from, but one a label can
    // still print), while an unreadable audio token is no audio
    // position at all — it does not route back to the narration.
    {
      name: "malformed stored value",
      stored: { resumeCfi: "¯\\_(ツ)_/¯", resumeAudio: "time:not-a-number" },
      primary: { kind: "unknown", raw: "¯\\_(ツ)_/¯" },
      narration: null,
    },
    // A pre-#200 row, when both shells shared one column. The timestamp
    // is not a text position and must not be offered as one.
    {
      name: "audio token stranded in the text field",
      stored: { resumeCfi: "time:42.00" },
      primary: null,
      narration: null,
    },
  ]

  for (const c of cases) {
    it(`answers both Renditions: ${c.name}`, () => {
      expect(resumeLocator(c.stored, "primary")).toEqual(c.primary)
      expect(resumeLocator(c.stored, "narration")).toEqual(c.narration)
    })
  }
})

// The round trip #200 is about, at the layer that decides it: a narrated
// book's two shells write two Locator kinds, and each has to read back
// the one it wrote. Before the split they shared one field, so the audio
// shell decoded a CFI, fell back to zero, and then overwrote the reading
// position with a timestamp — losing the place in both directions.
describe("resume positions per Rendition", () => {
  it("survives Read → Listen → Read", () => {
    let stored: ResumePositions = {}

    stored = store(stored, { kind: "cfi", cfi: CFI })
    stored = store(stored, { kind: "time", seconds: 60 })

    expect(resumeCfi(stored)).toBe(CFI)
    expect(resumeSeconds(stored)).toBe(60)
  })

  it("survives Listen → Read → Listen", () => {
    let stored: ResumePositions = {}

    stored = store(stored, { kind: "time", seconds: 90.5 })
    stored = store(stored, { kind: "cfi", cfi: "epubcfi(/6/2)" })

    expect(resumeSeconds(stored)).toBeCloseTo(90.5)
    expect(resumeCfi(stored)).toBe("epubcfi(/6/2)")
  })

  // A page locator is a text position: the PDF and comic shells write
  // it, and neither of them is the narration.
  it("files a page locator with the text position", () => {
    const stored = store({}, { kind: "page", page: 12 })

    expect(stored.resumeAudio).toBeUndefined()
    expect(resumePage(stored)).toBe(12)
    expect(resumeSeconds(stored)).toBeUndefined()
  })

  // A percent-only update carries no locator and must not wipe either.
  it("leaves both positions alone when there is no locator", () => {
    let stored: ResumePositions = {}
    stored = store(stored, { kind: "cfi", cfi: "epubcfi(/6/2)" })
    stored = store(stored, { kind: "time", seconds: 30 })

    const after = store(stored, null)

    expect(after.resumeCfi).toBe(stored.resumeCfi)
    expect(after.resumeAudio).toBe(stored.resumeAudio)
  })
})

describe("the shell-facing readings", () => {
  it("yields nothing when the stored kind is not the one asked for", () => {
    const paged: ResumePositions = { resumeCfi: "page:7" }

    expect(resumeCfi(paged)).toBeUndefined()
    expect(resumePage(paged)).toBe(7)
  })

  // The page a token carries is the human one. ComicReader's 0-indexed
  // model is its own boundary's problem, not this module's — see the
  // module header.
  it("reports the human page number, not a reader's index", () => {
    expect(resumePage({ resumeCfi: "page:1" })).toBe(1)
  })

  // A negative timestamp is decodable but not seekable.
  it("clamps a negative audio position to the start", () => {
    expect(resumeSeconds({ resumeAudio: "time:-3.00" })).toBe(0)
  })
})
