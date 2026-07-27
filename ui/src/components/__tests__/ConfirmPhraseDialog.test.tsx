// @vitest-environment jsdom
import { useState } from "react"
import { cleanup, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import {
  ConfirmPhraseDialog,
  matchesConfirmPhrase,
} from "../ConfirmPhraseDialog"
import { Switch } from "../ui/switch"

// ---------------------------------------------------------------------------
// The rule itself
// ---------------------------------------------------------------------------
//
// Three dialogs each carried their own version of this comparison and no
// two agreed: the library dialog compared a trimmed input to an untrimmed
// name, the book dialog trimmed both sides, the BookDrop dialog trimmed
// the input against a literal. Whatever the rule is, it is one rule now,
// and this block is where it is stated.

describe("matchesConfirmPhrase", () => {
  it("accepts the phrase typed exactly", () => {
    expect(matchesConfirmPhrase("Sci-Fi", "Sci-Fi")).toBe(true)
  })

  it("forgives whitespace the typist added around the phrase", () => {
    expect(matchesConfirmPhrase("  Sci-Fi ", "Sci-Fi")).toBe(true)
    expect(matchesConfirmPhrase("\tSci-Fi\n", "Sci-Fi")).toBe(true)
  })

  // The defect the unified rule closes. A library named "Sci-Fi " (a
  // trailing space is invisible in the list, and nothing forbids one)
  // could never be deleted: the dialog trimmed what was typed and
  // compared it to the untrimmed name, so no input could ever match.
  it("forgives whitespace carried by the phrase itself", () => {
    expect(matchesConfirmPhrase("Sci-Fi", "Sci-Fi ")).toBe(true)
    expect(matchesConfirmPhrase(" Sci-Fi ", "  Sci-Fi")).toBe(true)
  })

  it("keeps whitespace inside the phrase significant", () => {
    expect(matchesConfirmPhrase("Science  Fiction", "Science Fiction")).toBe(
      false
    )
  })

  it("is case-sensitive — a gate that shrugs at case is a weaker gate", () => {
    expect(matchesConfirmPhrase("sci-fi", "Sci-Fi")).toBe(false)
    expect(matchesConfirmPhrase("BOOKDROP", "bookdrop")).toBe(false)
  })

  it("rejects a partial or padded-out phrase", () => {
    expect(matchesConfirmPhrase("Sci", "Sci-Fi")).toBe(false)
    expect(matchesConfirmPhrase("Sci-Fi!", "Sci-Fi")).toBe(false)
  })

  it("rejects an empty input", () => {
    expect(matchesConfirmPhrase("", "Sci-Fi")).toBe(false)
    expect(matchesConfirmPhrase("   ", "Sci-Fi")).toBe(false)
  })

  // A phrase with nothing in it would otherwise be satisfied by an empty
  // box — the confirm button would arm itself the moment the dialog
  // opened, which is the one outcome this whole gate exists to prevent.
  it("never matches when there is no phrase to type", () => {
    expect(matchesConfirmPhrase("", "")).toBe(false)
    expect(matchesConfirmPhrase("   ", "  ")).toBe(false)
    expect(matchesConfirmPhrase("anything", "")).toBe(false)
  })
})

// ---------------------------------------------------------------------------
// The dialog around the rule
// ---------------------------------------------------------------------------

function typeInto(text: string) {
  const input = screen.getByLabelText(/to confirm/i) as HTMLInputElement
  fireEvent.change(input, { target: { value: text } })
  return input
}

function confirmButton(name: RegExp) {
  return screen.getByRole("button", { name }) as HTMLButtonElement
}

describe("ConfirmPhraseDialog", () => {
  afterEach(cleanup)

  function renderDialog(overrides: { busy?: boolean } = {}) {
    const onConfirm = vi.fn()
    render(
      <ConfirmPhraseDialog
        open
        onOpenChange={() => {}}
        title="Delete library"
        description="Removes Sci-Fi and everything in it."
        phrase="Sci-Fi"
        confirmLabel="Delete library"
        busyLabel="Deleting…"
        busy={overrides.busy ?? false}
        onConfirm={onConfirm}
      />
    )
    return onConfirm
  }

  it("arms the confirm button only once the phrase is typed", () => {
    const onConfirm = renderDialog()
    expect(confirmButton(/Delete library/).disabled).toBe(true)

    typeInto("Sci-F")
    expect(confirmButton(/Delete library/).disabled).toBe(true)

    typeInto("Sci-Fi")
    expect(confirmButton(/Delete library/).disabled).toBe(false)

    fireEvent.click(confirmButton(/Delete library/))
    expect(onConfirm).toHaveBeenCalledTimes(1)
  })

  it("applies the same whitespace forgiveness the rule states", () => {
    renderDialog()
    typeInto("  Sci-Fi  ")
    expect(confirmButton(/Delete library/).disabled).toBe(false)
  })

  it("stays disarmed while the action is in flight", () => {
    renderDialog({ busy: true })
    typeInto("Sci-Fi")
    const button = confirmButton(/Deleting/)
    expect(button.disabled).toBe(true)
    expect(screen.getByRole("button", { name: "Cancel" })).toHaveProperty(
      "disabled",
      true
    )
  })

  it("prompts with the phrase unless the caller words it differently", () => {
    renderDialog()
    expect(screen.getByText("Sci-Fi")).toBeTruthy()
    cleanup()

    render(
      <ConfirmPhraseDialog
        open
        onOpenChange={() => {}}
        title="Delete book"
        description="Permanently remove it."
        phrase="Dune"
        prompt="Type the title to confirm."
        confirmLabel="Delete book"
        busyLabel="Deleting…"
        busy={false}
        onConfirm={() => {}}
      />
    )
    expect(screen.getByLabelText("Type the title to confirm.")).toBeTruthy()
  })

  // The reset-on-close effect used to be copied into every dialog, each
  // with the same four-line comment. It lives here now, so closing a
  // half-typed dialog and reopening it cannot leave a primed button.
  it("forgets what was typed when it closes", () => {
    function Harness() {
      const [open, setOpen] = useState(true)
      return (
        <>
          <button type="button" onClick={() => setOpen((v) => !v)}>
            toggle
          </button>
          <ConfirmPhraseDialog
            open={open}
            onOpenChange={setOpen}
            title="Delete library"
            description="Removes Sci-Fi."
            phrase="Sci-Fi"
            confirmLabel="Delete library"
            busyLabel="Deleting…"
            busy={false}
            onConfirm={() => {}}
          />
        </>
      )
    }
    render(<Harness />)
    typeInto("Sci-Fi")
    expect(confirmButton(/Delete library/).disabled).toBe(false)

    fireEvent.click(screen.getByText("toggle"))
    fireEvent.click(screen.getByText("toggle"))

    expect(screen.getByLabelText(/to confirm/i)).toHaveProperty("value", "")
    expect(confirmButton(/Delete library/).disabled).toBe(true)
  })

  // The extra-switch slot. The library dialog's S3 purge toggle is the
  // existing case: its value has to reach the confirm handler, and it has
  // to reset with the rest of the dialog rather than surviving a close.
  it("carries an extra switch's value into the confirm handler", () => {
    const onConfirm = vi.fn()
    render(
      <ConfirmPhraseDialog<boolean>
        open
        onOpenChange={() => {}}
        title="Delete library"
        description="Removes Sci-Fi."
        phrase="Sci-Fi"
        confirmLabel="Delete library"
        busyLabel="Deleting…"
        busy={false}
        extras={{
          initial: false,
          render: (purge, setPurge) => (
            <label>
              <Switch
                checked={purge}
                onCheckedChange={(v) => setPurge(Boolean(v))}
              />
              <span>Also delete every object in the S3 bucket prefix</span>
            </label>
          ),
        }}
        onConfirm={onConfirm}
      />
    )

    typeInto("Sci-Fi")
    fireEvent.click(confirmButton(/Delete library/))
    expect(onConfirm).toHaveBeenLastCalledWith(false)

    fireEvent.click(screen.getByRole("switch"))
    fireEvent.click(confirmButton(/Delete library/))
    expect(onConfirm).toHaveBeenLastCalledWith(true)
  })

  it("resets an extra switch when it closes", () => {
    function Harness() {
      const [open, setOpen] = useState(true)
      return (
        <>
          <button type="button" onClick={() => setOpen((v) => !v)}>
            toggle
          </button>
          <ConfirmPhraseDialog<boolean>
            open={open}
            onOpenChange={setOpen}
            title="Delete library"
            description="Removes Sci-Fi."
            phrase="Sci-Fi"
            confirmLabel="Delete library"
            busyLabel="Deleting…"
            busy={false}
            extras={{
              initial: false,
              render: (purge, setPurge) => (
                <Switch
                  checked={purge}
                  onCheckedChange={(v) => setPurge(Boolean(v))}
                />
              ),
            }}
            onConfirm={() => {}}
          />
        </>
      )
    }
    render(<Harness />)
    fireEvent.click(screen.getByRole("switch"))
    expect(screen.getByRole("switch").getAttribute("data-state")).toBe(
      "checked"
    )

    fireEvent.click(screen.getByText("toggle"))
    fireEvent.click(screen.getByText("toggle"))

    expect(screen.getByRole("switch").getAttribute("data-state")).toBe(
      "unchecked"
    )
  })
})
