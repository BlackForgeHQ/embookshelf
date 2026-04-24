// Per-browser reader preferences. Stored in localStorage so they survive a
// refresh without a round trip. The reader route reads them on mount and the
// Settings panel is the canonical editor.

export type ReadingPreferences = {
  theme: "light" | "sepia" | "dark"
  fontFamily: "serif" | "sans" | "mono"
  fontSize: number
  lineHeight: number
  trackSessions: boolean
  twoPage: boolean
}

export const defaultReadingPreferences: ReadingPreferences = {
  theme: "light",
  fontFamily: "serif",
  fontSize: 17,
  lineHeight: 1.55,
  trackSessions: true,
  twoPage: false,
}

const KEY = "embookshelf.readingPreferences"

export function loadReadingPreferences(): ReadingPreferences {
  if (typeof window === "undefined") return defaultReadingPreferences
  try {
    const raw = window.localStorage.getItem(KEY)
    if (!raw) return defaultReadingPreferences
    const parsed = JSON.parse(raw) as Partial<ReadingPreferences>
    return { ...defaultReadingPreferences, ...parsed }
  } catch {
    return defaultReadingPreferences
  }
}

export function saveReadingPreferences(prefs: ReadingPreferences): void {
  if (typeof window === "undefined") return
  window.localStorage.setItem(KEY, JSON.stringify(prefs))
}
