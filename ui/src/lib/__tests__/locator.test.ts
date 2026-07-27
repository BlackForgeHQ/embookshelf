// ui/src/lib/__tests__/locator.test.ts
import { readFileSync, readdirSync } from "node:fs"
import { join } from "node:path"
import { describe, expect, it } from "vitest"

import type { Locator } from "../locator"
import { decodeLocator, encodeLocator, locatorLabel } from "../locator"

describe("round trip", () => {
  // One case per kind. A token that survives encode(decode(token)) is a
  // token every reader shell and every consumer agrees about.
  const cases: Array<[string, string, Locator]> = [
    [
      "cfi",
      "epubcfi(/6/14[chap03]!/4/1:0)",
      {
        kind: "cfi",
        cfi: "epubcfi(/6/14[chap03]!/4/1:0)",
      },
    ],
    ["page", "page:42", { kind: "page", page: 42 }],
    ["time", "time:3600.00", { kind: "time", seconds: 3600 }],
    ["unknown", "who-wrote-this", { kind: "unknown", raw: "who-wrote-this" }],
  ]

  for (const [kind, token, decoded] of cases) {
    it(`decodes and re-encodes a ${kind} token unchanged`, () => {
      expect(decodeLocator(token)).toEqual(decoded)
      expect(encodeLocator(decoded)).toBe(token)
    })
  }

  it("has no locator for an absent or empty token", () => {
    expect(decodeLocator(undefined)).toBeNull()
    expect(decodeLocator("")).toBeNull()
  })

  // The audio shells emit fractional seconds; two decimal places is the
  // encoder's contract and what ARCHITECTURE.md §5.6.1 documents.
  it("encodes fractional seconds to two decimal places", () => {
    expect(encodeLocator({ kind: "time", seconds: 1234.5678 })).toBe(
      "time:1234.57"
    )
  })

  // A prefix present but the number unreadable is not a position — it must
  // not decode to page 0 or second 0 and silently send a reader to the top.
  it("treats a malformed numeric payload as unknown", () => {
    expect(decodeLocator("page:")).toEqual({ kind: "unknown", raw: "page:" })
    expect(decodeLocator("time:soon")).toEqual({
      kind: "unknown",
      raw: "time:soon",
    })
  })
})

describe("labels", () => {
  it("renders an EPUB CFI as EPUB, since the string itself says nothing", () => {
    expect(locatorLabel("epubcfi(/6/14[chap03]!/4/1:0)")).toBe("EPUB")
  })

  it("renders a page token as p.N", () => {
    expect(locatorLabel("page:42")).toBe("p.42")
  })

  // The defect this module exists to close: an audiobook bookmark used to
  // reach the notebook and the book page as the literal string
  // "time:3661.00", because both copies of the decoder handled only page
  // and CFI tokens and fell through to returning the raw token.
  it("renders a time token as a clock reading, not the raw token", () => {
    expect(locatorLabel("time:3661.00")).toBe("1:01:01")
    expect(locatorLabel("time:95.40")).toBe("1:35")
  })

  it("falls back to the raw token when the kind is unknown", () => {
    expect(locatorLabel("who-wrote-this")).toBe("who-wrote-this")
  })

  it("labels nothing for an absent token", () => {
    expect(locatorLabel(undefined)).toBe("")
  })
})

describe("page indexing", () => {
  // A page token is the human page number the label promises. The comic
  // shell used to write its own 0-indexed page straight into the token
  // while labelling the same bookmark 1-indexed, so `page:7` meant p.8 in
  // the reader and p.7 everywhere else.
  it("labels a page token with the same number the token carries", () => {
    for (const page of [1, 7, 42]) {
      expect(locatorLabel(encodeLocator({ kind: "page", page }))).toBe(
        `p.${page}`
      )
    }
  })
})

describe("sole ownership", () => {
  // The token vocabulary lived in seven inline encoders and three copies
  // of the decoder. Adding a kind should be one file, so nothing outside
  // this module may spell a prefix out again. Matches code, not prose: a
  // quoted literal (a hand-rolled decode) or an interpolation head (a
  // hand-rolled encode), never a `page:N` mention inside a comment.
  const prefixes = [
    /"(page|time):/,
    /`(page|time):\$\{/,
    /"epubcfi/,
    /`epubcfi/,
  ]

  function sourceFiles(dir: string): Array<string> {
    const out: Array<string> = []
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name)
      if (entry.isDirectory()) {
        if (entry.name === "__tests__") continue
        out.push(...sourceFiles(path))
      } else if (
        /\.tsx?$/.test(entry.name) &&
        entry.name !== "routeTree.gen.ts"
      ) {
        out.push(path)
      }
    }
    return out
  }

  it("is the only module that spells a token prefix", () => {
    const offenders: Array<string> = []
    for (const path of sourceFiles(join(import.meta.dirname, "..", ".."))) {
      if (path.endsWith(join("lib", "locator.ts"))) continue
      const source = readFileSync(path, "utf8")
      if (prefixes.some((re) => re.test(source))) offenders.push(path)
    }
    expect(offenders).toEqual([])
  })
})
