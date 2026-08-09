// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { cleanup, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import { ReadingGuidePanel } from "@/components/book/ReadingGuidePanel"

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
    if (url === "/api/v1/books/b1/guide") {
      return Promise.resolve(reply(404, { error: "not found" }))
    }
    if (url === "/api/v1/books/b1/markdown") {
      return Promise.resolve(reply(200, markdownReply))
    }
    if (url === "/api/v1/me") {
      return Promise.resolve(reply(200, { user: { id: "u1", role: "admin" } }))
    }
    return Promise.reject(new Error(`unexpected fetch: ${url}`))
  })
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function renderPanel(format: string) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <ReadingGuidePanel bookId="b1" format={format} />
    </QueryClientProvider>
  )
}

// The loud-failure rule end-to-end (ADR-0033 §5): a conversion that
// failed after the guide was enqueued surfaces its message verbatim in
// the panel, not a permanent "no guide yet".
it("surfaces a failed conversion verbatim for a convertible book", async () => {
  markdownReply = {
    state: "failed",
    stale: false,
    error: "PDF has no extractable text (Scanned, 1 pages): OCR is required",
  }
  renderPanel("PDF")
  await waitFor(() => {
    expect(screen.getByText(/OCR is required/)).toBeTruthy()
  })
})

it("shows converting state while the rendition is in flight", async () => {
  markdownReply = { state: "running", stale: false }
  renderPanel("PDF")
  await waitFor(() => {
    expect(screen.getByText(/Converting the book's text/)).toBeTruthy()
  })
})

it("never queries markdown status for an EPUB", async () => {
  markdownReply = { state: "failed", stale: false, error: "should not appear" }
  renderPanel("EPUB")
  await waitFor(() => {
    expect(screen.getByText(/No reading guide yet/)).toBeTruthy()
  })
  expect(screen.queryByText(/should not appear/)).toBeNull()
})
