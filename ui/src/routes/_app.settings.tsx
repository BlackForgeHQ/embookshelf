import { useEffect, useMemo, useState } from 'react';
import {
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';

import {
  changePassword,
  fetchMe,
  meQueryKey,
  updateDisplayName,
} from '@/api/auth';
import type { ApiError } from '@/api/client';
import {
  DEVICE_KIND_LABELS,
  deleteDevice,
  devicesQueryKey,
  fetchDevices,
  pairDevice,
  type Device,
  type DeviceKind,
} from '@/api/devices';
import { Icon } from '@/components/Icon';
import {
  Avatar,
  Card,
  Field,
  Notice,
  Select,
  SettingsShell,
  Toggle,
} from '@/components/SettingsShared';
import { TopBar } from '@/components/TopBar';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  defaultReadingPreferences,
  loadReadingPreferences,
  saveReadingPreferences,
  type ReadingPreferences,
} from '@/lib/readingPreferences';

export const Route = createFileRoute('/_app/settings')({
  component: Settings,
});

type SectionKey = 'account' | 'reading' | 'devices';

type SectionSpec = { key: SectionKey; label: string; adminOnly?: boolean };

const SECTIONS: SectionSpec[] = [
  { key: 'account', label: 'Account' },
  { key: 'reading', label: 'Reading preferences' },
  { key: 'devices', label: 'Device sync' },
];

function Settings() {
  const me = useQuery({ queryKey: meQueryKey, queryFn: fetchMe, staleTime: 60_000 });
  const isAdmin = me.data?.role === 'admin';
  const [active, setActive] = useState<SectionKey>('account');

  return (
    <div className="fade-in">
      <TopBar title="My account" subtitle="Preferences scoped to this user account." />
      <SettingsShell
        sections={SECTIONS}
        active={active}
        onSelect={setActive}
        isAdmin={isAdmin}
      >
        {active === 'account' && <AccountPanel />}
        {active === 'reading' && <ReadingPreferencesPanel />}
        {active === 'devices' && <DevicesPanel />}
      </SettingsShell>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Account
// ---------------------------------------------------------------------------

const AUTH_METHODS: ReadonlyArray<{ n: string; on: boolean; sub: string }> = [
  { n: 'Local (session)', on: true, sub: 'Username + password' },
  { n: 'OIDC', on: false, sub: 'Pending' },
  { n: 'Remote / Forward Auth', on: false, sub: 'Reverse proxy headers' },
];

function AccountPanel() {
  const queryClient = useQueryClient();
  const me = useQuery({ queryKey: meQueryKey, queryFn: fetchMe, staleTime: 60_000 });
  const user = me.data;

  const [editing, setEditing] = useState(false);
  const [nameDraft, setNameDraft] = useState('');
  const [pwOpen, setPwOpen] = useState(false);
  const [pwCurrent, setPwCurrent] = useState('');
  const [pwNext, setPwNext] = useState('');
  const [pwConfirm, setPwConfirm] = useState('');
  const [notice, setNotice] = useState<{ kind: 'ok' | 'err'; msg: string } | null>(null);

  const nameMut = useMutation({
    mutationFn: (next: string) => updateDisplayName(next),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: meQueryKey });
      setEditing(false);
      setNotice({ kind: 'ok', msg: 'Display name updated.' });
    },
    onError: (e) => setNotice({ kind: 'err', msg: (e as unknown as ApiError).message }),
  });

  const pwMut = useMutation({
    mutationFn: ({ current, next }: { current: string; next: string }) =>
      changePassword(current, next),
    onSuccess: () => {
      setPwOpen(false);
      setPwCurrent('');
      setPwNext('');
      setPwConfirm('');
      setNotice({ kind: 'ok', msg: 'Password updated.' });
    },
    onError: (e) => setNotice({ kind: 'err', msg: (e as unknown as ApiError).message }),
  });

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

      {notice && <Notice kind={notice.kind} onClose={() => setNotice(null)}>{notice.msg}</Notice>}

      <Card>
        <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
          <Avatar initials={user?.initials} />
          <div style={{ flex: 1, minWidth: 0 }}>
            {editing ? (
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  nameMut.mutate(nameDraft.trim());
                }}
                style={{ display: 'flex', gap: 8, alignItems: 'center' }}
              >
                <Input
                  autoFocus
                  value={nameDraft}
                  onChange={(e) => setNameDraft(e.target.value)}
                  placeholder="Display name"
                  className="flex-1"
                />
                <Button type="submit" size="sm" disabled={nameMut.isPending}>
                  Save
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => setEditing(false)}
                >
                  Cancel
                </Button>
              </form>
            ) : (
              <>
                <div style={{ fontSize: 15, fontWeight: 500 }}>{user?.display ?? '…'}</div>
                <div className="t-small" style={{ fontSize: 12 }}>
                  {user?.email ?? '—'} · {roleLabel} · joined {joined}
                </div>
              </>
            )}
          </div>
          {!editing && (
            <>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setNameDraft(user?.name ?? '');
                  setEditing(true);
                  setNotice(null);
                }}
              >
                Edit name
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  setPwOpen((v) => !v);
                  setNotice(null);
                }}
              >
                Change password
              </Button>
            </>
          )}
        </div>

        {pwOpen && (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (pwNext !== pwConfirm) {
                setNotice({ kind: 'err', msg: 'New passwords do not match.' });
                return;
              }
              pwMut.mutate({ current: pwCurrent, next: pwNext });
            }}
            style={{
              marginTop: 16,
              paddingTop: 16,
              borderTop: '1px dashed var(--color-rule-soft)',
              display: 'flex',
              flexDirection: 'column',
              gap: 10,
            }}
          >
            <Field label="Current password">
              <Input
                type="password"
                value={pwCurrent}
                onChange={(e) => setPwCurrent(e.target.value)}
                autoComplete="current-password"
                required
              />
            </Field>
            <Field label="New password">
              <Input
                type="password"
                value={pwNext}
                onChange={(e) => setPwNext(e.target.value)}
                autoComplete="new-password"
                minLength={8}
                required
              />
            </Field>
            <Field label="Confirm new password">
              <Input
                type="password"
                value={pwConfirm}
                onChange={(e) => setPwConfirm(e.target.value)}
                autoComplete="new-password"
                minLength={8}
                required
              />
            </Field>
            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => setPwOpen(false)}
              >
                Cancel
              </Button>
              <Button type="submit" size="sm" disabled={pwMut.isPending}>
                {pwMut.isPending ? 'Updating…' : 'Update password'}
              </Button>
            </div>
          </form>
        )}
      </Card>

      <div className="t-label" style={{ marginTop: 24, marginBottom: 10 }}>
        Authentication
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
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

