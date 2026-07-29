import { describe, expect, it } from "vitest"

import { decodeLocator, encodeLocator } from "@/lib/locator"

// The round trip #200 is about, at the layer that decides it: a narrated
// book's two shells write two Locator kinds, and each has to read back
// the one it wrote. Before the split they shared one field, so the audio
// shell decoded a CFI, fell back to zero, and then overwrote the reading
// position with a timestamp — losing the place in both directions.
describe("resume positions per Rendition", () => {
  // Stands in for the two API fields, which is all the shells see.
  type Stored = { resumeCfi?: string; resumeAudio?: string }

  // What the server does with an incoming locator: route it by its own
  // kind (internal/repo/progress.go).
  function store(prev: Stored, locator: string): Stored {
    if (locator === "") return prev
    return locator.startsWith("time:")
      ? { ...prev, resumeAudio: locator }
      : { ...prev, resumeCfi: locator }
  }

  it("survives Read → Listen → Read", () => {
    let stored: Stored = {}

    stored = store(stored, encodeLocator({ kind: "cfi", cfi: "epubcfi(/6/14!/4/2)" }))
    stored = store(stored, encodeLocator({ kind: "time", seconds: 60 }))

    const text = decodeLocator(stored.resumeCfi)
    expect(text?.kind).toBe("cfi")
    if (text?.kind !== "cfi") throw new Error("unreachable")
    expect(text.cfi).toBe("epubcfi(/6/14!/4/2)")
  })

  it("survives Listen → Read → Listen", () => {
    let stored: Stored = {}

    stored = store(stored, encodeLocator({ kind: "time", seconds: 90.5 }))
    stored = store(stored, encodeLocator({ kind: "cfi", cfi: "epubcfi(/6/2)" }))

    const audio = decodeLocator(stored.resumeAudio)
    expect(audio?.kind).toBe("time")
    if (audio?.kind !== "time") throw new Error("unreachable")
    expect(audio.seconds).toBeCloseTo(90.5)
  })

  // A page locator is a text position: the PDF and comic shells write
  // it, and neither of them is the narration.
  it("files a page locator with the text position", () => {
    const stored = store({}, encodeLocator({ kind: "page", page: 12 }))

    expect(stored.resumeAudio).toBeUndefined()
    expect(decodeLocator(stored.resumeCfi)?.kind).toBe("page")
  })

  // A percent-only update carries no locator and must not wipe either.
  it("leaves both positions alone when there is no locator", () => {
    let stored: Stored = {}
    stored = store(stored, encodeLocator({ kind: "cfi", cfi: "epubcfi(/6/2)" }))
    stored = store(stored, encodeLocator({ kind: "time", seconds: 30 }))

    const after = store(stored, "")

    expect(after.resumeCfi).toBe(stored.resumeCfi)
    expect(after.resumeAudio).toBe(stored.resumeAudio)
  })
})
