// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import type { BookDetail } from "@/api/books"
import { ShelfMembership } from "@/components/book/ShelfMembership"

const shelves = [
  { id: "1", name: "Mine", slug: "mine", accent: "", icon: "library", bookCount: 2, isSmart: false, isPublic: false, createdAt: "2026-01-01T00:00:00Z" },
  { id: "2", name: "Mine Shared", slug: "mine-shared", accent: "", icon: "library", bookCount: 5, isSmart: false, isPublic: true, createdAt: "2026-01-01T00:00:00Z" },
  { id: "3", name: "Top Picks", slug: "top-picks", accent: "", icon: "library", bookCount: 9, isSmart: false, isPublic: true, ownerName: "admin", createdAt: "2026-01-01T00:00:00Z" },
]

beforeEach(() => {
  // cmdk measures and scrolls the selected item; jsdom has neither.
  Element.prototype.scrollIntoView = () => {}
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  )
  vi.stubGlobal("fetch", (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.startsWith("/api/v1/shelves")) {
      return Promise.resolve({
        ok: true,
        status: 200,
        text: () =>
          Promise.resolve(JSON.stringify({ shelves, unshelvedCount: 0 })),
      })
    }
    return Promise.reject(new Error(`unexpected fetch: ${url}`))
  })
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

// The picker offers only what the viewer curates (ADR-0017, #352):
// their own shelves, published or not — never a shared shelf someone
// else owns. Pinned at the component, not only on shelfGroups: this is
// the render that used to carry its own copy of the rule.
it("never offers someone else's shared shelf in the picker", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const book = { id: "b1", shelves: [] } as unknown as BookDetail
  render(
    <QueryClientProvider client={client}>
      <ShelfMembership book={book} />
    </QueryClientProvider>
  )

  await waitFor(() => expect(screen.getByLabelText("Add to shelf")).toBeTruthy())
  fireEvent.click(screen.getByLabelText("Add to shelf"))

  await waitFor(() => expect(screen.getByText("Mine")).toBeTruthy())
  expect(screen.getByText("Mine Shared")).toBeTruthy()
  expect(screen.queryByText("Top Picks")).toBeNull()
})
