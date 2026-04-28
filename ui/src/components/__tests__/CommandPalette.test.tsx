// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { CommandPalette } from "../CommandPalette"

// Polyfill ResizeObserver (used by cmdk/Radix)
global.ResizeObserver = class ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

// Polyfill scrollIntoView (used by cmdk)
Element.prototype.scrollIntoView = vi.fn()

const navigateMock = vi.fn()
vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigateMock,
}))

const searchSuggestMock = vi.fn()
vi.mock("@/api/search", () => ({
  searchQueryKey: (q: string, limit: number) => ["search", q, limit] as const,
  searchSuggest: (q: string, limit: number) => searchSuggestMock(q, limit),
}))

function renderPalette(open = true) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <CommandPalette open={open} onOpenChange={() => {}} />
    </QueryClientProvider>
  )
}

describe("CommandPalette", () => {
  afterEach(() => {
    cleanup()
  })

  beforeEach(() => {
    navigateMock.mockReset()
    searchSuggestMock.mockReset()
  })

  it("renders navigation with empty input", async () => {
    renderPalette()
    expect(await screen.findByText("Library")).toBeTruthy()
    expect(screen.getByText("Bookdrop")).toBeTruthy()
    expect(screen.getByText("Notebook")).toBeTruthy()
    expect(screen.getByText("Stats")).toBeTruthy()
    expect(screen.getByText("Settings")).toBeTruthy()
  })

  it("renders book results after typing", async () => {
    searchSuggestMock.mockResolvedValueOnce({
      books: [
        { id: "b1", title: "Dune", author: "Herbert", cover: "", hasCover: false },
      ],
      shelves: [],
      libraries: [],
    })
    renderPalette()
    const input = await screen.findByPlaceholderText(
      /search books, shelves/i
    )
    fireEvent.change(input, { target: { value: "dune" } })
    await waitFor(() => {
      expect(screen.getByText("Dune")).toBeTruthy()
    })
  })
})
