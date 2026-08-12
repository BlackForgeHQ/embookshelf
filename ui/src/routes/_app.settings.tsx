import { useMemo, useState } from "react"
import { createFileRoute } from "@tanstack/react-router"

import type { SettingsSectionKey } from "@/components/settings/sections"
import { SETTINGS_SECTIONS } from "@/components/settings/sections"
import { settingsUsersQuery } from "@/api/settings"
import { useApiQuery } from "@/api/query"
import { useViewer } from "@/lib/affordance"
import { SettingsShell } from "@/components/SettingsShared"
import { TopBar } from "@/components/TopBar"

export const Route = createFileRoute("/_app/settings")({
  component: Admin,
})

function Admin() {
  const { isAdmin } = useViewer()
  const [active, setActive] = useState<SettingsSectionKey>("libraries")

  const usersQuery = useApiQuery(settingsUsersQuery, { enabled: isAdmin })

  const pendingCount = useMemo(
    () => (usersQuery.data ?? []).filter((u) => u.status === "pending").length,
    [usersQuery.data]
  )

  // The badge is the one part of a section that the route knows and the
  // table can't: it counts pending users. Everything else — label, gate,
  // panel — comes from the table untouched.
  const sections = useMemo(
    () =>
      SETTINGS_SECTIONS.map((s) =>
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
      />
    </div>
  )
}

function PendingBadge({ count }: { count: number }) {
  return (
    <span
      data-testid="users-tab-badge"
      className="px-1.5 py-0.5 rounded-full bg-(--color-warn-soft) text-(--color-warn-ink) text-[10px] font-semibold leading-relaxed"
    >
      {count}
    </span>
  )
}
