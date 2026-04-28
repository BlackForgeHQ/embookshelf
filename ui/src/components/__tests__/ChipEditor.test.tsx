// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import { ChipEditor } from "../metadata/ChipEditor"

afterEach(cleanup)

describe("ChipEditor", () => {
  it("renders existing chips", () => {
    render(<ChipEditor value={["fiction", "memoir"]} onChange={() => {}} />)
    expect(screen.getByText("fiction")).toBeTruthy()
    expect(screen.getByText("memoir")).toBeTruthy()
  })

  it("adds a chip on Enter and clears the input", () => {
    const onChange = vi.fn()
    render(<ChipEditor value={["fiction"]} onChange={onChange} />)
    const input = screen.getByRole("textbox")
    fireEvent.change(input, { target: { value: "essays" } })
    fireEvent.keyDown(input, { key: "Enter" })
    expect(onChange).toHaveBeenCalledWith(["fiction", "essays"])
  })

  it("adds a chip on comma", () => {
    const onChange = vi.fn()
    render(<ChipEditor value={[]} onChange={onChange} />)
    const input = screen.getByRole("textbox")
    fireEvent.change(input, { target: { value: "history" } })
    fireEvent.keyDown(input, { key: "," })
    expect(onChange).toHaveBeenCalledWith(["history"])
  })

  it("does not add duplicates (case-insensitive)", () => {
    const onChange = vi.fn()
    render(<ChipEditor value={["Fiction"]} onChange={onChange} />)
    const input = screen.getByRole("textbox")
    fireEvent.change(input, { target: { value: "fiction" } })
    fireEvent.keyDown(input, { key: "Enter" })
    expect(onChange).not.toHaveBeenCalled()
  })

  it("trims whitespace", () => {
    const onChange = vi.fn()
    render(<ChipEditor value={[]} onChange={onChange} />)
    const input = screen.getByRole("textbox")
    fireEvent.change(input, { target: { value: "  poetry  " } })
    fireEvent.keyDown(input, { key: "Enter" })
    expect(onChange).toHaveBeenCalledWith(["poetry"])
  })

  it("removes the last chip on Backspace when input is empty", () => {
    const onChange = vi.fn()
    render(<ChipEditor value={["fiction", "memoir"]} onChange={onChange} />)
    const input = screen.getByRole("textbox")
    fireEvent.keyDown(input, { key: "Backspace" })
    expect(onChange).toHaveBeenCalledWith(["fiction"])
  })

  it("does not remove on Backspace when input has text", () => {
    const onChange = vi.fn()
    render(<ChipEditor value={["fiction"]} onChange={onChange} />)
    const input = screen.getByRole("textbox")
    fireEvent.change(input, { target: { value: "x" } })
    fireEvent.keyDown(input, { key: "Backspace" })
    expect(onChange).not.toHaveBeenCalled()
  })

  it("removes a specific chip when its × button is clicked", () => {
    const onChange = vi.fn()
    render(<ChipEditor value={["a", "b", "c"]} onChange={onChange} />)
    fireEvent.click(screen.getByLabelText("Remove b"))
    expect(onChange).toHaveBeenCalledWith(["a", "c"])
  })

  it("does not allow editing when disabled", () => {
    const onChange = vi.fn()
    render(<ChipEditor value={["a"]} onChange={onChange} disabled />)
    expect(screen.queryByRole("textbox")).toBeNull()
    expect(screen.queryByLabelText("Remove a")).toBeNull()
  })
})
