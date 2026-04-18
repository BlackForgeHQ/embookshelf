// Reader view — EPUB-style reflowable reader

const Reader = ({ bookId, onExit, readerFont, readerSize, readerLine }) => {
  const book = BOOKS.find(b => b.id === bookId);
  const [page, setPage] = React.useState(138);
  const [chromeVisible, setChromeVisible] = React.useState(true);
  const [tocOpen, setTocOpen] = React.useState(false);
  const [notesOpen, setNotesOpen] = React.useState(false);
  const [typePanelOpen, setTypePanelOpen] = React.useState(false);

  const totalPages = book?.pages || 412;
  const pct = page / totalPages;

  return (
    <div style={{ position: 'fixed', inset: 0, background: 'var(--paper-0)', zIndex: 200, display: 'flex', flexDirection: 'column' }} className="fade-in">
      {/* Top chrome */}
      {chromeVisible && (
        <div style={{
          display: 'flex', alignItems: 'center', gap: 12,
          padding: '10px 22px',
          borderBottom: '1px solid var(--rule-soft)',
          background: 'var(--paper-1)',
        }}>
          <button className="btn ghost small" onClick={onExit}><Icon name="arrow-left" size={14} /> Library</button>
          <div style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
            <div style={{ fontSize: 13, fontWeight: 500, fontStyle: 'italic' }}>{book?.title}</div>
            <div className="t-micro" style={{ fontSize: 10 }}>{book?.author} · p.{page} / {totalPages}</div>
          </div>
          <button className={`btn ghost small ${tocOpen ? 'primary' : ''}`} onClick={() => { setTocOpen(v => !v); setNotesOpen(false); setTypePanelOpen(false); }}><Icon name="contents" size={14} /></button>
          <button className={`btn ghost small ${typePanelOpen ? 'primary' : ''}`} onClick={() => { setTypePanelOpen(v => !v); setTocOpen(false); setNotesOpen(false); }}><Icon name="aA" size={14} /></button>
          <button className="btn ghost small"><Icon name="bookmark" size={14} /></button>
          <button className={`btn ghost small ${notesOpen ? 'primary' : ''}`} onClick={() => { setNotesOpen(v => !v); setTocOpen(false); setTypePanelOpen(false); }}><Icon name="note" size={14} /></button>
          <button className="btn ghost small" onClick={() => setChromeVisible(false)} title="Hide chrome">
            <Icon name="close" size={14} />
          </button>
        </div>
      )}

      <div style={{ flex: 1, display: 'flex', overflow: 'hidden', position: 'relative' }}>
        {/* Left TOC */}
        {tocOpen && (
          <aside style={{ width: 280, borderRight: '1px solid var(--rule-soft)', background: 'var(--paper-1)', overflow: 'auto', padding: '18px 0' }}>
            <div className="t-label" style={{ padding: '0 20px 10px' }}>Contents</div>
            {['Prologue','1. The Surveyor\'s House','2. Northlight','3. Instrument of Rain','4. The Inlet','5. Ingrid','6. A Map of Absence','7. The Mapmaker\'s Return','8. Ledgers','9. The Watermark','Epilogue'].map((c, i) => (
              <button key={i} style={{
                display: 'flex', justifyContent: 'space-between', alignItems: 'baseline',
                padding: '8px 20px', width: '100%', textAlign: 'left', border: 'none', background: i === 7 ? 'var(--paper-3)' : 'transparent',
                fontFamily: 'Spectral, serif', fontSize: 13.5, color: i === 7 ? 'var(--ink-1)' : 'var(--ink-2)',
                cursor: 'pointer', borderLeft: i === 7 ? '2px solid var(--accent)' : '2px solid transparent',
              }}>
                <span style={{ fontStyle: i === 0 || i === 10 ? 'italic' : 'normal' }}>{c}</span>
                <span className="mono" style={{ fontSize: 10.5, color: 'var(--ink-3)' }}>p.{[1,8,24,45,68,92,118,138,164,191,220][i]}</span>
              </button>
            ))}
          </aside>
        )}

        {/* Reading area */}
        <div
          onClick={() => setChromeVisible(true)}
          style={{
            flex: 1, overflow: 'auto', padding: '64px 40px 80px',
            display: 'flex', justifyContent: 'center',
            background: 'var(--paper-0)',
          }}
        >
          <div style={{ maxWidth: 640, width: '100%' }}>
            <div className="t-label" style={{ textAlign: 'center', marginBottom: 8 }}>{READER_CONTENT.chapter}</div>
            <h1 style={{ fontFamily: readerFont, textAlign: 'center', fontSize: 28, fontWeight: 500, marginBottom: 48, letterSpacing: '-0.01em' }}>{READER_CONTENT.title}</h1>
            {READER_CONTENT.paragraphs.map((p, i) => (
              <p key={i} style={{
                fontFamily: readerFont,
                fontSize: readerSize,
                lineHeight: readerLine,
                marginBottom: 18,
                textIndent: i === 0 ? 0 : '2em',
                color: 'var(--ink-1)',
                textAlign: 'justify',
                hyphens: 'auto',
                textWrap: 'pretty',
              }}>
                {i === 0 ? <><span style={{ fontSize: readerSize * 3.2, fontWeight: 500, float: 'left', lineHeight: 0.9, marginRight: 6, marginTop: 6, fontFamily: readerFont }}>{p[0]}</span>{p.slice(1)}</> : (
                  // Highlight the phrase on paragraph index 6 ("she had come six hundred miles north…")
                  i === 6 ? (() => {
                    const phrase = 'she had come six hundred miles north';
                    const idx = p.indexOf(phrase);
                    if (idx < 0) return p;
                    return <>{p.slice(0, idx)}<span style={{ background: 'oklch(0.92 0.07 85)', padding: '0 2px', borderBottom: '1px solid oklch(0.65 0.12 85)' }}>{p.slice(idx, idx + phrase.length)}</span>{p.slice(idx + phrase.length)}</>;
                  })() : p
                )}
              </p>
            ))}
            <div style={{ textAlign: 'center', margin: '40px 0', color: 'var(--ink-4)', letterSpacing: '1em' }}>· · ·</div>
          </div>
        </div>

        {/* Right notes panel */}
        {notesOpen && (
          <aside style={{ width: 300, borderLeft: '1px solid var(--rule-soft)', background: 'var(--paper-1)', overflow: 'auto', padding: '18px 16px' }}>
            <div className="t-label" style={{ marginBottom: 12 }}>Notes on this book</div>
            {NOTES.filter(n => n.bookId === bookId).map(n => (
              <div key={n.id} style={{ borderLeft: '3px solid var(--accent-soft)', padding: '4px 12px', marginBottom: 14, background: 'var(--paper-0)' }}>
                <div className="t-micro" style={{ fontSize: 10, marginBottom: 4 }}>Page {n.page}{n.highlight ? ' · Highlight' : ''}</div>
                <p style={{ fontSize: 13, lineHeight: 1.5, fontStyle: n.highlight ? 'italic' : 'normal' }}>{n.text}</p>
              </div>
            ))}
            <button className="btn small" style={{ width: '100%', justifyContent: 'center', marginTop: 8 }}><Icon name="plus" size={12} /> New note</button>
          </aside>
        )}

        {/* Type panel (floating) */}
        {typePanelOpen && (
          <div style={{
            position: 'absolute', top: 0, right: 16, width: 260,
            background: 'var(--paper-0)', border: '1px solid var(--ink-3)',
            boxShadow: '0 12px 32px -8px oklch(0.2 0.02 60 / 0.22)',
            padding: '14px 16px', borderRadius: 2, zIndex: 5,
          }}>
            <div className="t-label" style={{ marginBottom: 10 }}>Reader Type</div>
            <div style={{ fontSize: 12, color: 'var(--ink-3)', fontStyle: 'italic' }}>Change these in the Tweaks panel →</div>
          </div>
        )}
      </div>

      {/* Bottom — progress + page controls */}
      <div style={{
        borderTop: '1px solid var(--rule-soft)',
        padding: '10px 22px', display: 'flex', alignItems: 'center', gap: 14,
        background: 'var(--paper-1)',
      }}>
        <button className="btn ghost small" onClick={() => setPage(p => Math.max(1, p - 1))}>
          <Icon name="chevron-left" size={14} /> Prev
        </button>
        <div style={{ flex: 1, display: 'flex', alignItems: 'center', gap: 12 }}>
          <span className="mono" style={{ fontSize: 10.5, color: 'var(--ink-3)' }}>p.{page}</span>
          <div style={{ flex: 1, position: 'relative', height: 4, background: 'var(--paper-3)', borderRadius: 2 }}>
            <div style={{ height: 4, width: `${pct * 100}%`, background: 'var(--accent)', borderRadius: 2 }} />
            <input type="range" min={1} max={totalPages} value={page} onChange={e => setPage(+e.target.value)}
              style={{ position: 'absolute', inset: 0, width: '100%', opacity: 0, cursor: 'pointer' }} />
          </div>
          <span className="mono" style={{ fontSize: 10.5, color: 'var(--ink-3)' }}>p.{totalPages}</span>
        </div>
        <span className="mono" style={{ fontSize: 10.5, color: 'var(--ink-3)' }}>{Math.round(pct * 100)}% · 23 min left in chapter</span>
        <button className="btn ghost small" onClick={() => setPage(p => Math.min(totalPages, p + 1))}>
          Next <Icon name="chevron-right" size={14} />
        </button>
      </div>
    </div>
  );
};

window.Reader = Reader;
