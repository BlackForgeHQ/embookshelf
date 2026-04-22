import { useEffect, useMemo, useState, type ReactNode } from 'react';
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
  type AuthUser,
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
import {
  createLibrary,
  createLibraryPath,
  createSettingsUser,
  deleteLibraryPath,
  deleteSettingsUser,
  fetchInstanceInfo,
  fetchProviderSettings,
  fetchSettingsLibraries,
  fetchSettingsUsers,
  instanceInfoQueryKey,
  prescanLibraryPaths,
  providerSettingsQueryKey,
  scanLibraryPath,
  settingsLibrariesQueryKey,
  settingsUsersQueryKey,
  updateProviderSetting,
  updateSettingsUserRole,
  type ProviderInfo,
  type SettingsLibrary,
  type SettingsLibraryPath,
} from '@/api/settings';
import { Icon } from '@/components/Icon';
import { TopBar } from '@/components/TopBar';
import {
  Avatar as ShadcnAvatar,
  AvatarFallback,
} from '@/components/ui/avatar';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select as ShadcnSelect,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import {
  defaultReadingPreferences,
  loadReadingPreferences,
  saveReadingPreferences,
  type ReadingPreferences,
} from '@/lib/readingPreferences';

export const Route = createFileRoute('/_app/settings')({
  component: Settings,
});

type SectionKey =
  | 'account'
  | 'reading'
  | 'libraries'
  | 'providers'
  | 'devices'
  | 'email'
  | 'users'
  | 'backups'
  | 'about';

type SectionSpec = { key: SectionKey; label: string; adminOnly?: boolean };

const SECTIONS: SectionSpec[] = [
  { key: 'account', label: 'Account' },
  { key: 'reading', label: 'Reading preferences' },
  { key: 'libraries', label: 'Libraries', adminOnly: true },
  { key: 'providers', label: 'Metadata providers', adminOnly: true },
  { key: 'devices', label: 'Device sync' },
  { key: 'email', label: 'Email delivery', adminOnly: true },
  { key: 'users', label: 'Users & roles', adminOnly: true },
  { key: 'backups', label: 'Backups', adminOnly: true },
  { key: 'about', label: 'About' },
];

function Settings() {
  const me = useQuery({ queryKey: meQueryKey, queryFn: fetchMe, staleTime: 60_000 });
  const isAdmin = me.data?.role === 'admin';
  const [active, setActive] = useState<SectionKey>('account');

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
            const selected = s.key === active;
            const gated = s.adminOnly && !isAdmin;
            return (
              <button
                key={s.key}
                type="button"
                onClick={() => setActive(s.key)}
                disabled={gated}
                style={{
                  padding: '8px 12px',
                  textAlign: 'left',
                  background: selected ? 'var(--color-paper-3)' : 'transparent',
                  border: 'none',
                  cursor: gated ? 'default' : 'pointer',
                  fontFamily: 'var(--font-serif)',
                  fontSize: 13.5,
                  borderLeft: selected
                    ? '2px solid var(--color-accent)'
                    : '2px solid transparent',
                  color: gated
                    ? 'var(--color-ink-4)'
                    : selected
                      ? 'var(--color-ink-1)'
                      : 'var(--color-ink-2)',
                  opacity: gated ? 0.6 : 1,
                }}
                title={gated ? 'Admin-only' : undefined}
              >
                {s.label}
              </button>
            );
          })}
        </nav>

        <div style={{ maxWidth: 640 }}>
          {active === 'account' && <AccountPanel />}
          {active === 'reading' && <ReadingPreferencesPanel />}
          {active === 'libraries' && <LibrariesPanel isAdmin={isAdmin} />}
          {active === 'providers' && <ProvidersPanel isAdmin={isAdmin} />}
          {active === 'devices' && <DevicesPanel />}
          {active === 'email' && <EmailPanel isAdmin={isAdmin} />}
          {active === 'users' && <UsersPanel isAdmin={isAdmin} me={me.data ?? null} />}
          {active === 'backups' && <BackupsPanel isAdmin={isAdmin} />}
          {active === 'about' && <AboutPanel isAdmin={isAdmin} />}
        </div>
      </div>
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
// Libraries (existing, lightly tweaked)
// ---------------------------------------------------------------------------

