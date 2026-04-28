import { Link } from "@tanstack/react-router"

import { PROVIDER_LABELS } from "@/api/enrich"
import { cn } from "@/lib/utils"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

export type ProviderStatus = "disabled" | "pending" | "active" | "done" | "error"

export type ProviderState = {
  id: string
  status: ProviderStatus
  error?: string
}

const DOT_BY_STATUS: Record<ProviderStatus, string> = {
  disabled: "bg-(--color-ink-4)",
  pending: "bg-(--color-paper-3)",
  active: "bg-(--color-accent-soft) animate-pulse",
  done: "bg-(--color-accent-ink)",
  error: "bg-(--color-accent-ink)",
}

const TEXT_BY_STATUS: Record<ProviderStatus, string> = {
  disabled: "text-(--color-ink-4) line-through",
  pending: "text-(--color-ink-3)",
  active: "text-(--color-ink-1)",
  done: "text-(--color-ink-1)",
  error: "text-(--color-accent-ink)",
}

export function ProviderStatusChips({
  providers,
}: {
  providers: ReadonlyArray<ProviderState>
}) {
  if (providers.length === 0) {
    return (
      <Link
        to="/settings"
        className="t-small italic text-(--color-accent-ink) underline-offset-2 hover:underline"
      >
        No providers enabled — open Settings → Metadata providers.
      </Link>
    )
  }
  return (
    <div className="flex flex-wrap items-center gap-2">
      {providers.map((p) => {
        const label = PROVIDER_LABELS[p.id] ?? p.id
        const chip = (
          <span
            key={p.id}
            className={cn(
              "inline-flex items-center gap-1.5 rounded-[3px] border border-(--color-rule-soft) bg-(--color-paper-0) px-2 py-0.5 font-mono text-[11px]",
              TEXT_BY_STATUS[p.status]
            )}
          >
            <span
              aria-hidden="true"
              className={cn("inline-block h-1.5 w-1.5 rounded-full", DOT_BY_STATUS[p.status])}
            />
            {label}
            {p.status === "done" && <span aria-hidden>✓</span>}
            {p.status === "error" && <span aria-hidden>✗</span>}
          </span>
        )
        if (p.status === "disabled") {
          return (
            <Tooltip key={p.id}>
              <TooltipTrigger asChild>{chip}</TooltipTrigger>
              <TooltipContent>Enable in Settings → Metadata providers</TooltipContent>
            </Tooltip>
          )
        }
        if (p.status === "error" && p.error) {
          return (
            <Tooltip key={p.id}>
              <TooltipTrigger asChild>{chip}</TooltipTrigger>
              <TooltipContent>{p.error}</TooltipContent>
            </Tooltip>
          )
        }
        return chip
      })}
    </div>
  )
}
