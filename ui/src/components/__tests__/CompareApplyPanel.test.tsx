// ui/src/components/__tests__/CompareApplyPanel.test.tsx
import { describe, expect, it } from "vitest"

import {
  buildApplyBody,
  buildDiffRows,
} from "../metadata/CompareApplyPanel"
import type { BookDetail } from "@/api/books"
import type { EnrichMatch } from "@/api/enrich"

const baseBook: BookDetail = {
  id: "b1",
  libraryId: "lib",
  title: "Old Title",
  author: "Old Author",
  format: "EPUB",
  year: 1999,
  progress: 0,
  rating: 0,
  palette: "default",
  description: "old desc",
  isbn: "",
  publisher: "Old Pub",
  series: "",
  genres: [],
  moods: [],
  tags: [],
  hasCover: false,
  addedAt: "2025-01-01",
  shelves: [],
  locks: { description: true },
}

const baseMatch: EnrichMatch = {
  source: "google_books",
  sourceId: "g1",
  title: "New Title",
  authors: ["New Author"],
  description: "new desc",
  publisher: "New Pub",
  year: 2024,
  isbn: "9780132350884",
  series: "",
  categories: ["Fiction"],
  language: "en",
  coverUrl: "https://example.com/cover.jpg",
  confidence: 92,
}

describe("buildDiffRows", () => {
  it("returns one row per comparable field with current/new values", () => {
    const rows = buildDiffRows(baseBook, baseMatch)
    const titleRow = rows.find((r) => r.field === "title")!
    expect(titleRow.current).toBe("Old Title")
    expect(titleRow.next).toBe("New Title")
  })

  it("pre-checks rows where current is empty and value differs", () => {
    const rows = buildDiffRows(baseBook, baseMatch)
    const isbn = rows.find((r) => r.field === "isbn")!
    expect(isbn.current).toBe("")
    expect(isbn.next).toBe("9780132350884")
    expect(isbn.checked).toBe(true)
    expect(isbn.disabled).toBe(false)
  })

  it("pre-checks rows where unlocked AND values differ", () => {
    const rows = buildDiffRows(baseBook, baseMatch)
    const title = rows.find((r) => r.field === "title")!
    expect(title.checked).toBe(true)
  })

  it("disables and unchecks locked rows", () => {
    const rows = buildDiffRows(baseBook, baseMatch)
    const desc = rows.find((r) => r.field === "description")!
    expect(desc.disabled).toBe(true)
    expect(desc.checked).toBe(false)
  })

  it("does not pre-check rows where values are identical", () => {
    const rows = buildDiffRows(
      { ...baseBook, title: "New Title" },
      baseMatch
    )
    const title = rows.find((r) => r.field === "title")!
    expect(title.checked).toBe(false)
  })

  it("includes a cover row when match has a coverUrl", () => {
    const rows = buildDiffRows(baseBook, baseMatch)
    const cover = rows.find((r) => r.field === "cover")
    expect(cover).toBeDefined()
    expect(cover!.next).toBe("https://example.com/cover.jpg")
  })
})

describe("buildApplyBody", () => {
  it("includes only checked fields, leaves others undefined", () => {
    const rows = [
      { field: "title", current: "a", next: "b", checked: true, disabled: false },
      { field: "description", current: "old", next: "new", checked: false, disabled: false },
      { field: "cover", current: "", next: "https://x", checked: true, disabled: false },
    ]
    const body = buildApplyBody(baseMatch, rows)
    expect(body.title).toBe("New Title")
    expect(body.description).toBeUndefined()
    expect(body.applyCover).toBe(true)
    expect(body.coverUrl).toBe("https://example.com/cover.jpg")
  })

  it("includes provenance fields verbatim regardless of checks", () => {
    const rows = [
      { field: "title", current: "a", next: "b", checked: false, disabled: false },
    ]
    const body = buildApplyBody(baseMatch, rows)
    expect(body.source).toBe("google_books")
    expect(body.sourceId).toBe("g1")
  })

  it("sets applyCover false when cover row is unchecked", () => {
    const rows = [
      { field: "cover", current: "", next: "https://x", checked: false, disabled: false },
    ]
    const body = buildApplyBody(baseMatch, rows)
    expect(body.applyCover).toBe(false)
  })
})