// ---------------------------------------------------------------------------
// Reading preferences (client-only, localStorage)
// ---------------------------------------------------------------------------

function ReadingPreferencesPanel() {
  const [prefs, setPrefs] = useState<ReadingPreferences>(defaultReadingPreferences);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    setPrefs(loadReadingPreferences());
  }, []);

  const update = <K extends keyof ReadingPreferences>(key: K, value: ReadingPreferences[K]) => {
    const next = { ...prefs, [key]: value };
    setPrefs(next);
    saveReadingPreferences(next);
    setSaved(true);
    window.setTimeout(() => setSaved(false), 1200);
  };

  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 8 }}>Reading preferences</h2>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: 'italic' }}>
        Stored locally in this browser. The reader picks them up on next open.
        {saved && <span style={{ marginLeft: 8, color: 'oklch(0.5 0.12 140)' }}>✓ saved</span>}
      </p>

      <Card>
        <Field label="Theme">
          <Select
            value={prefs.theme}
            onChange={(v) => update('theme', v as ReadingPreferences['theme'])}
            options={[
              { value: 'light', label: 'Light (paper)' },
              { value: 'sepia', label: 'Sepia' },
              { value: 'dark', label: 'Dark' },
            ]}
          />
        </Field>

        <Field label="Font family">
          <Select
            value={prefs.fontFamily}
            onChange={(v) => update('fontFamily', v as ReadingPreferences['fontFamily'])}
            options={[
              { value: 'serif', label: 'Serif (default)' },
              { value: 'sans', label: 'Sans-serif' },
              { value: 'mono', label: 'Monospace' },
            ]}
          />
        </Field>

        <Field label={`Font size — ${prefs.fontSize}px`}>
          <input
            type="range"
            min={14}
            max={24}
            step={1}
            value={prefs.fontSize}
            onChange={(e) => update('fontSize', Number(e.target.value))}
            style={{ width: '100%' }}
          />
        </Field>

        <Field label={`Line height — ${prefs.lineHeight.toFixed(2)}`}>
          <input
            type="range"
            min={1.2}
            max={2.0}
            step={0.05}
            value={prefs.lineHeight}
            onChange={(e) => update('lineHeight', Number(e.target.value))}
            style={{ width: '100%' }}
          />
        </Field>

        <Toggle
          label="Record reading sessions"
          hint="Progress ticks feed the Stats dashboard heatmap."
          checked={prefs.trackSessions}
          onChange={(v) => update('trackSessions', v)}
        />

        <Toggle
          label="Two-page layout on wide screens"
          hint="Splits EPUB rendering into a spread when width allows."
          checked={prefs.twoPage}
          onChange={(v) => update('twoPage', v)}
        />
      </Card>
    </>
  );
}

