// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { ShelfDraftProvider, useShelfDraftDialog } from "../ShelfDraftProvider"

// Radix/cmdk internals the jsdom build does not supply.
global.ResizeObserver = class ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}
Element.prototype.scrollIntoView = vi.fn()

const toastError = vi.fn()
vi.mock("sonner", () => ({
  toast: {
    error: (msg: string) => toastError(msg),
    success: vi.fn(),
  },
}))

const REFUSAL = "A shelf with that name already exists."

const createShelfFn = vi.fn()
vi.mock("@/api/books", () => ({
  shelvesQuery: {
    key: ["shelves"],
    fn: () => Promise.resolve({ shelves: [], unshelvedCount: 0 }),
  },
  createShelf: {
    fn: (args: unknown) => createShelfFn(args),
    invalidates: [],
  },
}))

function Opener() {
  const { open } = useShelfDraftDialog()
  return (
    <button type="button" onClick={open}>
      New shelf
    </button>
  )
}

function renderProvider() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <ShelfDraftProvider>
        <Opener />
      </ShelfDraftProvider>
    </QueryClientProvider>
  )
}

beforeEach(() => {
  toastError.mockClear()
  createShelfFn.mockReset()
})

afterEach(() => {
  cleanup()
})

// The dialog is a form the reader is standing in, and it renders the
// create mutation's message under the fields. A toast on top of that is
// the same sentence twice — once in place, once floating away.
describe("ShelfDraftProvider", () => {
  it("reports a refused create once, inside the dialog", async () => {
    createShelfFn.mockRejectedValue({ status: 409, message: REFUSAL })
    renderProvider()

    fireEvent.click(screen.getByText("New shelf"))
    fireEvent.change(await screen.findByLabelText("Name"), {
      target: { value: "To finish" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Create shelf" }))

    await waitFor(() => {
      expect(screen.getAllByText(REFUSAL)).toHaveLength(1)
    })
    expect(toastError).not.toHaveBeenCalled()
  })
})
