// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import type { SettingsSectionKey } from "../settings/sections"
import { SETTINGS_SECTIONS } from "../settings/sections"
import { SettingsShell } from "../SettingsShared"

// Every panel is stubbed with a probe that shouts its own name when it
// mounts. The point of these tests is *whether* a panel mounts, not what
// it renders — and a stub has no queries, so "the panel never mounted"
// and "the panel never fetched" are the same assertion.
function probe(name: string) {
  return () => <div data-testid="mounted-panel">{name}</div>
}

vi.mock("@/components/settings/LibrariesPanel", () => ({
  LibrariesPanel: probe("LibrariesPanel"),
}))
vi.mock("@/components/settings/BookDropPanel", () => ({
  BookDropPanel: probe("BookDropPanel"),
}))
vi.mock("@/components/settings/ProvidersPanel", () => ({
  ProvidersPanel: probe("ProvidersPanel"),
}))
vi.mock("@/components/settings/ReadingGuidesPanel", () => ({
  ReadingGuidesPanel: probe("ReadingGuidesPanel"),
}))
vi.mock("@/components/settings/AudiobooksPanel", () => ({
  AudiobooksPanel: probe("AudiobooksPanel"),
}))
vi.mock("@/components/settings/EmailPanel", () => ({
  EmailPanel: probe("EmailPanel"),
}))
vi.mock("@/components/settings/ConverterPanel", () => ({
  ConverterPanel: probe("ConverterPanel"),
}))
vi.mock("@/components/settings/InvitesPanel", () => ({
  InvitesPanel: probe("InvitesPanel"),
}))
vi.mock("@/components/settings/UsersPanel", () => ({
  UsersPanel: probe("UsersPanel"),
}))
vi.mock("@/components/settings/OidcPanel", () => ({
  OidcPanel: probe("OidcPanel"),
}))
vi.mock("@/components/settings/ForwardAuthPanel", () => ({
  ForwardAuthPanel: probe("ForwardAuthPanel"),
}))
vi.mock("@/components/settings/InstancePanel", () => ({
  InstancePanel: probe("InstancePanel"),
}))

// Restated by hand rather than derived from SETTINGS_SECTIONS, so a
// section wired to the wrong panel fails here instead of agreeing with
// itself. Exhaustive by type: adding a section key breaks compilation
// until this map grows a row.
const EXPECTED_PANEL: Record<SettingsSectionKey, string> = {
  libraries: "LibrariesPanel",
  bookdrop: "BookDropPanel",
  providers: "ProvidersPanel",
  readingGuides: "ReadingGuidesPanel",
  audiobooks: "AudiobooksPanel",
  email: "EmailPanel",
  converter: "ConverterPanel",
  invites: "InvitesPanel",
  users: "UsersPanel",
  oidc: "OidcPanel",
  forwardAuth: "ForwardAuthPanel",
  instance: "InstancePanel",
}

function renderSettings(active: SettingsSectionKey, isAdmin: boolean) {
  return render(
    <SettingsShell
      sections={SETTINGS_SECTIONS}
      active={active}
      onSelect={() => {}}
      isAdmin={isAdmin}
    />
  )
}

describe("SettingsShell · the admin gate for /settings", () => {
  afterEach(cleanup)

  it("covers every section of the real settings table", () => {
    // Guards the loops below: if a section is added to the table and not
    // to EXPECTED_PANEL, the cases stop being exhaustive silently.
    expect(SETTINGS_SECTIONS.map((s) => s.key).sort()).toEqual(
      Object.keys(EXPECTED_PANEL).sort()
    )
  })

  for (const section of SETTINGS_SECTIONS) {
    it(`mounts the ${section.key} panel for an admin`, () => {
      renderSettings(section.key, true)
      expect(screen.getByTestId("mounted-panel").textContent).toBe(
        EXPECTED_PANEL[section.key]
      )
      expect(screen.queryByText(/admin-only/i)).toBeNull()
    })

    it(`refuses to mount the ${section.key} panel for a non-admin`, () => {
      renderSettings(section.key, false)
      // The regression this pins: About used to gate only its query, so a
      // non-admin got the panel's chrome with every value blank and no
      // explanation. No panel mounts now, and every gated section says so.
      expect(screen.queryByTestId("mounted-panel")).toBeNull()
      expect(screen.getByText(/admin-only/i)).toBeTruthy()
      expect(
        screen.getAllByRole("heading", { name: section.label })
      ).toHaveLength(1)
    })

    it(`disables the ${section.key} nav entry for a non-admin`, () => {
      renderSettings(section.key, false)
      const nav = screen.getByRole("button", {
        name: section.label,
      }) as HTMLButtonElement
      expect(nav.disabled).toBe(true)
    })
  }
})

describe("SettingsShell · the gate itself", () => {
  afterEach(cleanup)

  const openSections = [
    {
      key: "open" as const,
      label: "Open",
      render: () => <div>open panel</div>,
    },
    {
      key: "closed" as const,
      label: "Closed",
      adminOnly: true,
      render: () => <div>closed panel</div>,
    },
  ]

  it("mounts a section without adminOnly for a non-admin", () => {
    render(
      <SettingsShell
        sections={openSections}
        active="open"
        onSelect={() => {}}
        isAdmin={false}
      />
    )
    expect(screen.getByText("open panel")).toBeTruthy()
  })

  it("fails closed when the caller passes no admin flag at all", () => {
    render(
      <SettingsShell
        sections={openSections}
        active="closed"
        onSelect={() => {}}
      />
    )
    expect(screen.queryByText("closed panel")).toBeNull()
    expect(screen.getByText(/admin-only/i)).toBeTruthy()
  })
})