function LibrariesPanel({ isAdmin }: { isAdmin: boolean }) {
  const queryClient = useQueryClient();
  const [creatorOpen, setCreatorOpen] = useState(false);

  const libraries = useQuery({
    queryKey: settingsLibrariesQueryKey,
    queryFn: fetchSettingsLibraries,
    enabled: isAdmin,
  });

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: settingsLibrariesQueryKey });
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

  if (!isAdmin) return <AdminGate label="Libraries" />;

  const existingNames = (libraries.data ?? []).map((l) => l.name);

  return (
    <>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 12, marginBottom: 8 }}>
        <h2 className="t-h2">Libraries</h2>
        <div style={{ flex: 1 }} />
        <Button variant="outline" onClick={() => setCreatorOpen(true)}>
          <Icon name="plus" size={13} /> New library
        </Button>
      </div>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: 'italic' }}>
        Register filesystem roots each library scans. Scans discover new
        files and enqueue them through the BookDrop review queue.
      </p>

      {error && <Notice kind="err">{error.message}</Notice>}

      {libraries.isLoading && (
        <div className="t-small" style={{ fontStyle: 'italic' }}>Loading libraries…</div>
      )}

      {(libraries.data ?? []).map((lib) => (
        <LibraryCard
          key={lib.id}
          library={lib}
          busy={createMut.isPending || deleteMut.isPending || scanMut.isPending}
          onAddPath={(path) => createMut.mutate({ libraryId: lib.id, path })}
          onDeletePath={(id) => deleteMut.mutate(id)}
          onScanPath={(id) => scanMut.mutate(id)}
        />
      ))}

      <LibraryCreatorDialog
        open={creatorOpen}
        onOpenChange={setCreatorOpen}
        existingNames={existingNames}
        onCreated={() => {
          invalidate();
          setCreatorOpen(false);
        }}
      />
    </>
  );
}

type LibraryCreatorDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  existingNames: string[];
  onCreated: () => void;
};

