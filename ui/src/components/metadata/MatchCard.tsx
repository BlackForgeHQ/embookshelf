import { useState } from "react"

import type { EnrichMatch } from "@/api/enrich"
import { PROVIDER_LABELS } from "@/api/enrich"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

export function MatchCard({
  match,
  onCompare,
  onUseCover,
  busy = false,
}: {
  match: EnrichMatch
  onCompare: () => void
  onUseCover: () => void
  busy?: boolean
}) {
  const [expanded, setExpanded] = useState(false)
  const desc = match.description ?? ""
  const showReadMore = desc.length > 280

  const providerLabel = PROVIDER_LABELS[match.source] ?? match.source.replace("_", " ")

  return (
    <article className="relative flex gap-4 rounded-[3px] border border-(--color-rule-soft) bg-(--color-paper-0) p-4">
      <span className="absolute left-0 top-0 inline-flex items-center px-2 py-0.5 font-mono text-[10px] uppercase tracking-wider rounded-[3px_0_3px_0] bg-(--color-accent-soft) text-(--color-accent-ink)">
        {providerLabel}
      </span>
      <span className="absolute right-2 top-2 inline-flex items-center px-1.5 py-0.5 font-mono text-[10px] tabular-nums rounded-[3px] bg-(--color-paper-2) text-(--color-ink-2)">
        {match.confidence}%
      </span>

      <div className="flex-shrink-0 w-[120px] h-[180px] mt-5 bg-(--color-paper-2)">
        {match.coverUrl ? (
          <img
            src={match.coverUrl}
            alt=""
            width={120}
            height={180}
            loading="lazy"
            className="h-full w-full object-cover"
          />
        ) : (
          <div
            className="h-full w-full"
            style={{
              background:
                "repeating-linear-gradient(135deg, var(--color-paper-3) 0 8px, var(--color-paper-2) 8px 16px)",
            }}
          />
        )}
      </div>

      <div className="flex-1 min-w-0 mt-5">
        <h3 className="font-serif text-[18px] leading-snug text-balance text-(--color-ink-1)">
          {match.title}
        </h3>
        <div className="t-small italic mt-1">
          {match.authors.join(", ")}
          {match.year ? ` · ${match.year}` : ""}
          {match.series ? ` · ${match.series}` : ""}
        </div>
        {desc && (
          <p
            className={cn(
              "mt-2 text-[14px] leading-relaxed text-(--color-ink-2)",
              !expanded && "line-clamp-4",
            )}
          >
            {desc}
          </p>
        )}
        {showReadMore && (
          <button
            type="button"
            className="t-small mt-1 text-(--color-accent-ink) underline-offset-2 hover:underline cursor-pointer"
            onClick={() => setExpanded((v) => !v)}
          >
            {expanded ? "Read less" : "Read more"}
          </button>
        )}
        <div className="mt-3 flex flex-wrap items-center gap-3">
          <Button
            type="button"
            size="sm"
            onClick={onCompare}
            disabled={busy}
            className="hidden xl:inline-flex"
          >
            Compare →
          </Button>
          {match.coverUrl && (
            <button
              type="button"
              onClick={onUseCover}
              disabled={busy}
              className="t-small text-(--color-accent-ink) underline-offset-2 hover:underline disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
            >
              Use cover only
            </button>
          )}
        </div>
      </div>
    </article>
  )
}
