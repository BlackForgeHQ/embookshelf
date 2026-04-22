import { useState, type CSSProperties, type MouseEventHandler, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useRouterState } from '@tanstack/react-router';

import type { ApiError } from '@/api/client';
import {
  fetchMe,
  logout as apiLogout,
  meQueryKey,
  type AuthUser,
} from '@/api/auth';
import {
  createShelf,
  createSmartShelf,
  deleteShelf,
  fetchLibraries,
  fetchShelves,
  librariesQueryKey,
  shelvesQueryKey,
  updateShelf,
  type Shelf,
  type ShelfRule,
} from '@/api/books';
import { CURRENT_USER } from '@/data/mock';
import { Icon, type IconName } from './Icon';
import { RuleEditor } from './RuleEditor';
import { Button } from '@/components/ui/button';
import { Avatar, AvatarFallback } from '@/components/ui/avatar';

// Library colors are a design concern (no DB column backs them) so we key
// a stable palette by library slug. Unknown slugs fall back to the accent.
const LIBRARY_COLORS: Record<string, string> = {
  main: 'oklch(0.48 0.09 35)',
  academic: 'oklch(0.42 0.06 110)',
  comics: 'oklch(0.38 0.05 200)',
};

const BUILTIN_SHELF_ICONS: Record<string, IconName> = {
  reading: 'book-open',
  new: 'sparkle',
  finished: 'check',
  tofinish: 'flag',
  wishlist: 'bookmark',
};

// Sidebar is a pure navigation element — every item is a real router Link
// so browser back/forward, right-click, and deep-link all work.
export function Sidebar() {
  const state = useRouterState();
  const pathname = state.location.pathname;
  const search = state.location.search as { shelf?: string; library?: string };

  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const me = useQuery({
    queryKey: meQueryKey,
    queryFn: fetchMe,
    staleTime: 60_000,
  });
  const libraries = useQuery({
    queryKey: librariesQueryKey,
    queryFn: fetchLibraries,
  });
  const shelves = useQuery({
    queryKey: shelvesQueryKey,
    queryFn: fetchShelves,
  });
  const logoutMut = useMutation({
    mutationFn: apiLogout,
    onSuccess: () => {
      queryClient.setQueryData(meQueryKey, null);
      void navigate({ to: '/login', replace: true });
    },
  });

  // Regular shelf creation still goes through a native prompt — single
  // text input, not worth a modal. Smart shelves open the RuleEditor.
  const createShelfMut = useMutation({
    mutationFn: (name: string) => createShelf(name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: shelvesQueryKey });
    },
  });
  const createSmartMut = useMutation({
    mutationFn: (args: { name: string; rule: ShelfRule }) =>
      createSmartShelf(args.name, args.rule),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: shelvesQueryKey });
      setSmartDraft(null);
    },
  });
  const updateSmartMut = useMutation({
    mutationFn: (args: { slug: string; name: string; rule: ShelfRule }) =>
      updateShelf(args.slug, { name: args.name, rule: args.rule, ruleSet: true }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: shelvesQueryKey });
      setSmartDraft(null);
    },
  });
  const deleteShelfMut = useMutation({
    mutationFn: (slug: string) => deleteShelf(slug),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: shelvesQueryKey });
    },
  });

  // smartDraft drives the RuleEditor modal. `null` means closed; a
  // payload with slug === null opens in create mode, otherwise edit.
  const [smartDraft, setSmartDraft] = useState<
    | { mode: 'create' }
    | { mode: 'edit'; shelf: Shelf }
    | null
  >(null);

  const promptCreateShelf = () => {
    const name = window.prompt('Name the new shelf');
    if (name && name.trim()) {
      createShelfMut.mutate(name.trim());
    }
  };

  const libs = libraries.data ?? [];
  const allShelves = shelves.data ?? [];
  // "reading" is promoted into the Browse section as a first-class nav item,
  // so drop it from the Shelves list to avoid rendering two links to the
  // same filtered view.
  const shelfList = allShelves.filter((s) => !s.isSmart && s.slug !== 'reading');
  const smartList = allShelves.filter((s) => s.isSmart);
  const totalBooks = libs.reduce((n, lib) => n + lib.bookCount, 0);

  const smartMutError =
    (createSmartMut.error ?? updateSmartMut.error) as ApiError | null;

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
          count={totalBooks || undefined}
          active={isLibrary && !activeShelf && !activeLibrary}
        />
        <NavItem
          to="/library"
          search={{ shelf: 'reading' }}
          icon="book-open"
          label="Reading Now"
          count={shelfList.find((s) => s.slug === 'reading')?.bookCount}
          active={activeShelf === 'reading'}
        />
        <NavItem to="/notebook" icon="note" label="Notebook" active={pathname === '/notebook'} />
        <NavItem to="/stats" icon="chart" label="Stats" active={pathname === '/stats'} />
        <NavItem to="/bookdrop" icon="upload" label="BookDrop" active={pathname === '/bookdrop'} />
      </Section>

      <Section
        title="Libraries"
        action={
          <Button variant="ghost" size="icon-xs" aria-label="New library">
            <Icon name="plus" size={12} />
          </Button>
        }
      >
        {libs.map((lib) => (
          <NavItem
            key={lib.id}
            to="/library"
            search={{ library: lib.slug }}
            label={lib.name}
            count={lib.bookCount}
            color={LIBRARY_COLORS[lib.slug] ?? 'var(--color-accent)'}
            active={activeLibrary === lib.slug}
          />
        ))}
      </Section>

      <Section
        title="Shelves"
        action={
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label="New shelf"
            onClick={promptCreateShelf}
            disabled={createShelfMut.isPending}
          >
            <Icon name="plus" size={12} />
          </Button>
        }
      >
        {shelfList.map((s) => (
          <NavItem
            key={s.id}
            to="/library"
            search={{ shelf: s.slug }}
            icon={BUILTIN_SHELF_ICONS[s.slug] ?? 'folder'}
            label={s.name}
            count={s.bookCount}
            indent={4}
            active={activeShelf === s.slug}
          />
        ))}
      </Section>

      <Section
        title="Magic Shelves"
        action={
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label="New smart shelf"
            onClick={() => setSmartDraft({ mode: 'create' })}
          >
            <Icon name="plus" size={12} />
          </Button>
        }
      >
        {smartList.length === 0 && (
          <div className="t-small" style={{ padding: '4px 20px 8px', fontStyle: 'italic', color: 'var(--color-ink-3)' }}>
            No smart shelves yet.
          </div>
        )}
        {smartList.map((s) => (
          <SmartShelfRow
            key={s.id}
            shelf={s}
            active={activeShelf === s.slug}
            onEdit={() => setSmartDraft({ mode: 'edit', shelf: s })}
            onDelete={() => {
              if (window.confirm(`Delete smart shelf "${s.name}"?`)) {
                deleteShelfMut.mutate(s.slug);
              }
            }}
          />
        ))}
      </Section>

      <div style={{ flex: 1 }} />

      <UserBadge user={me.data ?? null} onLogout={() => logoutMut.mutate()} loggingOut={logoutMut.isPending} />

      {smartDraft && (
        <RuleEditor
          title={smartDraft.mode === 'create' ? 'New smart shelf' : `Edit ${smartDraft.shelf.name}`}
          submitLabel={smartDraft.mode === 'create' ? 'Create' : 'Save'}
          initialName={smartDraft.mode === 'edit' ? smartDraft.shelf.name : ''}
          initialRule={smartDraft.mode === 'edit' ? smartDraft.shelf.rule : undefined}
          busy={createSmartMut.isPending || updateSmartMut.isPending}
          error={smartMutError?.message ?? null}
          onSubmit={({ name, rule }) => {
            if (smartDraft.mode === 'create') {
              createSmartMut.mutate({ name, rule });
            } else {
              updateSmartMut.mutate({ slug: smartDraft.shelf.slug, name, rule });
            }
          }}
          onCancel={() => {
            createSmartMut.reset();
            updateSmartMut.reset();
            setSmartDraft(null);
          }}
        />
      )}
    </aside>
  );
}

