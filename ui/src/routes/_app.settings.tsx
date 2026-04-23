import { useEffect, useState, type ReactNode } from 'react';
import {
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';

import {
  fetchMe,
  meQueryKey,
  type AuthUser,
} from '@/api/auth';
import type { ApiError } from '@/api/client';
import {
  createLibrary,
  createSettingsUser,
  deleteLibrary,
  deleteSettingsUser,
  fetchInstanceInfo,
  fetchProviderSettings,
  fetchSettingsLibraries,
  fetchSettingsUsers,
  instanceInfoQueryKey,
  prescanLibraryPaths,
  previewNamingPattern,
  providerSettingsQueryKey,
  rescanLibrary,
  settingsLibrariesQueryKey,
  settingsUsersQueryKey,
  updateLibraryNamingPattern,
  updateProviderSetting,
  updateSettingsUserRole,
  type ProviderInfo,
  type SettingsLibrary,
} from '@/api/settings';
import {
  fetchOidcAdminSettings,
  oidcAdminSettingsQueryKey,
  saveOidcAdminSettings,
  testOidcProvider,
  type OidcAdminSettings,
  type OidcTestCheck,
  type OidcTestResult,
  type ProviderSlug,
} from '@/api/oidc';
import { Icon } from '@/components/Icon';
import {
  AdminGate,
  Avatar,
  Card,
  DefRow,
  Field,
  Notice,
  Select,
  SettingsShell,
} from '@/components/SettingsShared';
import { TopBar } from '@/components/TopBar';
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
import { Switch } from '@/components/ui/switch';

export const Route = createFileRoute('/_app/settings')({
  component: Admin,
});

type SectionKey =
  | 'libraries'
  | 'providers'
  | 'email'
  | 'users'
  | 'oidc'
  | 'backups'
  | 'about';

type SectionSpec = { key: SectionKey; label: string; adminOnly?: boolean };

const SECTIONS: SectionSpec[] = [
  { key: 'libraries', label: 'Libraries', adminOnly: true },
  { key: 'providers', label: 'Metadata providers', adminOnly: true },
  { key: 'email', label: 'Email delivery', adminOnly: true },
  { key: 'users', label: 'Users & roles', adminOnly: true },
  { key: 'oidc', label: 'OIDC / SSO', adminOnly: true },
  { key: 'backups', label: 'Backups', adminOnly: true },
  { key: 'about', label: 'About', adminOnly: true },
];

function Admin() {
  const me = useQuery({ queryKey: meQueryKey, queryFn: fetchMe, staleTime: 60_000 });
  const isAdmin = me.data?.role === 'admin';
  const [active, setActive] = useState<SectionKey>('libraries');

  return (
    <div className="fade-in">
      <TopBar title="Settings" subtitle="Instance, users, metadata providers, SSO." />
      <SettingsShell
        sections={SECTIONS}
        active={active}
        onSelect={setActive}
        isAdmin={isAdmin}
      >
        {active === 'libraries' && <LibrariesPanel isAdmin={isAdmin} />}
        {active === 'providers' && <ProvidersPanel isAdmin={isAdmin} />}
        {active === 'email' && <EmailPanel isAdmin={isAdmin} />}
        {active === 'users' && <UsersPanel isAdmin={isAdmin} me={me.data ?? null} />}
        {active === 'oidc' && <OidcPanel isAdmin={isAdmin} />}
        {active === 'backups' && <BackupsPanel isAdmin={isAdmin} />}
        {active === 'about' && <AboutPanel isAdmin={isAdmin} />}
      </SettingsShell>
    </div>
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

  const rescanMut = useMutation({
    mutationFn: (id: string) => rescanLibrary(id),
    onSuccess: invalidate,
  });
  const patternMut = useMutation({
    mutationFn: (args: { id: string; pattern: string | null }) =>
      updateLibraryNamingPattern(args.id, args.pattern),
    onSuccess: invalidate,
  });
  const deleteLibraryMut = useMutation({
    mutationFn: (id: string) => deleteLibrary(id),
    onSuccess: invalidate,
  });

  const error = (rescanMut.error ?? patternMut.error ?? deleteLibraryMut.error) as
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
        Each library owns one filesystem root, fixed at creation.
        Approved books are moved under that root using the library&apos;s
        file-naming pattern — scans discover new files and enqueue them
        through the BookDrop review queue.
      </p>

      {error && <Notice kind="err">{error.message}</Notice>}

      {libraries.isLoading && (
        <div className="t-small" style={{ fontStyle: 'italic' }}>Loading libraries…</div>
      )}

      {(libraries.data ?? []).map((lib) => (
        <LibraryCard
          key={lib.id}
          library={lib}
          rescanBusy={rescanMut.isPending}
          patternBusy={patternMut.isPending}
          deleteBusy={deleteLibraryMut.isPending}
          onRescan={() => rescanMut.mutate(lib.id)}
          onSavePattern={(pattern) => patternMut.mutate({ id: lib.id, pattern })}
          onDeleteLibrary={() => deleteLibraryMut.mutate(lib.id)}
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
  const [path, setPath] = useState('');
  const [scanOnCreate, setScanOnCreate] = useState(true);
  const [prescan, setPrescan] = useState<{ count: number; forPath: string } | null>(null);

  // Reset local state whenever the dialog closes so re-opening is a blank slate.
  useEffect(() => {
    if (open) return;
    setName('');
    setPath('');
    setScanOnCreate(true);
    setPrescan(null);
  }, [open]);

  const trimmedName = name.trim();
  const trimmedPath = path.trim().replace(/\/+$/, '');
  const nameCollision = existingNames.some(
    (existing) => existing.toLowerCase() === trimmedName.toLowerCase(),
  );
  const nameValid = trimmedName !== '' && !nameCollision;
  const pathValid = trimmedPath !== '';

  const prescanMut = useMutation({
    mutationFn: (value: string) => prescanLibraryPaths([value]),
    onSuccess: (count, value) => setPrescan({ count, forPath: value }),
  });

  const createMut = useMutation({
    mutationFn: () =>
      createLibrary({ name: trimmedName, path: trimmedPath, scan: scanOnCreate }),
    onSuccess: () => onCreated(),
  });

  // Prescan is valid only for the exact path the user is looking at.
  // Edits invalidate the count so they'll need to re-click "Count files".
  const prescanFresh = prescan !== null && prescan.forPath === trimmedPath;

  const submitDisabled = !nameValid || !pathValid || createMut.isPending;
  const createError = createMut.error as ApiError | null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[520px]">
        <DialogHeader>
          <DialogTitle>New library</DialogTitle>
          <DialogDescription>
            Point embookshelf at one folder on disk. Approved books are moved
            under that folder on accept, renamed via the library&apos;s
            file-naming pattern. The path is fixed at creation time.
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
            <Label htmlFor="lib-path" style={{ display: 'block', marginBottom: 6 }}>
              Folder
            </Label>
            <Input
              id="lib-path"
              value={path}
              onChange={(e) => {
                setPath(e.target.value);
                setPrescan(null);
              }}
              placeholder="/absolute/path/to/books"
              className="mono text-[12.5px]"
            />
            <div className="t-micro" style={{ marginTop: 6, fontStyle: 'italic' }}>
              This folder is fixed once the library is created and cannot be
              changed later.
            </div>
          </div>

          {pathValid && (
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
                    : 'Counts files before creation so you can spot typos.'}
                </div>
              </div>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => prescanMut.mutate(trimmedPath)}
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
            <span className="t-small">Scan folder immediately after creating</span>
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
  rescanBusy: boolean;
  patternBusy: boolean;
  deleteBusy: boolean;
  onRescan: () => void;
  onSavePattern: (pattern: string | null) => void;
  onDeleteLibrary: () => void;
};

function LibraryCard({
  library,
  rescanBusy,
  patternBusy,
  deleteBusy,
  onRescan,
  onSavePattern,
  onDeleteLibrary,
}: LibraryCardProps) {
  const [confirmOpen, setConfirmOpen] = useState(false);

  const lastScanned = library.lastScannedAt
    ? new Date(library.lastScannedAt).toLocaleString()
    : null;

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
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          title="Delete library"
          aria-label={`Delete ${library.name}`}
          disabled={deleteBusy}
          onClick={() => setConfirmOpen(true)}
          className="text-(--color-accent-ink)"
        >
          <Icon name="close" size={12} />
        </Button>
      </div>

      <DeleteLibraryDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        library={library}
        busy={deleteBusy}
        onConfirm={() => {
          onDeleteLibrary();
          setConfirmOpen(false);
        }}
      />

      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 12,
          padding: '10px 12px',
          background: 'var(--color-paper-2)',
          borderRadius: 2,
          marginBottom: 14,
        }}
      >
        <div style={{ flex: 1, minWidth: 0 }}>
          <div className="t-label" style={{ marginBottom: 4 }}>Folder (fixed)</div>
          <div className="mono" style={{ fontSize: 12.5, wordBreak: 'break-all' }}>
            {library.path || <em>(empty)</em>}
          </div>
          <div className="t-micro" style={{ marginTop: 4, fontStyle: 'italic' }}>
            {lastScanned
              ? `last scan ${lastScanned} · ${library.fileCount.toLocaleString()} files, ${library.discoveredCount.toLocaleString()} discovered`
              : 'never scanned'}
          </div>
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={onRescan}
          disabled={rescanBusy || library.path === ''}
        >
          <Icon name="refresh" size={13} /> {rescanBusy ? 'Scanning…' : 'Rescan'}
        </Button>
      </div>

      <NamingPatternEditor
        library={library}
        busy={patternBusy}
        onSave={onSavePattern}
      />
    </div>
  );
}

// DeleteLibraryDialog confirms a destructive library teardown. The
// "type the name to confirm" gate matches the weight of the action —
// cascading DELETE takes every book, annotation, reading session, and
// shelf assignment inside the library with it. Source files on disk
// are left alone (user-managed roots), so re-scanning the same path
// under a fresh library is how an admin undoes a mistake.
function DeleteLibraryDialog({
  open,
  onOpenChange,
  library,
  busy,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  library: SettingsLibrary;
  busy: boolean;
  onConfirm: () => void;
}) {
  const [confirmInput, setConfirmInput] = useState('');

  useEffect(() => {
    if (!open) setConfirmInput('');
  }, [open]);

  const matches = confirmInput.trim() === library.name;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[440px]">
        <DialogHeader>
          <DialogTitle>Delete library</DialogTitle>
          <DialogDescription>
            This removes <strong>{library.name}</strong>, all{' '}
            {library.bookCount} book records, their cover images, and every
            annotation, reading session, and shelf assignment inside it.
            Source files on disk are left alone.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-2">
          <Label htmlFor="delete-confirm">
            Type <span className="mono">{library.name}</span> to confirm.
          </Label>
          <Input
            id="delete-confirm"
            value={confirmInput}
            onChange={(e) => setConfirmInput(e.target.value)}
            autoFocus
          />
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            onClick={() => onOpenChange(false)}
            disabled={busy}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            onClick={onConfirm}
            disabled={!matches || busy}
          >
            {busy ? 'Deleting…' : 'Delete library'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// NamingPatternEditor is the per-library pattern field from spec §9.2.
// Edits are local until Save; a live preview hits the backend resolver so
// admins see the exact path a book would land at before committing.
function NamingPatternEditor({
  library,
  busy,
  onSave,
}: {
  library: SettingsLibrary;
  busy: boolean;
  onSave: (pattern: string | null) => void;
}) {
  const saved = library.fileNamingPattern ?? '';
  const [draft, setDraft] = useState(saved);

  // Resync when the server value changes (another tab saved, or a PATCH
  // completed and invalidated the query).
  useEffect(() => {
    setDraft(saved);
  }, [saved]);

  const trimmed = draft.trim();
  const dirty = trimmed !== saved;

  // Preview fires after a short idle — avoids hammering the endpoint
  // while the admin is typing.
  const [debounced, setDebounced] = useState(trimmed);
  useEffect(() => {
    const handle = window.setTimeout(() => setDebounced(trimmed), 250);
    return () => window.clearTimeout(handle);
  }, [trimmed]);

  const preview = useQuery({
    queryKey: ['settings', 'libraries', library.id, 'pattern-preview', debounced],
    queryFn: () => previewNamingPattern(debounced),
    enabled: debounced !== '',
    staleTime: 60_000,
  });

  return (
    <div
      style={{
        borderTop: '1px dashed var(--color-rule-soft)',
        marginTop: 14,
        paddingTop: 14,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 12, marginBottom: 6 }}>
        <span className="t-label">File naming pattern</span>
        <span
          className="t-micro"
          style={{
            color: library.fileNamingPattern
              ? 'var(--color-accent-ink)'
              : 'var(--color-ink-3)',
          }}
        >
          {library.fileNamingPattern ? 'custom' : 'default'}
        </span>
      </div>
      <p className="t-small" style={{ marginBottom: 8, fontStyle: 'italic' }}>
        Applied when you approve a book from BookDrop. Blank keeps the
        original filename on disk.
      </p>

      <Input
        placeholder="{authors}/<{series}/><{seriesIndex}. >{title}/{title}< - {authors}>< ({year})>"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        className="mono text-[12.5px]"
      />

      {debounced !== '' && (
        <div
          className="mono"
          style={{
            marginTop: 8,
            padding: '8px 12px',
            background: 'var(--color-paper-2)',
            borderRadius: 2,
            fontSize: 12,
            color: 'var(--color-ink-2)',
            wordBreak: 'break-all',
          }}
        >
          <span className="t-micro" style={{ marginRight: 8 }}>Preview</span>
          {preview.data ?? (preview.isLoading ? 'Resolving…' : '')}
        </div>
      )}

      <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
        <Button
          type="button"
          variant="outline"
          onClick={() => onSave(trimmed === '' ? null : trimmed)}
          disabled={busy || !dirty}
        >
          {busy ? 'Saving…' : 'Save pattern'}
        </Button>
        {library.fileNamingPattern && (
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              setDraft('');
              onSave(null);
            }}
            disabled={busy}
          >
            Clear
          </Button>
        )}
      </div>
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
// OIDC / SSO
// ---------------------------------------------------------------------------

function OidcPanel({ isAdmin }: { isAdmin: boolean }) {
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: oidcAdminSettingsQueryKey,
    queryFn: fetchOidcAdminSettings,
    enabled: isAdmin,
  });

  const [draft, setDraft] = useState<OidcAdminSettings | null>(null);
  // Per-provider "secret was touched" flags so an empty secret field
  // only clears the stored secret when the admin explicitly typed in
  // it (or clicked the clear button).
  const [secretTouched, setSecretTouched] = useState<Record<ProviderSlug, boolean>>({
    google: false,
    github: false,
    generic: false,
  });

  useEffect(() => {
    if (query.data && !draft) {
      setDraft(query.data);
    }
  }, [query.data, draft]);

  const saveMut = useMutation({
    mutationFn: saveOidcAdminSettings,
    onSuccess: (data) => {
      queryClient.setQueryData(oidcAdminSettingsQueryKey, data);
      setDraft(data);
      setSecretTouched({ google: false, github: false, generic: false });
      queryClient.invalidateQueries({ queryKey: ['oidc-config'] });
    },
  });

  if (!isAdmin) return <AdminGate label="OIDC / SSO" />;
  if (query.isLoading || !draft) {
    return (
      <>
        <h2 className="t-h2" style={{ marginBottom: 8 }}>OIDC / SSO</h2>
        <p className="t-small" style={{ fontStyle: 'italic' }}>Loading…</p>
      </>
    );
  }

  const saveError = saveMut.error as unknown as ApiError | null;
  const someEnabled =
    (draft.google.enabled && draft.google.clientId !== '' && (draft.google.clientSecretSet || (draft.google.clientSecret ?? '') !== '')) ||
    (draft.github.enabled && draft.github.clientId !== '' && (draft.github.clientSecretSet || (draft.github.clientSecret ?? '') !== '')) ||
    (draft.generic.enabled && draft.generic.clientId !== '' && draft.generic.issuerUri !== '');
  const canForceOnly = someEnabled;

  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 8 }}>OIDC / SSO</h2>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: 'italic' }}>
        Enable Google, GitHub, and a custom OpenID Connect provider independently —
        the login page shows a button for each one you turn on. Changes take
        effect on the next login, no restart required.
      </p>

      {saveError && <Notice kind="err">{saveError.message}</Notice>}

      <Card>
        <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
          <div style={{ flex: 1 }}>
            <div style={{ fontSize: 13.5, fontWeight: 500 }}>Force SSO (hide local login)</div>
            <div className="t-small" style={{ fontSize: 11.5 }}>
              Hides the password form. Escape hatch:{' '}
              <span className="mono">/login?local=true</span>. Requires at least
              one provider enabled.
            </div>
          </div>
          <Switch
            checked={draft.forceOnly}
            disabled={!canForceOnly}
            onCheckedChange={(v) => setDraft({ ...draft, forceOnly: v })}
            aria-label="Force OIDC"
          />
        </div>
      </Card>

      <GooglePanel
        value={draft.google}
        onChange={(next) => setDraft({ ...draft, google: next })}
        redirectUri={draft.redirectUri}
        secretTouched={secretTouched.google}
        onSecretTouch={(v) => setSecretTouched({ ...secretTouched, google: v })}
      />

      <GitHubPanel
        value={draft.github}
        onChange={(next) => setDraft({ ...draft, github: next })}
        redirectUri={draft.redirectUri}
        secretTouched={secretTouched.github}
        onSecretTouch={(v) => setSecretTouched({ ...secretTouched, github: v })}
      />

      <GenericOidcPanel
        value={draft.generic}
        onChange={(next) => setDraft({ ...draft, generic: next })}
        redirectUri={draft.redirectUri}
        secretTouched={secretTouched.generic}
        onSecretTouch={(v) => setSecretTouched({ ...secretTouched, generic: v })}
      />

      <h3 className="t-h3" style={{ marginTop: 24, marginBottom: 8 }}>Auto provisioning</h3>
      <Card>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
            <div style={{ flex: 1 }}>
              <div style={{ fontSize: 13.5, fontWeight: 500 }}>Auto-create users on first login</div>
              <div className="t-small" style={{ fontSize: 11.5 }}>
                When off, unknown SSO users are rejected unless linked to an existing local account.
              </div>
            </div>
            <Switch
              checked={draft.autoProvision.enableAutoProvisioning}
              onCheckedChange={(v) =>
                setDraft({
                  ...draft,
                  autoProvision: { ...draft.autoProvision, enableAutoProvisioning: v },
                })
              }
            />
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
            <div style={{ flex: 1 }}>
              <div style={{ fontSize: 13.5, fontWeight: 500 }}>Link by email</div>
              <div className="t-small" style={{ fontSize: 11.5 }}>
                Permits linking an existing local account to an SSO identity on first login when emails match.
              </div>
            </div>
            <Switch
              checked={draft.autoProvision.allowLocalAccountLinking}
              onCheckedChange={(v) =>
                setDraft({
                  ...draft,
                  autoProvision: { ...draft.autoProvision, allowLocalAccountLinking: v },
                })
              }
            />
          </div>
          <Field label="Default role for new users">
            <Select
              value={draft.autoProvision.defaultRole}
              onChange={(v) =>
                setDraft({
                  ...draft,
                  autoProvision: {
                    ...draft.autoProvision,
                    defaultRole: v === 'admin' ? 'admin' : 'user',
                  },
                })
              }
              options={[
                { value: 'user', label: 'User' },
                { value: 'admin', label: 'Admin' },
              ]}
            />
          </Field>
        </div>
      </Card>

      <div style={{ display: 'flex', gap: 10, marginTop: 20 }}>
        <Button onClick={() => saveMut.mutate(draft)} disabled={saveMut.isPending}>
          {saveMut.isPending ? 'Saving…' : 'Save all'}
        </Button>
      </div>
    </>
  );
}

