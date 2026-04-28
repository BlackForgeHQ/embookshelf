import { useState } from "react"
import type { KeyboardEvent } from "react"

import { Icon } from "@/components/Icon"
import { cn } from "@/lib/utils"

export function ChipEditor({
  value,
  onChange,
  placeholder,
  disabled = false,
  suggestions,
}: {
  value: Array<string>
  onChange: (next: Array<string>) => void
  placeholder?: string
  disabled?: boolean
  // Optional one-tap presets — clicking a suggestion appends it.
  suggestions?: ReadonlyArray<string>
}) {
  const [draft, setDraft] = useState("")

  const commit = (raw: string) => {
    const next = raw.trim()
    if (!next) return
    if (value.some((v) => v.toLowerCase() === next.toLowerCase())) return
    onChange([...value, next])
    setDraft("")
  }

  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter" || e.key === ",") {
      e.preventDefault()
      commit(draft)
      return
    }
    if (e.key === "Backspace" && draft === "" && value.length > 0) {
      e.preventDefault()
      onChange(value.slice(0, -1))
    }
  }

  return (
    <div
      className={cn(
        "flex flex-wrap items-center gap-1.5 rounded-[3px] border border-input bg-transparent px-2 py-1.5 min-h-9",
        disabled && "opacity-60",
      )}
    >
      {value.map((chip) => (
        <span
          key={chip}
          className="inline-flex items-center gap-1 rounded-[3px] border border-(--color-rule-soft) bg-(--color-paper-0) px-2 py-0.5 font-mono text-[11px] text-(--color-ink-2)"
        >
          {chip}
          {!disabled && (
            <button
              type="button"
              aria-label={`Remove ${chip}`}
              onClick={() => onChange(value.filter((v) => v !== chip))}
              className="inline-flex items-center justify-center text-(--color-ink-3) hover:text-(--color-accent-ink) cursor-pointer"
            >
              <Icon name="close" size={9} />
            </button>
          )}
        </span>
      ))}
      {!disabled && (
        <input
          type="text"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={onKey}
          onBlur={() => commit(draft)}
          placeholder={value.length === 0 ? placeholder : ""}
          className="min-w-[80px] flex-1 bg-transparent px-1 py-0.5 text-[13px] outline-none"
        />
      )}
      {!disabled && suggestions && suggestions.length > 0 && (
        <div className="basis-full pt-1.5">
          <div className="flex flex-wrap gap-1">
            {suggestions
              .filter(
                (s) => !value.some((v) => v.toLowerCase() === s.toLowerCase()),
              )
              .map((s) => (
                <button
                  key={s}
                  type="button"
                  onClick={() => commit(s)}
                  className="chip cursor-pointer"
                >
                  + {s}
                </button>
              ))}
          </div>
        </div>
      )}
    </div>
  )
}
