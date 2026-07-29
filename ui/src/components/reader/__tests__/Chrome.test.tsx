// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import {
  BookmarkButton,
  ChromeRestoreButton,
  ExitButton,
  ReaderContainer,
  ReaderFooter,
  ReaderHeader,
} from "@/components/reader/Chrome"

const toastInfo = vi.hoisted(() => vi.fn())
vi.mock("sonner", () => ({ toast: { info: toastInfo } }))

afterEach(() => {
  cleanup()
  toastInfo.mockClear()
})

describe("ReaderContainer", () => {
  // The background is the one thing that varies between the three
  // shells, which is the whole reason it is a prop and the rest is not.
  it("takes its background from the caller and fixes the rest", () => {
    render(
      <ReaderContainer background="var(--color-paper-2)">
        <span data-testid="surface" />
      </ReaderContainer>
    )

    const surface = screen.getByTestId("surface").parentElement
    expect(surface).not.toBeNull()
    expect(surface?.style.background).toBe("var(--color-paper-2)")
    expect(surface?.style.position).toBe("fixed")
    expect(surface?.style.zIndex).toBe("200")
    expect(surface?.style.flexDirection).toBe("column")
  })
})

describe("ReaderHeader", () => {
  // Deliberately unconditional: two shells gate it on `chromeVisible`
  // and the audio shell has no such state, so the decision stays with
  // whoever mounts it.
  it("renders whatever the shell puts in the strip", () => {
    render(
      <ReaderHeader>
        <span data-testid="tool" />
      </ReaderHeader>
    )

    expect(screen.getByTestId("tool")).not.toBeNull()
  })
})

describe("ExitButton", () => {
  it("calls back so the shell can flush its position before navigating", () => {
    const onExit = vi.fn()
    render(<ExitButton onExit={onExit} />)

    screen.getByRole("button").click()

    expect(onExit).toHaveBeenCalledTimes(1)
  })
})

describe("BookmarkButton", () => {
  it("hands the caller's locator back untouched", () => {
    const onBookmark = vi.fn()
    render(
      <BookmarkButton
        locator={{ kind: "page", page: 12 }}
        pending={false}
        onBookmark={onBookmark}
      />
    )

    screen.getByLabelText("Bookmark").click()

    expect(onBookmark).toHaveBeenCalledWith({ kind: "page", page: 12 })
    expect(toastInfo).not.toHaveBeenCalled()
  })

  // The text shell mounts before its renderer reports a position, so the
  // button is reachable with nothing to point at. Saying so is better
  // than writing a bookmark to the start of the book.
  it("explains itself instead of bookmarking a position it does not have", () => {
    const onBookmark = vi.fn()
    render(
      <BookmarkButton locator={null} pending={false} onBookmark={onBookmark} />
    )

    screen.getByLabelText("Bookmark").click()

    expect(onBookmark).not.toHaveBeenCalled()
    expect(toastInfo).toHaveBeenCalledWith(
      "Open the book first, then bookmark."
    )
  })

  it("is disabled while a write is in flight", () => {
    const onBookmark = vi.fn()
    render(
      <BookmarkButton
        locator={{ kind: "time", seconds: 90 }}
        pending
        onBookmark={onBookmark}
      />
    )

    const button = screen.getByLabelText("Bookmark") as HTMLButtonElement
    expect(button.disabled).toBe(true)
    button.click()
    expect(onBookmark).not.toHaveBeenCalled()
  })
})

describe("ReaderFooter", () => {
  it("frames the labels and the bar the shell supplies", () => {
    render(
      <ReaderFooter
        onPrev={() => {}}
        onNext={() => {}}
        leftLabel="p.7"
        rightLabel="p.240"
      >
        <span data-testid="bar" />
      </ReaderFooter>
    )

    expect(screen.getByText("p.7")).not.toBeNull()
    expect(screen.getByText("p.240")).not.toBeNull()
    // The bar arrives as children so the footer never has to know which
    // shell's bar is seekable — slice 1 already owns that.
    expect(screen.getByTestId("bar")).not.toBeNull()
  })

  it("turns pages through the shell's imperative handle", () => {
    const onPrev = vi.fn()
    const onNext = vi.fn()
    render(
      <ReaderFooter
        onPrev={onPrev}
        onNext={onNext}
        leftLabel="—"
        rightLabel="—"
      >
        <span />
      </ReaderFooter>
    )

    screen.getByText("Prev").click()
    screen.getByText("Next").click()

    expect(onPrev).toHaveBeenCalledTimes(1)
    expect(onNext).toHaveBeenCalledTimes(1)
  })
})

describe("ChromeRestoreButton", () => {
  it("restores through the caller, which owns the chrome state", () => {
    const onRestore = vi.fn()
    render(<ChromeRestoreButton onRestore={onRestore} />)

    screen.getByRole("button").click()

    expect(onRestore).toHaveBeenCalledTimes(1)
  })
})
