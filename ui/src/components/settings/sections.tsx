import type { SettingsSection } from "@/components/SettingsShared"

import { AudiobooksPanel } from "@/components/settings/AudiobooksPanel"
import { BookDropPanel } from "@/components/settings/BookDropPanel"
import { ConverterPanel } from "@/components/settings/ConverterPanel"
import { EmailPanel } from "@/components/settings/EmailPanel"
import { ForwardAuthPanel } from "@/components/settings/ForwardAuthPanel"
import { InstancePanel } from "@/components/settings/InstancePanel"
import { InvitesPanel } from "@/components/settings/InvitesPanel"
import { LibrariesPanel } from "@/components/settings/LibrariesPanel"
import { OidcPanel } from "@/components/settings/OidcPanel"
import { ProvidersPanel } from "@/components/settings/ProvidersPanel"
import { ReadingGuidesPanel } from "@/components/settings/ReadingGuidesPanel"
import { UsersPanel } from "@/components/settings/UsersPanel"

export type SettingsSectionKey =
  | "libraries"
  | "bookdrop"
  | "providers"
  | "readingGuides"
  | "audiobooks"
  | "email"
  | "converter"
  | "invites"
  | "users"
  | "oidc"
  | "forwardAuth"
  | "instance"

// The whole of /settings as data: label, gate, panel — one row per
// section, in nav order. The route used to hold half of this (the labels
// and the adminOnly flags) and spell the other half out again as a chain
// of `active === key && <Panel isAdmin={isAdmin} />`, which is what let a
// panel disagree with its own row. Here the gate and the panel sit in the
// same row, and SettingsShell reads both.
export const SETTINGS_SECTIONS: ReadonlyArray<
  SettingsSection<SettingsSectionKey>
> = [
  {
    key: "libraries",
    label: "Libraries",
    adminOnly: true,
    render: () => <LibrariesPanel />,
  },
  {
    key: "bookdrop",
    label: "BookDrop",
    adminOnly: true,
    render: () => <BookDropPanel />,
  },
  {
    key: "providers",
    label: "Metadata providers",
    adminOnly: true,
    render: () => <ProvidersPanel />,
  },
  {
    key: "readingGuides",
    label: "Reading guides",
    adminOnly: true,
    render: () => <ReadingGuidesPanel />,
  },
  {
    key: "audiobooks",
    label: "Audiobooks",
    adminOnly: true,
    render: () => <AudiobooksPanel />,
  },
  {
    key: "email",
    label: "Email delivery",
    adminOnly: true,
    render: () => <EmailPanel />,
  },
  {
    key: "converter",
    label: "Converter",
    adminOnly: true,
    render: () => <ConverterPanel />,
  },
  {
    key: "invites",
    label: "Invites",
    adminOnly: true,
    render: () => <InvitesPanel />,
  },
  {
    key: "users",
    label: "Users & roles",
    adminOnly: true,
    render: () => <UsersPanel />,
  },
  {
    key: "oidc",
    label: "OIDC / SSO",
    adminOnly: true,
    render: () => <OidcPanel />,
  },
  {
    key: "forwardAuth",
    label: "Forward auth",
    adminOnly: true,
    render: () => <ForwardAuthPanel />,
  },
  {
    key: "instance",
    label: "Instance",
    adminOnly: true,
    render: () => <InstancePanel />,
  },
]
