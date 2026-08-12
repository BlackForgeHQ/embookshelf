import { describe, expect, it, vi } from "vitest"

import {
  formatBytes,
  formatCost,
  formatDate,
  formatDateTime,
  formatDuration,
  relativeTime,
} from "@/lib/format"

// These three were spelled out at their call sites — bytes twice,
// verbatim except for the one line that mattered, and duration and cost
// once each inside the audiobook panel. Nothing measured them, so
// nothing noticed that the two byte copies answered zero differently.

describe("formatBytes", () => {
  const cases: Array<[number, string]> = [
    // Zero is a countable fact here, not an absent one: the BookDrop
    // settings panel says "0 files (0 B)" and a wipe that freed nothing
    // reports what it freed. An em dash in either sentence claims the
    // size is unknown when it is known to be nothing.
    [0, "0 B"],
    // Negative and non-finite are not sizes at all. They reach this
    // from a count subtracted past zero and from a `bytes` field the
    // API left out; "-1.0 B" and "NaN B" both render.
    [-1, "0 B"],
    [Number.NaN, "0 B"],
    [Number.POSITIVE_INFINITY, "0 B"],
    // One decimal below ten, none above — a 4.2 MB book is worth the
    // precision, a 431 KB one is not.
    [1, "1.0 B"],
    [9.9, "9.9 B"],
    [10, "10 B"],
    // The unit only steps at a full 1024, so the largest byte count is
    // four digits wide.
    [1023, "1023 B"],
    [1024, "1.0 KB"],
    [1536, "1.5 KB"],
    [1024 * 1024, "1.0 MB"],
    [1024 * 1024 * 1024, "1.0 GB"],
    // GB is the last unit. A library bigger than that keeps counting in
    // GB rather than inventing a TB nobody has asked for.
    [1024 * 1024 * 1024 * 1024, "1024 GB"],
  ]

  for (const [input, want] of cases) {
    it(`renders ${input} as ${want}`, () => {
      expect(formatBytes(input)).toBe(want)
    })
  }
})

describe("formatDuration", () => {
  const cases: Array<[number, string]> = [
    // Unlike bytes, there is no honest zero-length narration: a ready
    // MP3 always has a duration, so zero means the field never got
    // written. That is the em dash's actual job.
    [0, "—"],
    [-1, "—"],
    [Number.NaN, "—"],
    [Number.POSITIVE_INFINITY, "—"],
    [29, "0m"],
    [30, "1m"],
    [60, "1m"],
    [90, "2m"],
    // Rounding happens once, on the total, so it cannot round the
    // minutes up past an hour and leave the hours behind. Spelled as
    // `${h}h ${round(seconds % 3600 / 60)}m` this band read "60m".
    [3570, "1h 0m"],
    [3599, "1h 0m"],
    [3600, "1h 0m"],
    [3660, "1h 1m"],
    [7 * 3600 + 42 * 60, "7h 42m"],
  ]

  for (const [input, want] of cases) {
    it(`renders ${input}s as ${want}`, () => {
      expect(formatDuration(input)).toBe(want)
    })
  }
})

describe("formatCost", () => {
  const cases: Array<[number, string]> = [
    // Money is shown to two decimals until it rounds to nothing,
    // because "$0.00" for a run that costs three cents reads as free.
    [0, "free"],
    [0.001, "<$0.01"],
    [0.009, "<$0.01"],
    [0.01, "$0.01"],
    [0.1, "$0.10"],
    [1.005, "$1.00"],
    [12.3456, "$12.35"],
    // A price we could not compute is not a free one, and "$NaN" is
    // what the panel rendered when the estimate came back without a
    // cost.
    [Number.NaN, "—"],
    [Number.POSITIVE_INFINITY, "—"],
    [-1, "—"],
  ]

  for (const [input, want] of cases) {
    it(`renders ${input} as ${want}`, () => {
      expect(formatCost(input)).toBe(want)
    })
  }
})

describe("formatDate", () => {
  it("pins the locale so the same ISO string renders the same everywhere", () => {
    expect(formatDate("2026-08-09T10:30:00Z")).toBe("Aug 9, 2026")
    expect(formatDate(Date.parse("2026-01-02T00:00:00"))).toBe("Jan 2, 2026")
  })

  it("renders an unparsable date as an em dash, not 'Invalid Date'", () => {
    expect(formatDate("not-a-date")).toBe("—")
  })
})

describe("formatDateTime", () => {
  it("is formatDate plus the time", () => {
    // Constructed from local parts so the expectation holds in any zone.
    const d = new Date(2026, 7, 9, 14, 5)
    expect(formatDateTime(d)).toBe("Aug 9, 2026, 2:05 PM")
  })

  it("renders an unparsable date as an em dash", () => {
    expect(formatDateTime("nope")).toBe("—")
  })
})

describe("relativeTime", () => {
  const now = Date.parse("2026-07-31T12:00:00Z")

  it("reports the largest whole unit", () => {
    vi.setSystemTime(now)
    expect(relativeTime(now - 5_000)).toBe("5s ago")
    expect(relativeTime(now - 90_000)).toBe("1m ago")
    expect(relativeTime(now - 3 * 3_600_000)).toBe("3h ago")
    expect(relativeTime(now - 2 * 86_400_000)).toBe("2d ago")
    vi.useRealTimers()
  })

  it("has an em dash for a missing timestamp and a phrase for a future one", () => {
    vi.setSystemTime(now)
    expect(relativeTime(0)).toBe("—")
    expect(relativeTime(now + 60_000)).toBe("in the future")
    vi.useRealTimers()
  })
})
