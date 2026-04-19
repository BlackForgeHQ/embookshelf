import { useQueries } from '@tanstack/react-query';
import { createFileRoute, useNavigate } from '@tanstack/react-router';

import { fetchMe, meQueryKey } from '@/api/auth';
import {
  booksQueryKey,
  fetchBooks,
  fetchLibraries,
  librariesQueryKey,
  type Book,
  type Library,
} from '@/api/books';
import {
  fetchReadingStats,
  readingStatsQueryKey,
  type ReadingStats,
} from '@/api/reading';
import { Cover } from '@/components/Cover';
import { TopBar } from '@/components/TopBar';

export const Route = createFileRoute('/_app/')({
  component: Dashboard,
});

function heatColor(m: number): string {
  if (m === 0) return 'var(--color-paper-3)';
  if (m < 20) return 'oklch(0.78 0.06 35)';
  if (m < 35) return 'oklch(0.65 0.09 35)';
  if (m < 50) return 'oklch(0.52 0.11 35)';
  return 'oklch(0.42 0.11 35)';
}

function Dashboard() {
  const navigate = useNavigate();
  const openBook = (id: string) => void navigate({ to: '/book/$id', params: { id } });

  const [me, libraries, reading, recent, readingStats] = useQueries({
    queries: [
      { queryKey: meQueryKey, queryFn: fetchMe, staleTime: 60_000 },
      { queryKey: librariesQueryKey, queryFn: fetchLibraries },
      {
        queryKey: booksQueryKey({ shelf: 'reading' }),
        queryFn: () => fetchBooks({ shelf: 'reading' }),
      },
      {
        queryKey: booksQueryKey({ sort: 'recent' }),
        queryFn: () => fetchBooks({ sort: 'recent' }),
      },
      {
        queryKey: readingStatsQueryKey(84),
        queryFn: () => fetchReadingStats(84),
      },
    ],
  });

  const greeting = timeOfDayGreeting();
  const displayName = me.data?.name?.split(' ')[0] ?? me.data?.display ?? 'there';

  const readingList = reading.data?.books ?? [];
  // The recent endpoint returns every book sorted by creation time; cap the
  // horizontal strip at 12 so the layout doesn't stretch indefinitely.
  const recentList = (recent.data?.books ?? []).slice(0, 12);
  const libraryList: Library[] = libraries.data ?? [];

  const readingData: ReadingStats | undefined = readingStats.data;
  // Dashboard renders exactly 12 weeks of cells (84 days). Defensive
  // fill: if the API hands back fewer, pad with zeros so the grid
  // stays rectangular — and trim if it somehow returns more.
  const activity = fillDays(readingData?.heatmapMinutes ?? [], 84);
  const weeks: number[][] = [];
  for (let w = 0; w < 12; w++) weeks.push(activity.slice(w * 7, (w + 1) * 7));

  const quarterHours = Math.round((readingData?.quarterMinutes ?? 0) / 60);
  const quarterSessions = readingData?.quarterSessions ?? 0;

  return (
    <div className="fade-in">
      <TopBar
        title={`${greeting}, ${displayName}.`}
        subtitle={
          readingStats.isLoading
            ? 'Pulling your reading session log…'
            : quarterSessions === 0
              ? "No reading sessions this quarter yet — open a book and the tracker starts."
              : `You've been reading for ${quarterHours} hours this quarter across ${quarterSessions} sessions.`
        }
      />

      <div style={{ padding: '28px 32px 80px', display: 'flex', flexDirection: 'column', gap: 40 }}>
        {/* Currently reading */}
        <section>
          <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', marginBottom: 16 }}>
            <h2 className="t-h2">Currently reading</h2>
            <span className="t-micro">
              {reading.isLoading ? 'loading…' : `${readingList.length} open books`}
            </span>
          </div>
          {readingList.length === 0 && !reading.isLoading ? (
            <EmptyState message="Nothing on the Reading Now shelf. Open a book and tap “Continue reading” to add it." />
          ) : (
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 20 }}>
              {readingList.map((b) => (
                <ReadingCard key={b.id} book={b} onOpen={openBook} />
              ))}
            </div>
          )}
        </section>

        {/* Reading activity — still mocked until per-user sessions land. */}
        <section>
          <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', marginBottom: 16 }}>
            <h2 className="t-h2">Reading activity</h2>
            <span className="t-micro">Last 12 weeks</span>
          </div>
          <div
            style={{
              background: 'var(--color-paper-0)',
              border: '1px solid var(--color-rule-soft)',
              padding: 24,
              borderRadius: 2,
              display: 'flex',
              gap: 32,
            }}
          >
            <div>
              <div style={{ display: 'flex', gap: 4, alignItems: 'flex-start' }}>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 4, marginRight: 6, paddingTop: 2 }}>
                  {['Mon', '', 'Wed', '', 'Fri', '', ''].map((d, i) => (
                    <div key={i} className="mono" style={{ fontSize: 9, color: 'var(--color-ink-3)', height: 12 }}>
                      {d}
                    </div>
                  ))}
                </div>
                {weeks.map((week, wi) => (
                  <div key={wi} style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                    {week.map((m, di) => (
                      <div
                        key={di}
                        title={`${m} min`}
                        style={{ width: 12, height: 12, borderRadius: 1, background: heatColor(m) }}
                      />
                    ))}
                  </div>
                ))}
              </div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 14 }}>
                <span className="t-micro">Less</span>
                {[0, 15, 30, 45, 60].map((m) => (
                  <div key={m} style={{ width: 10, height: 10, background: heatColor(m), borderRadius: 1 }} />
                ))}
                <span className="t-micro">More</span>
              </div>
            </div>

            <div style={{ flex: 1, display: 'grid', gridTemplateColumns: 'repeat(2, 1fr)', gap: 20 }}>
              {[
                {
                  label: 'This week',
                  value: formatMinutes(readingData?.thisWeekMinutes ?? 0),
                  sub:
                    (readingData?.thisWeekMinutes ?? 0) === 0
                      ? 'no activity'
                      : 'across the last 7 days',
                },
                {
                  label: 'Current streak',
                  value: `${readingData?.currentStreak ?? 0} day${(readingData?.currentStreak ?? 0) === 1 ? '' : 's'}`,
                  sub: (readingData?.currentStreak ?? 0) > 0 ? 'keep it going' : 'start one today',
                },
                {
                  label: 'This quarter',
                  value: `${quarterHours}h`,
                  sub: `${quarterSessions} ${quarterSessions === 1 ? 'session' : 'sessions'}`,
                },
                {
                  label: 'All time',
                  value: `${Math.round((readingData?.allTimeMinutes ?? 0) / 60)}h`,
                  sub: 'total time in readers',
                },
              ].map((s) => (
                <div key={s.label} style={{ borderLeft: '2px solid var(--color-accent)', paddingLeft: 14 }}>
                  <div className="t-label" style={{ marginBottom: 6 }}>{s.label}</div>
                  <div
                    style={{
                      fontFamily: 'var(--font-serif)',
                      fontSize: 28,
                      fontWeight: 500,
                      letterSpacing: '-0.01em',
                    }}
                  >
                    {s.value}
                  </div>
                  <div className="t-small" style={{ fontSize: 11.5 }}>{s.sub}</div>
                </div>
              ))}
            </div>
          </div>
        </section>

        {/* Recently added + library snapshot */}
        <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 28 }}>
          <section>
            <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', marginBottom: 16 }}>
              <h2 className="t-h2">Recently added</h2>
              <span className="t-micro">
                {recent.isLoading ? 'loading…' : `${recent.data?.total ?? 0} books indexed`}
              </span>
            </div>
            {recentList.length === 0 && !recent.isLoading ? (
              <EmptyState message="Drop a book into /bookdrop to start growing your library." />
            ) : (
              <div style={{ display: 'flex', gap: 18, overflowX: 'auto', paddingBottom: 8 }}>
                {recentList.map((b) => (
                  <RecentTile key={b.id} book={b} onOpen={openBook} />
                ))}
              </div>
            )}
          </section>

          <section>
            <div style={{ marginBottom: 16 }}>
              <h2 className="t-h2">Your libraries</h2>
            </div>
            <div
              style={{
                background: 'var(--color-paper-0)',
                border: '1px solid var(--color-rule-soft)',
                borderRadius: 2,
              }}
            >
              {libraryList.length === 0 ? (
                <div style={{ padding: 16 }}>
                  <div className="t-small" style={{ fontStyle: 'italic' }}>
                    {libraries.isLoading ? 'Loading libraries…' : 'No libraries yet.'}
                  </div>
                </div>
              ) : (
                libraryList.map((lib, i) => (
                  <div
                    key={lib.id}
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 12,
                      padding: '14px 16px',
                      borderBottom: i < libraryList.length - 1 ? '1px solid var(--color-rule-soft)' : 'none',
                    }}
                  >
                    <div style={{ width: 8, height: 8, borderRadius: '50%', background: 'var(--color-accent)' }} />
                    <div style={{ flex: 1 }}>
                      <div style={{ fontSize: 13.5, fontWeight: 500 }}>{lib.name}</div>
                      <div className="mono" style={{ fontSize: 10.5, color: 'var(--color-ink-3)' }}>
                        /{lib.slug}
                      </div>
                    </div>
                    <span className="mono" style={{ fontSize: 11, color: 'var(--color-ink-2)' }}>
                      {lib.bookCount}
                    </span>
                  </div>
                ))
              )}
            </div>
          </section>
        </div>
      </div>
    </div>
  );
}

