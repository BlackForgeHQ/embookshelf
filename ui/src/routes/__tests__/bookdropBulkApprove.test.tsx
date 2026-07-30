// @vitest-environment jsdom
import type { ComponentType, ReactNode } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import type { BookDropItem } from "@/api/bookdrop"
import { Route } from "@/routes/_app.bookdrop"

// The route is exercised as a component: the sweep, the button that
// fires it and the report it raises are all in the route, and the point
// of the test is what a reviewer is told after clicking.
const navigate = vi.fn()
vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>()
  return {
    ...actual,
    // The route calls this at module scope. Handing the options object
    // straight back is what makes `Route.component` the component.
    createFileRoute: (() => (opts: unknown) =>
      opts) as unknown as typeof actual.createFileRoute,
    useNavigate: () => navigate,
  }
})

const toastError = vi.fn()
const toastSuccess = vi.fn()
vi.mock("sonner", () => ({
  toast: {
    error: (msg: string) => toastError(msg),
    success: (msg: string) => toastSuccess(msg),
  },
}))

// TopBar reads the sidebar context, which this test has no business
// standing up. The bulk-approve button is the route's own node, passed
// in as `right`, so it stays real.
vi.mock("@/components/TopBar", () => ({
  TopBar: ({ right }: { right?: ReactNode }) => <div>{right}</div>,
}))

// pdfjs at module scope, for a cover this test never renders.
vi.mock("@/lib/pdfCover", () => ({
  renderPdfPageOneJpeg: () => Promise.resolve(null),
}))

const BookDrop = (Route as unknown as { component: ComponentType }).component

function row(id: string, title: string): BookDropItem {
  return {
    id,
    filename: `${id}.epub`,
    path: `/bookdrop/${id}.epub`,
    fileSize: 1024,
    format: "EPUB",
    state: "ready",
    progress: 1,
    title,
    hasCover: false,
    discoveredAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
  }
}

const queue = [row("a", "Dune"), row("b", "Neuromancer"), row("c", "Solaris")]

/** Which ids the approve endpoint refuses, and with what. */
// The waits below can run to 10s on a cold runner, and vitest's default
// budget for a test is 5s — which would cut one short and report a
// timeout instead of the element error that says what was missing.
vi.setConfig({ testTimeout: 20_000 })

let refuse: Record<string, string> = {}

function reply(status: number, body: unknown) {
  return {
    ok: status < 400,
    status,
    statusText: String(status),
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body)),
  }
}

beforeEach(() => {
  refuse = {}
  navigate.mockReset()
  toastError.mockReset()
  toastSuccess.mockReset()
  vi.stubGlobal("fetch", (input: RequestInfo | URL) => {
    const url = String(input)
    if (url === "/api/v1/bookdrop") {
      return Promise.resolve(reply(200, { items: queue }))
    }
    if (url === "/api/v1/libraries") {
      return Promise.resolve(reply(200, { libraries: [] }))
    }
    const approve = /^\/api\/v1\/bookdrop\/(.+)\/approve$/.exec(url)
    if (approve) {
      const id = approve[1]!
      if (refuse[id]) return Promise.resolve(reply(500, { error: refuse[id] }))
      return Promise.resolve(reply(200, { book: { id: `book-${id}` } }))
    }
    return Promise.reject(new Error(`unexpected fetch: ${url}`))
  })
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

async function renderQueue() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  // Awaited: the first render in the file resolves the route's module
  // graph and suspends while it does.
  await act(async () => {
    render(
      <QueryClientProvider client={client}>
        <BookDrop />
      </QueryClientProvider>
    )
  })
  // The sweep button counts the reviewable rows, so its label is the
  // signal that the queue has loaded.
  //
  // Longer than the 1s default because the first render in this file
  // pays for the route's whole module graph, and a cold CI runner does
  // not finish that inside the default — which is how this went red on
  // main while the other three tests here, which reuse the warm graph,
  // stayed green. The bound is a patience limit, not an assertion: a
  // button that never arrives still fails, just later.
  return await screen.findByRole(
    "button",
    { name: /Approve 3/ },
    { timeout: 10_000 }
  )
}

describe("bulk approve", () => {
  it("says how many rows it could not approve, and which", async () => {
    refuse = { b: "storage backend unreachable" }
    const button = await renderQueue()

    fireEvent.click(button)

    // The queue is on its way out on a full success, so the sweep's one
    // report has to outlive it. Nothing on this page can carry it.
    await waitFor(() => expect(toastError).toHaveBeenCalledTimes(1))
    const said = toastError.mock.calls[0]![0] as string
    expect(said).toContain("1 of 3")
    expect(said).toContain("Neuromancer")
  })

  it("stays in the queue when a row failed", async () => {
    refuse = { b: "storage backend unreachable" }
    const button = await renderQueue()

    fireEvent.click(button)
    await waitFor(() => expect(toastError).toHaveBeenCalled())

    // Leaving for the book that did import would take the reviewer away
    // from the rows still waiting on them.
    expect(navigate).not.toHaveBeenCalled()
  })

  it("navigates and says nothing when every row succeeds", async () => {
    const button = await renderQueue()

    fireEvent.click(button)

    await waitFor(() => expect(navigate).toHaveBeenCalled())
    expect(navigate).toHaveBeenCalledWith({
      to: "/book/$id",
      params: { id: "book-c" },
    })
    expect(toastError).not.toHaveBeenCalled()
    expect(toastSuccess).not.toHaveBeenCalled()
  })

  it("leaves a single-row approve reporting inline", async () => {
    refuse = { a: "storage backend unreachable" }
    await renderQueue()

    fireEvent.click(
      await screen.findByRole("button", { name: /Approve import/ })
    )

    // The detail pane the reviewer is standing in renders it, and stays
    // mounted to do so — no toast, no navigation.
    expect(await screen.findByText("storage backend unreachable")).toBeTruthy()
    expect(toastError).not.toHaveBeenCalled()
    expect(navigate).not.toHaveBeenCalled()
  })
})
