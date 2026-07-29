// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import type { Locator } from "@/lib/locator"
import { useReadingPosition } from "../useReadingPosition"

// The two things the module reaches for on a shell's behalf: the write
// and the way out. A shell holds neither, which is what lets this file
// be the module's whole test surface — no shell is mounted anywhere in
// it (#205).
const updateProgress = vi.fn()
const navigate = vi.fn()

vi.mock("@/api/books", () => ({
  updateProgress: (...args: Array<unknown>) => updateProgress(...args),
}))
vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigate,
}))

const BOOK = "book-7"

const page = (n: number): Locator => ({ kind: "page", page: n })
const time = (seconds: number): Locator => ({ kind: "time", seconds })
const cfi = (c: string): Locator => ({ kind: "cfi", cfi: c })

beforeEach(() => {
  vi.useFakeTimers()
  updateProgress.mockReset()
  navigate.mockReset()
})
afterEach(() => {
  vi.useRealTimers()
})

function setup(debounceMs?: number) {
  return renderHook(() => useReadingPosition({ bookId: BOOK, debounceMs }))
}

describe("report", () => {
  it("does not write until the debounce elapses", () => {
    const { result } = setup(600)
    act(() => result.current.report(0.1, page(1)))

    act(() => void vi.advanceTimersByTime(599))
    expect(updateProgress).not.toHaveBeenCalled()

    act(() => void vi.advanceTimersByTime(1))
    expect(updateProgress).toHaveBeenCalledExactlyOnceWith(BOOK, 0.1, "page:1")
  })

  // Page turns fire faster than the debounce; only the position the reader
  // actually settled on should reach the server.
  it("keeps only the latest position when reported repeatedly", () => {
    const { result } = setup(600)
    act(() => {
      result.current.report(0.1, page(1))
      result.current.report(0.2, page(2))
      result.current.report(0.3, page(3))
    })
    act(() => void vi.advanceTimersByTime(600))
    expect(updateProgress).toHaveBeenCalledExactlyOnceWith(BOOK, 0.3, "page:3")
  })

  it("restarts the window on each report rather than writing on a fixed schedule", () => {
    const { result } = setup(600)
    act(() => result.current.report(0.1, page(1)))
    act(() => void vi.advanceTimersByTime(500))
    act(() => result.current.report(0.2, page(2)))
    act(() => void vi.advanceTimersByTime(500))
    expect(updateProgress).not.toHaveBeenCalled()

    act(() => void vi.advanceTimersByTime(100))
    expect(updateProgress).toHaveBeenCalledExactlyOnceWith(BOOK, 0.2, "page:2")
  })

  it("honours a caller-supplied debounce", () => {
    const { result } = setup(5000)
    act(() => result.current.report(0.5, time(12)))
    act(() => void vi.advanceTimersByTime(4999))
    expect(updateProgress).not.toHaveBeenCalled()
    act(() => void vi.advanceTimersByTime(1))
    expect(updateProgress).toHaveBeenCalledOnce()
  })
})

// A shell hands over where the reader is, not what to store. The token
// used to be assembled at the call site — three shells encoded and the
// text shell passed its CFI through raw, which read as an inconsistency
// nobody could check.
describe("the stored token", () => {
  it("encodes a page position", () => {
    const { result } = setup(600)
    act(() => result.current.report(0.5, page(12)))
    act(() => void vi.advanceTimersByTime(600))
    expect(updateProgress).toHaveBeenCalledWith(BOOK, 0.5, "page:12")
  })

  it("encodes a time position to a fixed width", () => {
    const { result } = setup(600)
    act(() => result.current.report(0.5, time(3661)))
    act(() => void vi.advanceTimersByTime(600))
    expect(updateProgress).toHaveBeenCalledWith(BOOK, 0.5, "time:3661.00")
  })

  // A CFI carries its own `epubcfi(` prefix, so it is stored verbatim.
  it("stores a CFI verbatim", () => {
    const { result } = setup(600)
    act(() => result.current.report(0.5, cfi("epubcfi(/6/14!/4/2)")))
    act(() => void vi.advanceTimersByTime(600))
    expect(updateProgress).toHaveBeenCalledWith(BOOK, 0.5, "epubcfi(/6/14!/4/2)")
  })
})

