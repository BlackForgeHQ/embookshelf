import { describe, expect, it } from "vitest"

import type { BookDropState } from "@/api/bookdrop"
import { BOOKDROP_STATE_LABEL, isTerminalState } from "@/api/bookdrop"

const ALL: Array<BookDropState> = [
  "discovered",
  "processing",
  "ready",
  "failed",
  "imported",
  "rejected",
]

describe("BOOKDROP_STATE_LABEL", () => {
  it("labels every state", () => {
    for (const state of ALL) {
      expect(BOOKDROP_STATE_LABEL[state]).toBeTruthy()
    }
  })

  // The shown word and the wire word differ where the wire word
  // describes what the scanner did rather than what the user is waiting
  // for.
  it("shows queue words, not scanner words", () => {
    expect(BOOKDROP_STATE_LABEL.discovered).toBe("queued")
    expect(BOOKDROP_STATE_LABEL.rejected).toBe("discarded")
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
