import type { CSSProperties, MouseEventHandler, ReactNode } from 'react';
import { Link, useRouterState } from '@tanstack/react-router';

import { CURRENT_USER, LIBRARIES, MAGIC_SHELVES, SHELVES } from '@/data/mock';
import { Icon, type IconName } from './Icon';

// Sidebar is a pure navigation element — every item is a real router Link
// so browser back/forward, right-click, and deep-link all work.
export function Sidebar() {
  const state = useRouterState();
  const pathname = state.location.pathname;
  const search = state.location.search as { shelf?: string; library?: string };

  const isLibrary = pathname.startsWith('/library');
  const activeShelf = isLibrary ? search.shelf ?? null : null;
  const activeLibrary = isLibrary ? search.library ?? null : null;

  return (
    <aside className="sidebar">
      {/* Brand */}
      <div style={{ padding: '0 18px 20px', display: 'flex', alignItems: 'center', gap: 10 }}>
        <div
          style={{
            width: 26,
            height: 26,
            background: 'var(--color-ink-1)',
            color: 'var(--color-paper-0)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            fontFamily: 'var(--font-serif)',
            fontWeight: 600,
            fontSize: 16,
            fontStyle: 'italic',
            borderRadius: 2,
          }}
        >
          e
        </div>
        <div
          style={{
            fontFamily: 'var(--font-serif)',
            fontSize: 18,
            fontWeight: 500,
            letterSpacing: '-0.01em',
          }}
        >
          embookshelf
        </div>
      </div>

      <Section title="Browse">
        <NavItem to="/" icon="home" label="Dashboard" active={pathname === '/'} />
        <NavItem
          to="/library"
          icon="library"
          label="All Books"
          count={1202}
          active={isLibrary && !activeShelf && !activeLibrary}
        />
        <NavItem
          to="/library"
          search={{ shelf: 'reading' }}
          icon="book-open"
          label="Reading Now"
          count={3}
          active={activeShelf === 'reading'}
        />
        <NavItem to="/notebook" icon="note" label="Notebook" count={84} active={pathname === '/notebook'} />
        <NavItem to="/bookdrop" icon="upload" label="BookDrop" count={5} active={pathname === '/bookdrop'} />
      </Section>

      <Section
        title="Libraries"
        action={
          <button className="btn ghost small" style={{ padding: '2px 4px' }} aria-label="New library">
            <Icon name="plus" size={12} />
          </button>
        }
      >
        {LIBRARIES.map((lib) => (
          <NavItem
            key={lib.id}
            to="/library"
            search={{ library: lib.id }}
            label={lib.name}
            count={lib.count}
            color={lib.color}
            active={activeLibrary === lib.id}
          />
        ))}
      </Section>

      <Section
        title="Shelves"
        action={
          <button className="btn ghost small" style={{ padding: '2px 4px' }} aria-label="New shelf">
            <Icon name="plus" size={12} />
          </button>
        }
      >
        {SHELVES.map((s) => (
          <NavItem
            key={s.id}
            to="/library"
            search={{ shelf: s.id }}
            icon={s.icon as IconName}
            label={s.name}
            count={s.count}
            indent={4}
            active={activeShelf === s.id}
          />
        ))}
      </Section>

      <Section title="Magic Shelves">
        {MAGIC_SHELVES.map((s) => (
          <NavItem
            key={s.id}
            to="/library"
            search={{ shelf: s.id }}
            icon="sparkle"
            label={s.name}
            count={s.count}
            indent={4}
            active={activeShelf === s.id}
          />
        ))}
      </Section>

      <div style={{ flex: 1 }} />

      <div
        style={{
          padding: '12px 16px',
          borderTop: '1px solid var(--color-rule-soft)',
          display: 'flex',
          alignItems: 'center',
          gap: 10,
        }}
      >
        <div
          style={{
            width: 28,
            height: 28,
            borderRadius: '50%',
            background: 'var(--color-accent)',
            color: 'var(--color-paper-0)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            fontFamily: 'var(--font-serif)',
            fontSize: 12,
            fontWeight: 500,
          }}
        >
          {CURRENT_USER.initials}
        </div>
        <div style={{ flex: 1, overflow: 'hidden' }}>
          <div style={{ fontSize: 13, fontWeight: 500 }}>{CURRENT_USER.name}</div>
          <div className="t-micro" style={{ fontSize: 10 }}>{CURRENT_USER.role}</div>
        </div>
        <Link to="/settings" className="btn ghost icon-only" title="Settings">
          <Icon name="settings" size={14} />
        </Link>
      </div>
    </aside>
  );
}

type SectionProps = { title: string; action?: ReactNode; children: ReactNode };

function Section({ title, action, children }: SectionProps) {
  return (
    <div style={{ marginBottom: 18 }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          padding: '0 18px 6px',
        }}
      >
        <div className="t-label">{title}</div>
        {action}
      </div>
      {children}
    </div>
  );
}

type NavItemProps = {
  to: string;
  search?: Record<string, string | undefined>;
  icon?: IconName;
  label: string;
  count?: number;
  indent?: number;
  color?: string;
  active?: boolean;
  onClick?: MouseEventHandler<HTMLAnchorElement>;
};

function NavItem({ to, search, icon, label, count, indent = 0, color, active, onClick }: NavItemProps) {
  const style: CSSProperties = {
    display: 'flex',
    alignItems: 'center',
    gap: 10,
    padding: `7px 16px 7px ${16 + indent}px`,
    background: active ? 'var(--color-paper-3)' : 'transparent',
    border: 'none',
    width: '100%',
    textAlign: 'left',
    cursor: 'pointer',
    color: active ? 'var(--color-ink-1)' : 'var(--color-ink-2)',
    fontFamily: 'var(--font-serif)',
    fontSize: 14,
    textDecoration: 'none',
    borderLeft: active ? '2px solid var(--color-accent)' : '2px solid transparent',
  };

  return (
    <Link
      to={to}
      search={search as never}
      style={style}
      onClick={onClick}
      onMouseEnter={(e) => {
        if (!active) e.currentTarget.style.background = 'var(--color-paper-3)';
      }}
      onMouseLeave={(e) => {
        if (!active) e.currentTarget.style.background = 'transparent';
      }}
    >
      {color && (
        <span
          style={{
            width: 7,
            height: 7,
            borderRadius: '50%',
            background: color,
            flexShrink: 0,
          }}
        />
      )}
      {icon && <Icon name={icon} size={15} />}
      <span
        style={{
          flex: 1,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}
      >
        {label}
      </span>
      {count != null && (
        <span className="mono" style={{ fontSize: 10.5, color: 'var(--color-ink-3)' }}>
          {count}
        </span>
      )}
    </Link>
  );
}
