import type { ReactNode } from "react"

import { Card } from "@/components/SettingsShared"
import { ProgressBar } from "@/components/ProgressBar"
import { Button } from "@/components/ui/button"

// BulkRunCard is the one bulk-run surface (#355): coverage → percent →
// progress bar → status notes → run button. It existed twice — the
// guide run card and the bulk conversion card — with the second's own
// comment saying "same shape as the guide run card": a comment naming
// the duplicate instead of removing it. A caller declares its coverage
// facts through a view; the card owns the frame. Polling stays on the
// caller's query (lib/artifactRun's pollWhile), because the cadence is
// the query's property, not the card's.
export type BulkRunView = {
  /** The whole population and how much of it is already covered. */
  total: number
  done: number
  /** What the run button would queue right now. */
  candidates: number
  /** Work in flight — suppresses the all-done arm while counts fall. */
  working: boolean
  coverageLabel: ReactNode
  progressLabel: string
  allDoneText: string
  /** Rendered instead of everything else when total is zero. */
  emptyText?: string
  runLabel: string
  /** Explanatory rows between the bar and the button. */
  notes?: ReactNode
}

export function BulkRunCard({
  title,
  checkingText,
  view,
  run,
}: {
  title: string
  checkingText: string
  /** Undefined while the coverage query has not answered yet. */
  view: BulkRunView | undefined
  run: { mutate: (arg: undefined) => void; isPending: boolean }
}) {
  return (
    <Card className="mt-6">
      <h3 className="t-h3 mb-2">{title}</h3>
      {!view ? (
        <p className="t-small">{checkingText}</p>
      ) : view.total === 0 && view.emptyText ? (
        <p className="t-small">{view.emptyText}</p>
      ) : (
        <>
          {view.total > 0 && (
            <div className="mb-4">
              <div
                className="mb-1 flex items-baseline justify-between"
                style={{ gap: 12 }}
              >
                <span className="t-small">{view.coverageLabel}</span>
                <span className="t-small tabular-nums">{percent(view)}%</span>
              </div>
              <ProgressBar value={percent(view) / 100} label={view.progressLabel} />
            </div>
          )}
          {view.notes}
          {view.candidates === 0 && !view.working ? (
            <p className="t-small">{view.allDoneText}</p>
          ) : (
            <Button
              variant="outline"
              disabled={run.isPending || view.candidates === 0}
              onClick={() => run.mutate(undefined)}
            >
              {view.runLabel}
            </Button>
          )}
        </>
      )}
    </Card>
  )
}

function percent(view: BulkRunView): number {
  return view.total > 0 ? Math.round((view.done / view.total) * 100) : 0
}
