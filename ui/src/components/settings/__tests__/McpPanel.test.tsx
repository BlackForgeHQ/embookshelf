// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import { McpPanel } from "@/components/settings/McpPanel"

function reply(status: number, body: unknown) {
  return {
    ok: status < 400,
    status,
    statusText: String(status),
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body)),
  }
}

let settings = { enabled: false }
let tokens: Array<Record<string, unknown>> = []
let calls: Array<{ url: string; method: string; body?: string }> = []

beforeEach(() => {
  settings = { enabled: false }
  tokens = [
    {
      id: "t1",
      name: "ci",
      prefix: "ebk_zzzz9999",
      createdAt: "2026-11-01T10:00:00Z",
      lastUsedAt: "2026-11-17T10:00:00Z",
      expiresAt: null,
      owner: { id: "u2", email: "bob@example.com", name: "Bob" },
    },
  ]
  calls = []
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  )
  vi.stubGlobal("confirm", () => true)
  vi.stubGlobal("fetch", (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    const method = init?.method ?? "GET"
    calls.push({ url, method, body: init?.body as string | undefined })
    if (url === "/api/v1/settings/mcp" && method === "GET") {
      return Promise.resolve(reply(200, settings))
    }
    if (url === "/api/v1/settings/mcp" && method === "PUT") {
      settings = JSON.parse(String(init?.body))
      return Promise.resolve(reply(200, settings))
    }
    if (url === "/api/v1/settings/mcp/tokens" && method === "GET") {
      return Promise.resolve(reply(200, { tokens }))
    }
    if (url === "/api/v1/settings/mcp/tokens/t1" && method === "DELETE") {
      tokens = []
      return Promise.resolve({ ...reply(204, null), json: () => Promise.reject() })
    }
    return Promise.reject(new Error(`unexpected fetch: ${method} ${url}`))
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
      <McpPanel />
    </QueryClientProvider>
  )
}

it("saves the switch and shows the endpoint once it is on", async () => {
  renderPanel()
  await waitFor(() => screen.getByText("Enable the MCP server"))
  expect(screen.queryByDisplayValue(`${window.location.origin}/mcp`)).toBeNull()

  fireEvent.click(screen.getByRole("switch"))
  fireEvent.click(screen.getByRole("button", { name: "Save" }))

  await waitFor(() => {
    expect(screen.getByDisplayValue(`${window.location.origin}/mcp`)).toBeTruthy()
  })
  const put = calls.find((c) => c.method === "PUT")
  expect(JSON.parse(put?.body ?? "{}")).toEqual({ enabled: true })
})

it("lists every user's tokens with their owner, and revokes", async () => {
  renderPanel()
  await waitFor(() => {
    expect(screen.getByText(/Bob/)).toBeTruthy()
  })
  expect(screen.getByText("ci")).toBeTruthy()

  fireEvent.click(screen.getByRole("button", { name: "Revoke" }))

  await waitFor(() => {
    expect(screen.getByText(/No one has made a token yet/)).toBeTruthy()
  })
  expect(
    calls.some((c) => c.method === "DELETE" && c.url === "/api/v1/settings/mcp/tokens/t1")
  ).toBe(true)
})
