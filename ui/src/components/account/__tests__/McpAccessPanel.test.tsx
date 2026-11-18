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

import { McpAccessPanel } from "@/components/account/McpAccessPanel"

function reply(status: number, body: unknown) {
  return {
    ok: status < 400,
    status,
    statusText: String(status),
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body)),
  }
}

const RAW = "ebk_abcd1234SECRETSECRETSECRETSECRETSECRET1"

let mcpEnabled = true
let tokens: Array<Record<string, unknown>> = []
let calls: Array<{ url: string; method: string }> = []

beforeEach(() => {
  mcpEnabled = true
  tokens = [
    {
      id: "t1",
      name: "Old laptop",
      prefix: "ebk_zzzz9999",
      createdAt: "2026-11-01T10:00:00Z",
      lastUsedAt: null,
      expiresAt: null,
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
    calls.push({ url, method })
    if (url === "/api/v1/config") {
      return Promise.resolve(
        reply(200, { s3Available: false, emailEnabled: false, mcpEnabled })
      )
    }
    if (url === "/api/v1/account/tokens" && method === "GET") {
      return Promise.resolve(reply(200, { tokens }))
    }
    if (url === "/api/v1/account/tokens" && method === "POST") {
      const meta = {
        id: "t2",
        name: "Claude Desktop",
        prefix: "ebk_abcd1234",
        createdAt: "2026-11-18T10:00:00Z",
        lastUsedAt: null,
        expiresAt: null,
      }
      tokens = [meta, ...tokens]
      return Promise.resolve(reply(201, { token: RAW, meta }))
    }
    if (url === "/api/v1/account/tokens/t1" && method === "DELETE") {
      tokens = tokens.filter((t) => t.id !== "t1")
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
      <McpAccessPanel />
    </QueryClientProvider>
  )
}

it("shows only a notice while MCP is off, and asks for nothing else", async () => {
  mcpEnabled = false
  renderPanel()
  await waitFor(() => {
    expect(screen.getByText(/hasn't enabled MCP/)).toBeTruthy()
  })
  expect(screen.queryByText(/New token/)).toBeNull()
  expect(calls.some((c) => c.url === "/api/v1/account/tokens")).toBe(false)
})

it("lists the user's tokens and offers placeholder snippets", async () => {
  renderPanel()
  await waitFor(() => {
    expect(screen.getByText("Old laptop")).toBeTruthy()
  })
  const claude = screen.getByTestId("snippet-claude-code").textContent ?? ""
  expect(claude).toContain("claude mcp add --transport http embookshelf")
  expect(claude).toContain(`${window.location.origin}/mcp`)
  expect(claude).toContain("Bearer <your-token>")
  expect(screen.getByTestId("snippet-json").textContent).toContain('"mcpServers"')
})

it("shows a new token once, fills it into the snippets, and forgets it on close", async () => {
  renderPanel()
  await waitFor(() => screen.getByText("Old laptop"))

  fireEvent.click(screen.getByRole("button", { name: /New token/ }))
  fireEvent.change(screen.getByPlaceholderText(/Claude Desktop/), {
    target: { value: "Claude Desktop" },
  })
  fireEvent.click(screen.getByRole("button", { name: "Create token" }))

  await waitFor(() => {
    expect((screen.getByTestId("new-token") as HTMLInputElement).value).toBe(RAW)
  })
  const post = calls.find((c) => c.method === "POST")
  expect(post?.url).toBe("/api/v1/account/tokens")
  const filled = screen
    .getAllByTestId("snippet-claude-code")
    .map((el) => el.textContent ?? "")
  expect(filled.some((s) => s.includes(`Bearer ${RAW}`))).toBe(true)

  fireEvent.click(screen.getByRole("button", { name: "Done" }))
  await waitFor(() => {
    expect(screen.queryByTestId("new-token")).toBeNull()
  })
  expect(document.body.textContent).not.toContain(RAW)
  expect(screen.getByTestId("snippet-claude-code").textContent).toContain(
    "Bearer <your-token>"
  )
})

it("revokes a token and drops its row", async () => {
  renderPanel()
  await waitFor(() => screen.getByText("Old laptop"))

  fireEvent.click(screen.getByRole("button", { name: "Revoke" }))

  await waitFor(() => {
    expect(screen.queryByText("Old laptop")).toBeNull()
  })
  expect(
    calls.some((c) => c.method === "DELETE" && c.url === "/api/v1/account/tokens/t1")
  ).toBe(true)
})
