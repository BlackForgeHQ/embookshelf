// @vitest-environment jsdom
import type { ReactElement } from "react"
import { render, screen, waitFor, fireEvent, cleanup } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest"

// Polyfill ResizeObserver (used by cmdk/Radix)
global.ResizeObserver = class ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

// Polyfill scrollIntoView (used by cmdk)
Element.prototype.scrollIntoView = vi.fn()

import { LibrarySearchCombobox } from "../LibrarySearchCombobox"

const navigateMock = vi.fn()
vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigateMock,
}))

const searchSuggestMock = vi.fn()
vi.mock("@/api/search", () => ({
  searchQueryKey: (q: string, limit: number) => ["search", q, limit] as const,
  searchSuggest: (q: string, limit: number) => searchSuggestMock(q, limit),
}))

function renderWithClient(ui: ReactElement) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>{ui}</QueryClientProvider>
  )
}

describe("LibrarySearchCombobox", () => {
  afterEach(() => {
    cleanup()
  })

  beforeEach(() => {
    navigateMock.mockReset()
    searchSuggestMock.mockReset()
  })

  it("calls onSearchChange synchronously while typing (grid filter)", () => {
    const setSearch = vi.fn()
    renderWithClient(
      <LibrarySearchCombobox value="" onSearchChange={setSearch} />
    )
    const input = screen.getByPlaceholderText(/search library/i)
    fireEvent.change(input, { target: { value: "dune" } })
    expect(setSearch).toHaveBeenCalledWith("dune")
  })

  it("renders a book suggestion and navigates on select", async () => {
    searchSuggestMock.mockResolvedValueOnce({
      books: [
        { id: "b1", title: "Dune", author: "Frank Herbert", cover: "", hasCover: false },
      ],
      shelves: [],
      libraries: [],
    })

    renderWithClient(
      <LibrarySearchCombobox value="dune" onSearchChange={() => {}} />
    )
    const input = screen.getByPlaceholderText(/search library/i)
    fireEvent.focus(input)

    await waitFor(() => {
      expect(screen.getByText("Dune")).toBeTruthy()
    })

    fireEvent.click(screen.getByText("Dune"))
    expect(navigateMock).toHaveBeenCalledWith({
      to: "/book/$id",
      params: { id: "b1" },
    })
  })

  it("renders 'No matches' when the query returns empty", async () => {
    searchSuggestMock.mockResolvedValueOnce({
      books: [],
      shelves: [],
      libraries: [],
    })
    renderWithClient(
      <LibrarySearchCombobox value="zzzzzz" onSearchChange={() => {}} />
    )
    fireEvent.focus(screen.getByPlaceholderText(/search library/i))
    await waitFor(() => {
      expect(screen.getByText(/no matches/i)).toBeTruthy()
    })
  })
})
