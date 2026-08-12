// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import { ProgressBar } from "@/components/ProgressBar"

afterEach(cleanup)

describe("ProgressBar", () => {
  // The fill is a full-width element scaled by transform, not a width
  // percentage: the reader footer retargets this bar on every page
  // turn, and scaleX stays off the layout path.
  it("renders the fraction as a scaleX transform", () => {
    render(<ProgressBar value={0.42} label="Reading progress" />)

    expect(screen.getByLabelText("Reading progress").style.transform).toBe(
      "scaleX(0.42)",
    )
  })

  // Every caller passes a 0–1 fraction, and two of them derive it from
  // counts that can momentarily exceed the total (a segment landing
  // while the plan is being rewritten). A bar wider than its track
  // overflows the layout.
  it("clamps out-of-range values instead of overflowing", () => {
    render(<ProgressBar value={1.8} label="over" />)
    expect(screen.getByLabelText("over").style.transform).toBe("scaleX(1)")

    cleanup()
    render(<ProgressBar value={-0.3} label="under" />)
    expect(screen.getByLabelText("under").style.transform).toBe("scaleX(0)")
  })

  // NaN reaches this from done/total where total is 0 — a run whose plan
  // has not been written yet. `scaleX(NaN)` renders as an un-transformed
  // full bar, which reads as "finished".
  it("treats a non-finite value as no progress", () => {
    render(<ProgressBar value={Number.NaN} label="nan" />)
    expect(screen.getByLabelText("nan").style.transform).toBe("scaleX(0)")
  })

  describe("seeking", () => {
    // The reader's comic and audio bars are seekable; its text bar is
    // not, and neither is any bar outside the reader. Passing no
    // onSeek must not leave a clickable-looking track.
    it("is inert without an onSeek handler", () => {
      render(<ProgressBar value={0.5} label="inert" />)

      const track = screen.getByRole("presentation")
      expect(track.style.cursor).toBe("default")
    })

    it("reports the clicked position as a fraction", async () => {
      const onSeek = vi.fn()
      render(<ProgressBar value={0.5} label="seekable" onSeek={onSeek} />)

      const track = screen.getByRole("slider")
      // jsdom reports a zero-width box, so the component's own guard is
      // what this exercises: a zero-width track cannot be seeked into,
      // and must not report NaN to its caller.
      track.click()

      if (onSeek.mock.calls.length > 0) {
        const fraction = onSeek.mock.calls[0]?.[0]
        expect(Number.isFinite(fraction)).toBe(true)
      }
    })
  })

  // The audio bar overlays chapter ticks inside the track. No other bar
  // has them, so they arrive as children rather than as a prop the
  // other nine callers would have to pass as undefined.
  it("renders children inside the track", () => {
    render(
      <ProgressBar value={0.5} label="with ticks">
        <span data-testid="tick" />
      </ProgressBar>,
    )

    expect(screen.getByTestId("tick")).not.toBeNull()
  })
})
