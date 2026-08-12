import { useState } from "react"

import type { ReadingPreferences } from "@/lib/readingPreferences"
import { Card, Field, Select, Toggle } from "@/components/SettingsShared"
import {
  loadReadingPreferences,
  saveReadingPreferences,
} from "@/lib/readingPreferences"

export function ReadingPreferencesPanel() {
  // Lazy initializer reads from localStorage exactly once on mount,
  // avoiding the useEffect→setState handshake (set-state-in-effect)
  // and a throwaway first render with defaults.
  const [prefs, setPrefs] = useState<ReadingPreferences>(() =>
    loadReadingPreferences()
  )
  const [saved, setSaved] = useState(false)

  const update = <TKey extends keyof ReadingPreferences>(
    key: TKey,
    value: ReadingPreferences[TKey]
  ) => {
    const next = { ...prefs, [key]: value }
    setPrefs(next)
    saveReadingPreferences(next)
    setSaved(true)
    window.setTimeout(() => setSaved(false), 1200)
  }

  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 8 }}>
        Reading preferences
      </h2>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: "italic" }}>
        Stored locally in this browser. The reader picks them up on next open.
        {saved && (
          <span style={{ marginLeft: 8, color: "var(--color-ok-ink)" }}>
            ✓ saved
          </span>
        )}
      </p>

      <Card>
        <Field label="Theme">
          <Select
            value={prefs.theme}
            onChange={(v) => update("theme", v as ReadingPreferences["theme"])}
            options={[
              { value: "light", label: "Light (paper)" },
              { value: "sepia", label: "Sepia" },
              { value: "dark", label: "Dark" },
            ]}
          />
        </Field>

        <Field label="Font family">
          <Select
            value={prefs.fontFamily}
            onChange={(v) =>
              update("fontFamily", v as ReadingPreferences["fontFamily"])
            }
            options={[
              { value: "serif", label: "Serif (default)" },
              { value: "sans", label: "Sans-serif" },
              { value: "mono", label: "Monospace" },
            ]}
          />
        </Field>

        <Field label={`Font size: ${prefs.fontSize}px`}>
          <input
            type="range"
            min={14}
            max={24}
            step={1}
            value={prefs.fontSize}
            onChange={(e) => update("fontSize", Number(e.target.value))}
            style={{ width: "100%" }}
          />
        </Field>

        <Field label={`Line height: ${prefs.lineHeight.toFixed(2)}`}>
          <input
            type="range"
            min={1.2}
            max={2.0}
            step={0.05}
            value={prefs.lineHeight}
            onChange={(e) => update("lineHeight", Number(e.target.value))}
            style={{ width: "100%" }}
          />
        </Field>

        <Toggle
          label="Record reading sessions"
          hint="Progress ticks feed the Stats dashboard heatmap."
          checked={prefs.trackSessions}
          onChange={(v) => update("trackSessions", v)}
        />

        <Toggle
          label="Two-page layout on wide screens"
          hint="Splits EPUB rendering into a spread when width allows."
          checked={prefs.twoPage}
          onChange={(v) => update("twoPage", v)}
        />
      </Card>
    </>
  )
}
