import type { StatusRow } from "@/components/settings/instanceStatus"
import { cn } from "@/lib/utils"

/**
 * The status board's rows.
 *
 * Two lines per row — verdict, then the evidence for it — which is the
 * one thing DefRow has no slot for, so this spells the row out rather
 * than bending that one. Deliberately not a Card: Card stacks its
 * children with a gap, and a ledger's rows meet at a hairline.
 *
 * Tone carries no colour of its own beyond the accent ink a warning
 * already uses elsewhere in settings. There is no status-dot vocabulary
 * here because there is none anywhere else in the app.
 */
export function StatusLedger({ rows }: { rows: Array<StatusRow> }) {
  return (
    <div className="mb-4 rounded-lg border border-border bg-card p-4 shadow-sm">
      {rows.map((r) => (
        <div
          key={r.key}
          data-testid={`status-row-${r.key}`}
          data-tone={r.tone}
          className="flex items-baseline gap-3 border-b border-dashed border-(--color-rule-soft) py-2 last:border-b-0"
        >
          <div className="t-label w-[150px] shrink-0">{r.label}</div>
          <div className="min-w-0 flex-1">
            <div
              className={cn(
                "text-[13.5px] break-words",
                r.tone === "warn" && "text-(--color-accent-ink)",
                r.tone === "idle" && "text-muted-foreground"
              )}
            >
              {r.state}
            </div>
            {r.evidence && (
              <div
                className={cn(
                  "t-small mt-0.5 break-words italic",
                  r.tone === "warn"
                    ? "text-(--color-accent-ink)"
                    : "text-muted-foreground"
                )}
              >
                {r.evidence}
              </div>
            )}
          </div>
        </div>
      ))}
    </div>
  )
}