type OAuthPresetValue = OidcAdminSettings['google'];

function PresetProviderPanel({
  title,
  slug,
  value,
  onChange,
  redirectUri,
  registerUrl,
  intro,
  secretTouched,
  onSecretTouch,
}: {
  title: string;
  slug: ProviderSlug;
  value: OAuthPresetValue;
  onChange: (next: OAuthPresetValue) => void;
  redirectUri: string;
  registerUrl: string;
  intro: ReactNode;
  secretTouched: boolean;
  onSecretTouch: (v: boolean) => void;
}) {
  const [testResult, setTestResult] = useState<OidcTestResult | null>(null);
  const testMut = useMutation({
    mutationFn: () =>
      testOidcProvider(slug, {
        [slug]: {
          clientId: value.clientId,
          clientSecret: value.clientSecret ?? '',
        },
      }),
    onSuccess: (res) => setTestResult(res),
  });

  return (
    <>
      <h3 className="t-h3" style={{ marginTop: 24, marginBottom: 8 }}>{title}</h3>
      <Card>
        <div style={{ display: 'flex', alignItems: 'center', gap: 14, marginBottom: 10 }}>
          <div style={{ flex: 1 }}>
            <div style={{ fontSize: 13.5, fontWeight: 500 }}>Enable</div>
            <div className="t-small" style={{ fontSize: 11.5 }}>{intro}</div>
          </div>
          <Switch
            checked={value.enabled}
            disabled={value.clientId === '' || (!value.clientSecretSet && (value.clientSecret ?? '') === '')}
            onCheckedChange={(v) => onChange({ ...value, enabled: v })}
          />
        </div>
        <p className="t-small" style={{ marginBottom: 10, fontStyle: 'italic' }}>
          Register an OAuth app at{' '}
          <a href={registerUrl} target="_blank" rel="noreferrer">{registerUrl}</a>,
          set its redirect URL to{' '}
          <span className="mono">{redirectUri || '(set APP_URL)'}</span>, then
          paste the Client ID and Secret below.
        </p>
        <Field label="Client ID">
          <Input
            value={value.clientId}
            onChange={(e) => onChange({ ...value, clientId: e.target.value })}
          />
        </Field>
        <Field label={`Client secret${value.clientSecretSet ? ' (stored — leave blank to keep)' : ''}`}>
          <Input
            type="password"
            autoComplete="new-password"
            placeholder={value.clientSecretSet ? '••••••••' : ''}
            onChange={(e) => {
              onSecretTouch(true);
              onChange({
                ...value,
                clientSecret: e.target.value,
                clientSecretSet: e.target.value !== '' || value.clientSecretSet,
              });
            }}
          />
          {value.clientSecretSet && !secretTouched && (
            <button
              type="button"
              className="t-small"
              style={{
                marginTop: 4,
                background: 'none',
                border: 'none',
                padding: 0,
                cursor: 'pointer',
                color: 'var(--color-accent)',
                alignSelf: 'flex-start',
              }}
              onClick={() => {
                onSecretTouch(true);
                onChange({ ...value, clientSecret: '', clientSecretSet: false });
              }}
            >
              Clear stored secret
            </button>
          )}
        </Field>
        <div style={{ marginTop: 10 }}>
          <Button variant="outline" onClick={() => testMut.mutate()} disabled={testMut.isPending}>
            {testMut.isPending ? 'Testing…' : 'Test connection'}
          </Button>
        </div>
        {testResult && <TestResultBlock result={testResult} />}
      </Card>
    </>
  );
}