// Modeled after spec/library-creation.spec.md §3 + §4. Embookshelf's library
// model is simpler than BookLore's (no icon/watch/format policy yet), so the
// form collapses to name + paths + an opt-in "scan immediately" toggle.
function LibraryCreatorDialog({
  open,
  onOpenChange,
  existingNames,
  onCreated,
}: LibraryCreatorDialogProps) {
  const [name, setName] = useState('');
  const [paths, setPaths] = useState<string[]>([]);
  const [pathDraft, setPathDraft] = useState('');
  const [scanOnCreate, setScanOnCreate] = useState(true);
  const [prescan, setPrescan] = useState<{ count: number; forPaths: string[] } | null>(null);
  const [pathError, setPathError] = useState<string | null>(null);

  // Reset local state whenever the dialog closes so re-opening is a blank slate.
  useEffect(() => {
    if (open) return;
    setName('');
    setPaths([]);
    setPathDraft('');
    setScanOnCreate(true);
    setPrescan(null);
    setPathError(null);
  }, [open]);

  const trimmedName = name.trim();
  const nameCollision = existingNames.some(
    (existing) => existing.toLowerCase() === trimmedName.toLowerCase(),
  );
  const nameValid = trimmedName !== '' && !nameCollision;

  const prescanMut = useMutation({
    mutationFn: (list: string[]) => prescanLibraryPaths(list),
    onSuccess: (count, list) => setPrescan({ count, forPaths: list }),
  });

  const createMut = useMutation({
    mutationFn: () =>
      createLibrary({ name: trimmedName, paths, scan: scanOnCreate }),
    onSuccess: () => onCreated(),
  });

  const addPath = () => {
    const cleaned = pathDraft.trim().replace(/\/+$/, '');
    if (cleaned === '') return;
    if (paths.includes(cleaned)) {
      setPathError('that path is already in the list');
      return;
    }
    setPathError(null);
    setPaths((prev) => [...prev, cleaned]);
    setPathDraft('');
    setPrescan(null);
  };

  const removePath = (value: string) => {
    setPaths((prev) => prev.filter((p) => p !== value));
    setPrescan(null);
  };

  // Prescan is valid only for the exact path list the user is looking at.
  // If they edit the list, we invalidate the count and they'll need to
  // re-click "Count files" (spec §3.3 step 2).
  const prescanFresh =
    prescan !== null &&
    prescan.forPaths.length === paths.length &&
    prescan.forPaths.every((p, i) => p === paths[i]);

  const submitDisabled =
    !nameValid || paths.length === 0 || createMut.isPending;

  const createError = createMut.error as ApiError | null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[520px]">
        <DialogHeader>
          <DialogTitle>New library</DialogTitle>
          <DialogDescription>
            Point embookshelf at one or more folders on disk. On create, the
            scanner walks each folder and stages new files through BookDrop.
          </DialogDescription>
        </DialogHeader>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div>
            <Label htmlFor="lib-name" style={{ display: 'block', marginBottom: 6 }}>
              Name
            </Label>
            <Input
              id="lib-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. Fiction"
              autoFocus
            />
            {trimmedName !== '' && nameCollision && (
              <div
                className="t-small"
                style={{ color: 'var(--color-accent-ink)', marginTop: 6 }}
              >
                A library with that name already exists.
              </div>
            )}
          </div>

          <div>
            <Label style={{ display: 'block', marginBottom: 6 }}>Folders</Label>
            {paths.length === 0 ? (
              <div
                className="t-small"
                style={{ fontStyle: 'italic', marginBottom: 8 }}
              >
                No folders yet — add at least one below.
              </div>
            ) : (
              <div
                style={{ display: 'flex', flexDirection: 'column', gap: 6, marginBottom: 8 }}
              >
                {paths.map((p) => (
                  <div
                    key={p}
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 8,
                      padding: '8px 12px',
                      background: 'var(--color-paper-2)',
                      borderRadius: 2,
                    }}
                  >
                    <span className="mono" style={{ flex: 1, fontSize: 12.5 }}>
                      {p}
                    </span>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      onClick={() => removePath(p)}
                      aria-label={`Remove ${p}`}
                    >
                      <Icon name="close" size={13} />
                    </Button>
                  </div>
                ))}
              </div>
            )}
            <div style={{ display: 'flex', gap: 8 }}>
              <Input
                value={pathDraft}
                onChange={(e) => {
                  setPathDraft(e.target.value);
                  if (pathError) setPathError(null);
                }}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault();
                    addPath();
                  }
                }}
                placeholder="/absolute/path/to/books"
                className="mono flex-1 text-[12.5px]"
              />
              <Button
                type="button"
                variant="outline"
                onClick={addPath}
                disabled={pathDraft.trim() === ''}
              >
                <Icon name="plus" size={13} /> Add
              </Button>
            </div>
            {pathError && (
              <div
                className="t-small"
                style={{ color: 'var(--color-accent-ink)', marginTop: 6 }}
              >
                {pathError}
              </div>
            )}
          </div>

          {paths.length > 0 && (
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 12,
                padding: '10px 12px',
                border: '1px dashed var(--color-rule-soft)',
                borderRadius: 2,
              }}
            >
              <div style={{ flex: 1 }}>
                <div className="t-small" style={{ fontWeight: 500 }}>
                  Pre-create scan
                </div>
                <div className="t-micro" style={{ fontStyle: 'italic' }}>
                  {prescanFresh
                    ? `${prescan!.count.toLocaleString()} supported file${prescan!.count === 1 ? '' : 's'} found`
                    : 'Counts files before creation so you can spot typos in the path.'}
                </div>
              </div>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => prescanMut.mutate(paths)}
                disabled={prescanMut.isPending}
              >
                {prescanMut.isPending ? 'Counting…' : 'Count files'}
              </Button>
            </div>
          )}

          <label
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 10,
              cursor: 'pointer',
            }}
          >
            <Switch
              checked={scanOnCreate}
              onCheckedChange={(v) => setScanOnCreate(Boolean(v))}
            />
            <span className="t-small">Scan folders immediately after creating</span>
          </label>

          {createError && <Notice kind="err">{createError.message}</Notice>}
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            onClick={() => onOpenChange(false)}
            disabled={createMut.isPending}
          >
            Cancel
          </Button>
          <Button
            type="button"
            onClick={() => createMut.mutate()}
            disabled={submitDisabled}
          >
            {createMut.isPending ? 'Creating…' : 'Create library'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
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
        <Input
          placeholder="/absolute/path/to/books"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          className="mono flex-1 text-[12.5px]"
        />
        <Button type="submit" variant="outline" disabled={busy || draft.trim() === ''}>
          <Icon name="plus" size={13} /> Add path
        </Button>
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
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={onScan}
        disabled={busy}
        title="Enqueue a library scan"
      >
        <Icon name="refresh" size={12} /> Scan
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        onClick={() => {
          if (window.confirm(`Remove ${path.path}?\nImported books stay in the library.`)) {
            onDelete();
          }
        }}
        disabled={busy}
        className="text-(--color-accent-ink)"
        aria-label="Remove path"
      >
        <Icon name="close" size={12} />
      </Button>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Metadata providers (read-only — configured via env var)
// ---------------------------------------------------------------------------

function ProvidersPanel({ isAdmin }: { isAdmin: boolean }) {
  const queryClient = useQueryClient();
  const providersQuery = useQuery({
    queryKey: providerSettingsQueryKey,
    queryFn: fetchProviderSettings,
    enabled: isAdmin,
  });

  const toggleMut = useMutation({
    mutationFn: (args: { id: string; enabled: boolean }) =>
      updateProviderSetting(args.id, args.enabled),
    // Optimistic: flip the cache immediately so the Switch doesn't wait
    // on the round trip. Rollback on error using the snapshot.
    onMutate: async ({ id, enabled }) => {
      await queryClient.cancelQueries({ queryKey: providerSettingsQueryKey });
      const prev = queryClient.getQueryData<ProviderInfo[]>(providerSettingsQueryKey);
      if (prev) {
        queryClient.setQueryData<ProviderInfo[]>(
          providerSettingsQueryKey,
          prev.map((p) => (p.id === id ? { ...p, enabled } : p)),
        );
      }
      return { prev };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev) {
        queryClient.setQueryData(providerSettingsQueryKey, ctx.prev);
      }
    },
    onSuccess: (providers) => {
      // Server returns the canonical post-update list; take it wholesale.
      queryClient.setQueryData(providerSettingsQueryKey, providers);
      // Also refresh the instance blob so About / status bar agree.
      queryClient.invalidateQueries({ queryKey: instanceInfoQueryKey });
    },
  });

  if (!isAdmin) return <AdminGate label="Metadata providers" />;

  const providers = providersQuery.data ?? [];
  const enabledCount = providers.filter((p) => p.enabled).length;
  const error = toggleMut.error as unknown as ApiError | null;

  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 8 }}>Metadata providers</h2>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: 'italic' }}>
        Enrichment queries fan out across enabled providers — toggle any row
        to include or skip it. The initial set is seeded from{' '}
        <span className="mono">ENRICHMENT_PROVIDERS</span>; after that, this
        page is authoritative and changes take effect on the next search.
      </p>

      {error && (
        <Notice kind="err">{error.message}</Notice>
      )}

      <div className="t-label" style={{ marginBottom: 10 }}>
        {providersQuery.isLoading
          ? 'Loading providers…'
          : `${enabledCount} of ${providers.length} enabled`}
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {providers.map((p) => (
          <label
            key={p.id}
            htmlFor={`provider-${p.id}`}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 14,
              padding: '12px 14px',
              border: '1px solid var(--color-rule-soft)',
              background: 'var(--color-paper-0)',
              borderRadius: 2,
              cursor: 'pointer',
            }}
          >
            <span
              style={{
                width: 8,
                height: 8,
                borderRadius: '50%',
                background: p.enabled ? 'oklch(0.58 0.12 140)' : 'var(--color-ink-4)',
                transition: 'background 160ms ease',
              }}
            />
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ fontSize: 13.5, fontWeight: 500 }}>{p.name}</div>
              <div className="t-small" style={{ fontSize: 11.5 }}>
                <span className="mono">{p.id}</span>
                {p.external && ' · external API'}
              </div>
            </div>
            <Switch
              id={`provider-${p.id}`}
              checked={p.enabled}
              disabled={toggleMut.isPending}
              onCheckedChange={(v) => toggleMut.mutate({ id: p.id, enabled: v })}
              aria-label={`${p.enabled ? 'Disable' : 'Enable'} ${p.name}`}
            />
          </label>
        ))}
      </div>
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

