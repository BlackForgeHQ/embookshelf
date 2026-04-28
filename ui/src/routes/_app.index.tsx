import { useQueries } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import type { ReactNode } from "react"

import type { Book, Library } from "@/api/books"
import type { ReadingStats } from "@/api/reading"
import { fetchMe, meQueryKey } from "@/api/auth"
import {
  booksQueryKey,
  fetchBooks,
  fetchLibraries,
  librariesQueryKey,
} from "@/api/books"
import { fetchReadingStats, readingStatsQueryKey } from "@/api/reading"
import { Cover } from "@/components/Cover"
import { TopBar } from "@/components/TopBar"

export const Route = createFileRoute("/_app/")({
  component: Dashboard,
})

function heatColor(m: number): string {
  if (m === 0) return "var(--color-paper-2)"
  if (m < 20) return "oklch(0.78 0.06 35)"
  if (m < 35) return "oklch(0.65 0.09 35)"
  if (m < 50) return "oklch(0.52 0.11 35)"
  return "oklch(0.42 0.11 35)"
}

function Dashboard() {
  const navigate = useNavigate()
  const openBook = (id: string) =>
    void navigate({ to: "/book/$id", params: { id } })

  const [me, libraries, reading, recent, readingStats] = useQueries({
    queries: [
      { queryKey: meQueryKey, queryFn: fetchMe, staleTime: 60_000 },
      { queryKey: librariesQueryKey, queryFn: fetchLibraries },
      {
        queryKey: booksQueryKey({ shelf: "reading" }),
        queryFn: () => fetchBooks({ shelf: "reading" }),
      },
      {
        queryKey: booksQueryKey({ sort: "recent" }),
        queryFn: () => fetchBooks({ sort: "recent" }),
      },
      {
        queryKey: readingStatsQueryKey(84),
        queryFn: () => fetchReadingStats(84),
      },
    ],
  })

  const greeting = timeOfDayGreeting()
  const displayName =
    me.data?.name.split(" ")[0] ?? me.data?.display ?? "there"

  const readingList = reading.data?.books ?? []
  // The recent endpoint returns every book sorted by creation time; cap the
  // horizontal strip at 12 so the layout doesn't stretch indefinitely.
  const recentList = (recent.data?.books ?? []).slice(0, 12)
  const libraryList: Array<Library> = libraries.data ?? []

  const readingData: ReadingStats | undefined = readingStats.data
  // Dashboard renders exactly 12 weeks of cells (84 days). Defensive
  // fill: if the API hands back fewer, pad with zeros so the grid
  // stays rectangular — and trim if it somehow returns more.
  const activity = fillDays(readingData?.heatmapMinutes ?? [], 84)
  const weeks: Array<Array<number>> = []
  for (let w = 0; w < 12; w++) weeks.push(activity.slice(w * 7, (w + 1) * 7))

  const quarterHours = Math.round((readingData?.quarterMinutes ?? 0) / 60)
  const quarterSessions = readingData?.quarterSessions ?? 0

  return (
    <div className="fade-in">
      <TopBar
        title={`${greeting}, ${displayName}.`}
        subtitle={
          readingStats.isLoading
            ? "Pulling your reading session log…"
            : quarterSessions === 0
              ? "No reading sessions this quarter yet — open a book and the tracker starts."
              : `You've been reading for ${quarterHours} hours this quarter across ${quarterSessions} sessions.`
        }
      />

      <div className="mx-auto flex max-w-[1180px] flex-col gap-16 px-8 pt-10 pb-24">
        {/* Currently reading */}
        <Section
          title="Currently reading"
          overline={
            reading.isLoading
              ? "loading…"
              : `${readingList.length} open book${readingList.length === 1 ? "" : "s"}`
          }
        >
          {readingList.length === 0 && !reading.isLoading ? (
            <EmptyState message="Nothing on the Reading Now shelf. Open a book and tap “Continue reading” to add it." />
          ) : (
            <div
              style={{
                display: "grid",
                gridTemplateColumns: "repeat(auto-fill, minmax(360px, 1fr))",
                gap: 20,
              }}
            >
              {readingList.map((b) => (
                <ReadingCard key={b.id} book={b} onOpen={openBook} />
              ))}
            </div>
          )}
        </Section>

        {/* Reading activity — heatmap + side metrics, chrome-free */}
        <Section title="Reading activity" overline="last 12 weeks">
          {readingStats.isLoading ? (
            <EmptyRow>Loading session log…</EmptyRow>
          ) : (
            <div className="grid gap-10 md:grid-cols-[auto_1fr] md:items-start">
              <div>
                <div
                  style={{ display: "flex", gap: 5, alignItems: "flex-start" }}
                >
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
                      key={wi}
                      style={{
                        display: "flex",
                        flexDirection: "column",
                        gap: 5,
                      }}
                    >
                      {week.map((m, di) => (
                        <div
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

              <div className="grid grid-cols-2 gap-x-6 gap-y-6 self-center md:max-w-[460px]">
                <SideMetric
                  label="This week"
                  value={formatMinutes(readingData?.thisWeekMinutes ?? 0)}
                  sub={
                    (readingData?.thisWeekMinutes ?? 0) === 0
                      ? "no activity"
                      : "across the last 7 days"
                  }
                />
                <SideMetric
                  label="Current streak"
                  value={`${readingData?.currentStreak ?? 0} day${(readingData?.currentStreak ?? 0) === 1 ? "" : "s"}`}
                  sub={
                    (readingData?.currentStreak ?? 0) > 0
                      ? "keep it going"
                      : "start one today"
                  }
                />
                <SideMetric
                  label="This quarter"
                  value={`${quarterHours}h`}
                  sub={`${quarterSessions} ${quarterSessions === 1 ? "session" : "sessions"}`}
                />
                <SideMetric
                  label="All time"
                  value={`${Math.round((readingData?.allTimeMinutes ?? 0) / 60)}h`}
                  sub="total time in readers"
                />
              </div>
            </div>
          )}
        </Section>

        {/* Recently added (wide) + Libraries (narrow) */}
        <div className="grid gap-12 md:grid-cols-[2fr_1fr]">
          <Section
            title="Recently added"
            overline={
              recent.isLoading
                ? "loading…"
                : `${recent.data?.total ?? 0} books indexed`
            }
          >
            {recentList.length === 0 && !recent.isLoading ? (
              <EmptyState message="Drop a book into /bookdrop to start growing your library." />
            ) : (
              <div
                style={{
                  display: "flex",
                  gap: 22,
                  overflowX: "auto",
                  paddingBottom: 8,
                }}
              >
                {recentList.map((b) => (
                  <RecentTile key={b.id} book={b} onOpen={openBook} />
                ))}
              </div>
            )}
          </Section>

          <Section title="Your libraries">
            {libraryList.length === 0 ? (
              <EmptyRow>
                {libraries.isLoading ? "Loading libraries…" : "No libraries yet."}
              </EmptyRow>
            ) : (
              <div className="flex flex-col">
                {libraryList.map((lib, i) => (
                  <div
                    key={lib.id}
                    className={
                      "flex items-center gap-3 py-3 " +
                      (i === 0 ? "" : "border-t border-(--color-rule-soft)")
                    }
                  >
                    <span
                      aria-hidden
                      style={{
                        width: 8,
                        height: 8,
                        borderRadius: "50%",
                        background: "var(--color-editorial-accent)",
                        flexShrink: 0,
                      }}
                    />
                    <div className="grow min-w-0">
                      <div className="t-item-title truncate">{lib.name}</div>
                      <div
                        className="mono truncate"
                        style={{
                          fontSize: 10.5,
                          color: "var(--color-ink-3)",
                        }}
                      >
                        /{lib.slug}
                      </div>
                    </div>
                    <span
                      className="mono tabular-nums"
                      style={{ fontSize: 12, color: "var(--color-ink-2)" }}
                    >
                      {lib.bookCount}
                    </span>
                  </div>
                ))}
              </div>
            )}
          </Section>
        </div>
      </div>
    </div>
  )
}

// Section — overline + serif heading + thin rule. Mirrors the stats
// redesign so dashboard + stats share one structural grammar.
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
      <div className="mb-5 flex items-baseline justify-between gap-3 border-b border-(--color-rule-soft) pb-3">
        <h2 className="t-h2" style={{ fontWeight: 500 }}>
          {title}
        </h2>
        {overline && <span className="t-micro">{overline}</span>}
      </div>
      <div>{children}</div>
    </section>
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
          fontSize: 26,
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

function ReadingCard({
  book,
  onOpen,
}: {
  book: Book
  onOpen: (id: string) => void
}) {
  const progress = book.progress
  return (
    <button
      type="button"
      onClick={() => onOpen(book.id)}
      className="card-link"
      aria-label={`Continue reading ${book.title}`}
      style={{ display: "flex", gap: 18, padding: 18 }}
    >
      <Cover book={book} size="sm" />
      <div
        style={{
          flex: 1,
          minWidth: 0,
          display: "flex",
          flexDirection: "column",
          justifyContent: "space-between",
        }}
      >
        <div>
          <div
            style={{
              fontSize: 14,
              fontWeight: 500,
              textWrap: "balance",
              marginBottom: 2,
            }}
          >
            {book.title}
          </div>
          <div
            className="t-small"
            style={{ fontStyle: "italic", fontSize: 12.5 }}
          >
            {book.author}
          </div>
        </div>
        <div>
          <div
            style={{
              display: "flex",
              justifyContent: "space-between",
              marginBottom: 4,
            }}
          >
            <span
              className="mono tabular-nums"
              style={{ fontSize: 10, color: "var(--color-ink-3)" }}
            >
              {Math.round(progress * 100)}%
            </span>
          </div>
          <div className="progress">
            <div style={{ width: `${progress * 100}%` }} />
          </div>
        </div>
      </div>
    </button>
  )
}

function RecentTile({
  book,
  onOpen,
}: {
  book: Book
  onOpen: (id: string) => void
}) {
  return (
    <button
      type="button"
      onClick={() => onOpen(book.id)}
      className="card-tile"
      aria-label={`Open ${book.title}`}
      style={{ flexShrink: 0, width: 130 }}
    >
      <Cover book={book} size="sm" style={{ width: 130, height: 195 }} />
      <div
        style={{
          fontSize: 13,
          fontWeight: 500,
          marginTop: 10,
          lineHeight: 1.3,
          textWrap: "balance",
        }}
      >
        {book.title}
      </div>
      <div className="t-small" style={{ fontSize: 11.5, fontStyle: "italic" }}>
        {book.author}
      </div>
    </button>
  )
}

function EmptyState({ message }: { message: string }) {
  return (
    <div
      className="t-small italic"
      style={{
        padding: "16px 0",
        color: "var(--color-ink-3)",
      }}
    >
      {message}
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

function timeOfDayGreeting(): string {
  const h = new Date().getHours()
  if (h < 12) return "Good morning"
  if (h < 18) return "Good afternoon"
  return "Good evening"
}

// fillDays normalizes the heatmap array to exactly `target` entries so
// the 12×7 grid doesn't warp when the API returns fewer days (new
// install) or — defensively — more than requested.
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
