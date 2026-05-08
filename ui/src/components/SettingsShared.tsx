import type { ReactNode } from "react"
import { cn } from "@/lib/utils"

import { AvatarFallback, Avatar as ShadcnAvatar } from "@/components/ui/avatar"
import {
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Select as ShadcnSelect,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"

// Shared primitives used across the /settings (admin) and /account
// (per-user) pages. Extracted so the two routes can import them without
// duplicating 100 lines each.

export function Card({
  children,
  className,
}: {
  children: ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        "mb-4 flex flex-col gap-3.5 rounded-lg border border-border bg-card p-4 shadow-sm",
        className
      )}
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
    <label className="flex flex-col gap-1.5">
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
    <label className="flex cursor-pointer items-center gap-3 border-t border-dashed border-border py-2 first:border-0">
      <Switch checked={checked} onCheckedChange={onChange} />
      <div className="grow">
        <div className="mb-1 text-[13.5px] leading-none font-medium">
          {label}
        </div>
        {hint && <div className="text-sm text-muted-foreground">{hint}</div>}
      </div>
    </label>
  )
}

export function DefRow({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex items-baseline gap-3 py-1.5">
      <div className="t-label w-40 shrink-0">{label}</div>
      <div className="min-w-0 flex-1 text-[13.5px] break-words">{value}</div>
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

// NotebookEmpty is the editorial-archival empty-state shell shared across
// settings panels and the library route. Dashed hairline well, decorative
// shelf mark, serif headline, italic 44ch subtitle, optional CTA. Defined
// here so the Libraries / BookDrop / Providers / Library route states stay
// visually identical without each panel re-implementing the markup.
export function NotebookEmpty({
  title,
  body,
  action,
  mark,
  className,
}: {
  title: string
  body: ReactNode
  action?: ReactNode
  // mark overrides the default ShelfMark — pass an alternative SVG when
  // the panel calls for a different metaphor (e.g. inbox for BookDrop).
  mark?: ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        "mt-2 grid place-items-center gap-6 rounded-xl border border-dashed border-rule-soft px-8 py-16 text-center",
        className
      )}
    >
      {mark ?? <ShelfMark />}
      <div className="max-w-[44ch]">
        <h3 className="font-heading text-[22px] tracking-tight">{title}</h3>
        <div className="t-small mt-2 text-(--color-ink-3) italic">{body}</div>
      </div>
      {action}
    </div>
  )
}

// ShelfMark — three uneven shelf bars. Decorative only, used by the
// Libraries panel and as the default mark for NotebookEmpty.
export function ShelfMark() {
  return (
    <svg
      width="84"
      height="56"
      viewBox="0 0 84 56"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.25"
      className="text-(--color-rule)"
      aria-hidden
    >
      <path d="M2 14h80M2 30h80M2 46h80" />
      <path d="M14 4v10M22 4v10M30 7v7M46 18v12M54 22v8M62 18v12M14 34v12M26 34v12M38 38v8" />
    </svg>
  )
}

// InboxMark — open inbox glyph, used for BookDrop "no items processed" empty.
export function InboxMark() {
  return (
    <svg
      width="74"
      height="56"
      viewBox="0 0 74 56"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.25"
      strokeLinecap="round"
      strokeLinejoin="round"
      className="text-(--color-rule)"
      aria-hidden
    >
      <path d="M8 26l8-18h42l8 18" />
      <path d="M8 26v22h58V26" />
      <path d="M8 26h16l4 6h18l4-6h16" />
      <path d="M30 14h14M30 20h14" />
    </svg>
  )
}

// QuillMark — a stylised feather for "no providers detected" / metadata
// empty states.
export function QuillMark() {
  return (
    <svg
      width="60"
      height="60"
      viewBox="0 0 60 60"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.25"
      strokeLinecap="round"
      strokeLinejoin="round"
      className="text-(--color-rule)"
      aria-hidden
    >
      <path d="M48 8c-18 4-30 16-34 32 6 0 12-2 18-6" />
      <path d="M14 40l-4 12 12-4" />
      <path d="M22 28l8 8M26 22l8 8M30 16l8 8" />
    </svg>
  )
}

export function AdminGate({ label }: { label: string }) {
  return (
    <>
      <h2 className="t-h2 mb-6">{label}</h2>
      <div className="t-small text-muted-foreground italic">
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
  sections: ReadonlyArray<{
    key: TKey
    label: string
    adminOnly?: boolean
    badge?: ReactNode
  }>
  active: TKey
  onSelect: (key: TKey) => void
  isAdmin: boolean
  children: ReactNode
}) {
  return (
    <div className="mx-auto grid w-full max-w-[960px] grid-cols-1 gap-6 p-4 md:grid-cols-[220px_1fr] md:gap-10 md:p-8">
      <nav className="scrollbar-hide -mx-4 flex gap-1 overflow-x-auto border-b border-border px-4 pb-2 md:mx-0 md:flex-col md:border-b-0 md:px-0 md:pb-0">
        {sections.map((s) => {
          const selected = s.key === active
          const gated = s.adminOnly && !isAdmin
          return (
            <button
              key={s.key}
              type="button"
              onClick={() => onSelect(s.key)}
              disabled={gated}
              className={cn(
                "flex items-center gap-2 rounded-md px-3 py-2 text-left font-serif text-[13.5px] whitespace-nowrap transition-colors md:rounded-none md:border-l-2",
                selected
                  ? "bg-muted text-foreground md:border-primary md:bg-transparent"
                  : "border-transparent text-muted-foreground hover:bg-muted/50 hover:text-foreground",
                gated && "cursor-not-allowed opacity-50"
              )}
              title={gated ? "Admin-only" : undefined}
            >
              <span className="flex-1">{s.label}</span>
              {s.badge}
            </button>
          )
        })}
      </nav>

      <div className="w-full max-w-[640px]">{children}</div>
    </div>
  )
}