// ---------------------------------------------------------------------------
// Email delivery (informational)
// ---------------------------------------------------------------------------

function EmailPanel({ isAdmin }: { isAdmin: boolean }) {
  if (!isAdmin) return <AdminGate label="Email delivery" />;
  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 8 }}>Email delivery</h2>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: 'italic' }}>
        SMTP is not yet wired. Send-to-Kindle and share-by-email will surface
        here once the transport is configured.
      </p>

      <Card>
        <DefRow label="Transport" value="—" />
        <DefRow label="From address" value="—" />
        <DefRow label="Send-to-Kindle" value="disabled" />
      </Card>

      <p className="t-small" style={{ marginTop: 16, fontStyle: 'italic' }}>
        Configure via <span className="mono">SMTP_HOST</span>,{' '}
        <span className="mono">SMTP_USERNAME</span>, and related env vars on
        the server.
      </p>
    </>
  );
}

// ---------------------------------------------------------------------------
// Users & roles (admin CRUD)
// ---------------------------------------------------------------------------

function UsersPanel({ isAdmin, me }: { isAdmin: boolean; me: AuthUser | null }) {
  const queryClient = useQueryClient();
  const users = useQuery({
    queryKey: settingsUsersQueryKey,
    queryFn: fetchSettingsUsers,
    enabled: isAdmin,
  });

  const [createOpen, setCreateOpen] = useState(false);
  const [draft, setDraft] = useState({
    email: '',
    name: '',
    password: '',
    role: 'user' as 'user' | 'admin',
  });
  const [notice, setNotice] = useState<{ kind: 'ok' | 'err'; msg: string } | null>(null);

  const invalidate = () => queryClient.invalidateQueries({ queryKey: settingsUsersQueryKey });

  const createMut = useMutation({
    mutationFn: () => createSettingsUser(draft),
    onSuccess: () => {
      invalidate();
      setCreateOpen(false);
      setDraft({ email: '', name: '', password: '', role: 'user' });
      setNotice({ kind: 'ok', msg: 'User created.' });
    },
    onError: (e) => setNotice({ kind: 'err', msg: (e as unknown as ApiError).message }),
  });

  const roleMut = useMutation({
    mutationFn: ({ id, role }: { id: string; role: 'admin' | 'user' }) =>
      updateSettingsUserRole(id, role),
    onSuccess: invalidate,
    onError: (e) => setNotice({ kind: 'err', msg: (e as unknown as ApiError).message }),
  });

  const deleteMut = useMutation({
    mutationFn: (id: string) => deleteSettingsUser(id),
    onSuccess: invalidate,
    onError: (e) => setNotice({ kind: 'err', msg: (e as unknown as ApiError).message }),
  });

  if (!isAdmin) return <AdminGate label="Users & roles" />;

  return (
    <>
      <div style={{ display: 'flex', alignItems: 'baseline', marginBottom: 8 }}>
        <h2 className="t-h2" style={{ flex: 1 }}>Users &amp; roles</h2>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => {
            setCreateOpen((v) => !v);
            setNotice(null);
          }}
        >
          <Icon name="plus" size={13} /> New user
        </Button>
      </div>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: 'italic' }}>
        Admins see every settings pane; regular users see only Account,
        Reading preferences, Device sync, and About.
      </p>

      {notice && <Notice kind={notice.kind} onClose={() => setNotice(null)}>{notice.msg}</Notice>}

      {createOpen && (
        <Card>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              createMut.mutate();
            }}
            style={{ display: 'flex', flexDirection: 'column', gap: 10 }}
          >
            <Field label="Email">
              <Input
                type="email"
                value={draft.email}
                onChange={(e) => setDraft({ ...draft, email: e.target.value })}
                required
              />
            </Field>
            <Field label="Display name">
              <Input
                value={draft.name}
                onChange={(e) => setDraft({ ...draft, name: e.target.value })}
              />
            </Field>
            <Field label="Initial password">
              <Input
                type="password"
                value={draft.password}
                onChange={(e) => setDraft({ ...draft, password: e.target.value })}
                minLength={8}
                required
              />
            </Field>
            <Field label="Role">
              <Select
                value={draft.role}
                onChange={(v) => setDraft({ ...draft, role: v as 'user' | 'admin' })}
                options={[
                  { value: 'user', label: 'User' },
                  { value: 'admin', label: 'Admin' },
                ]}
              />
            </Field>
            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => setCreateOpen(false)}
              >
                Cancel
              </Button>
              <Button type="submit" size="sm" disabled={createMut.isPending}>
                {createMut.isPending ? 'Creating…' : 'Create user'}
              </Button>
            </div>
          </form>
        </Card>
      )}

      {users.isLoading && (
        <div className="t-small" style={{ fontStyle: 'italic' }}>Loading users…</div>
      )}

      <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 16 }}>
        {(users.data ?? []).map((u) => {
          const isMe = u.id === me?.id;
          return (
            <div
              key={u.id}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 14,
                padding: '10px 14px',
                border: '1px solid var(--color-rule-soft)',
                background: 'var(--color-paper-0)',
              }}
            >
              <Avatar initials={u.initials} size={32} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 13.5, fontWeight: 500 }}>
                  {u.display} {isMe && <span className="t-micro">you</span>}
                </div>
                <div className="t-small" style={{ fontSize: 11.5 }}>
                  {u.email} · joined{' '}
                  {new Date(u.createdAt).toLocaleDateString(undefined, {
                    month: 'short',
                    year: 'numeric',
                  })}
                  {u.lastSeenAt && ` · last seen ${new Date(u.lastSeenAt).toLocaleDateString()}`}
                </div>
              </div>
              <Select
                value={u.role}
                onChange={(v) => roleMut.mutate({ id: u.id, role: v as 'admin' | 'user' })}
                options={[
                  { value: 'user', label: 'User' },
                  { value: 'admin', label: 'Admin' },
                ]}
                disabled={isMe || roleMut.isPending}
                triggerClassName="w-[110px] shrink-0"
              />
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                disabled={isMe || deleteMut.isPending}
                onClick={() => {
                  if (window.confirm(`Delete ${u.display}? This cannot be undone.`)) {
                    deleteMut.mutate(u.id);
                  }
                }}
                className="text-(--color-accent-ink)"
                aria-label="Delete user"
                title={isMe ? "You can't delete yourself" : 'Delete user'}
              >
                <Icon name="close" size={12} />
              </Button>
            </div>
          );
        })}
      </div>
    </>
  );
}

