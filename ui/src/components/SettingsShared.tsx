import type { ReactNode } from "react"

import { AvatarFallback, Avatar as ShadcnAvatar } from "@/components/ui/avatar"
import {
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Select as ShadcnSelect,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"

// Shared primitives used across the /settings (per-user) and /admin
// (global) pages. Extracted so the two routes can import them without
// duplicating 100 lines each.

export function Card({ children }: { children: ReactNode }) {
  return (
    <div
      style={{
        padding: 16,
        border: "1px solid var(--color-rule-soft)",
        background: "var(--color-paper-0)",
        marginBottom: 0,
        display: "flex",
        flexDirection: "column",
        gap: 14,
      }}
    >
      {children}
    </div>
  )
}

export function Field({
  label,
  children,
}: {
  label: string
  children: ReactNode
}) {
  return (
    <label style={{ display: "flex", flexDirection: "column", gap: 6 }}>
      <span className="t-label">{label}</span>
      {children}
    </label>
  )
}

export function Select({
  value,
  onChange,
  options,
  disabled,
  triggerClassName = "w-full",
}: {
  value: string
  onChange: (v: string) => void
  options: Array<{ value: string; label: string }>
  disabled?: boolean
  // Callers in inline rows (e.g. Users & roles) should override to
  // `w-[110px]` or similar so the trigger doesn't swallow the flex slot.
  triggerClassName?: string
}) {
  return (
    <ShadcnSelect value={value} onValueChange={onChange} disabled={disabled}>
      <SelectTrigger className={triggerClassName}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {options.map((o) => (
          <SelectItem key={o.value} value={o.value}>
            {o.label}
          </SelectItem>
        ))}
      </SelectContent>
    </ShadcnSelect>
  )
}

export function Toggle({
  label,
  hint,
  checked,
  onChange,
}: {
  label: string
  hint?: string
  checked: boolean
  onChange: (v: boolean) => void
}) {
  return (
    <label
      style={{
        display: "flex",
        alignItems: "center",
        gap: 12,
        padding: "8px 0",
        borderTop: "1px dashed var(--color-rule-soft)",
        cursor: "pointer",
      }}
    >
      <Switch checked={checked} onCheckedChange={onChange} />
      <div className="grow">
        <div style={{ fontSize: 13.5 }}>{label}</div>
        {hint && <div className="t-item-sub">{hint}</div>}
      </div>
    </label>
  )
}

export function DefRow({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div
      style={{
        display: "flex",
        gap: 12,
        padding: "6px 0",
        alignItems: "baseline",
      }}
    >
      <div className="t-label" style={{ width: 160, flexShrink: 0 }}>
        {label}
      </div>
      <div
        style={{
          fontSize: 13.5,
          flex: 1,
          minWidth: 0,
          wordBreak: "break-word",
        }}
      >
        {value}
      </div>
    </div>
  )
}

export function Avatar({
  initials,
  size = 48,
}: {
  initials?: string
  size?: number
}) {
  // Map the few call-site sizes (48 for Account header, 32 for user rows)
  // onto shadcn's `size` variants. Callers that pass an arbitrary number
  // still get a correctly-sized avatar via the inline style override.
  const preset = size <= 28 ? "sm" : size >= 40 ? "lg" : "default"
  return (
    <ShadcnAvatar
      size={preset}
      style={{ width: size, height: size }}
      className="shrink-0"
    >
      <AvatarFallback
        className="bg-(--color-editorial-accent) font-serif font-medium text-(--color-paper-0)"
        style={{ fontSize: Math.round(size * 0.375) }}
      >
        {initials ?? "…"}
      </AvatarFallback>
    </ShadcnAvatar>
  )
}

export function AdminGate({ label }: { label: string }) {
  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 24 }}>
        {label}
      </h2>
      <div className="t-small" style={{ fontStyle: "italic" }}>
        {label} are admin-only.
      </div>
    </>
  )
}

// SettingsShell is the two-column nav layout shared by /settings and
// /admin: left nav of section labels, right content slot. Callers
// control the section list, active section, and the content render.
export function SettingsShell<TKey extends string>({
  sections,
  active,
  onSelect,
  isAdmin,
  children,
}: {
  sections: ReadonlyArray<{ key: TKey; label: string; adminOnly?: boolean }>
  active: TKey
  onSelect: (key: TKey) => void
  isAdmin: boolean
  children: ReactNode
}) {
  return (
    <div
      style={{
        padding: "28px 32px",
        display: "grid",
        gridTemplateColumns: "220px 1fr",
        gap: 40,
        maxWidth: 960,
      }}
    >
      <nav style={{ display: "flex", flexDirection: "column", gap: 2 }}>
        {sections.map((s) => {
          const selected = s.key === active
          const gated = s.adminOnly && !isAdmin
          return (
            <button
              key={s.key}
              type="button"
              onClick={() => onSelect(s.key)}
              disabled={gated}
              style={{
                padding: "8px 12px",
                textAlign: "left",
                background: selected ? "var(--color-paper-3)" : "transparent",
                border: "none",
                cursor: gated ? "default" : "pointer",
                fontFamily: "var(--font-serif)",
                fontSize: 13.5,
                borderLeft: selected
                  ? "2px solid var(--color-accent)"
                  : "2px solid transparent",
                color: gated
                  ? "var(--color-ink-4)"
                  : selected
                    ? "var(--color-ink-1)"
                    : "var(--color-ink-2)",
                opacity: gated ? 0.6 : 1,
              }}
              title={gated ? "Admin-only" : undefined}
            >
              {s.label}
            </button>
          )
        })}
      </nav>

      <div style={{ maxWidth: 640 }}>{children}</div>
    </div>
  )
}
