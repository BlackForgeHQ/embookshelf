import { createFileRoute, useNavigate } from "@tanstack/react-router"

import { AccountPanel } from "@/components/account/AccountPanel"
import { DevicesPanel } from "@/components/account/DevicesPanel"
import { ReadingPreferencesPanel } from "@/components/account/ReadingPreferencesPanel"
import { SettingsShell } from "@/components/SettingsShared"
import { TopBar } from "@/components/TopBar"

type SectionKey = "account" | "reading" | "devices"

type AccountSearch = {
  section?: SectionKey
}

const SECTIONS: ReadonlyArray<{ key: SectionKey; label: string }> = [
  { key: "account", label: "Account" },
  { key: "reading", label: "Reading preferences" },
  { key: "devices", label: "Device sync" },
]

function isSectionKey(v: unknown): v is SectionKey {
  return v === "account" || v === "reading" || v === "devices"
}

export const Route = createFileRoute("/_app/account")({
  validateSearch: (raw: Record<string, unknown>): AccountSearch => ({
    section: isSectionKey(raw.section) ? raw.section : undefined,
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
        isAdmin
      >
        {active === "account" && <AccountPanel />}
        {active === "reading" && <ReadingPreferencesPanel />}
        {active === "devices" && <DevicesPanel />}
      </SettingsShell>
    </div>
  )
}
