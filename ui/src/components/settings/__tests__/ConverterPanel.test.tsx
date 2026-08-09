// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { cleanup, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import { ConverterPanel } from "@/components/settings/ConverterPanel"

function reply(status: number, body: unknown) {
  return {
    ok: status < 400,
    status,
    statusText: String(status),
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body)),
  }
}

let settingsReply: unknown = { enabled: true, baseUrl: "http://converter:6070" }
let healthReply: unknown = { status: "ok", version: "0.1.0" }

beforeEach(() => {
  // Radix's Toggle measures itself; jsdom has no ResizeObserver.
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
    if (url === "/api/v1/settings/converter") {
      return Promise.resolve(reply(200, settingsReply))
    }
    if (url === "/api/v1/settings/converter/health") {
      return Promise.resolve(reply(200, healthReply))
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
      <ConverterPanel />
    </QueryClientProvider>
  )
}

it("renders the stored row and the sidecar's version when reachable", async () => {
  renderPanel()
  await waitFor(() => {
    expect(
      screen.getByDisplayValue("http://converter:6070")
    ).toBeTruthy()
  })
  await waitFor(() => {
    expect(screen.getByText(/reachable/i)).toBeTruthy()
  })
  expect(screen.getByText(/0\.1\.0/)).toBeTruthy()
})

it("surfaces the dial error verbatim when the sidecar is unreachable", async () => {
  healthReply = {
    status: "unreachable",
    error: 'dial tcp: connect: connection refused',
  }
  renderPanel()
  await waitFor(() => {
    expect(screen.getByText(/connection refused/)).toBeTruthy()
  })
})

it("says not configured instead of probing when the extension is off", async () => {
  settingsReply = { enabled: false, baseUrl: "" }
  healthReply = { status: "not_configured" }
  renderPanel()
  await waitFor(() => {
    expect(screen.getByText(/not configured/i)).toBeTruthy()
  })
})
