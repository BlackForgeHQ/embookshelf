import { createFileRoute } from '@tanstack/react-router';

import { TopBar } from '@/components/TopBar';

export const Route = createFileRoute('/_app/settings')({
  component: Settings,
});

const SECTIONS = [
  'Account',
  'Reading preferences',
  'Libraries',
  'Metadata providers',
  'Device sync',
  'Email delivery',
  'Users & roles',
  'Backups',
  'About',
];

const AUTH_METHODS: ReadonlyArray<{ n: string; on: boolean; sub: string }> = [
  { n: 'Local (session)', on: true,  sub: 'Username + password' },
  { n: 'OIDC',            on: true,  sub: 'authentik.home.local' },
  { n: 'Remote / Forward Auth', on: false, sub: 'Reverse proxy headers' },
];

function Settings() {
  return (
    <div className="fade-in">
      <TopBar title="Settings" subtitle="Instance, users, metadata providers, sync." />
      <div
        style={{
          padding: '28px 32px',
          display: 'grid',
          gridTemplateColumns: '220px 1fr',
          gap: 40,
          maxWidth: 900,
        }}
      >
        <nav style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          {SECTIONS.map((x, i) => (
            <button
              key={x}
              style={{
                padding: '8px 12px',
                textAlign: 'left',
                background: i === 0 ? 'var(--color-paper-3)' : 'transparent',
                border: 'none',
                cursor: 'pointer',
                fontFamily: 'var(--font-serif)',
                fontSize: 13.5,
                borderLeft: i === 0 ? '2px solid var(--color-accent)' : '2px solid transparent',
                color: i === 0 ? 'var(--color-ink-1)' : 'var(--color-ink-2)',
              }}
            >
              {x}
            </button>
          ))}
        </nav>

        <div style={{ maxWidth: 560 }}>
          <h2 className="t-h2" style={{ marginBottom: 24 }}>Account</h2>

          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 16,
              padding: 16,
              border: '1px solid var(--color-rule-soft)',
              background: 'var(--color-paper-0)',
              marginBottom: 24,
            }}
          >
            <div
              style={{
                width: 48,
                height: 48,
                borderRadius: '50%',
                background: 'var(--color-accent)',
                color: 'var(--color-paper-0)',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                fontFamily: 'var(--font-serif)',
                fontSize: 18,
                fontWeight: 500,
              }}
            >
              RA
            </div>
            <div>
              <div style={{ fontSize: 15, fontWeight: 500 }}>Rowan Ashby</div>
              <div className="t-small" style={{ fontSize: 12 }}>
                rowan@home.local · Admin · joined Aug 2024
              </div>
            </div>
            <div style={{ flex: 1 }} />
            <button className="btn small">Change password</button>
          </div>

          <div className="t-label" style={{ marginBottom: 10 }}>Authentication</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 24 }}>
            {AUTH_METHODS.map((a) => (
              <div
                key={a.n}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 14,
                  padding: '10px 14px',
                  border: '1px solid var(--color-rule-soft)',
                  background: 'var(--color-paper-0)',
                }}
              >
                <span
                  style={{
                    width: 8,
                    height: 8,
                    borderRadius: '50%',
                    background: a.on ? 'oklch(0.58 0.12 140)' : 'var(--color-ink-4)',
                  }}
                />
                <div style={{ flex: 1 }}>
                  <div style={{ fontSize: 13.5, fontWeight: 500 }}>{a.n}</div>
                  <div className="t-small" style={{ fontSize: 11.5 }}>{a.sub}</div>
                </div>
                <span className="t-micro">{a.on ? 'enabled' : 'disabled'}</span>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}
