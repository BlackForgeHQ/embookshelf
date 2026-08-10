// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { cleanup, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import { VersionRows } from "@/components/book/VersionRows"

function reply(status: number, body: unknown) {
  return {
    ok: status < 400,
    status,
    statusText: String(status),
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body)),
  }
}

let markdownReply: unknown = { state: "none", stale: false }

beforeEach(() => {
  vi.stubGlobal("fetch", (input: RequestInfo | URL) => {
    const url = String(input)
    if (url === "/api/v1/books/b1/markdown") {
      return Promise.resolve(reply(200, markdownReply))
    }
    return Promise.reject(new Error(`unexpected fetch: ${url}`))
  })
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function renderRows() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <VersionRows bookId="b1" title="Dune" format="PDF" />
    </QueryClientProvider>
  )
}

it("always offers the primary file with a download", async () => {
  renderRows()
  expect(screen.getByText("Dune.pdf")).toBeTruthy()
  const links = screen.getAllByRole("link", { name: /download/i })
  expect(links[0]?.getAttribute("href")).toBe("/api/v1/books/b1/file?download=1")
})

it("shows a downloadable markdown row when the rendition is ready", async () => {
  markdownReply = {
    state: "ready",
    stale: false,
    sizeBytes: 204800,
    converterVersion: "0.1.0",
  }
  renderRows()
  await waitFor(() => {
    expect(screen.getByText("Dune.md")).toBeTruthy()
  })
  expect(screen.getByText(/converter v0\.1\.0/)).toBeTruthy()
  const links = screen.getAllByRole("link", { name: /download/i })
  expect(links).toHaveLength(2)
  expect(links[1]?.getAttribute("href")).toBe("/api/v1/books/b1/markdown/file")
})

it("labels a stale rendition without hiding it", async () => {
  markdownReply = { state: "ready", stale: true, sizeBytes: 1 }
  renderRows()
  await waitFor(() => {
    expect(screen.getByText(/Stale — converted from an older copy/)).toBeTruthy()
  })
  expect(screen.getAllByRole("link", { name: /download/i })).toHaveLength(2)
})

it("hides the markdown row for every non-ready state", async () => {
  markdownReply = { state: "failed", stale: false, error: "boom" }
  renderRows()
  await waitFor(() => {
    expect(screen.getByText("Dune.pdf")).toBeTruthy()
  })
  expect(screen.queryByText("Dune.md")).toBeNull()
  expect(screen.getAllByRole("link", { name: /download/i })).toHaveLength(1)
})
