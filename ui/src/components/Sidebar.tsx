import { useState } from "react"
import { Link, useRouterState } from "@tanstack/react-router"

import { Icon } from "./Icon"
import { RuleEditor } from "./RuleEditor"
import { ShelfIcon } from "./ShelfIcon"
import type { ShelfAccent } from "./AccentPicker"
import type { IconName } from "./Icon"
import type { ReactNode } from "react"

import type { AuthUser } from "@/api/auth"
import type { Shelf } from "@/api/books"
import {
  createSmartShelf,
  deleteShelf,
  librariesQuery,
  publishShelf,
  shelvesQuery,
  updateShelf,
} from "@/api/books"
import { useApiMutation } from "@/api/mutation"
import { meQuery } from "@/api/auth"
import { useApiQuery } from "@/api/query"
import { viewerOf } from "@/lib/affordance"
import { useLogout } from "@/hooks/useLogout"
import { useShelfDraftDialog } from "@/components/ShelfDraftProvider"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
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

// AppSidebar is mounted once inside the authed shell. Every nav target is a
// real TanStack Router <Link> so browser back/forward, right-click-open, and
// deep-link all behave correctly.
export function AppSidebar() {
  const state = useRouterState()
  const pathname = state.location.pathname
  const search = state.location.search as {
    shelf?: string
    library?: string
    unshelved?: string
  }

  const me = useApiQuery(meQuery)
  const libraries = useApiQuery(librariesQuery)
  const shelves = useApiQuery(shelvesQuery)
  const logoutMut = useLogout()
  const shelfDraft = useShelfDraftDialog()

  // Smart shelves keep using the RuleEditor, extended with the
  // same accent picker so both shelf types share one design language.
  // Inline, both: the rule editor renders whichever of the two refused
  // (`smartMutError` below) and stays open on failure, so the failed rule
  // is read next to the rule that caused it.
  const createSmartMut = useApiMutation(createSmartShelf, {
    reportErrors: "inline",
    onSuccess: () => setSmartDraft(null),
  })
  const updateSmartMut = useApiMutation(updateShelf, {
    reportErrors: "inline",
    onSuccess: () => setSmartDraft(null),
  })
  const deleteShelfMut = useApiMutation(deleteShelf)
  // Admin-only "share" toggle. SSE broadcast hits other viewers via
  // useRealtime; the colocated invalidates list on publishShelf covers
  // this local viewer.
  const publishShelfMut = useApiMutation(publishShelf)

  const [smartDraft, setSmartDraft] = useState<
    { mode: "create" } | { mode: "edit"; shelf: Shelf } | null
  >(null)

  const libs = libraries.data ?? []
  const allShelves = shelves.data?.shelves ?? []
  const unshelvedCount = shelves.data?.unshelvedCount ?? 0
  const { isAdmin } = viewerOf(me.data)
  // "reading" is promoted into the Browse section as a first-class nav item,
  // so drop it from the Shelves list to avoid rendering two links to the
  // same filtered view. Public shelves (own or otherwise) split off into
  // their own SHARED group below.
  const ownPrivateShelves = allShelves.filter(
    (s) => !s.isSmart && !s.isPublic && s.slug !== "reading"
  )
  const ownSharedShelves = allShelves.filter(
    (s) => !s.isSmart && s.isPublic && (s.ownerName ?? "") === ""
  )
  const otherSharedShelves = allShelves.filter(
    (s) => !s.isSmart && s.isPublic && (s.ownerName ?? "") !== ""
  )
  const sharedList = [...ownSharedShelves, ...otherSharedShelves]
  const shelfList = ownPrivateShelves
  const smartList = allShelves.filter((s) => s.isSmart)
  const totalBooks = libs.reduce((n, lib) => n + lib.bookCount, 0)

  const smartMutError = createSmartMut.error ?? updateSmartMut.error

  const isLibrary = pathname.startsWith("/library")
  const activeShelf = isLibrary ? (search.shelf ?? null) : null
  const activeLibrary = isLibrary ? (search.library ?? null) : null
  const isUnshelved = isLibrary && search.unshelved === "1"

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
                active={
                  isLibrary && !activeShelf && !activeLibrary && !isUnshelved
                }
              />
              <NavItem
                to="/library"
                search={{ unshelved: "1" }}
                icon="inbox"
                label="Unshelved"
                count={unshelvedCount || undefined}
                active={isUnshelved}
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
          <SidebarGroupAction
            className="group-data-[collapsible=icon]:!hidden"
            title="New shelf"
            aria-label="New shelf"
            onClick={() => shelfDraft.open()}
          >
            <Icon name="plus" size={12} />
          </SidebarGroupAction>
          <SidebarGroupContent>
            <SidebarMenu>
              {shelfList.map((s) => (
                <RegularShelfRow
                  key={s.id}
                  shelf={s}
                  active={activeShelf === s.slug}
                  canShare={isAdmin}
                  onShare={() =>
                    publishShelfMut.mutate({ slug: s.slug, isPublic: true })
                  }
                />
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>

        {sharedList.length > 0 && (
          <SidebarGroup>
            <SidebarGroupLabel>Shared</SidebarGroupLabel>
            <SidebarGroupContent>
              <SidebarMenu>
                {sharedList.map((s) => (
                  <SharedShelfRow
                    key={s.id}
                    shelf={s}
                    active={activeShelf === s.slug}
                    canUnshare={isAdmin && (s.ownerName ?? "") === ""}
                    onUnshare={() =>
                      publishShelfMut.mutate({
                        slug: s.slug,
                        isPublic: false,
                      })
                    }
                  />
                ))}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        )}

        <SidebarGroup>
          <SidebarGroupLabel>Magic Shelves</SidebarGroupLabel>
          <SidebarGroupAction
            className="group-data-[collapsible=icon]:!hidden"
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
        {isAdmin && (
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
          initialIcon={
            smartDraft.mode === "edit" ? smartDraft.shelf.icon : "sparkles"
          }
          busy={createSmartMut.isPending || updateSmartMut.isPending}
          error={smartMutError?.message ?? null}
          onSubmit={({ name, rule, accent, icon }) => {
            if (smartDraft.mode === "create") {
              createSmartMut.mutate({ name, rule, accent, icon })
            } else {
              updateSmartMut.mutate({
                slug: smartDraft.shelf.slug,
                body: { name, accent, icon, rule, ruleSet: true },
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

// RegularShelfRow is the private regular-shelf surface. Admins get a
// hover-revealed "share" affordance that flips is_public; non-admins
// see no extra controls.
function RegularShelfRow({
  shelf,
  active,
  canShare,
  onShare,
}: {
  shelf: Shelf
  active: boolean
  canShare: boolean
  onShare: () => void
}) {
  return (
    <SidebarMenuItem>
      <SidebarMenuButton asChild isActive={active} tooltip={shelf.name}>
        <Link to="/library" search={{ shelf: shelf.slug }}>
          <ShelfIcon name={shelf.icon} size={15} />
          <span>{shelf.name}</span>
        </Link>
      </SidebarMenuButton>
      <SidebarMenuBadge className="group-focus-within/menu-item:hidden group-hover/menu-item:hidden group-data-[collapsible=icon]:!hidden">
        {shelf.bookCount}
      </SidebarMenuBadge>
      {canShare && (
        <SidebarMenuAction
          showOnHover
          className="group-data-[collapsible=icon]:!hidden"
          title="Share with all users"
          aria-label={`Share ${shelf.name} with all users`}
          onClick={(e) => {
            e.preventDefault()
            e.stopPropagation()
            onShare()
          }}
        >
          <Icon name="upload" size={11} />
        </SidebarMenuAction>
      )}
    </SidebarMenuItem>
  )
}

// SharedShelfRow renders a public shelf in the SHARED section. Owner
// admins see an "unshare" affordance; everyone else sees a read-only
// row with the owner's name in the tooltip.
function SharedShelfRow({
  shelf,
  active,
  canUnshare,
  onUnshare,
}: {
  shelf: Shelf
  active: boolean
  canUnshare: boolean
  onUnshare: () => void
}) {
  const tooltip =
    (shelf.ownerName ?? "") !== ""
      ? `${shelf.name}, shared by ${shelf.ownerName}`
      : `${shelf.name}, shared with everyone`
  return (
    <SidebarMenuItem>
      <SidebarMenuButton asChild isActive={active} tooltip={tooltip}>
        <Link to="/library" search={{ shelf: shelf.slug }}>
          <ShelfIcon
            name={shelf.icon}
            size={15}
            aria-label="shared shelf"
          />
          <span>{shelf.name}</span>
        </Link>
      </SidebarMenuButton>
      <SidebarMenuBadge className="group-focus-within/menu-item:hidden group-hover/menu-item:hidden group-data-[collapsible=icon]:!hidden">
        {shelf.bookCount}
      </SidebarMenuBadge>
      {canUnshare && (
        <SidebarMenuAction
          showOnHover
          className="group-data-[collapsible=icon]:!hidden"
          title="Stop sharing"
          aria-label={`Stop sharing ${shelf.name}`}
          onClick={(e) => {
            e.preventDefault()
            e.stopPropagation()
            onUnshare()
          }}
        >
          <Icon name="close" size={11} />
        </SidebarMenuAction>
      )}
    </SidebarMenuItem>
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
          <ShelfIcon name={shelf.icon} size={13} aria-label="smart shelf" />
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

// UserBadge renders two adjacent footer affordances: the user row
// (avatar + name/email) is a Link to /account, and a separate icon
// button signs the user out. Per-user preferences live on the /account
// route; admin-wide settings remain at /settings.
function UserBadge({ user, onLogout, loggingOut }: UserBadgeProps) {
  // Skip rendering until /me resolves — the beforeLoad guard in _app.tsx
  // ensures a session exists by the time this component mounts, so the
  // null window is brief and avoids flashing fake identity details.
  if (!user) return null
  const { display, email, role, initials } = user

  return (
    <div className="flex w-full items-center gap-1 group-data-[collapsible=icon]:flex-col group-data-[collapsible=icon]:gap-1">
      <Link
        to="/account"
        aria-label="My account"
        title="My account"
        className="flex min-w-0 flex-1 items-center gap-2.5 rounded-md px-2 py-1.5 text-left group-data-[collapsible=icon]:flex-none group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-0 hover:bg-(--color-paper-3) focus-visible:ring-2 focus-visible:ring-(--color-accent) focus-visible:outline-none"
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
      </Link>
      <button
        type="button"
        onClick={() => onLogout()}
        disabled={loggingOut}
        aria-label={loggingOut ? "Signing out" : "Sign out"}
        title={loggingOut ? "Signing out…" : "Sign out"}
        className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-(--color-ink-3) hover:bg-(--color-paper-3) hover:text-(--color-ink-1) focus-visible:ring-2 focus-visible:ring-(--color-accent) focus-visible:outline-none disabled:opacity-50"
      >
        <Icon name="arrow-right" size={14} />
      </button>
    </div>
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

function NavItem({ to, search, icon, label, count, active }: NavItemProps) {
  return (
    <SidebarMenuItem>
      <SidebarMenuButton asChild isActive={active} tooltip={label}>
        <Link to={to} search={search}>
          {icon && <Icon name={icon} size={15} />}
          <NavLabel>{label}</NavLabel>
        </Link>
      </SidebarMenuButton>
      {count != null && (
        <SidebarMenuBadge className="group-data-[collapsible=icon]:!hidden">
          {count}
        </SidebarMenuBadge>
      )}
    </SidebarMenuItem>
  )
}

function NavLabel({ children }: { children: ReactNode }) {
  return <span>{children}</span>
}
