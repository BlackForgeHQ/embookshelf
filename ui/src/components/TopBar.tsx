import { Fragment, type ReactNode } from 'react';

import { Icon } from './Icon';
import { Input } from '@/components/ui/input';

type TopBarProps = {
  title: ReactNode;
  subtitle?: ReactNode;
  search?: string;
  setSearch?: (value: string) => void;
  right?: ReactNode;
  crumbs?: string[];
};

// Top bar — sticky header above each main view. Matches the prototype's
// padding + sticky behavior so sidebar scroll and crumb layout line up.
export function TopBar({ title, subtitle, search, setSearch, right, crumbs }: TopBarProps) {
  return (
    <div
      style={{
        padding: '18px 32px 14px',
        borderBottom: '1px solid var(--color-rule-soft)',
        background: 'var(--color-paper-1)',
        position: 'sticky',
        top: 0,
        zIndex: 10,
      }}
    >
      {crumbs && crumbs.length > 0 && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 8 }}>
          {crumbs.map((c, i) => (
            <Fragment key={`${i}-${c}`}>
              {i > 0 && <Icon name="chevron-right" size={12} className="mono" />}
              <span
                className="t-micro"
                style={{ color: i === crumbs.length - 1 ? 'var(--color-ink-2)' : 'var(--color-ink-3)' }}
              >
                {c}
              </span>
            </Fragment>
          ))}
        </div>
      )}
      <div style={{ display: 'flex', alignItems: 'flex-end', gap: 24 }}>
        <div style={{ flex: 1 }}>
          <h1 className="t-h1" style={{ fontWeight: 500 }}>{title}</h1>
          {subtitle && (
            <div style={{ color: 'var(--color-ink-3)', fontSize: 14, marginTop: 4, fontStyle: 'italic' }}>
              {subtitle}
            </div>
          )}
        </div>
        {setSearch && (
          <div style={{ position: 'relative', width: 280 }}>
            <Input
              placeholder="Search library…"
              value={search ?? ''}
              onChange={(e) => setSearch(e.target.value)}
              className="pl-8"
            />
            <div
              style={{
                position: 'absolute',
                left: 10,
                top: '50%',
                transform: 'translateY(-50%)',
                color: 'var(--color-ink-3)',
                pointerEvents: 'none',
              }}
            >
              <Icon name="search" size={14} />
            </div>
          </div>
        )}
        {right}
      </div>
    </div>
  );
}