// ---------------------------------------------------------------------------
// Backups (informational)
// ---------------------------------------------------------------------------

function BackupsPanel({ isAdmin }: { isAdmin: boolean }) {
  if (!isAdmin) return <AdminGate label="Backups" />;
  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 8 }}>Backups</h2>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: 'italic' }}>
        The on-disk data directory and the PostgreSQL volume hold every
        durable piece of state. Back them up together.
      </p>

      <Card>
        <DefRow
          label="Database"
          value={
            <>
              <span className="mono">pg_dump embookshelf</span> — ship to your
              usual blob store on a cron.
            </>
          }
        />
        <DefRow label="Book files" value={<span className="mono">library paths</span>} />
        <DefRow label="Covers + BookDrop queue" value={<span className="mono">$DATA_PATH</span>} />
      </Card>

      <p className="t-small" style={{ marginTop: 16, fontStyle: 'italic' }}>
        A scheduled-backups surface will land here once the job runner gains
        an "export" task.
      </p>
    </>
  );
}

// ---------------------------------------------------------------------------
// About
// ---------------------------------------------------------------------------

function AboutPanel({ isAdmin }: { isAdmin: boolean }) {
  const info = useQuery({
    queryKey: instanceInfoQueryKey,
    queryFn: fetchInstanceInfo,
    enabled: isAdmin,
  });

  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 24 }}>About</h2>

      <Card>
        <DefRow label="Product" value="embookshelf" />
        <DefRow
          label="Version"
          value={<span className="mono">{info.data?.version ?? '—'}</span>}
        />
        {isAdmin && (
          <>
            <DefRow
              label="Runtime"
              value={<span className="mono">{info.data?.goVersion ?? '—'}</span>}
            />
            <DefRow
              label="Disk mode"
              value={<span className="mono">{info.data?.diskMode ?? '—'}</span>}
            />
            <DefRow
              label="BookDrop path"
              value={<span className="mono">{info.data?.bookDropPath ?? '—'}</span>}
            />
            <DefRow
              label="Data path"
              value={<span className="mono">{info.data?.dataPath ?? '—'}</span>}
            />
            <DefRow
              label="Migrate on start"
              value={info.data ? (info.data.migrateOnStart ? 'yes' : 'no') : '—'}
            />
          </>
        )}
      </Card>

      {isAdmin && info.data && (
        <>
          <div className="t-label" style={{ marginTop: 24, marginBottom: 10 }}>
            Instance totals
          </div>
          <Card>
            <DefRow label="Users" value={info.data.counts.users} />
            <DefRow label="Libraries" value={info.data.counts.libraries} />
            <DefRow label="Books" value={info.data.counts.books.toLocaleString()} />
          </Card>
        </>
      )}

      <p className="t-small" style={{ marginTop: 24, fontStyle: 'italic' }}>
        embookshelf — self-hosted ebook library. AGPL-3.0.
      </p>
    </>
  );
}

