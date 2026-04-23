import { useEffect, useState, type ReactNode } from 'react';
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
import { AccentPicker, accentColor, type ShelfAccent } from './AccentPicker';
import { Icon, type IconName } from './Icon';
import { RuleEditor } from './RuleEditor';
import { Avatar, AvatarFallback } from '@/components/ui/avatar';
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
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupAction,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuAction,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
} from '@/components/ui/sidebar';

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

// AppSidebar is mounted once inside the authed shell. Every nav target is a
// real TanStack Router <Link> so browser back/forward, right-click-open, and
// deep-link all behave correctly.
export function AppSidebar() {
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

  // Regular shelves are created through a dedicated Dialog (name + accent
  // picker). Smart shelves keep using the RuleEditor, extended with the
  // same accent picker so both shelf types share one design language.
  const createShelfMut = useMutation({
    mutationFn: (args: { name: string; accent: ShelfAccent }) =>
      createShelf(args.name, args.accent),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: shelvesQueryKey });
      setShelfDraftOpen(false);
    },
  });
  const createSmartMut = useMutation({
    mutationFn: (args: { name: string; rule: ShelfRule; accent: ShelfAccent }) =>
      createSmartShelf(args.name, args.rule, args.accent),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: shelvesQueryKey });
      setSmartDraft(null);
    },
  });
  const updateSmartMut = useMutation({
    mutationFn: (args: {
      slug: string;
      name: string;
      rule: ShelfRule;
      accent: ShelfAccent;
    }) =>
      updateShelf(args.slug, {
        name: args.name,
        accent: args.accent,
        rule: args.rule,
        ruleSet: true,
      }),
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

  const [shelfDraftOpen, setShelfDraftOpen] = useState(false);
  const [smartDraft, setSmartDraft] = useState<
    | { mode: 'create' }
    | { mode: 'edit'; shelf: Shelf }
    | null
  >(null);

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
    <Sidebar collapsible="icon">
      <SidebarHeader>
        <div className="flex items-center gap-2.5 px-1.5 py-1">
          <div
            className="flex size-6 shrink-0 items-center justify-center rounded-sm bg-(--color-ink-1) font-serif text-base font-semibold italic text-(--color-paper-0)"
          >
            e
          </div>
          <div className="font-serif text-lg font-medium tracking-tight group-data-[collapsible=icon]:hidden">
            embookshelf
          </div>
        </div>
      </SidebarHeader>

      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>Browse</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              <NavItem
                to="/"
                icon="home"
                label="Dashboard"
                active={pathname === '/'}
              />
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
              <NavItem
                to="/notebook"
                icon="note"
                label="Notebook"
                active={pathname === '/notebook'}
              />
              <NavItem
                to="/stats"
                icon="chart"
                label="Stats"
                active={pathname === '/stats'}
              />
              <NavItem
                to="/bookdrop"
                icon="upload"
                label="BookDrop"
                active={pathname === '/bookdrop'}
              />
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>

        <SidebarGroup>
          <SidebarGroupLabel>Libraries</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
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
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>

        <SidebarGroup>
          <SidebarGroupLabel>Shelves</SidebarGroupLabel>
          <SidebarGroupAction
            title="New shelf"
            aria-label="New shelf"
            onClick={() => setShelfDraftOpen(true)}
            disabled={createShelfMut.isPending}
          >
            <Icon name="plus" size={12} />
          </SidebarGroupAction>
          <SidebarGroupContent>
            <SidebarMenu>
              {shelfList.map((s) => (
                <NavItem
                  key={s.id}
                  to="/library"
                  search={{ shelf: s.slug }}
                  icon={BUILTIN_SHELF_ICONS[s.slug] ?? 'folder'}
                  label={s.name}
                  count={s.bookCount}
                  color={accentColor(s.accent)}
                  active={activeShelf === s.slug}
                />
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>

        <SidebarGroup>
          <SidebarGroupLabel>Magic Shelves</SidebarGroupLabel>
          <SidebarGroupAction
            title="New smart shelf"
            aria-label="New smart shelf"
            onClick={() => setSmartDraft({ mode: 'create' })}
          >
            <Icon name="plus" size={12} />
          </SidebarGroupAction>
          <SidebarGroupContent>
            <SidebarMenu>
              {smartList.length === 0 && (
                <li className="t-small px-2 py-1 italic text-(--color-ink-3) group-data-[collapsible=icon]:hidden">
                  No smart shelves yet.
                </li>
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
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
        {me.data?.role === 'admin' && (
          <SidebarGroup className="mt-auto">
            <SidebarGroupContent>
              <SidebarMenu>
                <NavItem
                  to="/admin"
                  icon="settings"
                  label="Settings"
                  active={pathname.startsWith('/admin')}
                />
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        )}
      </SidebarContent>

      <SidebarFooter>
        <UserBadge
          user={me.data ?? null}
          onLogout={() => logoutMut.mutate()}
          loggingOut={logoutMut.isPending}
        />
      </SidebarFooter>

      <ShelfCreatorDialog
        open={shelfDraftOpen}
        onOpenChange={(open) => {
          if (!open) createShelfMut.reset();
          setShelfDraftOpen(open);
        }}
        existingNames={allShelves.map((s) => s.name)}
        busy={createShelfMut.isPending}
        error={(createShelfMut.error as ApiError | null)?.message ?? null}
        onSubmit={(draft) => createShelfMut.mutate(draft)}
      />

      {smartDraft && (
        <RuleEditor
          title={smartDraft.mode === 'create' ? 'New smart shelf' : `Edit ${smartDraft.shelf.name}`}
          submitLabel={smartDraft.mode === 'create' ? 'Create' : 'Save'}
          initialName={smartDraft.mode === 'edit' ? smartDraft.shelf.name : ''}
          initialRule={smartDraft.mode === 'edit' ? smartDraft.shelf.rule : undefined}
          initialAccent={
            smartDraft.mode === 'edit'
              ? (smartDraft.shelf.accent as ShelfAccent)
              : 'accent'
          }
          busy={createSmartMut.isPending || updateSmartMut.isPending}
          error={smartMutError?.message ?? null}
          onSubmit={({ name, rule, accent }) => {
            if (smartDraft.mode === 'create') {
              createSmartMut.mutate({ name, rule, accent });
            } else {
              updateSmartMut.mutate({
                slug: smartDraft.shelf.slug,
                name,
                rule,
                accent,
              });
            }
          }}
          onCancel={() => {
            createSmartMut.reset();
            updateSmartMut.reset();
            setSmartDraft(null);
          }}
        />
      )}
    </Sidebar>
  );
}

type ShelfCreatorDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  existingNames: string[];
  busy: boolean;
  error: string | null;
  onSubmit: (draft: { name: string; accent: ShelfAccent }) => void;
};

// ShelfCreatorDialog replaces the old window.prompt-based shelf creation.
// Matches the LibraryCreatorDialog shape (controlled open, reset on close)
// so the two creation flows feel like part of the same family.
function ShelfCreatorDialog({
  open,
  onOpenChange,
  existingNames,
  busy,
  error,
  onSubmit,
}: ShelfCreatorDialogProps) {
  const [name, setName] = useState('');
  const [accent, setAccent] = useState<ShelfAccent>('accent');

  useEffect(() => {
    if (open) return;
    setName('');
    setAccent('accent');
  }, [open]);

  const trimmed = name.trim();
  const collision = existingNames.some(
    (n) => n.toLowerCase() === trimmed.toLowerCase(),
  );
  const valid = trimmed !== '' && !collision;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[420px]">
        <DialogHeader>
          <DialogTitle>New shelf</DialogTitle>
          <DialogDescription>
            A place to group books by hand. Smart shelves live alongside these
            and fill themselves from rules.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="shelf-name">Name</Label>
            <Input
              id="shelf-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. To finish"
              autoFocus
            />
            {trimmed !== '' && collision && (
              <div className="t-small text-(--color-accent-ink)">
                A shelf with that name already exists.
              </div>
            )}
          </div>

          <div className="flex flex-col gap-2">
            <Label>Accent</Label>
            <AccentPicker value={accent} onChange={setAccent} />
          </div>

          {error && (
            <div
              className="t-small rounded-sm border border-(--color-accent-soft) bg-(--color-accent-soft) px-3 py-2 text-(--color-accent-ink)"
            >
              {error}
            </div>
          )}
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
            onClick={() => onSubmit({ name: trimmed, accent })}
            disabled={!valid || busy}
          >
            {busy ? 'Creating…' : 'Create shelf'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// SmartShelfRow is a NavItem with hover-revealed edit/delete affordances.
// SidebarMenuAction occupies the right gutter and automatically appears on
// hover/focus (see sidebar.tsx `showOnHover`).
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
  return (
    <SidebarMenuItem>
      <SidebarMenuButton asChild isActive={active} tooltip={shelf.name}>
        <Link to="/library" search={{ shelf: shelf.slug } as never}>
          <span
            aria-hidden
            className="relative size-1.5 shrink-0 rounded-full"
            style={{ background: accentColor(shelf.accent) }}
          />
          <Icon name="sparkle" size={13} aria-label="smart shelf" />
          <span>{shelf.name}</span>
        </Link>
      </SidebarMenuButton>
      <SidebarMenuBadge className="group-hover/menu-item:hidden group-focus-within/menu-item:hidden">
        {shelf.bookCount}
      </SidebarMenuBadge>
      <SidebarMenuAction
        showOnHover
        title="Edit rule"
        aria-label={`Edit ${shelf.name}`}
        onClick={(e) => {
          e.preventDefault();
          e.stopPropagation();
          onEdit();
        }}
      >
        <Icon name="edit" size={11} />
      </SidebarMenuAction>
      <SidebarMenuAction
        showOnHover
        className="right-7"
        title="Delete"
        aria-label={`Delete ${shelf.name}`}
        onClick={(e) => {
          e.preventDefault();
          e.stopPropagation();
          onDelete();
        }}
      >
        <Icon name="close" size={11} />
      </SidebarMenuAction>
    </SidebarMenuItem>
  );
}

type UserBadgeProps = {
  user: AuthUser | null;
  onLogout: () => void;
  loggingOut: boolean;
};

function UserBadge({ user, onLogout, loggingOut }: UserBadgeProps) {
  // Skip rendering until /me resolves — the beforeLoad guard in _app.tsx
  // ensures a session exists by the time this component mounts, so the
  // null window is brief and avoids flashing fake identity details.
  if (!user) return null;
  const { display, role, initials } = user;

  return (
    <div className="flex items-center gap-2.5 px-2 py-1.5">
      <Avatar size="sm">
        <AvatarFallback className="bg-(--color-editorial-accent) text-(--color-paper-0) font-serif font-medium">
          {initials}
        </AvatarFallback>
      </Avatar>
      <div className="flex-1 overflow-hidden group-data-[collapsible=icon]:hidden">
        <div className="text-[13px] font-medium leading-tight">{display}</div>
        <div className="t-micro text-[10px]">{role}</div>
      </div>
      <Button
        asChild
        variant="ghost"
        size="icon-sm"
        title="My account"
        className="group-data-[collapsible=icon]:hidden"
      >
        <Link to="/settings">
          <Icon name="user" size={14} />
        </Link>
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        title="Sign out"
        onClick={onLogout}
        disabled={loggingOut}
        className="group-data-[collapsible=icon]:hidden"
      >
        <Icon name="arrow-right" size={14} />
      </Button>
    </div>
  );
}

type NavItemProps = {
  to: string;
  search?: Record<string, string | undefined>;
  icon?: IconName;
  label: string;
  count?: number;
  color?: string;
  active?: boolean;
};

function NavItem({ to, search, icon, label, count, color, active }: NavItemProps) {
  return (
    <SidebarMenuItem>
      <SidebarMenuButton asChild isActive={active} tooltip={label}>
        <Link to={to} search={search as never}>
          {color && (
            <span
              aria-hidden
              className="size-1.5 shrink-0 rounded-full"
              style={{ background: color }}
            />
          )}
          {icon && <Icon name={icon} size={15} />}
          <NavLabel>{label}</NavLabel>
        </Link>
      </SidebarMenuButton>
      {count != null && <SidebarMenuBadge>{count}</SidebarMenuBadge>}
    </SidebarMenuItem>
  );
}

function NavLabel({ children }: { children: ReactNode }) {
  return <span>{children}</span>;
}
