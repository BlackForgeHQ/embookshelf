import { Fragment, useState } from 'react';
import { createFileRoute, useNavigate } from '@tanstack/react-router';

import { Icon } from '@/components/Icon';
import { findBook, NOTES, READER_CONTENT } from '@/data/mock';

export const Route = createFileRoute('/read/$id')({
  component: Reader,
});

// Rough chapter labels + first-page offsets, keyed to the prototype design.
const TOC = [
  { title: 'Prologue', page: 1 },
  { title: "1. The Surveyor's House", page: 8 },
  { title: '2. Northlight', page: 24 },
  { title: '3. Instrument of Rain', page: 45 },
  { title: '4. The Inlet', page: 68 },
  { title: '5. Ingrid', page: 92 },
  { title: '6. A Map of Absence', page: 118 },
  { title: "7. The Mapmaker's Return", page: 138 },
  { title: '8. Ledgers', page: 164 },
  { title: '9. The Watermark', page: 191 },
  { title: 'Epilogue', page: 220 },
];

function Reader() {
  const { id } = Route.useParams();
  const navigate = useNavigate();
  const book = findBook(id);

  const totalPages = book?.pages ?? 412;
  const [page, setPage] = useState(138);
  const [chromeVisible, setChromeVisible] = useState(true);
  const [tocOpen, setTocOpen] = useState(false);
  const [notesOpen, setNotesOpen] = useState(false);
  const [typePanelOpen, setTypePanelOpen] = useState(false);

  const pct = page / totalPages;

  // Reader typography — bound to the design's Tweaks defaults. Wire these
  // to a user-preferences backend once JSON endpoints land.
  const readerFont = 'Literata, Georgia, serif';
  const readerSize = 19;
  const readerLine = 1.65;

  const closePanels = () => {
    setTocOpen(false);
    setNotesOpen(false);
    setTypePanelOpen(false);
  };

  return (
    <div
      className="fade-in"
      style={{
        position: 'fixed',
        inset: 0,
        background: 'var(--color-paper-0)',
        zIndex: 200,
        display: 'flex',
        flexDirection: 'column',
      }}
    >
      {chromeVisible && (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 12,
            padding: '10px 22px',
            borderBottom: '1px solid var(--color-rule-soft)',
            background: 'var(--color-paper-1)',
          }}
        >
          <button
            className="btn ghost small"
            onClick={() => void navigate({ to: '/book/$id', params: { id } })}
          >
            <Icon name="arrow-left" size={14} /> Library
          </button>
          <div style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
            <div style={{ fontSize: 13, fontWeight: 500, fontStyle: 'italic' }}>{book?.title}</div>
            <div className="t-micro" style={{ fontSize: 10 }}>
              {book?.author} · p.{page} / {totalPages}
            </div>
          </div>
          <button
            className={`btn ghost small ${tocOpen ? 'primary' : ''}`}
            onClick={() => {
              const next = !tocOpen;
              closePanels();
              setTocOpen(next);
            }}
          >
            <Icon name="contents" size={14} />
          </button>
          <button
            className={`btn ghost small ${typePanelOpen ? 'primary' : ''}`}
            onClick={() => {
              const next = !typePanelOpen;
              closePanels();
              setTypePanelOpen(next);
            }}
          >
            <Icon name="aA" size={14} />
          </button>
          <button className="btn ghost small" aria-label="Bookmark">
            <Icon name="bookmark" size={14} />
          </button>
          <button
            className={`btn ghost small ${notesOpen ? 'primary' : ''}`}
            onClick={() => {
              const next = !notesOpen;
              closePanels();
              setNotesOpen(next);
            }}
          >
            <Icon name="note" size={14} />
          </button>
          <button className="btn ghost small" onClick={() => setChromeVisible(false)} title="Hide chrome">
            <Icon name="close" size={14} />
          </button>
        </div>
      )}

      <div style={{ flex: 1, display: 'flex', overflow: 'hidden', position: 'relative' }}>
        {tocOpen && (
          <aside
            style={{
              width: 280,
              borderRight: '1px solid var(--color-rule-soft)',
              background: 'var(--color-paper-1)',
              overflow: 'auto',
              padding: '18px 0',
            }}
          >
            <div className="t-label" style={{ padding: '0 20px 10px' }}>Contents</div>
            {TOC.map((c, i) => {
              const active = i === 7;
              return (
                <button
                  key={c.title}
                  onClick={() => setPage(c.page)}
                  style={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'baseline',
                    padding: '8px 20px',
                    width: '100%',
                    textAlign: 'left',
                    border: 'none',
                    background: active ? 'var(--color-paper-3)' : 'transparent',
                    fontFamily: 'var(--font-serif)',
                    fontSize: 13.5,
                    color: active ? 'var(--color-ink-1)' : 'var(--color-ink-2)',
                    cursor: 'pointer',
                    borderLeft: active ? '2px solid var(--color-accent)' : '2px solid transparent',
                  }}
                >
                  <span style={{ fontStyle: i === 0 || i === TOC.length - 1 ? 'italic' : 'normal' }}>
                    {c.title}
                  </span>
                  <span className="mono" style={{ fontSize: 10.5, color: 'var(--color-ink-3)' }}>
                    p.{c.page}
                  </span>
                </button>
              );
            })}
          </aside>
        )}

        {/* Reading area */}
        <div
          onClick={() => setChromeVisible(true)}
          style={{
            flex: 1,
            overflow: 'auto',
            padding: '64px 40px 80px',
            display: 'flex',
            justifyContent: 'center',
            background: 'var(--color-paper-0)',
          }}
        >
          <div style={{ maxWidth: 640, width: '100%' }}>
            <div className="t-label" style={{ textAlign: 'center', marginBottom: 8 }}>
              {READER_CONTENT.chapter}
            </div>
            <h1
              style={{
                fontFamily: readerFont,
                textAlign: 'center',
                fontSize: 28,
                fontWeight: 500,
                marginBottom: 48,
                letterSpacing: '-0.01em',
              }}
            >
              {READER_CONTENT.title}
            </h1>
            {READER_CONTENT.paragraphs.map((p, i) => {
              const style = {
                fontFamily: readerFont,
                fontSize: readerSize,
                lineHeight: readerLine,
                marginBottom: 18,
                textIndent: i === 0 ? 0 : '2em',
                color: 'var(--color-ink-1)',
                textAlign: 'justify' as const,
                hyphens: 'auto' as const,
                textWrap: 'pretty' as const,
              };
              if (i === 0) {
                return (
                  <p key={i} style={style}>
                    <span
                      style={{
                        fontSize: readerSize * 3.2,
                        fontWeight: 500,
                        float: 'left',
                        lineHeight: 0.9,
                        marginRight: 6,
                        marginTop: 6,
                        fontFamily: readerFont,
                      }}
                    >
                      {p[0]}
                    </span>
                    {p.slice(1)}
                  </p>
                );
              }
              if (i === 6) {
                const phrase = 'she had come six hundred miles north';
                const idx = p.indexOf(phrase);
                if (idx < 0) return <p key={i} style={style}>{p}</p>;
                return (
                  <p key={i} style={style}>
                    {p.slice(0, idx)}
                    <span
                      style={{
                        background: 'oklch(0.92 0.07 85)',
                        padding: '0 2px',
                        borderBottom: '1px solid oklch(0.65 0.12 85)',
                      }}
                    >
                      {p.slice(idx, idx + phrase.length)}
                    </span>
                    {p.slice(idx + phrase.length)}
                  </p>
                );
              }
              return (
                <Fragment key={i}>
                  <p style={style}>{p}</p>
                </Fragment>
              );
            })}
            <div
              style={{ textAlign: 'center', margin: '40px 0', color: 'var(--color-ink-4)', letterSpacing: '1em' }}
            >
              · · ·
            </div>
          </div>
        </div>

        {/* Right notes panel */}
        {notesOpen && (
          <aside
            style={{
              width: 300,
              borderLeft: '1px solid var(--color-rule-soft)',
              background: 'var(--color-paper-1)',
              overflow: 'auto',
              padding: '18px 16px',
            }}
          >
            <div className="t-label" style={{ marginBottom: 12 }}>Notes on this book</div>
            {NOTES.filter((n) => n.bookId === id).map((n) => (
              <div
                key={n.id}
                style={{
                  borderLeft: '3px solid var(--color-accent-soft)',
                  padding: '4px 12px',
                  marginBottom: 14,
                  background: 'var(--color-paper-0)',
                }}
              >
                <div className="t-micro" style={{ fontSize: 10, marginBottom: 4 }}>
                  Page {n.page}
                  {n.highlight ? ' · Highlight' : ''}
                </div>
                <p
                  style={{
                    fontSize: 13,
                    lineHeight: 1.5,
                    fontStyle: n.highlight ? 'italic' : 'normal',
                  }}
                >
                  {n.text}
                </p>
              </div>
            ))}
            <button
              className="btn small"
              style={{ width: '100%', justifyContent: 'center', marginTop: 8 }}
            >
              <Icon name="plus" size={12} /> New note
            </button>
          </aside>
        )}

        {/* Type panel (floating) */}
        {typePanelOpen && (
          <div
            style={{
              position: 'absolute',
              top: 0,
              right: 16,
              width: 260,
              background: 'var(--color-paper-0)',
              border: '1px solid var(--color-ink-3)',
              boxShadow: '0 12px 32px -8px oklch(0.2 0.02 60 / 0.22)',
              padding: '14px 16px',
              borderRadius: 2,
              zIndex: 5,
            }}
          >
            <div className="t-label" style={{ marginBottom: 10 }}>Reader type</div>
            <div style={{ fontSize: 12, color: 'var(--color-ink-3)', fontStyle: 'italic' }}>
              Font + size controls land once user preferences sync from the backend.
            </div>
          </div>
        )}
      </div>

      {/* Bottom — progress + page controls */}
      <div
        style={{
          borderTop: '1px solid var(--color-rule-soft)',
          padding: '10px 22px',
          display: 'flex',
          alignItems: 'center',
          gap: 14,
          background: 'var(--color-paper-1)',
        }}
      >
        <button className="btn ghost small" onClick={() => setPage((p) => Math.max(1, p - 1))}>
          <Icon name="chevron-left" size={14} /> Prev
        </button>
        <div style={{ flex: 1, display: 'flex', alignItems: 'center', gap: 12 }}>
          <span className="mono" style={{ fontSize: 10.5, color: 'var(--color-ink-3)' }}>
            p.{page}
          </span>
          <div
            style={{
              flex: 1,
              position: 'relative',
              height: 4,
              background: 'var(--color-paper-3)',
              borderRadius: 2,
            }}
          >
            <div
              style={{
                height: 4,
                width: `${pct * 100}%`,
                background: 'var(--color-accent)',
                borderRadius: 2,
              }}
            />
            <input
              type="range"
              min={1}
              max={totalPages}
              value={page}
              onChange={(e) => setPage(Number(e.target.value))}
              style={{
                position: 'absolute',
                inset: 0,
                width: '100%',
                opacity: 0,
                cursor: 'pointer',
              }}
            />
          </div>
          <span className="mono" style={{ fontSize: 10.5, color: 'var(--color-ink-3)' }}>
            p.{totalPages}
          </span>
        </div>
        <span className="mono" style={{ fontSize: 10.5, color: 'var(--color-ink-3)' }}>
          {Math.round(pct * 100)}% · 23 min left in chapter
        </span>
        <button
          className="btn ghost small"
          onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
        >
          Next <Icon name="chevron-right" size={14} />
        </button>
      </div>
    </div>
  );
}
