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

const fetchMeMock = vi.fn()
vi.mock("@/api/auth", () => ({
  fetchMe: () => fetchMeMock(),
  meQueryKey: ["me"] as const,
  logout: vi.fn(),
}))

const shelfDraftOpen = vi.fn()
vi.mock("@/components/ShelfDraftProvider", () => ({
  useShelfDraftDialog: () => ({ open: shelfDraftOpen }),
}))

const userSettingsOpen = vi.fn()
vi.mock("@/components/UserSettingsDialog", () => ({
  useUserSettingsDialog: () => ({ open: userSettingsOpen }),
}))

const toggleSidebar = vi.fn()
vi.mock("@/components/ui/sidebar", () => ({
  useSidebar: () => ({ toggleSidebar }),
}))

const logoutMutate = vi.fn()
vi.mock("@/hooks/useLogout", () => ({
  useLogout: () => ({ mutate: logoutMutate }),
}))

function renderPalette(open = true, role: "admin" | "user" = "user") {
  fetchMeMock.mockResolvedValue({
    id: "u1",
    email: "u@local",
    name: "U",
    role,
    display: "U",
    initials: "U",
    createdAt: "",
  })
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
    fetchMeMock.mockReset()
    shelfDraftOpen.mockReset()
    userSettingsOpen.mockReset()
    toggleSidebar.mockReset()
    logoutMutate.mockReset()
  })

  it("renders quick actions and navigation with empty input", async () => {
    renderPalette()
    expect(await screen.findByText("Open Bookdrop intake")).toBeTruthy()
    expect(screen.getByText("New shelf")).toBeTruthy()
    expect(screen.getByText("Library")).toBeTruthy()
    expect(screen.getByText("Settings")).toBeTruthy()
  })

  it("hides Library scan for non-admin users", async () => {
    renderPalette(true, "user")
    await screen.findByText("Open Bookdrop intake")
    expect(screen.queryByText(/library scan/i)).toBeNull()
  })

  it("shows Library scan for admin users", async () => {
    renderPalette(true, "admin")
    expect(await screen.findByText(/library scan/i)).toBeTruthy()
  })

  it("invokes the New shelf action", async () => {
    renderPalette()
    fireEvent.click(await screen.findByText("New shelf"))
    expect(shelfDraftOpen).toHaveBeenCalled()
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