// ---------------------------------------------------------------------------
// Shared UI primitives (kept inline — the rest of the codebase uses
// inline styles in this route)
// ---------------------------------------------------------------------------

function Card({ children }: { children: ReactNode }) {
  return (
    <div
      style={{
        padding: 16,
        border: '1px solid var(--color-rule-soft)',
        background: 'var(--color-paper-0)',
        marginBottom: 0,
        display: 'flex',
        flexDirection: 'column',
        gap: 14,
      }}
    >
      {children}
    </div>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <span className="t-label">{label}</span>
      {children}
    </label>
  );
}

function Select({
  value,
  onChange,
  options,
  disabled,
  triggerClassName = 'w-full',
}: {
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: string }[];
  disabled?: boolean;
  // Callers in inline rows (e.g. Users & roles) should override to
  // `w-[110px]` or similar so the trigger doesn't swallow the flex slot.
  triggerClassName?: string;
}) {
  return (
    <ShadcnSelect value={value} onValueChange={onChange} disabled={disabled}>
      <SelectTrigger className={triggerClassName}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {options.map((o) => (
          <SelectItem key={o.value} value={o.value}>
            {o.label}
          </SelectItem>
        ))}
      </SelectContent>
    </ShadcnSelect>
  );
}

function Toggle({
  label,
  hint,
  checked,
  onChange,
}: {
  label: string;
  hint?: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <label
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 12,
        padding: '8px 0',
        borderTop: '1px dashed var(--color-rule-soft)',
        cursor: 'pointer',
      }}
    >
      <Switch checked={checked} onCheckedChange={onChange} />
      <div style={{ flex: 1 }}>
        <div style={{ fontSize: 13.5 }}>{label}</div>
        {hint && <div className="t-small" style={{ fontSize: 11.5 }}>{hint}</div>}
      </div>
    </label>
  );
}

