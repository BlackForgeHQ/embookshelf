import { describe, expect, it } from "vitest"

import type { BookDropState } from "@/api/bookdrop"
import {
  BOOKDROP_STATE_LABEL,
  acceptsCoverUpload,
  isActiveState,
  isApprovableState,
  isTerminalState,
} from "@/lib/bookdropState"

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

  it("partitions the queue with isActiveState", () => {
    for (const state of ALL) {
      expect(isActiveState(state)).toBe(!isTerminalState(state))
    }
  })
})

describe("isApprovableState", () => {
  // Failed rows are approvable on purpose: a failed extraction means
  // poor metadata, not an unusable file, and the user can fill the gaps
  // in by hand rather than lose the book.
  it("accepts ready and failed rows", () => {
    expect(ALL.filter(isApprovableState)).toEqual(["ready", "failed"])
  })

  it("refuses rows that are already done with", () => {
    expect(isApprovableState("imported")).toBe(false)
    expect(isApprovableState("rejected")).toBe(false)
  })
})

describe("acceptsCoverUpload", () => {
  // The server's BookDropPutCover gate is the same three states. This
  // is the client half of that pair.
  it("is the three pre-approval states", () => {
    expect(ALL.filter(acceptsCoverUpload)).toEqual([
      "discovered",
      "processing",
      "ready",
    ])
  })

  // Approvable and cover-uploadable are different questions that happen
  // to overlap. A failed row can still be approved; it has no page to
  // render a cover from.
  it("is not the same set as approvable", () => {
    expect(isApprovableState("failed")).toBe(true)
    expect(acceptsCoverUpload("failed")).toBe(false)
  })
})
