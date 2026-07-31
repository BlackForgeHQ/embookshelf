// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { InstancePanel } from "@/components/settings/InstancePanel"

const instance = {
  version: "1.4.2",
  commit: "a3f19c2",
  goVersion: "go1.23.4",
  startedAt: "2026-07-31T09:00:00Z",
  allowedOrigins: [],
  bookDropPath: "/data/bookdrop",
  dataPath: "/data",
  migrateOnStart: true,
  enrichmentProviders: [],
  counts: { users: 2, libraries: 1, books: 40 },
  queueAttached: false,
  database: { pingMs: 1.4, inUse: 3, idle: 5, maxConns: 25 },
  schema: { version: 38, dirty: false },
}

function reply(status: number, body: unknown) {
  return {
    ok: status < 400,
    status,
    statusText: String(status),
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body)),
  }
}

// Overridable per test — most tests want the happy instance payload, a
// couple need the instance endpoint to fail or answer with a different
// body without hand-rolling a whole new fetch stub.
let instanceReply: { status: number; body: unknown } = { status: 200, body: instance }

beforeEach(() => {
  instanceReply = { status: 200, body: instance }
  vi.stubGlobal("fetch", (input: RequestInfo | URL) => {
    const url = String(input)
    if (url === "/api/v1/settings/instance") {
      return Promise.resolve(reply(instanceReply.status, instanceReply.body))
    }
    if (url === "/api/v1/settings/libraries") {
      return Promise.resolve(reply(200, { libraries: [] }))
    }
    if (url === "/api/v1/bookdrop") {
      return Promise.resolve(reply(200, { items: [] }))
    }
    return Promise.reject(new Error(`unexpected fetch: ${url}`))
  })
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function renderPanel() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <InstancePanel />
    </QueryClientProvider>
  )
}

describe("InstancePanel", () => {
  it("renders every row, and warns about the detached queue", async () => {
    renderPanel()

    for (const key of ["database", "queue", "providers", "libraries", "bookdrop"]) {
      expect(await screen.findByTestId(`status-row-${key}`)).toBeTruthy()
    }

    const queue = await screen.findByTestId("status-row-queue")
    expect(queue.getAttribute("data-tone")).toBe("warn")
    expect(queue.textContent).toContain("Not attached")
    expect(queue.textContent).toContain("narration")
  })

  it("shows the build identity in the header", async () => {
    renderPanel()
    expect(await screen.findByText("a3f19c2")).toBeTruthy()
    expect(await screen.findByText("1.4.2")).toBeTruthy()
  })

  it("says why the header is blank when the instance endpoint fails, and still renders the status rows", async () => {
    instanceReply = {
      status: 500,
      body: { error: "database unreachable" },
    }
    renderPanel()

    // The same message also lands as evidence on the warn rows below, so
    // this locates the header block specifically by its lead-in text
    // rather than matching "database unreachable" wherever it appears.
    const header = await screen.findByText(
      /This instance's details could not be read/
    )
    expect(header.textContent).toContain("database unreachable")

    for (const key of ["database", "queue", "providers", "libraries", "bookdrop"]) {
      expect(await screen.findByTestId(`status-row-${key}`)).toBeTruthy()
    }
  })

  it("does not render a future startedAt as an uptime", async () => {
    instanceReply = {
      status: 200,
      body: { ...instance, startedAt: "2099-01-01T00:00:00Z" },
    }
    renderPanel()

    expect(await screen.findByText("1.4.2")).toBeTruthy()
    expect(await screen.findByText("unknown")).toBeTruthy()
    expect(screen.queryByText(/in the future/)).toBeNull()
  })
})
