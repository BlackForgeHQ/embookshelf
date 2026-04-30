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

// Shared primitives used across the /settings (per-user) and /admin
// (global) pages. Extracted so the two routes can import them without
// duplicating 100 lines each.

export function Card({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={cn("flex flex-col gap-3.5 p-4 mb-4 bg-card border border-border rounded-lg shadow-sm", className)}>
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
    <label className="flex items-center gap-3 py-2 cursor-pointer border-t border-dashed border-border first:border-0">
      <Switch checked={checked} onCheckedChange={onChange} />
      <div className="grow">
        <div className="text-[13.5px] font-medium leading-none mb-1">{label}</div>
        {hint && <div className="text-sm text-muted-foreground">{hint}</div>}
      </div>
    </label>
  )
}

export function DefRow({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex gap-3 py-1.5 items-baseline">
      <div className="t-label w-40 shrink-0">{label}</div>
      <div className="text-[13.5px] flex-1 min-w-0 break-words">{value}</div>
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
      <h2 className="t-h2 mb-6">{label}</h2>
      <div className="t-small italic text-muted-foreground">
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
    <div className="p-4 md:p-8 grid grid-cols-1 md:grid-cols-[220px_1fr] gap-6 md:gap-10 max-w-[960px] mx-auto w-full">
      <nav className="flex md:flex-col gap-1 overflow-x-auto pb-2 md:pb-0 scrollbar-hide -mx-4 px-4 md:mx-0 md:px-0 border-b md:border-b-0 border-border">
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
                "flex items-center gap-2 px-3 py-2 text-left whitespace-nowrap transition-colors rounded-md md:rounded-none md:border-l-2 font-serif text-[13.5px]",
                selected
                  ? "bg-muted md:bg-transparent md:border-primary text-foreground"
                  : "border-transparent text-muted-foreground hover:bg-muted/50 hover:text-foreground",
                gated && "opacity-50 cursor-not-allowed"
              )}
              title={gated ? "Admin-only" : undefined}
            >
              <span className="flex-1">{s.label}</span>
              {s.badge}
            </button>
          )
        })}
      </nav>

      <div className="max-w-[640px] w-full">{children}</div>
    </div>
  )
}
