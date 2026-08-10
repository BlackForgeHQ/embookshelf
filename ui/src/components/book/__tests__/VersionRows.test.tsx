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
let epubReply: unknown = { state: "none", stale: false }

beforeEach(() => {
  markdownReply = { state: "none", stale: false }
  epubReply = { state: "none", stale: false }
  vi.stubGlobal("fetch", (input: RequestInfo | URL) => {
    const url = String(input)
    if (url === "/api/v1/books/b1/markdown") {
      return Promise.resolve(reply(200, markdownReply))
    }
    if (url === "/api/v1/books/b1/epub") {
      return Promise.resolve(reply(200, epubReply))
    }
    return Promise.reject(new Error(`unexpected fetch: ${url}`))
  })
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function renderRows(opts: { isAdmin?: boolean; format?: string } = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <VersionRows
        bookId="b1"
        title="Dune"
        format={opts.format ?? "PDF"}
        isAdmin={opts.isAdmin ?? false}
      />
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

it("shows a downloadable generated EPUB row when ready", async () => {
  epubReply = { state: "ready", stale: false, converterVersion: "0.2.0" }
  renderRows()
  await waitFor(() => {
    expect(screen.getByText("Dune.epub")).toBeTruthy()
  })
  const links = screen.getAllByRole("link", { name: /download/i })
  expect(
    links.some(
      (l) =>
        l.getAttribute("href") ===
        "/api/v1/books/b1/file?rendition=epub&download=1"
    )
  ).toBe(true)
})

it("offers Generate EPUB to admins on a convertible book without one", async () => {
  renderRows({ isAdmin: true })
  await waitFor(() => {
    expect(screen.getByRole("button", { name: /Generate EPUB/ })).toBeTruthy()
  })
})

it("never offers the button to non-admins or for EPUB books", async () => {
  renderRows({ isAdmin: false })
  await waitFor(() => {
    expect(screen.getByText("Dune.pdf")).toBeTruthy()
  })
  expect(screen.queryByRole("button", { name: /Generate EPUB/ })).toBeNull()

  cleanup()
  renderRows({ isAdmin: true, format: "EPUB" })
  await waitFor(() => {
    expect(screen.getByText("Dune.epub")).toBeTruthy()
  })
  expect(screen.queryByRole("button", { name: /Generate EPUB/ })).toBeNull()
})

it("surfaces a failed render verbatim with a retry", async () => {
  epubReply = {
    state: "failed",
    stale: false,
    error: "PDF has no extractable text (Scanned, 1 pages): OCR is required",
  }
  renderRows({ isAdmin: true })
  await waitFor(() => {
    expect(screen.getByText(/OCR is required/)).toBeTruthy()
  })
  expect(screen.getByRole("button", { name: /Regenerate EPUB/ })).toBeTruthy()
})

it("shows generating state while the chain runs", async () => {
  epubReply = { state: "running", stale: false }
  renderRows()
  await waitFor(() => {
    expect(screen.getByText(/Generating EPUB/)).toBeTruthy()
  })
})
