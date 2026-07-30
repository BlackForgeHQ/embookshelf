import { describe, expect, it } from "vitest"

import type { BookDropItem, BookDropState, BookDropView } from "@/api/bookdrop"
import { bookdropView } from "@/api/bookdrop"

function item(over: Partial<BookDropItem> = {}): BookDropItem {
  return {
    id: "i1",
    filename: "book.epub",
    path: "/bookdrop/book.epub",
    fileSize: 1024,
    format: "EPUB",
    state: "ready",
    progress: 0,
    hasCover: false,
    discoveredAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    ...over,
  }
}

/**
 * One row per state, one column per fact the interface asks about.
 *
 * `showError` is missing on purpose: it is the only fact that also reads
 * the row's message, so it gets its own case below. Everything else here
 * is a function of the state alone, which is what makes a new state one
 * row in this table and one row in the module — and the `Record` makes
 * forgetting either half a type error rather than a surprise at runtime.
 */
const TABLE: Record<BookDropState, Omit<BookDropView, "showError">> = {
  discovered: {
    phase: "waiting",
    label: "queued",
    eyebrow: "Discovered",
    canApprove: false,
    canUploadCover: true,
    showProgress: false,
    imported: false,
  },
  processing: {
    phase: "extracting",
    label: "processing",
    eyebrow: "Extracting metadata",
    canApprove: false,
    canUploadCover: true,
    showProgress: true,
    imported: false,
  },
  ready: {
    phase: "reviewable",
    label: "ready",
    eyebrow: "Review import",
    canApprove: true,
    canUploadCover: true,
    showProgress: false,
    imported: false,
  },
  failed: {
    phase: "attention",
    label: "failed",
    eyebrow: "Needs attention",
    canApprove: true,
    canUploadCover: false,
    showProgress: false,
    imported: false,
  },
  imported: {
    phase: "done",
    label: "imported",
    eyebrow: "In your library",
    canApprove: false,
    canUploadCover: false,
    showProgress: false,
    imported: true,
  },
  rejected: {
    phase: "done",
    label: "discarded",
    eyebrow: "Discarded",
    canApprove: false,
    canUploadCover: false,
    showProgress: false,
    imported: false,
  },
}

describe("bookdropView, state by state", () => {
  for (const [state, want] of Object.entries(TABLE) as Array<
    [BookDropState, Omit<BookDropView, "showError">]
  >) {
    it(`describes a ${state} row`, () => {
      const { showError: _showError, ...got } = bookdropView(item({ state }))
      expect(got).toEqual(want)
    })
  }
})

describe("bookdropView approval", () => {
  // Approve is offered on a failure on purpose: extraction failing means
  // the metadata is poor, not that the file is unusable, and the user can
  // fill the gaps in by hand rather than lose the book. This pair is the
  // client half of the gate in BookDropService.Approve, which accepts
  // exactly ready and failed and refuses the rest.
  it("is ready and failed, and nothing else", () => {
    const states = Object.keys(TABLE) as Array<BookDropState>
    expect(states.filter((s) => bookdropView(item({ state: s })).canApprove)).toEqual([
      "ready",
      "failed",
    ])
  })

  // Only the reviewable phase, not everything approvable. Bulk approve
  // sweeps rows the user has not opened; taking a failed row along would
  // import a book whose error message nobody read.
  it("keeps the sweepable phase narrower than the approvable pair", () => {
    expect(bookdropView(item({ state: "failed" })).phase).not.toBe("reviewable")
  })
})

describe("bookdropView cover upload", () => {
  // The client half of the state gate in BookDropPutCover
  // (internal/handler/bookdrop.go), which accepts exactly these three and
  // answers 409 for the rest. Failed rows are excluded even though they
  // are approvable: the upload exists to supply a cover the extractor
  // could not find, and a failed extraction has no page to render one
  // from.
  it("is the three pre-approval states, and nothing else", () => {
    const states = Object.keys(TABLE) as Array<BookDropState>
    expect(
      states.filter((s) => bookdropView(item({ state: s })).canUploadCover)
    ).toEqual(["discovered", "processing", "ready"])
  })
})

describe("bookdropView error line", () => {
  // A failure with nothing to say would render an empty callout headed
  // "Processing error."
  it("shows a failure only when the failure came with a message", () => {
    expect(
      bookdropView(item({ state: "failed", errorMsg: "no spine" })).showError
    ).toBe(true)
    expect(bookdropView(item({ state: "failed" })).showError).toBe(false)
  })

  // A message left over from an earlier attempt is not news once the row
  // has moved on.
  it("stays quiet on a row that is not in trouble", () => {
    expect(
      bookdropView(item({ state: "ready", errorMsg: "no spine" })).showError
    ).toBe(false)
  })
})

describe("bookdropView phases", () => {
  // The queue's active list is every row not yet done with, and the
  // settings panel's processed list is the complement. Both are this one
  // phase, so neither can drift from the other.
  it("collapses imported and discarded into one done phase", () => {
    expect(bookdropView(item({ state: "imported" })).phase).toBe("done")
    expect(bookdropView(item({ state: "rejected" })).phase).toBe("done")
  })

  // Which of the two a done row was still matters in one place: the
  // processed list links to the book and colours the word differently.
  it("keeps imported distinguishable from discarded", () => {
    expect(bookdropView(item({ state: "imported" })).imported).toBe(true)
    expect(bookdropView(item({ state: "rejected" })).imported).toBe(false)
  })
})
