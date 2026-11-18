import { describe, expect, it } from "vitest"

import {
  parseAccountSearch,
  parseLibrarySearch,
  parseLoginSearch,
  paths,
  safeNext,
  withSearch,
} from "@/lib/routes"

const qs = (s: string) => new URLSearchParams(s)

describe("safeNext — where a sign-in may send the user", () => {
  it.each([
    ["/library?shelf=x", "/library?shelf=x"],
    ["/book/42", "/book/42"],
  ])("keeps a same-origin path %s", (raw, want) => {
    expect(safeNext(raw)).toBe(want)
  })

  it.each([
    [undefined],
    [""],
    ["https://evil.example"],
    // Off-origin to a browser despite the leading slash.
    ["//evil.example"],
    ["/\\evil.example"],
    // The login page itself would loop.
    ["/login"],
    ["/login?next=/x"],
    ["/login/pending"],
  ])("collapses %s to the dashboard", (raw) => {
    expect(safeNext(raw)).toBe("/")
  })
})

describe("search parsers", () => {
  it("library: keeps known values, drops the rest", () => {
    expect(
      parseLibrarySearch(
        qs("shelf=to-read&layout=list&sort=author&unshelved=1&library=main")
      )
    ).toEqual({
      shelf: "to-read",
      library: "main",
      layout: "list",
      unshelved: "1",
      sort: "author",
    })
    expect(
      parseLibrarySearch(qs("layout=wall&sort=random&unshelved=true&shelf="))
    ).toEqual({
      shelf: undefined,
      library: undefined,
      layout: undefined,
      unshelved: undefined,
      sort: undefined,
    })
  })

  it("account: only a known section", () => {
    expect(parseAccountSearch(qs("section=devices&linked=google"))).toEqual({
      section: "devices",
      linked: "google",
      error: undefined,
    })
    expect(parseAccountSearch(qs("section=admin")).section).toBeUndefined()
  })

  it("login: mode defaults to login, local only when true", () => {
    expect(parseLoginSearch(qs(""))).toEqual({
      next: undefined,
      mode: "login",
      oidcError: undefined,
      local: false,
    })
    expect(parseLoginSearch(qs("mode=signup&local=true")).mode).toBe("signup")
    expect(parseLoginSearch(qs("local=true")).local).toBe(true)
    expect(parseLoginSearch(qs("local=1")).local).toBe(false)
  })
})

describe("builders", () => {
  it("drop empty search values so URLs stay clean", () => {
    expect(withSearch("/library", { shelf: undefined, layout: "" })).toBe(
      "/library"
    )
    expect(paths.library({ shelf: "a b", sort: undefined })).toBe(
      "/library?shelf=a+b"
    )
    expect(paths.account({ section: "devices" })).toBe(
      "/account?section=devices"
    )
  })

  it("round-trip through the parser they pair with", () => {
    const search = { shelf: "x", layout: "grid", unshelved: "1" } as const
    const url = new URL(paths.library(search), "http://h")
    expect(parseLibrarySearch(url.searchParams)).toMatchObject(search)
  })

  it("encode ids into their segment", () => {
    expect(paths.book("a/b")).toBe("/book/a%2Fb")
    expect(paths.bookEdit("x")).toBe("/book/x/edit")
    expect(paths.read("x")).toBe("/read/x")
  })
})
