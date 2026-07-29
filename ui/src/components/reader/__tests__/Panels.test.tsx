// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import type { Annotation } from "@/api/annotations"
import { NotesPanel } from "@/components/reader/Panels"

afterEach(cleanup)

function annotation(over: Partial<Annotation> = {}): Annotation {
  return {
    id: "a1",
    bookId: "b1",
    color: "yellow",
    createdAt: "2026-07-29T00:00:00Z",
    updatedAt: "2026-07-29T00:00:00Z",
    ...over,
  }
}

// The panel is the one piece both paged shells kept when they split, so
// what used to be `book.format === "PDF" && …` inside it is now a prop.
// These tests are about those props: the panel must not decide anything
// a format owns, and it must still decide everything neither does.
describe("NotesPanel", () => {
  const noGoTo = () => null

  it("shows the caller's empty-state copy, not one of its own", () => {
    render(
      <NotesPanel
        annotations={[]}
        loading={false}
        emptyText="Select text in the page to highlight or annotate."
        renderGoTo={noGoTo}
        onDelete={() => {}}
        deleting={false}
      />
    )

    expect(
      screen.getByText("Select text in the page to highlight or annotate.")
    ).not.toBeNull()
  })

  // Loading and empty are different states: an empty list mid-fetch must
  // not tell the reader there is nothing to see.
  it("says nothing about emptiness while the list is loading", () => {
    render(
      <NotesPanel
        annotations={[]}
        loading
        emptyText="No notes yet."
        renderGoTo={noGoTo}
        onDelete={() => {}}
        deleting={false}
      />
    )

    expect(screen.getByText("Loading…")).not.toBeNull()
    expect(screen.queryByText("No notes yet.")).toBeNull()
  })

  // The PDF shell's "new note on page N" hangs here. It sits above the
  // list rather than below it, which is where the shell put it before.
  it("puts the shell's action between the heading and the list", () => {
    render(
      <NotesPanel
        annotations={[annotation({ note: "a note" })]}
        loading={false}
        emptyText="No notes yet."
        renderGoTo={noGoTo}
        onDelete={() => {}}
        deleting={false}
      >
        <button type="button" data-testid="new-note">
          New note on page 7
        </button>
      </NotesPanel>
    )

    const action = screen.getByTestId("new-note")
    const heading = screen.getByText("Notes on this book")
    expect(action.previousElementSibling).toBe(heading)
  })

  // Only the shell holds the imperative handle, and the two handles take
  // different arguments — so the button is the shell's to render, and it
  // gets the decoded locator rather than the raw token.
  it("hands the decoded locator to the shell's go-to renderer", () => {
    const renderGoTo = vi.fn(() => null)
    render(
      <NotesPanel
        annotations={[annotation({ locator: "page:12", note: "n" })]}
        loading={false}
        emptyText=""
        renderGoTo={renderGoTo}
        onDelete={() => {}}
        deleting={false}
      />
    )

    expect(renderGoTo).toHaveBeenCalledWith({ kind: "page", page: 12 })
  })

  it("does not ask for a go-to button when there is no locator to go to", () => {
    const renderGoTo = vi.fn(() => null)
    render(
      <NotesPanel
        annotations={[annotation({ note: "locator-less note" })]}
        loading={false}
        emptyText=""
        renderGoTo={renderGoTo}
        onDelete={() => {}}
        deleting={false}
      />
    )

    expect(renderGoTo).not.toHaveBeenCalled()
  })

  // A CFI reduces to "EPUB", which says nothing the reader is not
  // already looking at; a page is worth printing. That is a property of
  // the locator, not of the shell, so it stayed in the panel.
  it("labels a page locator and leaves a CFI unlabelled", () => {
    render(
      <NotesPanel
        annotations={[
          annotation({ id: "p", locator: "page:12", note: "on a page" }),
          annotation({
            id: "c",
            locator: "epubcfi(/6/4!/4/2)",
            selectedText: "in a chapter",
          }),
        ]}
        loading={false}
        emptyText=""
        renderGoTo={noGoTo}
        onDelete={() => {}}
        deleting={false}
      />
    )

    expect(screen.getByText(/Note · p\.12/)).not.toBeNull()
    expect(screen.getByText("Highlight")).not.toBeNull()
  })

  it("deletes through the shell, which owns the mutation", () => {
    const onDelete = vi.fn()
    const a = annotation({ note: "n" })
    render(
      <NotesPanel
        annotations={[a]}
        loading={false}
        emptyText=""
        renderGoTo={noGoTo}
        onDelete={onDelete}
        deleting={false}
      />
    )

    screen.getByLabelText("Delete").click()

    expect(onDelete).toHaveBeenCalledWith(a)
  })

  it("disables delete while one is in flight", () => {
    const onDelete = vi.fn()
    render(
      <NotesPanel
        annotations={[annotation({ note: "n" })]}
        loading={false}
        emptyText=""
        renderGoTo={noGoTo}
        onDelete={onDelete}
        deleting
      />
    )

    const button = screen.getByLabelText("Delete") as HTMLButtonElement
    expect(button.disabled).toBe(true)
    button.click()
    expect(onDelete).not.toHaveBeenCalled()
  })
})