// The reader has stopped moving but the session continues — pausing
// playback is the only caller today. Distinct from exit, which also
// leaves.
describe("settle", () => {
  it("writes the pending position immediately", () => {
    const { result } = setup(600)
    act(() => result.current.report(0.4, page(4)))
    act(() => result.current.settle())
    expect(updateProgress).toHaveBeenCalledExactlyOnceWith(BOOK, 0.4, "page:4")
  })

  it("does not write a second time once the window elapses", () => {
    const { result } = setup(600)
    act(() => result.current.report(0.4, page(4)))
    act(() => result.current.settle())
    act(() => void vi.advanceTimersByTime(5000))
    expect(updateProgress).toHaveBeenCalledOnce()
  })

  it("does nothing when there is no pending position", () => {
    const { result } = setup(600)
    act(() => result.current.settle())
    expect(updateProgress).not.toHaveBeenCalled()
  })

  it("is idempotent across repeated calls", () => {
    const { result } = setup(600)
    act(() => result.current.report(0.4, page(4)))
    act(() => {
      result.current.settle()
      result.current.settle()
      result.current.settle()
    })
    expect(updateProgress).toHaveBeenCalledOnce()
  })

  it("reports again normally afterwards", () => {
    const { result } = setup(600)
    act(() => result.current.report(0.4, page(4)))
    act(() => result.current.settle())
    act(() => result.current.report(0.9, page(9)))
    act(() => void vi.advanceTimersByTime(600))
    expect(updateProgress).toHaveBeenCalledTimes(2)
    expect(updateProgress).toHaveBeenLastCalledWith(BOOK, 0.9, "page:9")
  })

  it("does not leave the reader", () => {
    const { result } = setup(600)
    act(() => result.current.settle())
    expect(navigate).not.toHaveBeenCalled()
  })
})

// Leaving the reader. Four shells wrote this closure by hand, three of
// them carrying the same explanatory comment about not relying on the
// unmount backstop (#205).
describe("exit", () => {
  it("writes the held position before navigating", () => {
    const { result } = setup(600)
    act(() => result.current.report(0.8, page(8)))
    act(() => result.current.exit())

    expect(updateProgress).toHaveBeenCalledExactlyOnceWith(BOOK, 0.8, "page:8")
    // Ordering is the point: the unmount backstop fires mid-teardown,
    // which is why the shells did not rely on it.
    expect(updateProgress.mock.invocationCallOrder[0] ?? -1).toBeLessThan(
      navigate.mock.invocationCallOrder[0] ?? -1
    )
  })

  it("returns to the book page", () => {
    const { result } = setup(600)
    act(() => result.current.exit())
    expect(navigate).toHaveBeenCalledWith({
      to: "/book/$id",
      params: { id: BOOK },
    })
  })

  it("navigates even when no position is pending", () => {
    const { result } = setup(600)
    act(() => result.current.exit())
    expect(updateProgress).not.toHaveBeenCalled()
    expect(navigate).toHaveBeenCalledOnce()
  })
})

describe("unmount", () => {
  // A short reading session can end before any debounce window closes.
  it("writes a pending position when the reader closes", () => {
    const { result, unmount } = setup(600)
    act(() => result.current.report(0.7, page(7)))
    unmount()
    expect(updateProgress).toHaveBeenCalledExactlyOnceWith(BOOK, 0.7, "page:7")
  })

  it("does not write when nothing is pending", () => {
    const { unmount } = setup(600)
    unmount()
    expect(updateProgress).not.toHaveBeenCalled()
  })

  it("does not write twice when the position was already settled", () => {
    const { result, unmount } = setup(600)
    act(() => result.current.report(0.7, page(7)))
    act(() => result.current.settle())
    unmount()
    expect(updateProgress).toHaveBeenCalledOnce()
  })

  it("does not fire a stale timer after unmount", () => {
    const { result, unmount } = setup(600)
    act(() => result.current.report(0.7, page(7)))
    unmount()
    updateProgress.mockReset()
    vi.advanceTimersByTime(5000)
    expect(updateProgress).not.toHaveBeenCalled()
  })
})

