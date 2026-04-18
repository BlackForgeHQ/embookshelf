import { createFileRoute, useNavigate } from '@tanstack/react-router';

import { Cover } from '@/components/Cover';
import { TopBar } from '@/components/TopBar';
import { ACTIVITY, BOOKS, LIBRARIES } from '@/data/mock';

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

  const reading = BOOKS.filter((b) => b.shelf?.includes('Reading Now'));
  const recent = BOOKS.filter((b) => b.shelf?.includes('New'));

  const weeks: number[][] = [];
  for (let w = 0; w < 12; w++) weeks.push(ACTIVITY.slice(w * 7, (w + 1) * 7));

  const totalMin = ACTIVITY.reduce((a, b) => a + b, 0);
  const readDays = ACTIVITY.filter((m) => m > 0).length;

  return (
    <div className="fade-in">
      <TopBar
        title="Good afternoon, Rowan."
        subtitle={`You've been reading for ${Math.round(totalMin / 60)} hours this quarter across ${readDays} sessions.`}
      />

      <div style={{ padding: '28px 32px 80px', display: 'flex', flexDirection: 'column', gap: 40 }}>
        {/* Currently reading */}
        <section>
          <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', marginBottom: 16 }}>
            <h2 className="t-h2">Currently reading</h2>
            <span className="t-micro">{reading.length} open books</span>
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 20 }}>
            {reading.map((b) => (
              <div
                key={b.id}
                onClick={() => openBook(b.id)}
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
                <Cover book={b} size="sm" />
                <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', justifyContent: 'space-between' }}>
                  <div>
                    <div style={{ fontSize: 14, fontWeight: 500, textWrap: 'balance', marginBottom: 2 }}>{b.title}</div>
                    <div className="t-small" style={{ fontStyle: 'italic', fontSize: 12.5 }}>{b.author}</div>
                  </div>
                  <div>
                    <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
                      <span className="mono" style={{ fontSize: 10, color: 'var(--color-ink-3)' }}>
                        {Math.round((b.progress ?? 0) * 100)}%
                      </span>
                      <span className="mono" style={{ fontSize: 10, color: 'var(--color-ink-3)' }}>
                        p.{Math.round(b.pages * (b.progress ?? 0))}/{b.pages}
                      </span>
                    </div>
                    <div className="progress">
                      <div style={{ width: `${(b.progress ?? 0) * 100}%` }} />
                    </div>
                  </div>
                </div>
              </div>
            ))}
          </div>
        </section>

        {/* Reading activity */}
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
            {/* Heatmap */}
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

            {/* Stats */}
            <div style={{ flex: 1, display: 'grid', gridTemplateColumns: 'repeat(2, 1fr)', gap: 20 }}>
              {[
                { label: 'This week', value: '4h 12m', sub: '+ 38m vs last' },
                { label: 'Current streak', value: '6 days', sub: 'longest this year' },
                { label: 'Books finished', value: '24', sub: '7 this quarter' },
                { label: 'Pages read', value: '4,812', sub: '~160 / week' },
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

        {/* Recently added & library snapshot */}
        <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 28 }}>
          <section>
            <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', marginBottom: 16 }}>
              <h2 className="t-h2">Recently added</h2>
              <span className="t-micro">9 new this month</span>
            </div>
            <div style={{ display: 'flex', gap: 18, overflowX: 'auto', paddingBottom: 8 }}>
              {recent.map((b) => (
                <div
                  key={b.id}
                  onClick={() => openBook(b.id)}
                  style={{ flexShrink: 0, width: 110, cursor: 'pointer' }}
                >
                  <Cover book={b} size="sm" style={{ width: 110, height: 165 }} />
                  <div style={{ fontSize: 12, fontWeight: 500, marginTop: 8, lineHeight: 1.3 }}>{b.title}</div>
                  <div className="t-small" style={{ fontSize: 11, fontStyle: 'italic' }}>{b.author}</div>
                </div>
              ))}
            </div>
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
              {LIBRARIES.map((lib, i) => (
                <div
                  key={lib.id}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 12,
                    padding: '14px 16px',
                    borderBottom: i < LIBRARIES.length - 1 ? '1px solid var(--color-rule-soft)' : 'none',
                  }}
                >
                  <div style={{ width: 8, height: 8, borderRadius: '50%', background: lib.color }} />
                  <div style={{ flex: 1 }}>
                    <div style={{ fontSize: 13.5, fontWeight: 500 }}>{lib.name}</div>
                    <div className="mono" style={{ fontSize: 10.5, color: 'var(--color-ink-3)' }}>{lib.path}</div>
                  </div>
                  <span className="mono" style={{ fontSize: 11, color: 'var(--color-ink-2)' }}>{lib.count}</span>
                </div>
              ))}
            </div>
          </section>
        </div>
      </div>
    </div>
  );
}
