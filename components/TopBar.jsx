// Top bar with search, filters, view toggle

const TopBar = ({ title, subtitle, search, setSearch, right, crumbs }) => {
  return (
    <div style={{
      padding: '18px 32px 14px',
      borderBottom: '1px solid var(--rule-soft)',
      background: 'var(--paper-1)',
      position: 'sticky', top: 0, zIndex: 10,
    }}>
      {crumbs && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 8 }}>
          {crumbs.map((c, i) => (
            <React.Fragment key={i}>
              {i > 0 && <Icon name="chevron-right" size={12} className="mono" />}
              <span className="t-micro" style={{ color: i === crumbs.length - 1 ? 'var(--ink-2)' : 'var(--ink-3)' }}>{c}</span>
            </React.Fragment>
          ))}
        </div>
      )}
      <div style={{ display: 'flex', alignItems: 'flex-end', gap: 24 }}>
        <div style={{ flex: 1 }}>
          <h1 className="t-h1" style={{ fontWeight: 500 }}>{title}</h1>
          {subtitle && <div style={{ color: 'var(--ink-3)', fontSize: 14, marginTop: 4, fontStyle: 'italic' }}>{subtitle}</div>}
        </div>
        {setSearch && (
          <div style={{ position: 'relative', width: 280 }}>
            <Icon name="search" size={14} className="mono" />
            <input
              className="input"
              placeholder="Search library…"
              value={search || ''}
              onChange={e => setSearch(e.target.value)}
              style={{ paddingLeft: 32 }}
            />
            <div style={{ position: 'absolute', left: 10, top: '50%', transform: 'translateY(-50%)', color: 'var(--ink-3)' }}>
              <Icon name="search" size={14} />
            </div>
          </div>
        )}
        {right}
      </div>
    </div>
  );
};

window.TopBar = TopBar;
