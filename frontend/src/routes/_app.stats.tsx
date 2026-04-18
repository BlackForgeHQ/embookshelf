import { useMemo, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';

import {
  fetchStats,
  statsQueryKey,
  type StatsBucket,
  type StatsRatingBucket,
  type StatsYearBucket,
} from '@/api/stats';
import { TopBar } from '@/components/TopBar';

export const Route = createFileRoute('/_app/stats')({
  component: StatsPage,
});

function StatsPage() {
  const stats = useQuery({ queryKey: statsQueryKey, queryFn: fetchStats });

  // Cover coverage makes more sense as a percentage than a raw count.
  const coverPct = useMemo(() => {
    const t = stats.data?.totals;
    if (!t || t.books === 0) return 0;
    return Math.round((t.booksWithCover / t.books) * 100);
  }, [stats.data]);

  return (
    <div className="fade-in">
      <TopBar
        title="Statistics"
        subtitle="A view of your collection — size, shape, and texture."
      />

      <div
        style={{
          padding: '28px 32px 80px',
          display: 'flex',
          flexDirection: 'column',
          gap: 40,
          maxWidth: 1040,
        }}
      >
        {stats.isLoading && <PanelMessage>Crunching…</PanelMessage>}
        {stats.isError && (
          <PanelMessage error>Failed to load statistics.</PanelMessage>
        )}

        {stats.data && (
          <>
            {/* Headline tiles */}
            <section>
              <div
                style={{
                  display: 'grid',
                  gridTemplateColumns: 'repeat(5, 1fr)',
                  gap: 16,
                }}
              >
                <Tile label="Books" value={stats.data.totals.books.toLocaleString()} />
                <Tile
                  label="Covers"
                  value={`${coverPct}%`}
                  sub={`${stats.data.totals.booksWithCover.toLocaleString()} of ${stats.data.totals.books.toLocaleString()}`}
                />
                <Tile label="Reading" value={stats.data.user.reading.toString()} sub="in progress" />
                <Tile label="Finished" value={stats.data.user.finished.toString()} sub="by you" />
                <Tile
                  label="Annotations"
                  value={stats.data.user.annotations.toString()}
                  sub={`${stats.data.user.shelves} shelves · ${stats.data.user.smartShelves} smart`}
                />
              </div>
            </section>

            {/* Two-column: libraries + formats */}
            <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 28 }}>
              <Card title="Books per library">
                {stats.data.libraries.length === 0 ? (
                  <EmptyRow>No libraries configured.</EmptyRow>
                ) : (
                  <BarList buckets={stats.data.libraries} />
                )}
              </Card>
              <Card title="Formats">
                {stats.data.formats.length === 0 ? (
                  <EmptyRow>No books yet.</EmptyRow>
                ) : (
                  <BarList buckets={stats.data.formats} />
                )}
              </Card>
            </div>

            {/* Year histogram (full width) */}
            <Card title="Publication years" subtitle="grouped by decade">
              {stats.data.yearHistogram.length === 0 ? (
                <EmptyRow>Years aren't populated on any books yet.</EmptyRow>
              ) : (
                <YearBars buckets={stats.data.yearHistogram} />
              )}
            </Card>

            {/* Rating distribution */}
            <Card title="Rating distribution" subtitle="books you have rated">
              {stats.data.ratings.length === 0 ? (
                <EmptyRow>No ratings yet.</EmptyRow>
              ) : (
                <RatingBars buckets={stats.data.ratings} />
              )}
            </Card>

            {/* Two-column: top authors + top tags */}
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 28 }}>
              <Card title="Top authors" subtitle="most-represented in your library">
                {stats.data.topAuthors.length === 0 ? (
                  <EmptyRow>—</EmptyRow>
                ) : (
                  <BarList buckets={stats.data.topAuthors} />
                )}
              </Card>
              <Card title="Top tags" subtitle="most-used tag values">
                {stats.data.topTags.length === 0 ? (
                  <EmptyRow>No tags yet.</EmptyRow>
                ) : (
                  <BarList buckets={stats.data.topTags} />
                )}
              </Card>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

// Tile is the headline stat card — big number up top, label underneath,
// optional sub-line for secondary context. Mirrors the dashboard's
// stats block.
function Tile({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div
      style={{
        padding: 16,
        background: 'var(--color-paper-0)',
        border: '1px solid var(--color-rule-soft)',
        borderLeft: '2px solid var(--color-accent)',
        borderRadius: 2,
      }}
    >
      <div className="t-label" style={{ marginBottom: 8 }}>{label}</div>
      <div
        style={{
          fontFamily: 'var(--font-serif)',
          fontSize: 28,
          fontWeight: 500,
          letterSpacing: '-0.01em',
          lineHeight: 1.1,
        }}
      >
        {value}
      </div>
      {sub && (
        <div className="t-small" style={{ fontSize: 11.5, marginTop: 4 }}>{sub}</div>
      )}
    </div>
  );
}

function Card({
  title,
  subtitle,
  children,
}: {
  title: string;
  subtitle?: string;
  children: ReactNode;
}) {
  return (
    <section>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 12, marginBottom: 14 }}>
        <h2 className="t-h2">{title}</h2>
        {subtitle && <span className="t-micro">{subtitle}</span>}
      </div>
      <div
        style={{
          padding: 18,
          background: 'var(--color-paper-0)',
          border: '1px solid var(--color-rule-soft)',
          borderRadius: 2,
        }}
      >
        {children}
      </div>
    </section>
  );
}

