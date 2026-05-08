import {  useDeferredValue, useMemo, useState } from "react"
import { iconNames as LUCIDE_ICON_NAMES } from "lucide-react/dynamic"
import type {ReactNode} from "react";

import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import { SHELF_ICON_SUGGESTIONS, ShelfIcon } from "@/components/ShelfIcon"
import { cn } from "@/lib/utils"


const MAX_RESULTS = 200

type Props = {
  value: string
  onChange: (next: string) => void
  trigger?: ReactNode
  className?: string
}

// ShelfIconPicker pops a search-driven grid over lucide's full icon list.
// The catalog is large (~1500), so results are capped at MAX_RESULTS and
// the user is expected to refine via the search box for tail icons.
// Suggestions render up-front so the common case needs zero typing.
export function ShelfIconPicker({ value, onChange, trigger, className }: Props) {
  const [query, setQuery] = useState("")
  const deferred = useDeferredValue(query.trim().toLowerCase())

  const results = useMemo(() => {
    if (deferred === "") {
      return [] as Array<string>
    }
    const out: Array<string> = []
    for (const name of LUCIDE_ICON_NAMES) {
      if (name.includes(deferred)) {
        out.push(name)
        if (out.length >= MAX_RESULTS) break
      }
    }
    return out
  }, [deferred])

  return (
    <Popover>
      <PopoverTrigger asChild>
        {trigger ?? (
          <button
            type="button"
            aria-label="Choose icon"
            title={value}
            className={cn(
              "inline-flex size-7 items-center justify-center rounded-md border border-(--color-paper-3) bg-(--color-paper-1) text-(--color-ink-1) hover:bg-(--color-paper-3)",
              "focus-visible:ring-2 focus-visible:ring-(--color-accent) focus-visible:outline-none",
              className
            )}
          >
            <ShelfIcon name={value} size={14} />
          </button>
        )}
      </PopoverTrigger>
      <PopoverContent className="flex w-80 flex-col gap-3 p-3" align="start">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="shelf-icon-search">Icon</Label>
          <Input
            id="shelf-icon-search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search icons…"
            autoFocus
          />
        </div>
        {deferred === "" && (
          <div className="flex flex-col gap-1.5">
            <div className="t-micro text-(--color-ink-3)">Suggestions</div>
            <IconGrid
              names={SHELF_ICON_SUGGESTIONS}
              value={value}
              onChange={onChange}
            />
          </div>
        )}
        {deferred !== "" && (
          <div className="flex flex-col gap-1.5">
            <div className="t-micro text-(--color-ink-3)">
              {results.length === 0
                ? "No matches"
                : results.length >= MAX_RESULTS
                  ? `${MAX_RESULTS}+ — refine search`
                  : `${results.length} match${results.length === 1 ? "" : "es"}`}
            </div>
            <IconGrid names={results} value={value} onChange={onChange} />
          </div>
        )}
      </PopoverContent>
    </Popover>
  )
}

function IconGrid({
  names,
  value,
  onChange,
}: {
  names: ReadonlyArray<string>
  value: string
  onChange: (next: string) => void
}) {
  return (
    <div
      className="grid max-h-64 grid-cols-8 gap-1 overflow-y-auto"
      style={{ contentVisibility: "auto" }}
    >
      {names.map((name) => {
        const selected = name === value
        return (
          <button
            key={name}
            type="button"
            aria-label={name}
            title={name}
            aria-pressed={selected}
            onClick={() => onChange(name)}
            className={cn(
              "inline-flex size-8 items-center justify-center rounded-sm transition-colors",
              "hover:bg-(--color-paper-3) focus-visible:bg-(--color-paper-3) focus-visible:outline-none",
              selected
                ? "bg-(--color-paper-3) text-(--color-ink-1) ring-1 ring-(--color-ink-1)"
                : "text-(--color-ink-2)"
            )}
          >
            <ShelfIcon name={name} size={16} />
          </button>
        )
      })}
    </div>
  )
}
