// Pure validators — return null on success, string error on failure.
// Empty string is always valid (every field on the edit form is optional).

export function validateIsbn13(raw: string): string | null {
  const v = raw.trim()
  if (v === "") return null
  // Accept either 13 raw digits or "978-XXXXXXXXXX" / "979-XXXXXXXXXX"
  const digits = v.replace(/-/g, "")
  if (!/^\d{13}$/.test(digits)) return "ISBN-13 must be 13 digits."
  return null
}

export function validateIsbn10(raw: string): string | null {
  const v = raw.trim()
  if (v === "") return null
  const digits = v.replace(/-/g, "")
  if (!/^\d{9}[\dX]$/.test(digits)) return "ISBN-10 must be 10 characters."
  return null
}

export function validateYear(raw: string): string | null {
  const v = raw.trim()
  if (v === "") return null
  if (!/^\d{4}$/.test(v)) return "Year must be a 4-digit number."
  const n = Number.parseInt(v, 10)
  const max = new Date().getFullYear() + 1
  if (n < 1400 || n > max) return `Year must be between 1400 and ${max}.`
  return null
}

export function validatePages(raw: string): string | null {
  const v = raw.trim()
  if (v === "") return null
  if (!/^\d+$/.test(v)) return "Pages must be a positive integer."
  const n = Number.parseInt(v, 10)
  if (n <= 0) return "Pages must be a positive integer."
  return null
}

export type ValidatableField = "isbn13" | "isbn10" | "year" | "pages"

export function validateField(field: string, value: string): string | null {
  switch (field) {
    case "isbn13":
      return validateIsbn13(value)
    case "isbn10":
      return validateIsbn10(value)
    case "year":
      return validateYear(value)
    case "pages":
      return validatePages(value)
    default:
      return null
  }
}
