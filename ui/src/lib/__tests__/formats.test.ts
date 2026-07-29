import { describe, expect, it } from "vitest"

import {
  NARRATABLE_FORMATS,
  isNarratableFormat,
  narratableFormatList,
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

describe("narratableFormatList", () => {
  it("reads as a sentence fragment", () => {
    const list = narratableFormatList()
    expect(list).toBe("EPUB")
    expect(list).not.toContain('"')
    expect(list).not.toContain("[")
  })
})