function ReadingCard({ book, onOpen }: { book: Book; onOpen: (id: string) => void }) {
  const progress = book.progress ?? 0;
  return (
    <div
      onClick={() => onOpen(book.id)}
      style={{
        display: 'flex',
        gap: 18,
        padding: 18,
        background: 'var(--color-paper-0)',
        border: '1px solid var(--color-rule-soft)',
        borderRadius: 2,
        cursor: 'pointer',
      }}
      onMouseEnter={(e) => (e.currentTarget.style.borderColor = 'var(--color-ink-3)')}
      onMouseLeave={(e) => (e.currentTarget.style.borderColor = 'var(--color-rule-soft)')}
    >
      <Cover book={book} size="sm" />
      <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', justifyContent: 'space-between' }}>
        <div>
          <div style={{ fontSize: 14, fontWeight: 500, textWrap: 'balance', marginBottom: 2 }}>{book.title}</div>
          <div className="t-small" style={{ fontStyle: 'italic', fontSize: 12.5 }}>{book.author}</div>
        </div>
        <div>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
            <span className="mono" style={{ fontSize: 10, color: 'var(--color-ink-3)' }}>
              {Math.round(progress * 100)}%
            </span>
          </div>
          <div className="progress">
            <div style={{ width: `${progress * 100}%` }} />
          </div>
        </div>
      </div>
    </div>
  );
}