// ---------------------------------------------------------------------------
// Device sync (OPDS endpoint)
// ---------------------------------------------------------------------------

function DevicesPanel() {
  const queryClient = useQueryClient();
  const devices = useQuery({
    queryKey: devicesQueryKey,
    queryFn: fetchDevices,
  });

  const [adding, setAdding] = useState<DeviceKind | null>(null);
  const [notice, setNotice] = useState<{ kind: 'ok' | 'err'; msg: string } | null>(null);

  const deleteMut = useMutation({
    mutationFn: (id: string) => deleteDevice(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: devicesQueryKey }),
    onError: (e) => setNotice({ kind: 'err', msg: (e as unknown as ApiError).message }),
  });

  const [copied, setCopied] = useState(false);
  const opdsUrl = useMemo(() => {
    if (typeof window === 'undefined') return '';
    return `${window.location.origin}/opds`;
  }, []);
  const copy = async () => {
    await navigator.clipboard.writeText(opdsUrl);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  };

  return (
    <>
      <div style={{ display: 'flex', alignItems: 'baseline', marginBottom: 8 }}>
        <h2 className="t-h2" style={{ flex: 1 }}>Device sync</h2>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => setAdding('remarkable-paper-pro')}
        >
          <Icon name="plus" size={13} /> Add device
        </Button>
      </div>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: 'italic' }}>
        Pair a device once; push books from the library with a single click.
        Any OPDS-aware reader can also pull the catalog below directly.
      </p>

      {notice && <Notice kind={notice.kind} onClose={() => setNotice(null)}>{notice.msg}</Notice>}

      {adding && (
        <AddDeviceForm
          kind={adding}
          onClose={() => setAdding(null)}
          onPaired={() => {
            queryClient.invalidateQueries({ queryKey: devicesQueryKey });
            setAdding(null);
            setNotice({ kind: 'ok', msg: 'Device paired.' });
          }}
        />
      )}

      <div className="t-label" style={{ marginBottom: 10 }}>Registered devices</div>

      {devices.isLoading && (
        <div className="t-small" style={{ fontStyle: 'italic', marginBottom: 16 }}>
          Loading devices…
        </div>
      )}

      {devices.data && devices.data.length === 0 && (
        <div
          className="t-small"
          style={{
            fontStyle: 'italic',
            padding: '12px 14px',
            border: '1px dashed var(--color-rule-soft)',
            background: 'var(--color-paper-2)',
            marginBottom: 24,
          }}
        >
          No devices paired yet. Click "Add device" to register a reMarkable Paper Pro.
        </div>
      )}

      <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 24 }}>
        {(devices.data ?? []).map((d) => (
          <DeviceRow
            key={d.id}
            device={d}
            onDelete={() => {
              if (window.confirm(`Remove ${d.name}?`)) deleteMut.mutate(d.id);
            }}
            busy={deleteMut.isPending}
          />
        ))}
      </div>

      <div className="t-label" style={{ marginBottom: 10 }}>OPDS catalog</div>
      <Card>
        <Field label="Catalog URL">
          <div style={{ display: 'flex', gap: 8 }}>
            <Input readOnly value={opdsUrl} className="mono flex-1" />
            <Button type="button" variant="outline" onClick={copy}>
              {copied ? 'Copied' : 'Copy'}
            </Button>
          </div>
        </Field>

        <div className="t-small">
          <div style={{ marginBottom: 6 }}>
            <strong>Authentication:</strong> HTTP Basic Auth (account email + password).
          </div>
          <div style={{ marginBottom: 6 }}>
            <strong>Search:</strong> OpenSearch at{' '}
            <span className="mono">{opdsUrl}/search</span>
          </div>
          <div>
            <strong>Compatible:</strong> KOReader, Moon+ Reader, FBReader, Marvin, …
          </div>
        </div>
      </Card>
    </>
  );
}

