import { useState, type ReactNode } from 'react';
import {
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';

import { fetchMe, meQueryKey } from '@/api/auth';
import type { ApiError } from '@/api/client';
import {
  createLibraryPath,
  deleteLibraryPath,
  fetchSettingsLibraries,
  scanLibraryPath,
  settingsLibrariesQueryKey,
  type SettingsLibrary,
  type SettingsLibraryPath,
} from '@/api/settings';
import { Icon } from '@/components/Icon';
import { TopBar } from '@/components/TopBar';

export const Route = createFileRoute('/_app/settings')({
  component: Settings,
});

type SectionKey = 'account' | 'libraries' | 'placeholder';

type SectionSpec = { key: SectionKey; label: string; available: boolean };

const SECTIONS: SectionSpec[] = [
  { key: 'account', label: 'Account', available: true },
  { key: 'placeholder', label: 'Reading preferences', available: false },
  { key: 'libraries', label: 'Libraries', available: true },
  { key: 'placeholder', label: 'Metadata providers', available: false },
  { key: 'placeholder', label: 'Device sync', available: false },
  { key: 'placeholder', label: 'Email delivery', available: false },
  { key: 'placeholder', label: 'Users & roles', available: false },
  { key: 'placeholder', label: 'Backups', available: false },
  { key: 'placeholder', label: 'About', available: false },
];

function Settings() {
  const [activeLabel, setActiveLabel] = useState<string>('Account');
  const activeSection =
    SECTIONS.find((s) => s.label === activeLabel) ?? SECTIONS[0];

  return (
    <div className="fade-in">
      <TopBar title="Settings" subtitle="Instance, users, metadata providers, sync." />
      <div
        style={{
          padding: '28px 32px',
          display: 'grid',
          gridTemplateColumns: '220px 1fr',
          gap: 40,
          maxWidth: 960,
        }}
      >
        <nav style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          {SECTIONS.map((s) => {
            const selected = s.label === activeLabel;
            return (
              <button
                key={s.label}
                type="button"
                onClick={() => setActiveLabel(s.label)}
                disabled={!s.available}
                style={{
                  padding: '8px 12px',
                  textAlign: 'left',
                  background: selected ? 'var(--color-paper-3)' : 'transparent',
                  border: 'none',
                  cursor: s.available ? 'pointer' : 'default',
                  fontFamily: 'var(--font-serif)',
                  fontSize: 13.5,
                  borderLeft: selected ? '2px solid var(--color-accent)' : '2px solid transparent',
                  color: !s.available
                    ? 'var(--color-ink-4)'
                    : selected
                      ? 'var(--color-ink-1)'
                      : 'var(--color-ink-2)',
                  opacity: s.available ? 1 : 0.6,
                }}
              >
                {s.label}
              </button>
            );
          })}
        </nav>

        <div style={{ maxWidth: 640 }}>
          {activeSection.key === 'account' && <AccountPanel />}
          {activeSection.key === 'libraries' && <LibrariesPanel />}
          {activeSection.key === 'placeholder' && <PlaceholderPanel label={activeLabel} />}
        </div>
      </div>
    </div>
  );
}

const AUTH_METHODS: ReadonlyArray<{ n: string; on: boolean; sub: string }> = [
  { n: 'Local (session)', on: true,  sub: 'Username + password' },
  { n: 'OIDC',            on: false, sub: 'Pending' },
  { n: 'Remote / Forward Auth', on: false, sub: 'Reverse proxy headers' },
];

function AccountPanel() {
  const me = useQuery({ queryKey: meQueryKey, queryFn: fetchMe, staleTime: 60_000 });
  const user = me.data;
  const joined = user?.createdAt
    ? new Date(user.createdAt).toLocaleDateString(undefined, {
        month: 'short',
        year: 'numeric',
      })
    : '—';
  const roleLabel = user?.role === 'admin' ? 'Admin' : 'User';

  return (
    <>
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
          {user?.initials ?? '…'}
        </div>
        <div>
          <div style={{ fontSize: 15, fontWeight: 500 }}>{user?.display ?? '…'}</div>
          <div className="t-small" style={{ fontSize: 12 }}>
            {user?.email ?? '—'} · {roleLabel} · joined {joined}
          </div>
        </div>
        <div style={{ flex: 1 }} />
        <button className="btn small" disabled title="Endpoint pending">
          Change password
        </button>
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
    </>
  );
}

function LibrariesPanel() {
  const queryClient = useQueryClient();
  const me = useQuery({ queryKey: meQueryKey, queryFn: fetchMe, staleTime: 60_000 });
  const isAdmin = me.data?.role === 'admin';

  const libraries = useQuery({
    queryKey: settingsLibrariesQueryKey,
    queryFn: fetchSettingsLibraries,
    enabled: isAdmin,
  });

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: settingsLibrariesQueryKey });
    // Bust the public library list too — adding a path doesn't flip the
    // book count, but deleting one can, and a scan will.
    queryClient.invalidateQueries({ queryKey: ['libraries'] });
  };

  const createMut = useMutation({
    mutationFn: ({ libraryId, path }: { libraryId: string; path: string }) =>
      createLibraryPath(libraryId, path),
    onSuccess: invalidate,
  });
  const deleteMut = useMutation({
    mutationFn: (id: string) => deleteLibraryPath(id),
    onSuccess: invalidate,
  });
  const scanMut = useMutation({
    mutationFn: (id: string) => scanLibraryPath(id),
    onSuccess: invalidate,
  });

  const error = (createMut.error ?? deleteMut.error ?? scanMut.error) as
    | ApiError
    | null;

  if (!isAdmin) {
    return (
      <>
        <h2 className="t-h2" style={{ marginBottom: 24 }}>Libraries</h2>
        <div className="t-small" style={{ fontStyle: 'italic' }}>
          Library filesystem settings are admin-only.
        </div>
      </>
    );
  }

  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 8 }}>Libraries</h2>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: 'italic' }}>
        Register filesystem roots each library scans. Scans discover new
        files and enqueue them through the BookDrop review queue.
      </p>

      {error && (
        <div
          style={{
            padding: '10px 14px',
            border: '1px solid var(--color-accent-soft)',
            background: 'var(--color-accent-soft)',
            color: 'var(--color-accent-ink)',
            borderRadius: 2,
            fontSize: 13,
            marginBottom: 20,
          }}
        >
          {error.message}
        </div>
      )}

      {libraries.isLoading && (
        <div className="t-small" style={{ fontStyle: 'italic' }}>
          Loading libraries…
        </div>
      )}

      {(libraries.data ?? []).map((lib) => (
        <LibraryCard
          key={lib.id}
          library={lib}
          busy={
            createMut.isPending ||
            deleteMut.isPending ||
            scanMut.isPending
          }
          onAddPath={(path) => createMut.mutate({ libraryId: lib.id, path })}
          onDeletePath={(id) => deleteMut.mutate(id)}
          onScanPath={(id) => scanMut.mutate(id)}
        />
      ))}
    </>
  );
}