function GooglePanel(props: {
  value: OAuthPresetValue;
  onChange: (v: OAuthPresetValue) => void;
  redirectUri: string;
  secretTouched: boolean;
  onSecretTouch: (v: boolean) => void;
}) {
  return (
    <PresetProviderPanel
      title="Google"
      slug="google"
      registerUrl="https://console.cloud.google.com/apis/credentials"
      intro="Lets users sign in with their Google account. Scopes and claims are baked in."
      {...props}
    />
  );
}

function GitHubPanel(props: {
  value: OAuthPresetValue;
  onChange: (v: OAuthPresetValue) => void;
  redirectUri: string;
  secretTouched: boolean;
  onSecretTouch: (v: boolean) => void;
}) {
  return (
    <PresetProviderPanel
      title="GitHub"
      slug="github"
      registerUrl="https://github.com/settings/developers"
      intro="Lets users sign in with their GitHub account. Endpoints, scopes, and the user API are baked in."
      {...props}
    />
  );
}

function GenericOidcPanel({
  value,
  onChange,
  redirectUri,
  secretTouched,
  onSecretTouch,
}: {
  value: OidcAdminSettings['generic'];
  onChange: (v: OidcAdminSettings['generic']) => void;
  redirectUri: string;
  secretTouched: boolean;
  onSecretTouch: (v: boolean) => void;
}) {
  const [testResult, setTestResult] = useState<OidcTestResult | null>(null);
  const testMut = useMutation({
    mutationFn: () =>
      testOidcProvider('generic', {
        generic: {
          clientId: value.clientId,
          clientSecret: value.clientSecret ?? '',
          issuerUri: value.issuerUri,
          scopes: value.scopes,
          claimMapping: value.claimMapping,
        },
      }),
    onSuccess: (res) => setTestResult(res),
  });

  const canEnable =
    value.clientId.trim() !== '' &&
    value.issuerUri.trim() !== '' &&
    value.claimMapping.username.trim() !== '' &&
    value.claimMapping.email.trim() !== '' &&
    value.claimMapping.name.trim() !== '';

  return (
    <>
      <h3 className="t-h3" style={{ marginTop: 24, marginBottom: 8 }}>Custom OIDC provider</h3>
      <Card>
        <div style={{ display: 'flex', alignItems: 'center', gap: 14, marginBottom: 10 }}>
          <div style={{ flex: 1 }}>
            <div style={{ fontSize: 13.5, fontWeight: 500 }}>Enable</div>
            <div className="t-small" style={{ fontSize: 11.5 }}>
              Authentik, Authelia, Keycloak, Pocket ID, or any OpenID Connect
              provider with a <span className="mono">/.well-known/openid-configuration</span> document.
            </div>
          </div>
          <Switch
            checked={value.enabled}
            disabled={!canEnable}
            onCheckedChange={(v) => onChange({ ...value, enabled: v })}
          />
        </div>
        <Field label="Provider display name">
          <Input
            value={value.providerName}
            onChange={(e) => onChange({ ...value, providerName: e.target.value })}
            placeholder="Authentik"
          />
        </Field>
        <Field label="Issuer URI">
          <Input
            value={value.issuerUri}
            onChange={(e) => onChange({ ...value, issuerUri: e.target.value })}
            placeholder="https://auth.example.com/application/o/embookshelf/"
          />
        </Field>
        <Field label="Client ID">
          <Input
            value={value.clientId}
            onChange={(e) => onChange({ ...value, clientId: e.target.value })}
          />
        </Field>
        <Field label={`Client secret${value.clientSecretSet ? ' (stored — leave blank to keep)' : ''}`}>
          <Input
            type="password"
            autoComplete="new-password"
            placeholder={value.clientSecretSet ? '••••••••' : ''}
            onChange={(e) => {
              onSecretTouch(true);
              onChange({
                ...value,
                clientSecret: e.target.value,
                clientSecretSet: e.target.value !== '' || value.clientSecretSet,
              });
            }}
          />
          {value.clientSecretSet && !secretTouched && (
            <button
              type="button"
              className="t-small"
              style={{
                marginTop: 4,
                background: 'none',
                border: 'none',
                padding: 0,
                cursor: 'pointer',
                color: 'var(--color-accent)',
                alignSelf: 'flex-start',
              }}
              onClick={() => {
                onSecretTouch(true);
                onChange({ ...value, clientSecret: '', clientSecretSet: false });
              }}
            >
              Clear stored secret
            </button>
          )}
        </Field>
        <Field label="Scopes (space-separated)">
          <Input
            value={value.scopes}
            onChange={(e) => onChange({ ...value, scopes: e.target.value })}
            placeholder="openid profile email"
          />
        </Field>
        <div className="t-label" style={{ marginTop: 12 }}>Claim mapping</div>
        <Field label="Username claim">
          <Input
            value={value.claimMapping.username}
            onChange={(e) =>
              onChange({
                ...value,
                claimMapping: { ...value.claimMapping, username: e.target.value },
              })
            }
          />
        </Field>
        <Field label="Email claim">
          <Input
            value={value.claimMapping.email}
            onChange={(e) =>
              onChange({
                ...value,
                claimMapping: { ...value.claimMapping, email: e.target.value },
              })
            }
          />
        </Field>
        <Field label="Display name claim">
          <Input
            value={value.claimMapping.name}
            onChange={(e) =>
              onChange({
                ...value,
                claimMapping: { ...value.claimMapping, name: e.target.value },
              })
            }
          />
        </Field>
        <p className="t-small" style={{ marginTop: 10, fontStyle: 'italic' }}>
          Redirect URI: <span className="mono">{redirectUri || '(set APP_URL)'}</span>
        </p>
        <div style={{ marginTop: 10 }}>
          <Button variant="outline" onClick={() => testMut.mutate()} disabled={testMut.isPending}>
            {testMut.isPending ? 'Testing…' : 'Test connection'}
          </Button>
        </div>
        {testResult && <TestResultBlock result={testResult} />}
      </Card>
    </>
  );
}