describe("identity", () => {
  // The shells pass report straight into child reader props. A new
  // function identity every render would retrigger their effects on each
  // page turn.
  it("keeps report, settle and exit stable across re-renders", () => {
    const { result, rerender } = setup(600)
    const first = { ...result.current }
    rerender()
    expect(result.current.report).toBe(first.report)
    expect(result.current.settle).toBe(first.settle)
    expect(result.current.exit).toBe(first.exit)
  })

  // The reader emits progress on a timer; a bookId captured at mount
  // would keep posting to whichever book was open when the hook first
  // ran.
  it("writes to the newest book, not the one captured at mount", () => {
    const { result, rerender } = renderHook(
      ({ bookId }) => useReadingPosition({ bookId, debounceMs: 600 }),
      { initialProps: { bookId: BOOK } }
    )
    rerender({ bookId: "book-9" })
    act(() => result.current.report(0.2, page(2)))
    act(() => void vi.advanceTimersByTime(600))
    expect(updateProgress).toHaveBeenCalledExactlyOnceWith(
      "book-9",
      0.2,
      "page:2"
    )
  })

  // Putting the write in the unmount effect's deps makes React run the
  // cleanup on every render where its identity changes — mid-session,
  // with a position still held, that ships the position early.
  it("does not write when the book changes mid-session", () => {
    const { result, rerender } = renderHook(
      ({ bookId }) => useReadingPosition({ bookId, debounceMs: 600 }),
      { initialProps: { bookId: BOOK } }
    )
    act(() => result.current.report(0.2, page(2)))
    rerender({ bookId: "book-9" })
    expect(updateProgress).not.toHaveBeenCalled()
  })
})

describe("a reader that never settles", () => {
  // The debounce restarts on every call, which is right for page turns —
  // they land seconds apart, so the window elapses normally. Audio
  // reports progress several times a second, so the window was cancelled
  // and restarted faster than it could ever fire: the 5 s "much longer
  // window" was infinity, and only an explicit flush ever wrote (#204).
  it("still writes while progress keeps arriving", () => {
    const { result } = setup(5000)

    // Twenty-five seconds of playback at four reports a second — past
    // the ceiling, which is what a listener who never pauses looks like.
    for (let i = 0; i < 100; i++) {
      act(() => {
        result.current.report(i / 100, time(i * 0.25))
        vi.advanceTimersByTime(250)
      })
    }

    expect(updateProgress).toHaveBeenCalled()
  })

  it("still holds the newest position, not the one that opened the window", () => {
    const { result } = setup(5000)

    for (let i = 0; i < 100; i++) {
      act(() => {
        result.current.report(i / 100, time(i))
        vi.advanceTimersByTime(250)
      })
    }

    const last = updateProgress.mock.calls[updateProgress.mock.calls.length - 1]
    // The 80th report is the newest one held when the ceiling elapses.
    expect(last?.[2]).toBe("time:79.00")
  })
})

describe("backgrounding", () => {
  // The hook's own contract named backgrounding as one of the three
  // exits a caller must flush on, and no caller ever did — so a mobile
  // listener who swiped the browser away lost the session, because
  // unmount effects do not run on a tab kill (#204).
  it("writes the held position when the tab is hidden", () => {
    const { result } = setup()
    act(() => result.current.report(0.4, cfi("epubcfi(/6/14)")))

    act(() => {
      Object.defineProperty(document, "visibilityState", {
        value: "hidden",
        configurable: true,
      })
      document.dispatchEvent(new Event("visibilitychange"))
    })

    expect(updateProgress).toHaveBeenCalledWith(BOOK, 0.4, "epubcfi(/6/14)")
  })

  it("writes the held position on pagehide", () => {
    const { result } = setup()
    act(() => result.current.report(0.6, page(12)))

    act(() => {
      window.dispatchEvent(new Event("pagehide"))
    })

    expect(updateProgress).toHaveBeenCalledWith(BOOK, 0.6, "page:12")
  })
})