type LibraryCardProps = {
  library: SettingsLibrary;
  busy: boolean;
  onAddPath: (path: string) => void;
  onDeletePath: (id: string) => void;
  onScanPath: (id: string) => void;
};

function LibraryCard({ library, busy, onAddPath, onDeletePath, onScanPath }: LibraryCardProps) {
  const [draft, setDraft] = useState('');

  return (
    <div
      style={{
        border: '1px solid var(--color-rule-soft)',
        background: 'var(--color-paper-0)',
        padding: 18,
        marginBottom: 20,
        borderRadius: 2,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 12, marginBottom: 14 }}>
        <div>
          <div style={{ fontSize: 15, fontWeight: 500 }}>{library.name}</div>
          <div className="mono" style={{ fontSize: 11, color: 'var(--color-ink-3)' }}>
            /{library.slug}
          </div>
        </div>
        <div style={{ flex: 1 }} />
        <span className="t-micro">{library.bookCount} volumes</span>
      </div>

      {library.paths.length === 0 ? (
        <div className="t-small" style={{ fontStyle: 'italic', marginBottom: 14 }}>
          No paths yet.
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginBottom: 14 }}>
          {library.paths.map((p) => (
            <PathRow
              key={p.id}
              path={p}
              busy={busy}
              onDelete={() => onDeletePath(p.id)}
              onScan={() => onScanPath(p.id)}
            />
          ))}
        </div>
      )}

      <form
        onSubmit={(e) => {
          e.preventDefault();
          const value = draft.trim();
          if (!value) return;
          onAddPath(value);
          setDraft('');
        }}
        style={{ display: 'flex', gap: 8, borderTop: '1px dashed var(--color-rule-soft)', paddingTop: 14 }}
      >
        <input
          className="input mono"
          placeholder="/absolute/path/to/books"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          style={{ flex: 1, fontSize: 12.5 }}
        />
        <button type="submit" className="btn" disabled={busy || draft.trim() === ''}>
          <Icon name="plus" size={13} /> Add path
        </button>
      </form>
    </div>
  );
}

function PathRow({
  path,
  busy,
  onDelete,
  onScan,
}: {
  path: SettingsLibraryPath;
  busy: boolean;
  onDelete: () => void;
  onScan: () => void;
}): ReactNode {
  const lastScanned = path.lastScannedAt
    ? new Date(path.lastScannedAt).toLocaleString()
    : 'never';
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 12,
        padding: '10px 12px',
        background: 'var(--color-paper-2)',
        borderRadius: 2,
      }}
    >
      <div style={{ flex: 1, minWidth: 0 }}>
        <div
          className="mono"
          style={{
            fontSize: 12,
            color: 'var(--color-ink-1)',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }}
        >
          {path.path}
        </div>
        <div className="t-small" style={{ fontSize: 11.5, color: 'var(--color-ink-3)' }}>
          last scan {lastScanned} · {path.fileCount} files · {path.discoveredCount} discovered
        </div>
      </div>
      <button
        type="button"
        className="btn small"
        onClick={onScan}
        disabled={busy}
        title="Enqueue a library scan"
      >
        <Icon name="refresh" size={12} /> Scan
      </button>
      <button
        type="button"
        className="btn ghost small"
        onClick={() => {
          if (window.confirm(`Remove ${path.path}?\nImported books stay in the library.`)) {
            onDelete();
          }
        }}
        disabled={busy}
        style={{ color: 'var(--color-accent-ink)' }}
        aria-label="Remove path"
      >
        <Icon name="close" size={12} />
      </button>
    </div>
  );
}

function PlaceholderPanel({ label }: { label: string }) {
  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 8 }}>{label}</h2>
      <div className="t-small" style={{ fontStyle: 'italic' }}>
        This pane will light up when the corresponding backend endpoints land.
      </div>
    </>
  );
}