function TestResultBlock({ result }: { result: OidcTestResult }) {
  return (
    <div style={{ marginTop: 14 }}>
      <Notice kind={result.success ? 'ok' : 'err'}>
        {result.success ? 'All critical checks passed.' : 'One or more checks failed.'}
      </Notice>
      <div style={{ marginTop: 10, display: 'flex', flexDirection: 'column', gap: 6 }}>
        {result.checks.map((c: OidcTestCheck, i: number) => (
          <div
            key={i}
            style={{
              display: 'grid',
              gridTemplateColumns: '70px 1fr',
              gap: 10,
              fontSize: 13,
              padding: '6px 10px',
              border: '1px solid var(--color-rule-soft)',
              borderRadius: 2,
              background: 'var(--color-paper-0)',
            }}
          >
            <span
              className="mono"
              style={{
                color:
                  c.status === 'PASS'
                    ? 'oklch(0.58 0.12 140)'
                    : c.status === 'WARN'
                      ? 'oklch(0.72 0.14 70)'
                      : 'oklch(0.62 0.22 25)',
                fontWeight: 600,
              }}
            >
              {c.status}
            </span>
            <div>
              <div style={{ fontWeight: 500 }}>{c.name}</div>
              <div className="t-small" style={{ fontSize: 11.5 }}>{c.message}</div>
            </div>
          </div>
        ))}
      </div>
    </div>
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

