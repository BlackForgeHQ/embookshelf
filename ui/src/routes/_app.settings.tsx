import { useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import type { ReactNode } from "react"

import { AboutPanel } from "@/components/settings/AboutPanel"
import { BackupsPanel } from "@/components/settings/BackupsPanel"
import { BookDropPanel } from "@/components/settings/BookDropPanel"
import { OidcPanel } from "@/components/settings/OidcPanel"
import { UsersPanel } from "@/components/settings/UsersPanel"
import { EmailPanel } from "@/components/settings/EmailPanel"
import { ProvidersPanel } from "@/components/settings/ProvidersPanel"
import { LibrariesPanel } from "@/components/settings/LibrariesPanel"
import {
  fetchSettingsUsers,
  settingsUsersQueryKey,
} from "@/api/settings"
import { fetchMe, meQueryKey } from "@/api/auth"
import {
  SettingsShell,
} from "@/components/SettingsShared"
import { TopBar } from "@/components/TopBar"

export const Route = createFileRoute("/_app/settings")({
  component: Admin,
})

type SectionKey =
  | "libraries"
  | "bookdrop"
  | "providers"
  | "email"
  | "users"
  | "oidc"
  | "backups"
  | "about"

type SectionSpec = {
  key: SectionKey
  label: string
  adminOnly?: boolean
  badge?: ReactNode
}

const SECTIONS: Array<SectionSpec> = [
  { key: "libraries", label: "Libraries", adminOnly: true },
  { key: "bookdrop", label: "BookDrop", adminOnly: true },
  { key: "providers", label: "Metadata providers", adminOnly: true },
  { key: "email", label: "Email delivery", adminOnly: true },
  { key: "users", label: "Users & roles", adminOnly: true },
  { key: "oidc", label: "OIDC / SSO", adminOnly: true },
  { key: "backups", label: "Backups", adminOnly: true },
  { key: "about", label: "About", adminOnly: true },
]

function Admin() {
  const me = useQuery({
    queryKey: meQueryKey,
    queryFn: fetchMe,
    staleTime: 60_000,
  })
  const isAdmin = me.data?.role === "admin"
  const [active, setActive] = useState<SectionKey>("libraries")

  const usersQuery = useQuery({
    queryKey: settingsUsersQueryKey,
    queryFn: fetchSettingsUsers,
    enabled: isAdmin,
  })

  const pendingCount = useMemo(
    () =>
      (usersQuery.data ?? []).filter((u) => u.status === "pending").length,
    [usersQuery.data]
  )

  const sections = useMemo<Array<SectionSpec>>(
    () =>
      SECTIONS.map((s) =>
        s.key === "users" && pendingCount > 0
          ? { ...s, badge: <PendingBadge count={pendingCount} /> }
          : s
      ),
    [pendingCount]
  )

  return (
    <div className="fade-in">
      <TopBar
        title="Settings"
        subtitle="Instance, users, metadata providers, SSO."
      />
      <SettingsShell
        sections={sections}
        active={active}
        onSelect={setActive}
        isAdmin={isAdmin}
      >
        {active === "libraries" && <LibrariesPanel isAdmin={isAdmin} />}
        {active === "bookdrop" && <BookDropPanel isAdmin={isAdmin} />}
        {active === "providers" && <ProvidersPanel isAdmin={isAdmin} />}
        {active === "email" && <EmailPanel isAdmin={isAdmin} />}
        {active === "users" && (
          <UsersPanel isAdmin={isAdmin} me={me.data ?? null} />
        )}
        {active === "oidc" && <OidcPanel isAdmin={isAdmin} />}
        {active === "backups" && <BackupsPanel isAdmin={isAdmin} />}
        {active === "about" && <AboutPanel isAdmin={isAdmin} />}
      </SettingsShell>
    </div>
  )
}

function PendingBadge({ count }: { count: number }) {
  return (
    <span
      data-testid="users-tab-badge"
      className="px-1.5 py-0.5 rounded-full bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400 text-[10px] font-semibold leading-relaxed"
    >
      {count}
    </span>
  )
}
