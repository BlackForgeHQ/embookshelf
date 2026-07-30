// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"

import { Notice } from "@/components/Notice"

afterEach(cleanup)

describe("Notice", () => {
  it("renders its message", () => {
    render(<Notice>Shelf name already taken.</Notice>)

    expect(screen.getByRole("alert").textContent).toBe(
      "Shelf name already taken."
    )
  })

  // The reason this is a component and not a class. Of the twelve blocks
  // it replaces, four set role="alert" and eight did not, so whether a
  // failed save was announced to a screen reader depended on which
  // developer wrote which panel. It is not a prop.
  it("always announces itself as an alert", () => {
    render(<Notice>Could not save.</Notice>)

    expect(screen.getByRole("alert")).not.toBeNull()
  })

  // The blocks differed on their margins — mb-6 in the BookDrop detail
  // pane, mt-2.5 under the drop zone, mx-5 my-2 in the rail — because
  // spacing belongs to the surrounding layout, not to the notice. Only
  // spacing: the tokens are the component's.
  it("takes layout classes from the caller without losing its own", () => {
    render(<Notice className="mb-6">spaced</Notice>)

    const el = screen.getByRole("alert")
    expect(el.className).toContain("mb-6")
    expect(el.className).toContain("flash")
    expect(el.className).toContain("error")
  })

  // login.tsx spelled `.flash.error` out as an inline style *and* also
  // applied the class, and an e2e spec asserts on that selector to check
  // a wrong password keeps you on /login. The class is the look; the
  // inline copy was the fourth idiom.
  it("keeps the .flash.error selector the login spec watches", () => {
    const { container } = render(<Notice>Invalid credentials.</Notice>)

    expect(container.querySelector(".flash.error")).not.toBeNull()
  })

  it("renders markup, not just a string", () => {
    render(
      <Notice>
        <strong>4</strong> failed
      </Notice>
    )

    expect(screen.getByRole("alert").textContent).toBe("4 failed")
  })
})