// SmartShelfRow is a NavItem with inline edit/delete affordances — the
// extra buttons reveal on hover so the sidebar stays quiet when the
// user isn't targeting them.
function SmartShelfRow({
  shelf,
  active,
  onEdit,
  onDelete,
}: {
  shelf: Shelf;
  active: boolean;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const [hovered, setHovered] = useState(false);
  return (
    <div
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      style={{ position: 'relative' }}
    >
      <NavItem
        to="/library"
        search={{ shelf: shelf.slug }}
        icon="sparkle"
        label={shelf.name}
        count={hovered ? undefined : shelf.bookCount}
        indent={4}
        active={active}
      />
      {hovered && (
        <div
          style={{
            position: 'absolute',
            right: 6,
            top: '50%',
            transform: 'translateY(-50%)',
            display: 'flex',
            gap: 2,
            background: 'var(--color-paper-3)',
            padding: 2,
            borderRadius: 2,
          }}
        >
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            onClick={(e) => {
              e.preventDefault();
              e.stopPropagation();
              onEdit();
            }}
            aria-label={`Edit ${shelf.name}`}
            title="Edit rule"
          >
            <Icon name="edit" size={11} />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            onClick={(e) => {
              e.preventDefault();
              e.stopPropagation();
              onDelete();
            }}
            aria-label={`Delete ${shelf.name}`}
            title="Delete"
          >
            <Icon name="close" size={11} />
          </Button>
        </div>
      )}
    </div>
  );
}

type UserBadgeProps = {
  user: AuthUser | null;
  onLogout: () => void;
  loggingOut: boolean;
};

function UserBadge({ user, onLogout, loggingOut }: UserBadgeProps) {
  // Fall back to the prototype's mock identity while the /me query is still
  // warming up — keeps the sidebar from flickering on first paint. Cleared
  // automatically once a real AuthUser resolves.
  const display = user ? user.display : CURRENT_USER.name;
  const role = user ? user.role : CURRENT_USER.role;
  const initials = user ? user.initials : CURRENT_USER.initials;

  return (
    <div
      style={{
        padding: '12px 16px',
        borderTop: '1px solid var(--color-rule-soft)',
        display: 'flex',
        alignItems: 'center',
        gap: 10,
      }}
    >
      <Avatar size="sm">
        <AvatarFallback className="bg-(--color-editorial-accent) text-(--color-paper-0) font-serif font-medium">
          {initials}
        </AvatarFallback>
      </Avatar>
      <div style={{ flex: 1, overflow: 'hidden' }}>
        <div style={{ fontSize: 13, fontWeight: 500 }}>{display}</div>
        <div className="t-micro" style={{ fontSize: 10 }}>{role}</div>
      </div>
      <Button asChild variant="ghost" size="icon-sm" title="Settings">
        <Link to="/settings">
          <Icon name="settings" size={14} />
        </Link>
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        title="Sign out"
        onClick={onLogout}
        disabled={loggingOut}
      >
        <Icon name="arrow-right" size={14} />
      </Button>
    </div>
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
