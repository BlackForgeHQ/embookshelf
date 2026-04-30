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


export function EmailPanel({ isAdmin }: { isAdmin: boolean }) {
  if (!isAdmin) return <AdminGate label="Email delivery" />
  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 8 }}>
        Email delivery
      </h2>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: "italic" }}>
        SMTP is not yet wired. Send-to-Kindle and share-by-email will surface
        here once the transport is configured.
      </p>

      <Card>
        <DefRow label="Transport" value="—" />
        <DefRow label="From address" value="—" />
        <DefRow label="Send-to-Kindle" value="disabled" />
      </Card>

      <p className="t-small" style={{ marginTop: 16, fontStyle: "italic" }}>
        Configure via <span className="mono">SMTP_HOST</span>,{" "}
        <span className="mono">SMTP_USERNAME</span>, and related env vars on the
        server.
      </p>
    </>
  )
}

// ---------------------------------------------------------------------------
// Users & roles (admin CRUD)
// ---------------------------------------------------------------------------

function PendingBadge({ count }: { count: number }) {
  return (
    <span
      data-testid="users-tab-badge"
      style={{
        padding: "1px 6px",
        borderRadius: 999,
        background: "var(--color-amber-bg, #fff5cc)",
        color: "var(--color-amber-ink, #8a5b00)",
        fontSize: 10,
        fontWeight: 600,
        lineHeight: 1.6,
      }}
    >
      {count}
    </span>
  )
}

