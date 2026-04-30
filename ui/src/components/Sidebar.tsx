import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Link, useRouterState } from "@tanstack/react-router"

import { Icon } from "./Icon"
import { RuleEditor } from "./RuleEditor"
import { useUserSettingsDialog } from "./UserSettingsDialog"
import type { ShelfAccent } from "./AccentPicker"
import type { IconName } from "./Icon"
import type { ReactNode } from "react"
import type { ApiError } from "@/api/client"
import type { AuthUser } from "@/api/auth"
import type { Shelf, ShelfRule } from "@/api/books"
import {
  createSmartShelf,
  deleteShelf,
  fetchLibraries,
  fetchShelves,
  librariesQueryKey,
  shelvesQueryKey,
  updateShelf,
} from "@/api/books"
import { fetchMe, meQueryKey } from "@/api/auth"
import { useLogout } from "@/hooks/useLogout"
import { useShelfDraftDialog } from "@/components/ShelfDraftProvider"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
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
} from "@/components/ui/sidebar"

const BUILTIN_SHELF_ICONS: Record<string, IconName> = {
  reading: "book-open",
  new: "sparkle",
  finished: "check",
  tofinish: "flag",
  wishlist: "bookmark",
}

// AppSidebar is mounted once inside the authed shell. Every nav target is a
// real TanStack Router <Link> so browser back/forward, right-click-open, and
// deep-link all behave correctly.
export function AppSidebar() {
  const state = useRouterState()
  const pathname = state.location.pathname
  const search = state.location.search as { shelf?: string; library?: string }

  const queryClient = useQueryClient()
  const me = useQuery({
    queryKey: meQueryKey,
    queryFn: fetchMe,
    staleTime: 60_000,
  })
  const libraries = useQuery({
    queryKey: librariesQueryKey,
    queryFn: fetchLibraries,
  })
  const shelves = useQuery({
    queryKey: shelvesQueryKey,
    queryFn: fetchShelves,
  })
  const logoutMut = useLogout()
  const shelfDraft = useShelfDraftDialog()

  // Smart shelves keep using the RuleEditor, extended with the
  // same accent picker so both shelf types share one design language.
  const createSmartMut = useMutation({
    mutationFn: (args: {
      name: string
      rule: ShelfRule
      accent: ShelfAccent
    }) => createSmartShelf(args.name, args.rule, args.accent),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: shelvesQueryKey })
      setSmartDraft(null)
    },
  })
  const updateSmartMut = useMutation({
    mutationFn: (args: {
      slug: string
      name: string
      rule: ShelfRule
      accent: ShelfAccent
    }) =>
      updateShelf(args.slug, {
        name: args.name,
        accent: args.accent,
        rule: args.rule,
        ruleSet: true,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: shelvesQueryKey })
      setSmartDraft(null)
    },
  })
  const deleteShelfMut = useMutation({
    mutationFn: (slug: string) => deleteShelf(slug),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: shelvesQueryKey })
    },
  })

  const [smartDraft, setSmartDraft] = useState<
    { mode: "create" } | { mode: "edit"; shelf: Shelf } | null
  >(null)

  const libs = libraries.data ?? []
  const allShelves = shelves.data ?? []
  // "reading" is promoted into the Browse section as a first-class nav item,
  // so drop it from the Shelves list to avoid rendering two links to the
  // same filtered view.
  const shelfList = allShelves.filter((s) => !s.isSmart && s.slug !== "reading")
  const smartList = allShelves.filter((s) => s.isSmart)
  const totalBooks = libs.reduce((n, lib) => n + lib.bookCount, 0)

  const smartMutError = (createSmartMut.error ??
    updateSmartMut.error) as ApiError | null

  const isLibrary = pathname.startsWith("/library")
  const activeShelf = isLibrary ? (search.shelf ?? null) : null
  const activeLibrary = isLibrary ? (search.library ?? null) : null

  return (
    <Sidebar collapsible="icon">
      <SidebarHeader>
        <div className="flex items-center gap-2.5 px-1.5 py-1">
          <img
            src="/logo.png"
            alt="embookshelf"
            className="size-6 shrink-0 rounded-sm object-contain"
          />
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
                active={pathname === "/"}
              />
              <NavItem
                to="/library"
                icon="library"
                label="All Books"
                count={totalBooks || undefined}
                active={isLibrary && !activeShelf && !activeLibrary}
              />
              <NavItem
                to="/notebook"
                icon="note"
                label="Notebook"
                active={pathname === "/notebook"}
              />
              <NavItem
                to="/stats"
                icon="chart"
                label="Stats"
                active={pathname === "/stats"}
              />
              <NavItem
                to="/bookdrop"
                icon="upload"
                label="BookDrop"
                active={pathname === "/bookdrop"}
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
                  icon="library"
                  label={lib.name}
                  count={lib.bookCount}
                  active={activeLibrary === lib.slug}
                />
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>

        <SidebarGroup>
          <SidebarGroupLabel>Shelves</SidebarGroupLabel>
          <SidebarGroupAction className="group-data-[collapsible=icon]:!hidden"
            title="New shelf"
            aria-label="New shelf"
            onClick={() => shelfDraft.open()}
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
                  icon={BUILTIN_SHELF_ICONS[s.slug] ?? "folder"}
                  label={s.name}
                  count={s.bookCount}
                  active={activeShelf === s.slug}
                />
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>

        <SidebarGroup>
          <SidebarGroupLabel>Magic Shelves</SidebarGroupLabel>
          <SidebarGroupAction className="group-data-[collapsible=icon]:!hidden"
            title="New smart shelf"
            aria-label="New smart shelf"
            onClick={() => setSmartDraft({ mode: "create" })}
          >
            <Icon name="plus" size={12} />
          </SidebarGroupAction>
          <SidebarGroupContent>
            <SidebarMenu>
              {smartList.length === 0 && (
                <li className="t-small px-2 py-1 text-(--color-ink-3) italic group-data-[collapsible=icon]:hidden">
                  No smart shelves yet.
                </li>
              )}
              {smartList.map((s) => (
                <SmartShelfRow
                  key={s.id}
                  shelf={s}
                  active={activeShelf === s.slug}
                  onEdit={() => setSmartDraft({ mode: "edit", shelf: s })}
                  onDelete={() => {
                    if (window.confirm(`Delete smart shelf "${s.name}"?`)) {
                      deleteShelfMut.mutate(s.slug)
                    }
                  }}
                />
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
        {me.data?.role === "admin" && (
          <SidebarGroup className="mt-auto">
            <SidebarGroupContent>
              <SidebarMenu>
                <NavItem
                  to="/settings"
                  icon="settings"
                  label="Settings"
                  active={pathname.startsWith("/settings")}
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

      {smartDraft && (
        <RuleEditor
          title={
            smartDraft.mode === "create"
              ? "New smart shelf"
              : `Edit ${smartDraft.shelf.name}`
          }
          submitLabel={smartDraft.mode === "create" ? "Create" : "Save"}
          initialName={smartDraft.mode === "edit" ? smartDraft.shelf.name : ""}
          initialRule={
            smartDraft.mode === "edit" ? smartDraft.shelf.rule : undefined
          }
          initialAccent={
            smartDraft.mode === "edit"
              ? (smartDraft.shelf.accent as ShelfAccent)
              : "accent"
          }
          busy={createSmartMut.isPending || updateSmartMut.isPending}
          error={smartMutError?.message ?? null}
          onSubmit={({ name, rule, accent }) => {
            if (smartDraft.mode === "create") {
              createSmartMut.mutate({ name, rule, accent })
            } else {
              updateSmartMut.mutate({
                slug: smartDraft.shelf.slug,
                name,
                rule,
                accent,
              })
            }
          }}
          onCancel={() => {
            createSmartMut.reset()
            updateSmartMut.reset()
            setSmartDraft(null)
          }}
        />
      )}
    </Sidebar>
  )
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
  shelf: Shelf
  active: boolean
  onEdit: () => void
  onDelete: () => void
}) {
  return (
    <SidebarMenuItem>
      <SidebarMenuButton asChild isActive={active} tooltip={shelf.name}>
        <Link to="/library" search={{ shelf: shelf.slug }}>
          <Icon name="sparkle" size={13} aria-label="smart shelf" />
          <span>{shelf.name}</span>
        </Link>
      </SidebarMenuButton>
      <SidebarMenuBadge className="group-focus-within/menu-item:hidden group-hover/menu-item:hidden group-data-[collapsible=icon]:!hidden">
        {shelf.bookCount}
      </SidebarMenuBadge>
      <SidebarMenuAction
        showOnHover
        className="group-data-[collapsible=icon]:!hidden"
        title="Edit rule"
        aria-label={`Edit ${shelf.name}`}
        onClick={(e) => {
          e.preventDefault()
          e.stopPropagation()
          onEdit()
        }}
      >
        <Icon name="edit" size={11} />
      </SidebarMenuAction>
      <SidebarMenuAction
        showOnHover
        className="right-7 group-data-[collapsible=icon]:!hidden"
        title="Delete"
        aria-label={`Delete ${shelf.name}`}
        onClick={(e) => {
          e.preventDefault()
          e.stopPropagation()
          onDelete()
        }}
      >
        <Icon name="close" size={11} />
      </SidebarMenuAction>
    </SidebarMenuItem>
  )
}

type UserBadgeProps = {
  user: AuthUser | null
  onLogout: () => void
  loggingOut: boolean
}

// UserBadge is a single dropdown trigger wrapping the user row (avatar +
// name/email) in the sidebar footer. The menu exposes "Account" (opens
// the per-user settings dialog) and "Sign out". No separate /settings
// route — preferences live entirely in the dialog now.
function UserBadge({ user, onLogout, loggingOut }: UserBadgeProps) {
  const { open: openUserSettings } = useUserSettingsDialog()
  // Skip rendering until /me resolves — the beforeLoad guard in _app.tsx
  // ensures a session exists by the time this component mounts, so the
  // null window is brief and avoids flashing fake identity details.
  if (!user) return null
  const { display, email, role, initials } = user

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-left group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-0 hover:bg-(--color-paper-3) focus-visible:ring-2 focus-visible:ring-(--color-accent) focus-visible:outline-none"
          aria-label="Account menu"
        >
          <Avatar size="sm">
            <AvatarFallback className="bg-(--color-editorial-accent) font-serif font-medium text-(--color-paper-0)">
              {initials}
            </AvatarFallback>
          </Avatar>
          <div className="flex-1 overflow-hidden group-data-[collapsible=icon]:hidden">
            <div className="truncate text-[13px] leading-tight font-medium">
              {display}
            </div>
            <div className="t-micro truncate text-[10px]">{email || role}</div>
          </div>
          <Icon
            name="more"
            size={14}
            aria-hidden
            className="shrink-0 text-(--color-ink-3) group-data-[collapsible=icon]:hidden"
          />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        side="right"
        align="end"
        sideOffset={8}
        className="min-w-56"
      >
        <DropdownMenuLabel>
          <div className="flex items-center gap-2.5">
            <Avatar size="sm">
              <AvatarFallback className="bg-(--color-editorial-accent) font-serif font-medium text-(--color-paper-0)">
                {initials}
              </AvatarFallback>
            </Avatar>
            <div className="min-w-0 flex-1">
              <div className="truncate text-[13px] leading-tight font-medium text-(--color-ink-1)">
                {display}
              </div>
              <div className="t-micro truncate text-[10px]">
                {email || role}
              </div>
            </div>
          </div>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => openUserSettings("account")}>
          <Icon name="user" size={13} />
          Account
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => openUserSettings("reading")}>
          <Icon name="book-open" size={13} />
          Reading preferences
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => openUserSettings("devices")}>
          <Icon name="device" size={13} />
          Device sync
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          variant="destructive"
          disabled={loggingOut}
          onSelect={() => onLogout()}
        >
          <Icon name="arrow-right" size={13} />
          {loggingOut ? "Signing out…" : "Sign out"}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

type NavItemProps = {
  to: string
  search?: Record<string, string | undefined>
  icon?: IconName
  label: string
  count?: number
  active?: boolean
}

function NavItem({
  to,
  search,
  icon,
  label,
  count,
  active,
}: NavItemProps) {
  return (
    <SidebarMenuItem>
      <SidebarMenuButton asChild isActive={active} tooltip={label}>
        <Link to={to} search={search}>
          {icon && <Icon name={icon} size={15} />}
          <NavLabel>{label}</NavLabel>
        </Link>
      </SidebarMenuButton>
      {count != null && <SidebarMenuBadge className="group-data-[collapsible=icon]:!hidden">{count}</SidebarMenuBadge>}
    </SidebarMenuItem>
  )
}

function NavLabel({ children }: { children: ReactNode }) {
  return <span>{children}</span>
}
