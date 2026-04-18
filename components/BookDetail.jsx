// Book detail page

const BookDetail = ({ bookId, onBack, onRead, onEdit }) => {
  const book = BOOKS.find(b => b.id === bookId);
  if (!book) return null;
  const [tab, setTab] = React.useState('overview');
  const bookNotes = NOTES.filter(n => n.bookId === bookId);

  const Meta = ({ label, children }) => (
    <div style={{ display: 'flex', gap: 14, padding: '7px 0', borderBottom: '1px dashed var(--rule-soft)' }}>
      <div className="t-label" style={{ width: 96, flexShrink: 0 }}>{label}</div>
      <div style={{ fontSize: 13.5, color: 'var(--ink-1)' }}>{children}</div>
    </div>
  );

  return (
    <div className="fade-in">
      <div style={{ padding: '16px 32px', borderBottom: '1px solid var(--rule-soft)', display: 'flex', alignItems: 'center', gap: 12 }}>
        <button className="btn ghost small" onClick={onBack}><Icon name="arrow-left" size={14} /> Back to library</button>
        <div style={{ flex: 1 }} />
        <button className="btn small" onClick={() => onEdit(bookId)}><Icon name="edit" size={13} /> Edit metadata</button>
        <button className="btn small"><Icon name="download" size={13} /> Download</button>
        <button className="btn small"><Icon name="device" size={13} /> Send to device</button>
        <button className="btn ghost icon-only"><Icon name="more" size={14} /></button>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '280px 1fr', gap: 48, padding: '40px 48px' }}>
        {/* Left — cover & actions */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
          <Cover book={book} size="hero" />
          <button className="btn primary" style={{ justifyContent: 'center', padding: '10px 14px', fontSize: 14 }} onClick={() => onRead(bookId)}>
            <Icon name="book-open" size={14} /> {book.progress > 0 && book.progress < 1 ? 'Continue reading' : 'Open book'}
          </button>
          {book.progress != null && book.progress > 0 && book.progress < 1 && (
            <div>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 6 }}>
                <span className="t-micro">Progress</span>
                <span className="mono" style={{ fontSize: 11 }}>{Math.round(book.progress * 100)}% · p.{Math.round(book.pages * book.progress)}/{book.pages}</span>
              </div>
              <div className="progress"><div style={{ width: `${book.progress * 100}%` }} /></div>
            </div>
          )}
          <div style={{ border: '1px solid var(--rule-soft)', padding: 16, background: 'var(--paper-0)', borderRadius: 2 }}>
            <div className="t-label" style={{ marginBottom: 10 }}>Shelves</div>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
              {(book.shelf || []).map(s => <span key={s} className="chip accent">{s}</span>)}
              <button className="chip" style={{ cursor: 'pointer' }}><Icon name="plus" size={10} /> Add</button>
            </div>
          </div>
        </div>

        {/* Right — info */}
        <div>
          <div className="t-micro" style={{ marginBottom: 8 }}>{book.format} · {book.year} · {book.pages} pages</div>
          <h1 className="t-display" style={{ marginBottom: 6, textWrap: 'balance' }}>{book.title}</h1>
          <div style={{ fontSize: 17, color: 'var(--ink-2)', fontStyle: 'italic', marginBottom: 16 }}>by {book.author}</div>

          <div style={{ display: 'flex', alignItems: 'center', gap: 16, marginBottom: 28 }}>
            <StarRating rating={book.rating} size={15} />
            <span className="mono" style={{ fontSize: 12, color: 'var(--ink-2)' }}>{book.rating.toFixed(1)}</span>
            <span style={{ color: 'var(--rule)' }}>·</span>
            {(book.tags || []).map(t => <span key={t} className="chip">{t}</span>)}
          </div>

          {book.description && (
            <p style={{ fontSize: 16, lineHeight: 1.65, color: 'var(--ink-1)', marginBottom: 32, maxWidth: 640, textWrap: 'pretty' }}>
              {book.description}
            </p>
          )}

          {/* Tabs */}
          <div style={{ borderBottom: '1px solid var(--rule-soft)', display: 'flex', gap: 0, marginBottom: 24 }}>
            {['overview','notes','annotations','versions','activity'].map(t => (
              <button key={t} onClick={() => setTab(t)}
                style={{
                  background: 'none', border: 'none', cursor: 'pointer',
                  padding: '10px 16px 12px',
                  fontFamily: 'Spectral, serif', fontSize: 13.5,
                  color: tab === t ? 'var(--ink-1)' : 'var(--ink-3)',
                  borderBottom: tab === t ? '2px solid var(--accent)' : '2px solid transparent',
                  fontWeight: tab === t ? 500 : 400,
                  textTransform: 'capitalize',
                }}>{t}{t === 'notes' && bookNotes.length ? ` · ${bookNotes.length}` : ''}</button>
            ))}
          </div>

          {tab === 'overview' && (
            <div style={{ maxWidth: 640 }}>
              <Meta label="Title">{book.title}</Meta>
              <Meta label="Author">{book.author}</Meta>
              {book.series && <Meta label="Series">{book.series}, Book {book.seriesNum}</Meta>}
              <Meta label="Published">{book.year}</Meta>
              <Meta label="Format">{book.format} · {book.pages} pages</Meta>
              <Meta label="Categories">{(book.tags || []).join(' · ')}</Meta>
              <Meta label="Added">{book.addedAt}</Meta>
              <Meta label="File path"><span className="mono" style={{ fontSize: 11.5, color: 'var(--ink-2)' }}>/books/main/{book.author.replace(/\W+/g,'_').toLowerCase()}/{book.title.replace(/\W+/g,'_').toLowerCase()}.{book.format.toLowerCase()}</span></Meta>
              <Meta label="Identifiers"><span className="mono" style={{ fontSize: 11.5 }}>ISBN · 978-1-{book.id.charCodeAt(1) * 7 + 24000}-{book.id.charCodeAt(1) * 3 + 100}-{book.id.charCodeAt(1) % 10}</span></Meta>
            </div>
          )}

          {tab === 'notes' && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14, maxWidth: 640 }}>
              {bookNotes.length === 0 && <div className="t-small" style={{ fontStyle: 'italic' }}>No notes yet. Highlights and margin notes you take while reading will appear here.</div>}
              {bookNotes.map(n => (
                <div key={n.id} style={{ borderLeft: '3px solid var(--accent-soft)', padding: '6px 14px', background: 'var(--paper-0)' }}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
                    <span className="t-micro">Page {n.page}{n.highlight ? ' · Highlight' : ''}</span>
                    <span className="t-micro">{n.date}</span>
                  </div>
                  <p style={{ fontSize: 14.5, lineHeight: 1.55, fontStyle: n.highlight ? 'italic' : 'normal' }}>{n.text}</p>
                </div>
              ))}
            </div>
          )}

          {tab === 'annotations' && (
            <div className="t-small" style={{ fontStyle: 'italic' }}>No PDF annotations for this book.</div>
          )}

          {tab === 'versions' && (
            <div style={{ maxWidth: 640 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 16, padding: '10px 12px', border: '1px solid var(--rule-soft)', background: 'var(--paper-0)' }}>
                <Icon name="book" size={16} />
                <div style={{ flex: 1 }}>
                  <div style={{ fontSize: 13.5, fontWeight: 500 }}>{book.title}.{book.format.toLowerCase()}</div>
                  <div className="mono" style={{ fontSize: 11, color: 'var(--ink-3)' }}>Primary · {book.format} · {(Math.random() * 3 + 1).toFixed(1)} MB</div>
                </div>
                <button className="btn small">Replace</button>
              </div>
            </div>
          )}

          {tab === 'activity' && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10, maxWidth: 640 }}>
              {['Started reading · Apr 12, 2026','Reached 25% · Apr 15','Note added on page 138 · Apr 15','Reached 50% · Apr 17','Note added on page 142 · Apr 15'].map((e, i) => (
                <div key={i} style={{ display: 'flex', gap: 14, fontSize: 13 }}>
                  <Icon name="dot" size={10} style={{ marginTop: 5, color: 'var(--accent)' }} />
                  <span style={{ color: 'var(--ink-2)' }}>{e}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
};

window.BookDetail = BookDetail;
