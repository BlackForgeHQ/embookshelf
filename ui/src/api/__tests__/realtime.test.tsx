// @vitest-environment jsdom
// ui/src/api/__tests__/realtime.test.tsx
import type { ReactNode } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, renderHook } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

// --- Router double -----------------------------------------------------
//
// Mirrors the two properties the hook leans on: useRouter hands back one
// object for the lifetime of the app, and `.state` is a live getter that
// always reports the current location. Navigation therefore changes what
// the handlers read without changing any identity React can depend on —
// so these tests navigate without re-rendering, which is precisely the
// case a location captured at subscribe time gets wrong.
const navigateSpy = vi.fn()

let routerState: { location: { pathname: string; search: unknown } } = {
  location: { pathname: "/", search: {} },
}

function navigateTo(pathname: string, search: Record<string, unknown> = {}) {
  routerState = { location: { pathname, search } }
}

const routerDouble = {
  get state() {
    return routerState
  },
}

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigateSpy,
  useRouter: () => routerDouble,
}))

const toastInfo = vi.fn()
const toastSuccess = vi.fn()
const toastError = vi.fn()
vi.mock("sonner", () => ({
  toast: { info: toastInfo, success: toastSuccess, error: toastError },
}))

// --- EventSource double ------------------------------------------------
//
// Counts constructions, because "one connection per session" is a claim
// about how many times the constructor ran, not about what it delivered.
class FakeEventSource {
  static instances: Array<FakeEventSource> = []

  readonly listeners = new Map<string, Set<EventListener>>()
  closed = false
  onerror: ((e: Event) => void) | null = null

  constructor(
    readonly url: string,
    readonly init?: EventSourceInit
  ) {
    FakeEventSource.instances.push(this)
  }

  addEventListener(name: string, listener: EventListener) {
    const set = this.listeners.get(name) ?? new Set<EventListener>()
    set.add(listener)
    this.listeners.set(name, set)
  }

  removeEventListener(name: string, listener: EventListener) {
    this.listeners.get(name)?.delete(listener)
  }

  close() {
    this.closed = true
  }

  emit(name: string, data: string) {
    const event = new MessageEvent(name, { data })
    for (const listener of this.listeners.get(name) ?? []) {
      listener(event)
    }
  }
}

const { useRealtime } = await import("../realtime")

// One client for the mount, not one per render — useQueryClient's result is
// in the effect's dependency array, so a fresh client each render would
// re-open the connection for a reason that has nothing to do with routing.
let queryClient = new QueryClient()

function wrapper({ children }: { children: ReactNode }) {
  return (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

function onlySource(): FakeEventSource {
  expect(FakeEventSource.instances).toHaveLength(1)
  return FakeEventSource.instances[0]!
}

beforeEach(() => {
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  FakeEventSource.instances = []
  navigateSpy.mockReset()
  toastInfo.mockReset()
  toastSuccess.mockReset()
  toastError.mockReset()
  routerState = { location: { pathname: "/", search: {} } }
  vi.stubGlobal("EventSource", FakeEventSource)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("connection lifetime", () => {
  // The single-connection-per-session property from ARCHITECTURE.md §5.7.
  // A location in the subscribing effect's dependency array quietly turns
  // it into one connection per navigation.
  it("opens exactly one EventSource across multiple navigations", () => {
    const { rerender } = renderHook(() => useRealtime(), { wrapper })
    expect(FakeEventSource.instances).toHaveLength(1)

    for (const path of ["/library", "/notebook", "/book/42", "/library"]) {
      act(() => {
        navigateTo(path)
      })
      rerender()
    }

    expect(onlySource().closed).toBe(false)
  })

  it("keeps its listeners attached across navigations", () => {
    renderHook(() => useRealtime(), { wrapper })
    const es = onlySource()

    act(() => {
      navigateTo("/library", { shelf: "public:top-picks" })
    })

    // Nine events, one listener each — a teardown/resubscribe cycle would
    // still leave nine, so also assert the source itself is the original.
    expect(es.closed).toBe(false)
    expect([...es.listeners.keys()]).toHaveLength(9)
  })

  it("closes the connection and detaches listeners on unmount", () => {
    const { unmount } = renderHook(() => useRealtime(), { wrapper })
    const es = onlySource()
    unmount()

    expect(es.closed).toBe(true)
    for (const set of es.listeners.values()) {
      expect(set.size).toBe(0)
    }
  })
})

describe("shared-shelf handlers", () => {
  // The two handlers that need the location need it at *event* time. A
  // location captured when the effect subscribed is the location the user
  // was on when the session started, not the one they are on now.
  it("redirects when the removed shelf is the one the viewer navigated to", () => {
    renderHook(() => useRealtime(), { wrapper })
    const es = onlySource()

    act(() => {
      navigateTo("/library", { shelf: "public:top-picks" })
    })

    act(() => {
      es.emit(
        "shelf.public.removed",
        JSON.stringify({ slug: "public:top-picks" })
      )
    })

    expect(toastInfo).toHaveBeenCalledOnce()
    expect(navigateSpy).toHaveBeenCalledWith({ to: "/library", search: {} })
  })

  it("does not redirect a viewer who has navigated away from that shelf", () => {
    navigateTo("/library", { shelf: "public:top-picks" })
    renderHook(() => useRealtime(), { wrapper })
    const es = onlySource()

    act(() => {
      navigateTo("/notebook")
    })

    act(() => {
      es.emit(
        "shelf.public.removed",
        JSON.stringify({ slug: "public:top-picks" })
      )
    })

    expect(navigateSpy).not.toHaveBeenCalled()
  })

  it("does not redirect when a different shared shelf is removed", () => {
    renderHook(() => useRealtime(), { wrapper })
    const es = onlySource()

    act(() => {
      navigateTo("/library", { shelf: "public:top-picks" })
    })

    act(() => {
      es.emit(
        "shelf.public.removed",
        JSON.stringify({ slug: "public:staff-reads" })
      )
    })

    expect(navigateSpy).not.toHaveBeenCalled()
  })
})
