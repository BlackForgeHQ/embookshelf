"use client"


import { AccountPanel } from "@/components/account/AccountPanel"
import { DevicesPanel } from "@/components/account/DevicesPanel"
import { McpAccessPanel } from "@/components/account/McpAccessPanel"
import { MyShelvesPanel } from "@/components/account/MyShelvesPanel"
import { ReadingPreferencesPanel } from "@/components/account/ReadingPreferencesPanel"
import type { SettingsSection } from "@/components/SettingsShared"
import { SettingsShell } from "@/components/SettingsShared"
import { TopBar } from "@/components/TopBar"
import type { AccountSection } from "@/lib/routes"
import { parseAccountSearch, paths } from "@/lib/routes"
import { useRouteSearch } from "@/hooks/useRoute"
import { useAppRouter } from "@/lib/navigation"

// No adminOnly rows: /account is per-user by definition, so the shell's
// gate never fires here and the route has no admin-ness to pass it.
const SECTIONS: ReadonlyArray<SettingsSection<AccountSection>> = [
  { key: "account", label: "Account", render: () => <AccountPanel /> },
  { key: "shelves", label: "My shelves", render: () => <MyShelvesPanel /> },
  {
    key: "reading",
    label: "Reading preferences",
    render: () => <ReadingPreferencesPanel />,
  },
  { key: "devices", label: "Device sync", render: () => <DevicesPanel /> },
  { key: "mcp", label: "MCP access", render: () => <McpAccessPanel /> },
]

export default function AccountPage() {
  const router = useAppRouter()
  const { section } = useRouteSearch(parseAccountSearch)
  const active: AccountSection = section ?? "account"

  return (
    <div className="fade-in">
      <TopBar
        title="My account"
        subtitle="Preferences scoped to this user account."
      />
      <SettingsShell
        sections={SECTIONS}
        active={active}
        onSelect={(key) =>
          router.push(
            paths.account(key === "account" ? {} : { section: key })
          )
        }
      />
    </div>
  )
}
