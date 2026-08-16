// @vitest-environment jsdom
import { fireEvent, render, screen, cleanup } from "@testing-library/react"
import { afterEach, expect, it, vi } from "vitest"

import { Panel, SaveRow } from "@/components/settings/Panel"

afterEach(cleanup)

it("gates the body on loading with the one loading state", () => {
  const { rerender } = render(
    <Panel title="Email delivery" loading>
      <p>body</p>
    </Panel>
  )
  expect(screen.getByRole("status")).toBeTruthy()
  expect(screen.queryByText("body")).toBeNull()

  rerender(
    <Panel title="Email delivery">
      <p>body</p>
    </Panel>
  )
  expect(screen.getByText("body")).toBeTruthy()
})

// Revert has been implemented on the draft since the hook was written;
// the scaffold is the first surface to offer it (#354): visible only
// while dirty, and it calls the draft's own revert.
it("offers Revert only on a dirty draft, and wires it through", () => {
  const revert = vi.fn()
  const { rerender } = render(
    <SaveRow draft={{ saving: false, dirty: false, revert }} />
  )
  expect(screen.queryByText("Revert")).toBeNull()

  rerender(<SaveRow draft={{ saving: false, dirty: true, revert }} />)
  fireEvent.click(screen.getByText("Revert"))
  expect(revert).toHaveBeenCalledOnce()

  // Mid-save, neither button invites a second decision.
  rerender(<SaveRow draft={{ saving: true, dirty: true, revert }} />)
  expect(screen.queryByText("Revert")).toBeNull()
  expect(screen.getByText("Saving…")).toBeTruthy()
})

it("save is a submit button inside a form, a click handler outside one", () => {
  const onSave = vi.fn()
  render(<SaveRow draft={{ saving: false, dirty: false, revert: vi.fn() }} onSave={onSave} label="Save all" />)
  fireEvent.click(screen.getByText("Save all"))
  expect(onSave).toHaveBeenCalledOnce()
})
