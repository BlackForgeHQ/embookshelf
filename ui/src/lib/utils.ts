import { clsx } from "clsx"
import { twMerge } from "tailwind-merge"
import type { ClassValue } from "clsx"

export function cn(...inputs: Array<ClassValue>) {
  return twMerge(clsx(inputs))
}

// slugify mirrors the Go slugify in internal/service/library.go.
// Lowercase ASCII alphanumerics pass through; everything else becomes
// a single '-'; leading/trailing dashes are trimmed.
export function slugify(s: string): string {
  const lower = s.trim().toLowerCase()
  let dash = true
  let out = ""
  for (const ch of lower) {
    if ((ch >= "a" && ch <= "z") || (ch >= "0" && ch <= "9")) {
      out += ch
      dash = false
    } else if (!dash) {
      out += "-"
      dash = true
    }
  }
  return out.replace(/^-+|-+$/g, "")
}
