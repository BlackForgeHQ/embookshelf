import { describe, expect, it } from "vitest"

import type { BookDropState } from "@/api/bookdrop"
import { bookdropView, isTerminalState } from "@/api/bookdrop"

const ALL: Array<BookDropState> = [
  "discovered",
  "processing",
  "ready",
  "failed",
  "imported",
  "rejected",
]

describe("the label a state carries", () => {
  it("labels every state", () => {
    for (const state of ALL) {
      expect(bookdropView({ state } as never).label).toBeTruthy()
    }
  })

  // The shown word and the wire word differ where the wire word
  // describes what the scanner did rather than what the user is waiting
  // for.
  it("shows queue words, not scanner words", () => {
    expect(bookdropView({ state: "discovered" } as never).label).toBe("queued")
    expect(bookdropView({ state: "rejected" } as never).label).toBe("discarded")
  })
})

describe("isTerminalState", () => {
  it("is imported and discarded, and nothing else", () => {
    expect(ALL.filter(isTerminalState)).toEqual(["imported", "rejected"])
  })

  // The queue route's active list is the complement, spelled inline
  // there — the predicate that wrapped this negation was a function for
  // a `!` (#213).
})
