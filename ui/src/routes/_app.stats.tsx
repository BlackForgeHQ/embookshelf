import { useMemo } from "react"
import { createFileRoute } from "@tanstack/react-router"
import type { ReactNode } from "react"

import type { ReadingStats } from "@/api/reading"
import type {
  StatsBucket,
  StatsRatingBucket,
  StatsYearBucket,
} from "@/api/stats"
import { statsQuery } from "@/api/stats"
import { readingStatsQuery } from "@/api/reading"
import { useApiQuery } from "@/api/query"
import { heatColor } from "@/lib/heat"
import { ProgressBar } from "@/components/ProgressBar"
import { TopBar } from "@/components/TopBar"

export const Route = createFileRoute("/_app/stats")({
  component: StatsPage,
})

function StatsPage() {
  const stats = useApiQuery(statsQuery)
  const reading = useApiQuery(readingStatsQuery(84))

  // Cover coverage makes more sense as a percentage than a raw count.
  const coverPct = useMemo(() => {
    const t = stats.data?.totals
    if (!t || t.books === 0) return 0
    return Math.round((t.booksWithCover / t.books) * 100)
  }, [stats.data])

  return (
    <div className="fade-in">
      <TopBar
        title="Statistics"
        subtitle="A view of your collection: size, shape, and texture."
      />

      <div className="mx-auto flex max-w-[1100px] flex-col gap-16 px-8 pt-10 pb-24">
        {stats.isLoading && <PanelMessage>Crunching…</PanelMessage>}
        {stats.isError && (
          <PanelMessage error>Failed to load statistics.</PanelMessage>
        )}

        {stats.data && (
          <>
            {/* Editorial hero metrics — display numbers, no boxes */}
            <section>
              <p className="t-micro mb-6">At a glance</p>
              <div className="grid grid-cols-2 gap-x-8 gap-y-10 md:grid-cols-5 md:divide-x md:divide-(--color-rule-soft)">
                <Headline
                  label="Books"
                  value={stats.data.totals.books.toLocaleString()}
                />
                <Headline
                  label="Covers"
                  value={`${coverPct}%`}
                  sub={`${stats.data.totals.booksWithCover.toLocaleString()} of ${stats.data.totals.books.toLocaleString()}`}
                />
                <Headline
                  label="Reading"
                  value={stats.data.user.reading.toString()}
                  sub="in progress"
                />
                <Headline
                  label="Finished"
                  value={stats.data.user.finished.toString()}
                  sub="by you"
                />
                <Headline
                  label="Annotations"
                  value={stats.data.user.annotations.toString()}
                  sub={`${stats.data.user.shelves} shelves · ${stats.data.user.smartShelves} smart`}
                />
              </div>
            </section>

            {/* Reading activity — heatmap + side metrics */}
            <Section title="Reading activity" overline="last 12 weeks">
              {reading.isLoading && <EmptyRow>Loading session log…</EmptyRow>}
              {reading.isError && (
                <EmptyRow>Failed to load reading sessions.</EmptyRow>
              )}
              {reading.data && <ReadingActivity data={reading.data} />}
            </Section>

            {/* Libraries (wide) + Formats (narrow) */}
            <div className="grid gap-12 md:grid-cols-[2fr_1fr]">
              <Section title="Books per library">
                {stats.data.libraries.length === 0 ? (
                  <EmptyRow>No libraries configured.</EmptyRow>
                ) : (
                  <BarList buckets={stats.data.libraries} />
                )}
              </Section>
              <Section title="Formats">
                {stats.data.formats.length === 0 ? (
                  <EmptyRow>No books yet.</EmptyRow>
                ) : (
                  <BarList buckets={stats.data.formats} />
                )}
              </Section>
            </div>

            {/* Year histogram */}
            <Section title="Publication years" overline="grouped by decade">
              {stats.data.yearHistogram.length === 0 ? (
                <EmptyRow>Years aren't populated on any books yet.</EmptyRow>
              ) : (
                <YearBars buckets={stats.data.yearHistogram} />
              )}
            </Section>

            {/* Ratings */}
            <Section title="Rating distribution" overline="books you have rated">
              {stats.data.ratings.length === 0 ? (
                <EmptyRow>No ratings yet.</EmptyRow>
              ) : (
                <RatingBars buckets={stats.data.ratings} />
              )}
            </Section>

            {/* Top authors / Top tags — symmetric split */}
            <div className="grid gap-12 md:grid-cols-2">
              <Section
                title="Top authors"
                overline="most-represented in your library"
              >
                {stats.data.topAuthors.length === 0 ? (
                  <EmptyRow>
                    No authors yet. Add books to your library.
                  </EmptyRow>
                ) : (
                  <BarList buckets={stats.data.topAuthors} />
                )}
              </Section>
              <Section title="Top tags" overline="most-used tag values">
                {stats.data.topTags.length === 0 ? (
                  <EmptyRow>No tags yet.</EmptyRow>
                ) : (
                  <BarList buckets={stats.data.topTags} />
                )}
              </Section>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

// Headline — display-size serif number, label overline, optional sub.
// No box; visually grouped via grid divider.
function Headline({
  label,
  value,
  sub,
}: {
  label: string
  value: string
  sub?: string
}) {
  return (
    <div className="md:px-6 md:first:pl-0 md:last:pr-0">
      <div className="t-label mb-3">{label}</div>
      <div
        className="font-serif text-(--color-ink-1) tabular-nums"
        style={{
          fontSize: 40,
          fontWeight: 500,
          lineHeight: 1,
          letterSpacing: "-0.015em",
        }}
      >
        {value}
      </div>
      {sub && (
        <div className="t-small mt-2" style={{ fontSize: 11.5 }}>
          {sub}
        </div>
      )}
    </div>
  )
}

// Section — overline + serif heading + thin rule. No card chrome.
function Section({
  title,
  overline,
  children,
}: {
  title: string
  overline?: string
  children: ReactNode
}) {
  return (
    <section>
      <div className="mb-5 flex items-baseline gap-3 border-b border-(--color-rule-soft) pb-3">
        <h2 className="t-h2" style={{ fontWeight: 500 }}>
          {title}
        </h2>
        {overline && <span className="t-micro">{overline}</span>}
      </div>
      <div>{children}</div>
    </section>
  )
}

// BarList renders a simple horizontal bar chart. No chart library —
// width % is computed from the max count so the longest bar is full.
function BarList({ buckets }: { buckets: Array<StatsBucket> }) {
  const max = Math.max(1, ...buckets.map((b) => b.count))
  return (
    <div className="flex flex-col">
      {buckets.map((b, i) => (
        <Bar
          key={b.label}
          label={b.label}
          count={b.count}
          max={max}
          first={i === 0}
        />
      ))}
    </div>
  )
}

function YearBars({ buckets }: { buckets: Array<StatsYearBucket> }) {
  const max = Math.max(1, ...buckets.map((b) => b.count))
  return (
    <div className="flex flex-col">
      {buckets.map((b, i) => (
        <Bar
          key={b.decade}
          label={`${b.decade}s`}
          count={b.count}
          max={max}
          mono
          first={i === 0}
        />
      ))}
    </div>
  )
}

function RatingBars({ buckets }: { buckets: Array<StatsRatingBucket> }) {
  // Backfill missing ratings (1..5) with zero so the chart axis always
  // shows the full range, not a collapsed subset.
  const byRating = new Map(buckets.map((b) => [b.rating, b.count]))
  const full = [1, 2, 3, 4, 5].map((r) => ({
    rating: r,
    count: byRating.get(r) ?? 0,
  }))
  const max = Math.max(1, ...full.map((b) => b.count))
  return (
    <div className="flex flex-col">
      {full.map((b, i) => (
        <Bar
          key={b.rating}
          label={"★".repeat(b.rating) + "☆".repeat(5 - b.rating)}
          count={b.count}
          max={max}
          first={i === 0}
        />
      ))}
    </div>
  )
}

function Bar({
  label,
  count,
  max,
  mono,
  first,
}: {
  label: string
  count: number
  max: number
  mono?: boolean
  first?: boolean
}) {
  const pct = max === 0 ? 0 : Math.max(2, Math.round((count / max) * 100))
  return (
    <div
      className={
        "grid items-center gap-4 py-2.5 " +
        (first ? "" : "border-t border-(--color-rule-soft)")
      }
      style={{ gridTemplateColumns: "180px 1fr 56px" }}
    >
      <span
        className={mono ? "mono" : undefined}
        style={{
          fontSize: 13,
          color: "var(--color-ink-2)",
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
        title={label}
      >
        {label}
      </span>
      <ProgressBar value={pct / 100} label={label} style={{ height: 6 }} />
      <span
        className="mono tabular-nums"
        style={{
          fontSize: 12,
          color: "var(--color-ink-2)",
          textAlign: "right",
        }}
      >
        {count.toLocaleString()}
      </span>
    </div>
  )
}

function EmptyRow({ children }: { children: ReactNode }) {
  return (
    <div
      className="t-small italic"
      style={{ color: "var(--color-ink-3)", padding: "8px 0" }}
    >
      {children}
    </div>
  )
}

function ReadingActivity({ data }: { data: ReadingStats }) {
  // Normalize to exactly 84 days so the weekly grid stays rectangular.
  const days = fillDays(data.heatmapMinutes, 84)
  const weeks: Array<Array<number>> = []
  for (let w = 0; w < 12; w++) weeks.push(days.slice(w * 7, (w + 1) * 7))

  const totalMin = days.reduce((a, b) => a + b, 0)
  const readDays = days.filter((m) => m > 0).length

  return (
    <div className="grid gap-10 md:grid-cols-[auto_1fr] md:items-start">
      {/* Heatmap */}
      <div>
        <div style={{ display: "flex", gap: 5, alignItems: "flex-start" }}>
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              gap: 5,
              marginRight: 8,
              paddingTop: 2,
            }}
          >
            {["Mon", "", "Wed", "", "Fri", "", ""].map((d, i) => (
              <div
                // biome-ignore lint/suspicious/noArrayIndexKey: the weekday labels are a fixed literal list — position is the day
                key={i}
                className="mono"
                style={{
                  fontSize: 9,
                  color: "var(--color-ink-3)",
                  height: 14,
                  lineHeight: "14px",
                }}
              >
                {d}
              </div>
            ))}
          </div>
          {weeks.map((week, wi) => (
            <div
              // biome-ignore lint/suspicious/noArrayIndexKey: the heatmap is a positional grid: week N is the Nth column
              key={wi}
              style={{ display: "flex", flexDirection: "column", gap: 5 }}
            >
              {week.map((m, di) => (
                <div
                  // biome-ignore lint/suspicious/noArrayIndexKey: the heatmap is a positional grid: day N is the Nth cell
                  key={di}
                  title={m === 0 ? "no activity" : `${m} min`}
                  style={{
                    width: 14,
                    height: 14,
                    borderRadius: 2,
                    background: heatColor(m),
                    boxShadow:
                      m === 0
                        ? "inset 0 0 0 1px var(--color-rule-soft)"
                        : undefined,
                  }}
                />
              ))}
            </div>
          ))}
        </div>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 8,
            marginTop: 16,
          }}
        >
          <span className="t-micro">Less</span>
          {[0, 15, 30, 45, 60].map((m) => (
            <div
              key={m}
              style={{
                width: 11,
                height: 11,
                background: heatColor(m),
                borderRadius: 2,
                boxShadow:
                  m === 0
                    ? "inset 0 0 0 1px var(--color-rule-soft)"
                    : undefined,
              }}
            />
          ))}
          <span className="t-micro">More</span>
        </div>
      </div>

      {/* Side metrics — chrome-free, hairline-divided */}
      <div className="grid grid-cols-2 gap-x-6 gap-y-6 self-center md:max-w-[420px]">
        <SideMetric
          label="This week"
          value={formatMinutes(data.thisWeekMinutes)}
        />
        <SideMetric
          label="Current streak"
          value={`${data.currentStreak} day${data.currentStreak === 1 ? "" : "s"}`}
        />
        <SideMetric
          label="12-week total"
          value={formatMinutes(totalMin)}
          sub={`${readDays} active days`}
        />
        <SideMetric
          label="All time"
          value={formatMinutes(data.allTimeMinutes)}
          sub={`${data.quarterSessions} sessions this quarter`}
        />
      </div>
    </div>
  )
}

function SideMetric({
  label,
  value,
  sub,
}: {
  label: string
  value: string
  sub?: string
}) {
  return (
    <div className="border-t border-(--color-rule-soft) pt-3">
      <div className="t-label mb-1.5">{label}</div>
      <div
        className="font-serif tabular-nums text-(--color-ink-1)"
        style={{
          fontSize: 22,
          fontWeight: 500,
          lineHeight: 1.1,
          letterSpacing: "-0.01em",
        }}
      >
        {value}
      </div>
      {sub && (
        <div className="t-small mt-1" style={{ fontSize: 11.5 }}>
          {sub}
        </div>
      )}
    </div>
  )
}

function fillDays(minutes: Array<number>, target: number): Array<number> {
  if (minutes.length === target) return minutes
  if (minutes.length > target) return minutes.slice(minutes.length - target)
  return Array(target - minutes.length)
    .fill(0)
    .concat(minutes)
}

function formatMinutes(m: number): string {
  if (m <= 0) return "0m"
  const h = Math.floor(m / 60)
  const mins = m % 60
  if (h === 0) return `${mins}m`
  if (mins === 0) return `${h}h`
  return `${h}h ${mins}m`
}

function PanelMessage({
  children,
  error,
}: {
  children: ReactNode
  error?: boolean
}) {
  return (
    <div
      style={{
        padding: "48px 24px",
        textAlign: "center",
        border: error
          ? "1px solid var(--color-accent-soft)"
          : "1px dashed var(--color-rule)",
        background: error ? "var(--color-accent-soft)" : "var(--color-paper-0)",
        color: error ? "var(--color-accent-ink)" : "var(--color-ink-2)",
        borderRadius: 2,
        fontStyle: "italic",
      }}
    >
      {children}
    </div>
  )
}
