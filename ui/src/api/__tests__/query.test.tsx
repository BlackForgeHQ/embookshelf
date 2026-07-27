// @vitest-environment jsdom
import { readFileSync, readdirSync } from "node:fs"
import { join } from "node:path"
import type { ReactNode } from "react"
import {
  QueryClient,
  QueryClientProvider,
  useQuery,
} from "@tanstack/react-query"
import { cleanup, render, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { appConfigQuery } from "../settings"
import { meQuery } from "../auth"
import { SESSION_STALE_TIME, defineQuery, useApiQuery } from "../query"

// The app's own client defaults, restated so this file tests what ships
// rather than a client invented for the test. See `ui/src/router.tsx`.
function appClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { staleTime: 30_000, refetchOnWindowFocus: false, retry: false },
    },
  })
}

let client: QueryClient
function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

beforeEach(() => {
  client = appClient()
})
afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

describe("useApiQuery", () => {
  it("reads through the spec's key and fetcher", async () => {
    const spec = defineQuery({
      key: ["probe"] as const,
      fn: () => Promise.resolve("payload"),
    })
    let seen: string | undefined
    function Probe() {
      seen = useApiQuery(spec).data
      return null
    }
    render(<Probe />, { wrapper })

    await waitFor(() => expect(seen).toBe("payload"))
    expect(client.getQueryData(["probe"])).toBe("payload")
  })

  it("does not fetch while the caller says the query is not ready", async () => {
    const fn = vi.fn(() => Promise.resolve("payload"))
    const spec = defineQuery({ key: ["gated"] as const, fn })
    function Probe({ ready }: { ready: boolean }) {
      useApiQuery(spec, { enabled: ready })
      return null
    }
    const view = render(<Probe ready={false} />, { wrapper })
    expect(fn).not.toHaveBeenCalled()

    view.rerender(<Probe ready={true} />)
    await waitFor(() => expect(fn).toHaveBeenCalledTimes(1))
  })
})

// ---------------------------------------------------------------------------
// The caching policy
// ---------------------------------------------------------------------------
//
// This is the defect the module closes, stated as a test. Ten call sites
// read the current user; eight declared a 60s stale time and two declared
// nothing, so those two inherited the client-wide 30s default and refetched
// `/api/v1/me` the moment a page mounted more than thirty seconds into a
// session — which is every book page a reader opens. Neither the policy nor
// its absence was assertable anywhere, because the policy was an argument
// at the call site rather than a property of the endpoint.

describe("caching policy lives with the endpoint", () => {
  it("a spec'd stale time outlives the client-wide default", async () => {
    vi.useFakeTimers()
    const fn = vi.fn(() => Promise.resolve("me"))
    const spec = defineQuery({
      key: ["spec-policy"] as const,
      fn,
      staleTime: SESSION_STALE_TIME,
    })
    function Probe() {
      useApiQuery(spec)
      return null
    }

    const first = render(<Probe />, { wrapper })
    await vi.waitFor(() => expect(fn).toHaveBeenCalledTimes(1))
    first.unmount()

    // Thirty-one seconds later: past the client-wide default, inside the
    // policy this endpoint declares.
    await vi.advanceTimersByTimeAsync(31_000)
    render(<Probe />, { wrapper })
    await vi.advanceTimersByTimeAsync(0)
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it("a call site that declares nothing refetches on the next mount", async () => {
    vi.useFakeTimers()
    const fn = vi.fn(() => Promise.resolve("me"))
    // Exactly what the two drifted panels wrote: key and fetcher paired by
    // hand, no policy, so the client-wide default decides.
    function Probe() {
      useQuery({ queryKey: ["hand-paired"], queryFn: fn })
      return null
    }

    const first = render(<Probe />, { wrapper })
    await vi.waitFor(() => expect(fn).toHaveBeenCalledTimes(1))
    first.unmount()

    await vi.advanceTimersByTimeAsync(31_000)
    render(<Probe />, { wrapper })
    await vi.waitFor(() => expect(fn).toHaveBeenCalledTimes(2))
  })

  // The criterion, as an assertion: the session-scoped reads are one
  // policy, not two that happen to agree today.
  it("the current user and the app config share one policy", () => {
    expect(meQuery.staleTime).toBe(SESSION_STALE_TIME)
    expect(appConfigQuery.staleTime).toBe(SESSION_STALE_TIME)
  })
})

// ---------------------------------------------------------------------------
// Sole ownership
// ---------------------------------------------------------------------------

describe("sole ownership", () => {
  // A key paired with a fetcher outside the api modules is a call site
  // re-deriving the query, which is how the policy drifted. Outside
  // `api/`, a `queryFn` may only come from a spec — `someQuery.fn`, or
  // the options object `useSettingsDraft` is handed. That keeps the pair
  // together even at the two sites that cannot call `useApiQuery`.
  const FROM_A_SPEC = /queryFn:\s*(opts\.queryFn|[A-Za-z]\w*(\([^)]*\))?\.fn)/

  // `useSettingsDraft` declares `queryFn` as part of its own options type.
  // That is the module receiving a query, not a call site inventing one.
  const EXEMPT = [join("hooks", "useSettingsDraft.ts")]

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

  const root = join(import.meta.dirname, "..", "..")

  it("takes every fetcher outside the api modules from a spec", () => {
    const offenders: Array<string> = []
    for (const path of sourceFiles(root)) {
      const rel = path.slice(root.length + 1)
      if (rel.startsWith(`api${"/"}`)) continue
      if (EXEMPT.some((e) => rel.endsWith(e))) continue
      for (const line of readFileSync(path, "utf8").split("\n")) {
        if (!/\bqueryFn\s*:/.test(line)) continue
        if (!FROM_A_SPEC.test(line)) offenders.push(`${rel}: ${line.trim()}`)
      }
    }
    expect(offenders).toEqual([])
  })

  it("only the api modules set a stale time", () => {
    const offenders: Array<string> = []
    for (const path of sourceFiles(root)) {
      const rel = path.slice(root.length + 1)
      if (rel.startsWith(`api${"/"}`)) continue
      if (rel === "router.tsx") continue // the client-wide default
      if (/\bstaleTime\s*:/.test(readFileSync(path, "utf8")))
        offenders.push(rel)
    }
    expect(offenders).toEqual([])
  })
})
