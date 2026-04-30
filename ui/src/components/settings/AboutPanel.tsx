import { useEffect, useMemo, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { toast } from "sonner"
import type { CSSProperties, ReactNode } from "react"

import type { ApiError } from "@/api/client"
import type {
  LibraryKind,
  MetadataSettings,
  ProviderConfigField,
  ProviderInfo,
  ProviderPatch,
  SettingsLibrary,
} from "@/api/settings"
import type {
  OidcAdminSettings,
  OidcTestCheck,
  OidcTestResult,
  ProviderSlug,
} from "@/api/oidc"
import type { AuthUser } from "@/api/auth"
import {
  appConfigQueryKey,
  approveSettingsUser,
  createLibrary,
  createSettingsUser,
  deleteLibrary,
  deleteSettingsUser,
  denySettingsUser,
  fetchAppConfig,
  fetchInstanceInfo,
  fetchMetadataSettings,
  fetchProviderSettings,
  fetchSettingsLibraries,
  fetchSettingsUsers,
  instanceInfoQueryKey,
  metadataSettingsQueryKey,
  prescanLibraryPaths,
  providerSettingsQueryKey,
  rescanLibrary,
  settingsLibrariesQueryKey,
  settingsUsersQueryKey,
  updateMetadataSettings,
  updateProviderSetting,
  updateSettingsUserRole,
} from "@/api/settings"
import {
  fetchOidcAdminSettings,
  oidcAdminSettingsQueryKey,
  saveOidcAdminSettings,
  testOidcProvider,
} from "@/api/oidc"
import { fetchMe, meQueryKey } from "@/api/auth"
import { Icon } from "@/components/Icon"
import {
  AdminGate,
  Avatar,
  Card,
  DefRow,
  Field,
  Select,
  SettingsShell,
} from "@/components/SettingsShared"
import { TopBar } from "@/components/TopBar"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"


export function AboutPanel({ isAdmin }: { isAdmin: boolean }) {
  const info = useQuery({
    queryKey: instanceInfoQueryKey,
    queryFn: fetchInstanceInfo,
    enabled: isAdmin,
  })

  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 24 }}>
        About
      </h2>

      <Card>
        <DefRow label="Product" value="embookshelf" />
        <DefRow
          label="Version"
          value={<span className="mono">{info.data?.version ?? "—"}</span>}
        />
        {isAdmin && (
          <>
            <DefRow
              label="Runtime"
              value={
                <span className="mono">{info.data?.goVersion ?? "—"}</span>
              }
            />
            <DefRow
              label="BookDrop path"
              value={
                <span className="mono">{info.data?.bookDropPath ?? "—"}</span>
              }
            />
            <DefRow
              label="Data path"
              value={<span className="mono">{info.data?.dataPath ?? "—"}</span>}
            />
            <DefRow
              label="Migrate on start"
              value={
                info.data ? (info.data.migrateOnStart ? "yes" : "no") : "—"
              }
            />
          </>
        )}
      </Card>

      {isAdmin && info.data && (
        <>
          <div className="t-label" style={{ marginTop: 24, marginBottom: 10 }}>
            Instance totals
          </div>
          <Card>
            <DefRow label="Users" value={info.data.counts.users} />
            <DefRow label="Libraries" value={info.data.counts.libraries} />
            <DefRow
              label="Books"
              value={info.data.counts.books.toLocaleString()}
            />
          </Card>
        </>
      )}

      <p className="t-small" style={{ marginTop: 24, fontStyle: "italic" }}>
        embookshelf — self-hosted ebook library. AGPL-3.0.
      </p>
    </>
  )
}