function RecentTile({ book, onOpen }: { book: Book; onOpen: (id: string) => void }) {
  return (
    <div
      onClick={() => onOpen(book.id)}
      style={{ flexShrink: 0, width: 110, cursor: 'pointer' }}
    >
      <Cover book={book} size="sm" style={{ width: 110, height: 165 }} />
      <div style={{ fontSize: 12, fontWeight: 500, marginTop: 8, lineHeight: 1.3 }}>{book.title}</div>
      <div className="t-small" style={{ fontSize: 11, fontStyle: 'italic' }}>{book.author}</div>
    </div>
  );
}

function EmptyState({ message }: { message: string }) {
  return (
    <div
      style={{
        padding: 24,
        background: 'var(--color-paper-0)',
        border: '1px dashed var(--color-rule)',
        borderRadius: 2,
        color: 'var(--color-ink-3)',
        fontStyle: 'italic',
        fontSize: 13.5,
      }}
    >
      {message}
    </div>
  );
}

function timeOfDayGreeting(): string {
  const h = new Date().getHours();
  if (h < 12) return 'Good morning';
  if (h < 18) return 'Good afternoon';
  return 'Good evening';
}

// fillDays normalizes the heatmap array to exactly `target` entries so
// the 12×7 grid doesn't warp when the API returns fewer days (new
// install) or — defensively — more than requested.
function fillDays(minutes: number[], target: number): number[] {
  if (minutes.length === target) return minutes;
  if (minutes.length > target) return minutes.slice(minutes.length - target);
  return Array(target - minutes.length).fill(0).concat(minutes);
}

function formatMinutes(m: number): string {
  if (m <= 0) return '0m';
  const h = Math.floor(m / 60);
  const mins = m % 60;
  if (h === 0) return `${mins}m`;
  if (mins === 0) return `${h}h`;
  return `${h}h ${mins}m`;
}
