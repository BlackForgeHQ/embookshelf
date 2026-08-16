// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, renderHook, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import { useReaderShell } from "@/components/reader/useReaderShell"

const fetchSpy = vi.fn()

beforeEach(() => {
  fetchSpy.mockReset()
  vi.stubGlobal("fetch", (input: RequestInfo | URL, init?: RequestInit) => {
    fetchSpy(String(input), init)
    return Promise.resolve({
      ok: true,
      status: 200,
      json: () => Promise.resolve({}),
      text: () => Promise.resolve("{}"),
    })
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

// The invariant every shell used to re-implement with a closePanels()
// + toggle pair: one panel open at a time, toggling closed its
// siblings — and a second toggle of the same panel closes it.
it("keeps panels exclusive", () => {
  const { result } = renderHook(() => useReaderShell("b1"), { wrapper })

  act(() => result.current.togglePanel("toc"))
  expect(result.current.openPanel).toBe("toc")

  act(() => result.current.togglePanel("notes"))
  expect(result.current.openPanel).toBe("notes")

  act(() => result.current.togglePanel("notes"))
  expect(result.current.openPanel).toBeNull()
})

it("owns chrome visibility with one restore path", () => {
  const { result } = renderHook(() => useReaderShell("b1"), { wrapper })
  expect(result.current.chromeVisible).toBe(true)

  act(() => result.current.hideChrome())
  expect(result.current.chromeVisible).toBe(false)

  act(() => result.current.showChrome())
  expect(result.current.chromeVisible).toBe(true)

  act(() => result.current.toggleChrome())
  expect(result.current.chromeVisible).toBe(false)
})

// A null locator is a renderer that has not reported a position yet —
// the no-op guard every paged shell used to write by hand.
it("bookmarks a real position and ignores a null one", async () => {
  const { result } = renderHook(() => useReaderShell("b1"), { wrapper })

  act(() => result.current.bookmark(null))
  expect(fetchSpy).not.toHaveBeenCalled()

  act(() => result.current.bookmark({ kind: "page", page: 7 }))
  await waitFor(() => expect(fetchSpy).toHaveBeenCalled())
  const [url, init] = fetchSpy.mock.calls[0]!
  expect(String(url)).toContain("/api/v1/")
  // createBookmark encodes the locator token — the wire shape the
  // notebook reads back.
  expect(String(init?.body ?? "")).toContain('"locator":"page:7"')
})
