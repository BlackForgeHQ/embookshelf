// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { useReadingPosition } from "../useReadingPosition"

const save = vi.fn<(progress: number, token: string) => void>()

beforeEach(() => {
  vi.useFakeTimers()
  save.mockReset()
})
afterEach(() => {
  vi.useRealTimers()
})

function setup(debounceMs?: number) {
  return renderHook(() => useReadingPosition({ save, debounceMs }))
}

describe("queue", () => {
  it("does not save until the debounce elapses", () => {
    const { result } = setup(600)
    act(() => result.current.queue(0.1, "page:1"))

    act(() => void vi.advanceTimersByTime(599))
    expect(save).not.toHaveBeenCalled()

    act(() => void vi.advanceTimersByTime(1))
    expect(save).toHaveBeenCalledExactlyOnceWith(0.1, "page:1")
  })

  // Page turns fire faster than the debounce; only the position the reader
  // actually settled on should reach the server.
  it("keeps only the latest position when queued repeatedly", () => {
    const { result } = setup(600)
    act(() => {
      result.current.queue(0.1, "page:1")
      result.current.queue(0.2, "page:2")
      result.current.queue(0.3, "page:3")
    })
    act(() => void vi.advanceTimersByTime(600))
    expect(save).toHaveBeenCalledExactlyOnceWith(0.3, "page:3")
  })

  it("restarts the window on each queue rather than saving on a fixed schedule", () => {
    const { result } = setup(600)
    act(() => result.current.queue(0.1, "page:1"))
    act(() => void vi.advanceTimersByTime(500))
    act(() => result.current.queue(0.2, "page:2"))
    act(() => void vi.advanceTimersByTime(500))
    expect(save).not.toHaveBeenCalled()

    act(() => void vi.advanceTimersByTime(100))
    expect(save).toHaveBeenCalledExactlyOnceWith(0.2, "page:2")
  })

  it("honours a caller-supplied debounce", () => {
    const { result } = setup(5000)
    act(() => result.current.queue(0.5, "time:12.00"))
    act(() => void vi.advanceTimersByTime(4999))
    expect(save).not.toHaveBeenCalled()
    act(() => void vi.advanceTimersByTime(1))
    expect(save).toHaveBeenCalledOnce()
  })
})

describe("flush", () => {
  it("saves the pending position immediately", () => {
    const { result } = setup(600)
    act(() => result.current.queue(0.4, "page:4"))
    act(() => result.current.flush())
    expect(save).toHaveBeenCalledExactlyOnceWith(0.4, "page:4")
  })

  // Guards the pending slot being cleared. (The timer cancel inside flush is
  // belt-and-braces: a timer that fires afterwards finds nothing pending and
  // no-ops, so it cannot be observed through save.)
  it("does not save a second time once the window elapses", () => {
    const { result } = setup(600)
    act(() => result.current.queue(0.4, "page:4"))
    act(() => result.current.flush())
    act(() => void vi.advanceTimersByTime(5000))
    expect(save).toHaveBeenCalledOnce()
  })

  it("does nothing when there is no pending position", () => {
    const { result } = setup(600)
    act(() => result.current.flush())
    expect(save).not.toHaveBeenCalled()
  })

  it("is idempotent across repeated calls", () => {
    const { result } = setup(600)
    act(() => result.current.queue(0.4, "page:4"))
    act(() => {
      result.current.flush()
      result.current.flush()
      result.current.flush()
    })
    expect(save).toHaveBeenCalledOnce()
  })

  it("queues again normally after a flush", () => {
    const { result } = setup(600)
    act(() => result.current.queue(0.4, "page:4"))
    act(() => result.current.flush())
    act(() => result.current.queue(0.9, "page:9"))
    act(() => void vi.advanceTimersByTime(600))
    expect(save).toHaveBeenCalledTimes(2)
    expect(save).toHaveBeenLastCalledWith(0.9, "page:9")
  })
})

describe("unmount", () => {
  // A short reading session can end before any debounce window closes.
  it("saves a pending position when the reader closes", () => {
    const { result, unmount } = setup(600)
    act(() => result.current.queue(0.7, "page:7"))
    unmount()
    expect(save).toHaveBeenCalledExactlyOnceWith(0.7, "page:7")
  })

  it("does not save when nothing is pending", () => {
    const { unmount } = setup(600)
    unmount()
    expect(save).not.toHaveBeenCalled()
  })

  it("does not save twice when the position was already flushed", () => {
    const { result, unmount } = setup(600)
    act(() => result.current.queue(0.7, "page:7"))
    act(() => result.current.flush())
    unmount()
    expect(save).toHaveBeenCalledOnce()
  })

  it("does not fire a stale timer after unmount", () => {
    const { result, unmount } = setup(600)
    act(() => result.current.queue(0.7, "page:7"))
    unmount()
    save.mockReset()
    vi.advanceTimersByTime(5000)
    expect(save).not.toHaveBeenCalled()
  })
})

describe("identity", () => {
  // The shells pass queue straight into child reader props. A new function
  // identity every render would retrigger their effects on each page turn.
  it("keeps queue and flush stable across re-renders", () => {
    const { result, rerender } = setup(600)
    const first = { queue: result.current.queue, flush: result.current.flush }
    rerender()
    expect(result.current.queue).toBe(first.queue)
    expect(result.current.flush).toBe(first.flush)
  })

  // Putting save in the unmount effect's deps makes React run the cleanup on
  // every render where its identity changes — mid-session, with a position
  // still held, that ships the position early and through the stale callback.
  it("does not save when the save callback identity changes mid-session", () => {
    const newer = vi.fn()
    const { result, rerender } = renderHook(
      ({ fn }) => useReadingPosition({ save: fn, debounceMs: 600 }),
      { initialProps: { fn: save } }
    )
    act(() => result.current.queue(0.2, "page:2"))
    rerender({ fn: newer })
    expect(save).not.toHaveBeenCalled()
    expect(newer).not.toHaveBeenCalled()
  })

  // The reader emits progress on a timer; a save captured at mount would
  // keep posting to whichever book was open when the hook first ran.
  it("uses the newest save callback, not the one captured at mount", () => {
    const newer = vi.fn()
    const { result, rerender } = renderHook(
      ({ fn }) => useReadingPosition({ save: fn, debounceMs: 600 }),
      { initialProps: { fn: save } }
    )
    rerender({ fn: newer })
    act(() => result.current.queue(0.2, "page:2"))
    act(() => void vi.advanceTimersByTime(600))
    expect(save).not.toHaveBeenCalled()
    expect(newer).toHaveBeenCalledExactlyOnceWith(0.2, "page:2")
  })
})
