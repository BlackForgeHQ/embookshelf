import { createFileRoute, useNavigate } from "@tanstack/react-router"

import { AccountPanel } from "@/components/account/AccountPanel"
import { DevicesPanel } from "@/components/account/DevicesPanel"
import { MyShelvesPanel } from "@/components/account/MyShelvesPanel"
import { ReadingPreferencesPanel } from "@/components/account/ReadingPreferencesPanel"
import type { SettingsSection } from "@/components/SettingsShared"
import { SettingsShell } from "@/components/SettingsShared"
import { TopBar } from "@/components/TopBar"

type SectionKey = "account" | "reading" | "devices" | "shelves"

type AccountSearch = {
  section?: SectionKey
  // Redirect-back signals from the OIDC link callback. The panel
  // renders a toast and clears these via router.navigate so the URL
  // doesn't keep firing the toast on refresh.
  linked?: string
  error?: string
}

// No adminOnly rows: /account is per-user by definition, so the shell's
// gate never fires here and the route has no admin-ness to pass it.
const SECTIONS: ReadonlyArray<SettingsSection<SectionKey>> = [
  { key: "account", label: "Account", render: () => <AccountPanel /> },
  { key: "shelves", label: "My shelves", render: () => <MyShelvesPanel /> },
  {
    key: "reading",
    label: "Reading preferences",
    render: () => <ReadingPreferencesPanel />,
  },
  { key: "devices", label: "Device sync", render: () => <DevicesPanel /> },
]

function isSectionKey(v: unknown): v is SectionKey {
  return (
    v === "account" || v === "reading" || v === "devices" || v === "shelves"
  )
}

export const Route = createFileRoute("/_app/account")({
  validateSearch: (raw: Record<string, unknown>): AccountSearch => ({
    section: isSectionKey(raw.section) ? raw.section : undefined,
    linked: typeof raw.linked === "string" ? raw.linked : undefined,
    error: typeof raw.error === "string" ? raw.error : undefined,
  }),
  component: AccountPage,
})

function AccountPage() {
  const navigate = useNavigate()
  const { section } = Route.useSearch()
  const active: SectionKey = section ?? "account"

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
          navigate({
            to: "/account",
            search: key === "account" ? {} : { section: key },
          })
        }
      />
    </div>
  )
}
