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


export function BackupsPanel({ isAdmin }: { isAdmin: boolean }) {
  if (!isAdmin) return <AdminGate label="Backups" />
  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 8 }}>
        Backups
      </h2>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: "italic" }}>
        The on-disk data directory and the PostgreSQL volume hold every durable
        piece of state. Back them up together.
      </p>

      <Card>
        <DefRow
          label="Database"
          value={
            <>
              <span className="mono">pg_dump embookshelf</span> — ship to your
              usual blob store on a cron.
            </>
          }
        />
        <DefRow
          label="Book files"
          value={<span className="mono">library paths</span>}
        />
        <DefRow
          label="Covers + BookDrop queue"
          value={<span className="mono">$DATA_PATH</span>}
        />
      </Card>

      <p className="t-small" style={{ marginTop: 16, fontStyle: "italic" }}>
        A scheduled-backups surface will land here once the job runner gains an
        "export" task.
      </p>
    </>
  )
}

// ---------------------------------------------------------------------------
// About
// ---------------------------------------------------------------------------