// BarList renders a simple horizontal bar chart. No chart library —
// width % is computed from the max count so the longest bar is full.
function BarList({ buckets }: { buckets: StatsBucket[] }) {
  const max = Math.max(1, ...buckets.map((b) => b.count));
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {buckets.map((b) => (
        <Bar key={b.label} label={b.label} count={b.count} max={max} />
      ))}
    </div>
  );
}

function YearBars({ buckets }: { buckets: StatsYearBucket[] }) {
  const max = Math.max(1, ...buckets.map((b) => b.count));
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {buckets.map((b) => (
        <Bar
          key={b.decade}
          label={`${b.decade}s`}
          count={b.count}
          max={max}
          mono
        />
      ))}
    </div>
  );
}

function RatingBars({ buckets }: { buckets: StatsRatingBucket[] }) {
  // Backfill missing ratings (1..5) with zero so the chart axis always
  // shows the full range, not a collapsed subset.
  const byRating = new Map(buckets.map((b) => [b.rating, b.count]));
  const full = [1, 2, 3, 4, 5].map((r) => ({
    rating: r,
    count: byRating.get(r) ?? 0,
  }));
  const max = Math.max(1, ...full.map((b) => b.count));
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {full.map((b) => (
        <Bar
          key={b.rating}
          label={'★'.repeat(b.rating) + '☆'.repeat(5 - b.rating)}
          count={b.count}
          max={max}
        />
      ))}
    </div>
  );
}

function Bar({
  label,
  count,
  max,
  mono,
}: {
  label: string;
  count: number;
  max: number;
  mono?: boolean;
}) {
  const pct = max === 0 ? 0 : Math.max(2, Math.round((count / max) * 100));
  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: '160px 1fr 48px',
        gap: 12,
        alignItems: 'center',
      }}
    >
      <span
        className={mono ? 'mono' : undefined}
        style={{
          fontSize: 13,
          color: 'var(--color-ink-2)',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}
        title={label}
      >
        {label}
      </span>
      <div
        style={{
          height: 10,
          background: 'var(--color-paper-2)',
          borderRadius: 2,
          overflow: 'hidden',
        }}
      >
        <div
          style={{
            height: '100%',
            width: `${pct}%`,
            background: 'var(--color-accent)',
            transition: 'width 180ms ease',
          }}
        />
      </div>
      <span
        className="mono"
        style={{
          fontSize: 11,
          color: 'var(--color-ink-3)',
          textAlign: 'right',
        }}
      >
        {count.toLocaleString()}
      </span>
    </div>
  );
}

function EmptyRow({ children }: { children: ReactNode }) {
  return (
    <div className="t-small" style={{ fontStyle: 'italic', color: 'var(--color-ink-3)' }}>
      {children}
    </div>
  );
}

function PanelMessage({ children, error }: { children: ReactNode; error?: boolean }) {
  return (
    <div
      style={{
        padding: '48px 24px',
        textAlign: 'center',
        border: error ? '1px solid var(--color-accent-soft)' : '1px dashed var(--color-rule)',
        background: error ? 'var(--color-accent-soft)' : 'var(--color-paper-0)',
        color: error ? 'var(--color-accent-ink)' : 'var(--color-ink-2)',
        borderRadius: 2,
        fontStyle: 'italic',
      }}
    >
      {children}
    </div>
  );
}