function DefRow({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div
      style={{
        display: 'flex',
        gap: 12,
        padding: '6px 0',
        alignItems: 'baseline',
      }}
    >
      <div className="t-label" style={{ width: 160, flexShrink: 0 }}>
        {label}
      </div>
      <div style={{ fontSize: 13.5, flex: 1, minWidth: 0, wordBreak: 'break-word' }}>
        {value}
      </div>
    </div>
  );
}

function Avatar({ initials, size = 48 }: { initials?: string; size?: number }) {
  // Map the few call-site sizes (48 for Account header, 32 for user rows)
  // onto shadcn's `size` variants. Callers that pass an arbitrary number
  // still get a correctly-sized avatar via the inline style override.
  const preset = size <= 28 ? 'sm' : size >= 40 ? 'lg' : 'default';
  return (
    <ShadcnAvatar
      size={preset}
      style={{ width: size, height: size }}
      className="shrink-0"
    >
      <AvatarFallback
        className="bg-(--color-editorial-accent) text-(--color-paper-0) font-serif font-medium"
        style={{ fontSize: Math.round(size * 0.375) }}
      >
        {initials ?? '…'}
      </AvatarFallback>
    </ShadcnAvatar>
  );
}

function Notice({
  kind,
  children,
  onClose,
}: {
  kind: 'ok' | 'err';
  children: ReactNode;
  onClose?: () => void;
}) {
  const accent = kind === 'ok' ? 'oklch(0.58 0.12 140)' : 'var(--color-accent-ink)';
  return (
    <div
      style={{
        padding: '10px 14px',
        border: `1px solid ${accent}`,
        background: 'var(--color-paper-0)',
        color: accent,
        borderRadius: 2,
        fontSize: 13,
        marginBottom: 20,
        display: 'flex',
        alignItems: 'center',
        gap: 10,
      }}
    >
      <span style={{ flex: 1 }}>{children}</span>
      {onClose && (
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          onClick={onClose}
          aria-label="Dismiss"
        >
          <Icon name="close" size={11} />
        </Button>
      )}
    </div>
  );
}

function AdminGate({ label }: { label: string }) {
  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 24 }}>{label}</h2>
      <div className="t-small" style={{ fontStyle: 'italic' }}>
        {label} are admin-only.
      </div>
    </>
  );
}
