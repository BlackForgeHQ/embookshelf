import { Fragment } from "react"

import { Icon } from "./Icon"

import type { ReactNode } from "react"
import { useSidebar } from "@/components/ui/sidebar"

type TopBarProps = {
  title: ReactNode
  subtitle?: ReactNode
  commandHint?: boolean
  right?: ReactNode
  crumbs?: Array<string>
}

// Top bar — sticky header above each main view. Matches the prototype's
// padding + sticky behavior so sidebar scroll and crumb layout line up.
export function TopBar({
  title,
  subtitle,
  commandHint = true,
  right,
  crumbs,
}: TopBarProps) {
  const { toggleSidebar, state } = useSidebar()
  const collapsed = state === "collapsed"

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
            // biome-ignore lint/suspicious/noArrayIndexKey: the crumb list is positional — two segments of a path may repeat the same label, so position is the only identity
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
        <button
          type="button"
          onClick={toggleSidebar}
          aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          aria-pressed={!collapsed}
          title={`${collapsed ? "Expand" : "Collapse"} sidebar  (⌘B)`}
          className="inline-flex h-8 w-8 items-center justify-center self-center rounded-md text-(--color-ink-3) hover:bg-(--color-paper-3) hover:text-(--color-ink-1) focus-visible:ring-2 focus-visible:ring-(--color-ink-3) focus-visible:outline-none"
        >
          <Icon name="menu" size={14} />
        </button>
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

        {commandHint && (
          <button
            type="button"
            onClick={() =>
              window.dispatchEvent(new CustomEvent("embookshelf:open-command"))
            }
            className="group inline-flex h-7 w-[240px] items-center gap-2 border-b border-(--color-rule) bg-transparent text-[13px] text-(--color-ink-3) transition-colors hover:border-(--color-ink-2) hover:text-(--color-ink-1) focus-visible:border-(--color-ink-1) focus-visible:outline-none"
            aria-label="Open command palette"
          >
            <Icon name="search" size={13} className="text-(--color-ink-3)" />
            <span className="font-serif tracking-tight italic">
              Search the library
            </span>
            <span className="grow" />
            <kbd className="font-mono text-[10px] tracking-[0.08em] text-(--color-ink-3) uppercase">
              ⌘K
            </kbd>
          </button>
        )}

        {right && (
          <div className="flex items-center gap-2.5">
            <span
              aria-hidden
              className="mr-1 hidden h-4 w-px bg-(--color-rule) md:block"
            />
            {right}
          </div>
        )}
      </div>
    </div>
  )
}