function DeviceRow({
  device,
  onDelete,
  busy,
}: {
  device: Device;
  onDelete: () => void;
  busy: boolean;
}) {
  const lastSent = device.lastSentAt
    ? new Date(device.lastSentAt).toLocaleString()
    : null;
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 14,
        padding: '12px 14px',
        border: '1px solid var(--color-rule-soft)',
        background: 'var(--color-paper-0)',
      }}
    >
      <span
        style={{
          width: 8,
          height: 8,
          borderRadius: '50%',
          background: device.lastError
            ? 'var(--color-accent)'
            : 'oklch(0.58 0.12 140)',
        }}
      />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 13.5, fontWeight: 500 }}>{device.name}</div>
        <div className="t-small" style={{ fontSize: 11.5 }}>
          {DEVICE_KIND_LABELS[device.kind]}
          {lastSent && ` · last sent ${lastSent}`}
          {!lastSent && ' · no pushes yet'}
        </div>
        {device.lastError && (
          <div
            className="mono"
            style={{ fontSize: 11, color: 'var(--color-accent-ink)', marginTop: 4 }}
          >
            {device.lastError}
          </div>
        )}
      </div>
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        onClick={onDelete}
        disabled={busy}
        className="text-(--color-accent-ink)"
        aria-label="Remove device"
      >
        <Icon name="close" size={12} />
      </Button>
    </div>
  );
}

function AddDeviceForm({
  kind,
  onClose,
  onPaired,
}: {
  kind: DeviceKind;
  onClose: () => void;
  onPaired: () => void;
}) {
  const [name, setName] = useState(DEVICE_KIND_LABELS[kind]);
  const [code, setCode] = useState('');
  const [err, setErr] = useState<string | null>(null);

  const pairMut = useMutation({
    mutationFn: () =>
      pairDevice({
        kind,
        name: name.trim(),
        params: { code: code.trim() },
      }),
    onSuccess: () => onPaired(),
    onError: (e) => setErr((e as unknown as ApiError).message),
  });

  return (
    <Card>
      <div style={{ fontSize: 14, fontWeight: 500 }}>
        Add {DEVICE_KIND_LABELS[kind]}
      </div>
      <div className="t-small">
        Visit{' '}
        <a
          href="https://my.remarkable.com/device/desktop/connect"
          target="_blank"
          rel="noreferrer"
          style={{ color: 'var(--color-accent-ink)' }}
        >
          my.remarkable.com/device/desktop/connect
        </a>{' '}
        and sign in. Copy the 8-character one-time code and paste it below.
        The code is consumed once — re-pairing later requires a fresh code.
      </div>

      <form
        onSubmit={(e) => {
          e.preventDefault();
          setErr(null);
          pairMut.mutate();
        }}
        style={{ display: 'flex', flexDirection: 'column', gap: 10 }}
      >
        <Field label="Display name">
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
          />
        </Field>
        <Field label="One-time code">
          <Input
            className="mono"
            value={code}
            onChange={(e) => setCode(e.target.value)}
            placeholder="abcd1234"
            autoComplete="off"
            spellCheck={false}
            required
          />
        </Field>

        {err && <Notice kind="err">{err}</Notice>}

        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <Button type="button" variant="ghost" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="submit"
            size="sm"
            disabled={pairMut.isPending || !code.trim()}
          >
            {pairMut.isPending ? 'Pairing…' : 'Pair device'}
          </Button>
        </div>
      </form>
    </Card>
  );
}


