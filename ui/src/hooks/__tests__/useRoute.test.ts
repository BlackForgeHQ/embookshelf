// @vitest-environment jsdom
import { renderHook } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { useRouteId, useRouteSearch } from "../useRoute"
import { parseLibrarySearch } from "@/lib/routes"

let pathname = "/"
let search = new URLSearchParams()

vi.mock("next/navigation", () => ({
  usePathname: () => pathname,
  useSearchParams: () => search,
  // Never consulted: in the static export this would report the `_`
  // placeholder, which is the reason useRouteId exists.
  useParams: () => ({ id: "_" }),
}))

beforeEach(() => {
  pathname = "/"
  search = new URLSearchParams()
})

describe("useRouteId", () => {
  it.each([
    ["/book/42", "42"],
    ["/book/42/edit", "42"],
    ["/book/42/find", "42"],
    ["/read/42", "42"],
    ["/book/a%2Fb", "a/b"],
  ])("reads the id from %s", (path, want) => {
    pathname = path
    const { result } = renderHook(() => useRouteId())
    expect(result.current).toBe(want)
  })

  it("survives a malformed escape", () => {
    pathname = "/book/%E0%A4%A"
    const { result } = renderHook(() => useRouteId())
    expect(result.current).toBe("%E0%A4%A")
  })
})

describe("useRouteSearch", () => {
  it("parses the current query string", () => {
    search = new URLSearchParams("shelf=to-read&layout=list")
    const { result } = renderHook(() => useRouteSearch(parseLibrarySearch))
    expect(result.current).toMatchObject({ shelf: "to-read", layout: "list" })
  })

  it("keeps one result per query string", () => {
    search = new URLSearchParams("shelf=x")
    const { result, rerender } = renderHook(() =>
      useRouteSearch(parseLibrarySearch)
    )
    const first = result.current
    rerender()
    expect(result.current).toBe(first)
  })
})
