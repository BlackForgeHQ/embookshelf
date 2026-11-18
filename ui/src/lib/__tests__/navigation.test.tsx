// @vitest-environment jsdom
import { cleanup, render, renderHook } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import {
  navigationBlocked,
  useAppRouter,
  useNavigationGuard,
} from "../navigation"

const push = vi.fn()
const replace = vi.fn()
const nextRouter = { push, replace, back: vi.fn(), refresh: vi.fn() }

vi.mock("next/navigation", () => ({
  useRouter: () => nextRouter,
}))

function Guarded({ block }: { block: () => boolean }) {
  useNavigationGuard(block)
  return (
    <>
      <a href="/notebook">elsewhere</a>
      <a href="/notebook" target="_blank" rel="noopener">
        new tab
      </a>
      <a href="https://example.com/">external</a>
    </>
  )
}

function click(text: string, init: MouseEventInit = {}) {
  const anchor = [...document.querySelectorAll("a")].find(
    (a) => a.textContent === text
  )!
  const event = new MouseEvent("click", {
    bubbles: true,
    cancelable: true,
    button: 0,
    ...init,
  })
  anchor.dispatchEvent(event)
  return event
}

beforeEach(() => {
  push.mockReset()
  replace.mockReset()
})
afterEach(() => cleanup())

describe("useAppRouter", () => {
  it("navigates when nothing is guarding", () => {
    const { result } = renderHook(() => useAppRouter())
    result.current.push("/library")
    result.current.replace("/login")
    expect(push).toHaveBeenCalledWith("/library", undefined)
    expect(replace).toHaveBeenCalledWith("/login", undefined)
  })

  it("is refused while a guard blocks", () => {
    render(<Guarded block={() => true} />)
    const { result } = renderHook(() => useAppRouter())
    result.current.push("/library")
    result.current.replace("/library")
    expect(push).not.toHaveBeenCalled()
    expect(replace).not.toHaveBeenCalled()
  })

  it("keeps one identity across renders, as Next's router does", () => {
    const { result, rerender } = renderHook(() => useAppRouter())
    const first = result.current
    rerender()
    expect(result.current).toBe(first)
  })
})

describe("useNavigationGuard", () => {
  it("stops an in-app link click before the router sees it", () => {
    const block = vi.fn(() => true)
    render(<Guarded block={block} />)
    expect(click("elsewhere").defaultPrevented).toBe(true)
    expect(block).toHaveBeenCalledOnce()
  })

  it("lets the click through when the guard allows it", () => {
    render(<Guarded block={() => false} />)
    expect(click("elsewhere").defaultPrevented).toBe(false)
  })

  it("leaves new-tab, modified and external clicks alone", () => {
    const block = vi.fn(() => true)
    render(<Guarded block={block} />)
    click("new tab")
    click("elsewhere", { metaKey: true })
    click("external")
    expect(block).not.toHaveBeenCalled()
  })

  it("reads the latest closure without re-registering", () => {
    let dirty = false
    const { rerender } = render(<Guarded block={() => dirty} />)
    expect(navigationBlocked()).toBe(false)
    dirty = true
    rerender(<Guarded block={() => dirty} />)
    expect(navigationBlocked()).toBe(true)
  })

  it("releases on unmount", () => {
    const { unmount } = render(<Guarded block={() => true} />)
    unmount()
    expect(navigationBlocked()).toBe(false)
    const { result } = renderHook(() => useAppRouter())
    result.current.push("/library")
    expect(push).toHaveBeenCalledOnce()
  })
})
