import { describe, expect, it } from "vitest"
import {
  validateField,
  validateIsbn10,
  validateIsbn13,
  validatePages,
  validateYear,
} from "../metadata-validators"

describe("validateIsbn13", () => {
  it("accepts empty string (field is optional)", () => {
    expect(validateIsbn13("")).toBeNull()
  })
  it("accepts 13 raw digits", () => {
    expect(validateIsbn13("9780132350884")).toBeNull()
  })
  it("accepts hyphenated 978-XXXXXXXXXX shape", () => {
    expect(validateIsbn13("978-0132350884")).toBeNull()
  })
  it("rejects 12 digits", () => {
    expect(validateIsbn13("978013235088")).toMatch(/13 digits/i)
  })
  it("rejects letters", () => {
    expect(validateIsbn13("978013235088X")).toMatch(/digits/i)
  })
})

describe("validateIsbn10", () => {
  it("accepts empty", () => {
    expect(validateIsbn10("")).toBeNull()
  })
  it("accepts 10 chars (digits + optional trailing X)", () => {
    expect(validateIsbn10("0306406152")).toBeNull()
    expect(validateIsbn10("020161622X")).toBeNull()
  })
  it("rejects 9 chars", () => {
    expect(validateIsbn10("030640615")).toMatch(/10 characters/i)
  })
})

describe("validateYear", () => {
  const currentYear = new Date().getFullYear()
  it("accepts empty", () => {
    expect(validateYear("")).toBeNull()
  })
  it("accepts 4-digit year in range", () => {
    expect(validateYear("1984")).toBeNull()
    expect(validateYear(String(currentYear + 1))).toBeNull()
  })
  it("rejects out-of-range", () => {
    expect(validateYear("1399")).toMatch(/between/i)
    expect(validateYear(String(currentYear + 2))).toMatch(/between/i)
  })
  it("rejects non-numeric", () => {
    expect(validateYear("nineteen")).toMatch(/year/i)
  })
})

describe("validatePages", () => {
  it("accepts empty", () => {
    expect(validatePages("")).toBeNull()
  })
  it("accepts positive integer", () => {
    expect(validatePages("320")).toBeNull()
  })
  it("rejects zero / negative / decimals", () => {
    expect(validatePages("0")).toMatch(/positive/i)
    expect(validatePages("-5")).toMatch(/positive/i)
    expect(validatePages("3.5")).toMatch(/integer/i)
  })
})

describe("validateField dispatcher", () => {
  it("dispatches by field name", () => {
    expect(validateField("isbn13", "9780132350884")).toBeNull()
    expect(validateField("isbn13", "abc")).not.toBeNull()
    expect(validateField("year", "1984")).toBeNull()
    expect(validateField("pages", "abc")).not.toBeNull()
  })
  it("returns null for unknown fields (no validation)", () => {
    expect(validateField("title", "anything")).toBeNull()
  })
})
