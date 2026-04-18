// Sidebar with libraries, shelves, magic shelves, user

const Sidebar = ({ view, setView, activeLibrary, setActiveLibrary, activeShelf, setActiveShelf }) => {
  const Item = ({ icon, label, count, active, onClick, indent = 0, color }) => (
    <button
      onClick={onClick}
      style={{
        display: 'flex', alignItems: 'center', gap: 10,
        padding: `7px 16px 7px ${16 + indent}px`,
        background: active ? 'var(--paper-3)' : 'transparent',
        border: 'none', width: '100%', textAlign: 'left', cursor: 'pointer',
        color: active ? 'var(--ink-1)' : 'var(--ink-2)',
        fontFamily: 'Spectral, serif', fontSize: 14,
        borderLeft: active ? '2px solid var(--accent)' : '2px solid transparent',
      }}
      onMouseEnter={e => { if (!active) e.currentTarget.style.background = 'var(--paper-3)'; }}
      onMouseLeave={e => { if (!active) e.currentTarget.style.background = 'transparent'; }}
    >
      {color && <span style={{ width: 7, height: 7, borderRadius: '50%', background: color, flexShrink: 0 }} />}
      {icon && <Icon name={icon} size={15} />}
      <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{label}</span>
      {count != null && <span className="mono" style={{ fontSize: 10.5, color: 'var(--ink-3)' }}>{count}</span>}
    </button>
  );

  const Section = ({ title, action, children }) => (
    <div style={{ marginBottom: 18 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '0 18px 6px' }}>
        <div className="t-label">{title}</div>
        {action}
      </div>
      {children}
    </div>
  );

  return (
    <aside className="sidebar">
      {/* Brand */}
      <div style={{ padding: '0 18px 20px', display: 'flex', alignItems: 'center', gap: 10 }}>
        <div style={{
          width: 26, height: 26, background: 'var(--ink-1)', color: 'var(--paper-0)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          fontFamily: 'Spectral, serif', fontWeight: 600, fontSize: 16, fontStyle: 'italic',
          borderRadius: 2,
        }}>e</div>
        <div style={{ fontFamily: 'Spectral, serif', fontSize: 18, fontWeight: 500, letterSpacing: '-0.01em' }}>embookshelf</div>
      </div>

      <Section title="Browse">
        <Item icon="home" label="Dashboard" active={view === 'dashboard'} onClick={() => setView('dashboard')} />
        <Item icon="library" label="All Books" active={view === 'library' && !activeShelf} onClick={() => { setView('library'); setActiveShelf(null); }} count={1202} />
        <Item icon="book-open" label="Reading Now" active={view === 'library' && activeShelf === 'reading'} onClick={() => { setView('library'); setActiveShelf('reading'); }} count={3} />
        <Item icon="note" label="Notebook" active={view === 'notebook'} onClick={() => setView('notebook')} count={84} />
        <Item icon="upload" label="BookDrop" active={view === 'bookdrop'} onClick={() => setView('bookdrop')} count={5} />
      </Section>

      <Section title="Libraries" action={<button className="btn ghost small" style={{ padding: '2px 4px' }}><Icon name="plus" size={12} /></button>}>
        {LIBRARIES.map(lib => (
          <Item key={lib.id} label={lib.name} count={lib.count} color={lib.color}
            active={activeLibrary === lib.id && view === 'library'}
            onClick={() => { setActiveLibrary(lib.id); setView('library'); setActiveShelf(null); }} />
        ))}
      </Section>

      <Section title="Shelves" action={<button className="btn ghost small" style={{ padding: '2px 4px' }}><Icon name="plus" size={12} /></button>}>
        {SHELVES.map(s => (
          <Item key={s.id} label={s.name} count={s.count} icon={s.icon}
            active={view === 'library' && activeShelf === s.id}
            onClick={() => { setView('library'); setActiveShelf(s.id); }} indent={4} />
        ))}
      </Section>

      <Section title="Magic Shelves">
        {MAGIC_SHELVES.map(s => (
          <Item key={s.id} label={s.name} count={s.count} icon="sparkle"
            active={view === 'library' && activeShelf === s.id}
            onClick={() => { setView('library'); setActiveShelf(s.id); }} indent={4} />
        ))}
      </Section>

      <div style={{ flex: 1 }} />

      <div style={{ padding: '12px 16px', borderTop: '1px solid var(--rule-soft)', display: 'flex', alignItems: 'center', gap: 10 }}>
        <div style={{
          width: 28, height: 28, borderRadius: '50%', background: 'var(--accent)', color: 'var(--paper-0)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          fontFamily: 'Spectral, serif', fontSize: 12, fontWeight: 500,
        }}>{CURRENT_USER.initials}</div>
        <div style={{ flex: 1, overflow: 'hidden' }}>
          <div style={{ fontSize: 13, fontWeight: 500 }}>{CURRENT_USER.name}</div>
          <div className="t-micro" style={{ fontSize: 10 }}>{CURRENT_USER.role}</div>
        </div>
        <button className="btn ghost icon-only" onClick={() => setView('settings')} title="Settings">
          <Icon name="settings" size={14} />
        </button>
      </div>
    </aside>
  );
};

window.Sidebar = Sidebar;
