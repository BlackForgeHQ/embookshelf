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

beforeEach(() => {
  vi.stubGlobal("fetch", (input: RequestInfo | URL) => {
    const url = String(input)
    if (url === "/api/v1/settings/instance") {
      return Promise.resolve(reply(200, instance))
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
})
