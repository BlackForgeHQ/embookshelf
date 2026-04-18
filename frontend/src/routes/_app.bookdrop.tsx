import { useState, type ReactNode } from 'react';
import { createFileRoute } from '@tanstack/react-router';

import { Icon } from '@/components/Icon';
import { TopBar } from '@/components/TopBar';
import { BOOKDROP_FILES } from '@/data/mock';

export const Route = createFileRoute('/_app/bookdrop')({
  component: BookDrop,
});

const STATUS_COLOR: Record<string, string> = {
  ready: 'oklch(0.58 0.12 140)',
  'needs-review': 'var(--color-accent)',
  processing: 'oklch(0.65 0.09 80)',
};

function BookDrop() {
  const [items] = useState(BOOKDROP_FILES);
  const [selectedId, setSelectedId] = useState<string | undefined>(items[0]?.id);
  const current = items.find((i) => i.id === selectedId);

  return (
    <div className="fade-in">
      <TopBar
        title="BookDrop"
        subtitle="Drop files into /bookdrop and they'll appear here for review before joining your library."
        right={
          <div style={{ display: 'flex', gap: 8 }}>
            <button className="btn small">
              <Icon name="refresh" size={13} /> Rescan
            </button>
            <button className="btn primary small">
              <Icon name="check" size={13} /> Approve all ready
            </button>
          </div>
        }
      />

      <div style={{ display: 'grid', gridTemplateColumns: '440px 1fr', flex: 1, minHeight: 0 }}>
        {/* Left — file list */}
        <div style={{ borderRight: '1px solid var(--color-rule-soft)', overflow: 'auto' }}>
          {/* Drop zone */}
          <div
            style={{
              margin: 20,
              padding: '28px 16px',
              border: '2px dashed var(--color-rule)',
              borderRadius: 3,
              background: 'var(--color-paper-2)',
              textAlign: 'center',
            }}
          >
            <Icon name="upload" size={20} className="mono" />
            <div style={{ fontSize: 14, fontWeight: 500, marginTop: 8 }}>Drop files here</div>
            <div className="t-small" style={{ fontSize: 12 }}>
              or watch{' '}
              <span className="mono" style={{ fontSize: 11, color: 'var(--color-accent-ink)' }}>
                /bookdrop
              </span>{' '}
              on the host
            </div>
          </div>

          <div className="t-label" style={{ padding: '4px 20px 10px' }}>
            In queue · {items.length}
          </div>
          {items.map((f) => (
            <div
              key={f.id}
              onClick={() => setSelectedId(f.id)}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 12,
                padding: '12px 20px',
                cursor: 'pointer',
                borderBottom: '1px solid var(--color-rule-soft)',
                background: selectedId === f.id ? 'var(--color-paper-3)' : 'transparent',
                borderLeft: selectedId === f.id ? '2px solid var(--color-accent)' : '2px solid transparent',
              }}
            >
              <div
                style={{
                  width: 36,
                  height: 48,
                  background: 'var(--color-paper-2)',
                  border: '1px solid var(--color-rule)',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  fontFamily: 'var(--font-mono)',
                  fontSize: 9,
                  color: 'var(--color-ink-3)',
                  flexShrink: 0,
                }}
              >
                {f.format}
              </div>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div
                  className="mono"
                  style={{
                    fontSize: 11,
                    color: 'var(--color-ink-2)',
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                  }}
                >
                  {f.filename}
                </div>
                <div
                  style={{
                    fontSize: 13,
                    fontWeight: 500,
                    marginTop: 2,
                    fontStyle: f.detected.title ? 'normal' : 'italic',
                    color: f.detected.title ? 'var(--color-ink-1)' : 'var(--color-ink-3)',
                  }}
                >
                  {f.detected.title || 'Could not detect metadata'}
                </div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 4 }}>
                  <span
                    style={{
                      width: 6,
                      height: 6,
                      borderRadius: '50%',
                      background: STATUS_COLOR[f.status],
                    }}
                  />
                  <span className="t-micro" style={{ fontSize: 9.5 }}>
                    {f.status.replace('-', ' ')}
                  </span>
                  <span className="mono" style={{ fontSize: 10, color: 'var(--color-ink-3)' }}>
                    {f.size}
                  </span>
                </div>
              </div>
            </div>
          ))}
        </div>

        {/* Right — detail */}
        {current && (
          <div style={{ overflow: 'auto', padding: '32px 40px' }}>
            <div className="t-label" style={{ marginBottom: 6 }}>Review import</div>
            <div className="mono" style={{ fontSize: 12, color: 'var(--color-ink-3)', marginBottom: 20 }}>
              {current.filename}
            </div>

            <div style={{ display: 'grid', gridTemplateColumns: '160px 1fr', gap: 32 }}>
              <div>
                {current.detected.cover ? (
                  <div
                    style={{
                      width: 160,
                      height: 240,
                      background: 'var(--color-cov-teal)',
                      color: 'var(--color-cov-cream)',
                      padding: 18,
                      display: 'flex',
                      flexDirection: 'column',
                      justifyContent: 'space-between',
                      boxShadow: '2px 4px 12px oklch(0.2 0.02 60 / 0.15)',
                    }}
                  >
                    <div
                      className="mono"
                      style={{ fontSize: 8, letterSpacing: '0.1em', textTransform: 'uppercase', opacity: 0.8 }}
                    >
                      {current.detected.author}
                    </div>
                    <div
                      style={{
                        fontFamily: 'var(--font-serif)',
                        fontSize: 17,
                        fontWeight: 500,
                        lineHeight: 1.15,
                      }}
                    >
                      {current.detected.title}
                    </div>
                  </div>
                ) : (
                  <div
                    style={{
                      width: 160,
                      height: 240,
                      background:
                        'repeating-linear-gradient(135deg, var(--color-paper-3) 0 8px, var(--color-paper-2) 8px 16px)',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      border: '1px solid var(--color-rule)',
                    }}
                  >
                    <div className="t-micro" style={{ textAlign: 'center', lineHeight: 1.4 }}>
                      no cover
                      <br />
                      detected
                    </div>
                  </div>
                )}
                <button
                  className="btn small"
                  style={{ width: '100%', marginTop: 10, justifyContent: 'center' }}
                >
                  <Icon name="refresh" size={12} /> Fetch cover
                </button>
              </div>

              <div>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14, marginBottom: 20 }}>
                  <Field label="Title" value={current.detected.title ?? ''} placeholder="Could not detect" />
                  <Field label="Author" value={current.detected.author ?? ''} placeholder="Could not detect" />
                  <Field label="Published" value={current.detected.year?.toString() ?? ''} />
                  <Field label="Format" value={String(current.format)} readOnly />
                  <Field label="Library" value="Main Library" select />
                  <Field label="Shelf" value="New" select />
                </div>

                <div className="t-label" style={{ marginBottom: 8 }}>
                  Metadata sources
                </div>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginBottom: 24 }}>
                  {[
                    { s: 'Embedded (EPUB)', status: current.detected.title ? 'match' : 'miss' },
                    { s: 'Google Books', status: current.detected.title ? 'match' : 'skipped' },
                    { s: 'Open Library', status: 'match' },
                    { s: 'Amazon', status: 'skipped' },
                  ].map((m) => (
                    <div key={m.s} style={{ display: 'flex', alignItems: 'center', gap: 10, fontSize: 13 }}>
                      <Icon
                        name={m.status === 'match' ? 'check-circle' : m.status === 'miss' ? 'x-circle' : 'circle'}
                        size={13}
                        style={{
                          color:
                            m.status === 'match'
                              ? 'oklch(0.58 0.12 140)'
                              : m.status === 'miss'
                                ? 'var(--color-accent)'
                                : 'var(--color-ink-4)',
                        }}
                      />
                      <span style={{ color: 'var(--color-ink-2)' }}>{m.s}</span>
                      <span
                        className="mono"
                        style={{ fontSize: 10.5, color: 'var(--color-ink-3)', marginLeft: 'auto' }}
                      >
                        {m.status}
                      </span>
                    </div>
                  ))}
                </div>

                <div
                  style={{
                    display: 'flex',
                    gap: 8,
                    borderTop: '1px solid var(--color-rule-soft)',
                    paddingTop: 20,
                  }}
                >
                  <button className="btn primary">
                    <Icon name="check" size={13} /> Approve & add to library
                  </button>
                  <button className="btn">Skip for now</button>
                  <div style={{ flex: 1 }} />
                  <button className="btn ghost" style={{ color: 'var(--color-accent-ink)' }}>
                    Discard file
                  </button>
                </div>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

type FieldProps = {
  label: string;
  value: string;
  placeholder?: string;
  readOnly?: boolean;
  select?: boolean;
};

function Field({ label, value, placeholder, readOnly, select }: FieldProps): ReactNode {
  return (
    <div>
      <div className="t-label" style={{ marginBottom: 6 }}>{label}</div>
      {select ? (
        <select className="input" defaultValue={value}>
          <option>{value}</option>
        </select>
      ) : (
        <input
          className="input"
          defaultValue={value}
          placeholder={placeholder}
          readOnly={readOnly}
          style={readOnly ? { background: 'var(--color-paper-2)', color: 'var(--color-ink-3)' } : undefined}
        />
      )}
    </div>
  );
}
