import { Fragment } from "react"

import { Icon } from "./Icon"
import { LibrarySearchCombobox } from "./LibrarySearchCombobox"

import type { ReactNode } from "react"
import { Input } from "@/components/ui/input"

type SearchVariant = "input" | "command"

type TopBarProps = {
  title: ReactNode
  subtitle?: ReactNode
  search?: string
  setSearch?: (value: string) => void
  searchVariant?: SearchVariant
  commandHint?: boolean
  right?: ReactNode
  crumbs?: Array<string>
}

// Top bar — sticky header above each main view. Matches the prototype's
// padding + sticky behavior so sidebar scroll and crumb layout line up.
export function TopBar({
  title,
  subtitle,
  search,
  setSearch,
  searchVariant = "input",
  commandHint = true,
  right,
  crumbs,
}: TopBarProps) {
  return (
    <div
      style={{
        padding: "18px 32px 14px",
        borderBottom: "1px solid var(--color-rule-soft)",
        background: "var(--color-paper-1)",
        position: "sticky",
        top: 0,
        zIndex: 10,
      }}
    >
      {crumbs && crumbs.length > 0 && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 6,
            marginBottom: 8,
          }}
        >
          {crumbs.map((c, i) => (
            <Fragment key={`${i}-${c}`}>
              {i > 0 && (
                <Icon name="chevron-right" size={12} className="mono" />
              )}
              <span
                className="t-micro"
                style={{
                  color:
                    i === crumbs.length - 1
                      ? "var(--color-ink-2)"
                      : "var(--color-ink-3)",
                }}
              >
                {c}
              </span>
            </Fragment>
          ))}
        </div>
      )}
      <div style={{ display: "flex", alignItems: "flex-end", gap: 24 }}>
        <div className="grow">
          <h1 className="t-h1" style={{ fontWeight: 500 }}>
            {title}
          </h1>
          {subtitle && (
            <div
              style={{
                color: "var(--color-ink-3)",
                fontSize: 14,
                marginTop: 4,
                fontStyle: "italic",
              }}
            >
              {subtitle}
            </div>
          )}
        </div>

        {setSearch && searchVariant === "command" && (
          <LibrarySearchCombobox
            value={search ?? ""}
            onSearchChange={setSearch}
          />
        )}

        {setSearch && searchVariant === "input" && (
          <div style={{ position: "relative", width: 280 }}>
            <Input
              placeholder="Search library…"
              value={search ?? ""}
              onChange={(e) => setSearch(e.target.value)}
              className="pl-8"
            />
            <div
              style={{
                position: "absolute",
                left: 10,
                top: "50%",
                transform: "translateY(-50%)",
                color: "var(--color-ink-3)",
                pointerEvents: "none",
              }}
            >
              <Icon name="search" size={14} />
            </div>
          </div>
        )}

        {commandHint && (
          <button
            type="button"
            onClick={() =>
              window.dispatchEvent(new CustomEvent("embookshelf:open-command"))
            }
            className="inline-flex h-9 items-center gap-2 rounded-md border px-3 text-xs text-(--color-ink-3) hover:text-(--color-ink-1)"
            aria-label="Open command palette"
          >
            <Icon name="search" size={12} />
            <span>Search</span>
            <kbd className="rounded bg-(--color-paper-2) px-1.5 py-0.5 font-mono text-[10px]">
              ⌘K
            </kbd>
          </button>
        )}

        {right}
      </div>
    </div>
  )
}
