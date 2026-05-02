import { api } from "./client"
import type { AuthUser } from "./auth"
import type { Library } from "./books"

// SettingsLibrary mirrors the server's admin shape. Path is inline
// because a library owns exactly one filesystem root, fixed at
// creation.
export type SettingsLibrary = Library & {
  path: string
  lastScannedAt: string | null
  fileCount: number
  discoveredCount: number
}

export async function fetchSettingsLibraries(): Promise<
  Array<SettingsLibrary>
> {
  const { libraries } = await api<{ libraries: Array<SettingsLibrary> }>(
    "/api/v1/settings/libraries"
  )
  return libraries
}

export type LibraryKind = "local" | "s3"

export type CreateLibraryInput = {
  name: string
  kind: LibraryKind // 'local' | 's3'
  scan?: boolean
}

export async function createLibrary(
  input: CreateLibraryInput
): Promise<SettingsLibrary> {
  const { name, kind, scan } = input
  const { library } = await api<{ library: SettingsLibrary }>(
    "/api/v1/settings/libraries",
    {
      method: "POST",
      body: JSON.stringify({ name, kind, scan }),
    }
  )
  return library
}

// deleteLibrary tears down a library and every book/annotation/etc
// that depends on it. Source files on disk are left alone (they live
// under the user-managed root); cover images and DB rows are removed.
// When opts.purge is true and the library is backed by an S3 backend,
// all objects under the library's prefix are also deleted.
export async function deleteLibrary(
  id: string,
  opts?: { purge?: boolean }
): Promise<void> {
  const qs = opts?.purge ? "?purge=true" : ""
  await api<void>(`/api/v1/settings/libraries/${id}${qs}`, {
    method: "DELETE",
  })
}

// rescanLibrary enqueues a library.scan job against the library's
// filesystem root. The response is fire-and-forget (202).
export async function rescanLibrary(id: string): Promise<void> {
  await api<void>(`/api/v1/settings/libraries/${id}/rescan`, {
    method: "POST",
  })
}

export const settingsLibrariesQueryKey = ["settings", "libraries"] as const

// AppConfig is the lightweight feature-flag payload from GET /api/v1/config.
export type AppConfig = {
  s3Available: boolean
}

export async function fetchAppConfig(): Promise<AppConfig> {
  return api<AppConfig>("/api/v1/config")
}

export const appConfigQueryKey = ["app", "config"] as const

// --- Instance info ---------------------------------------------------------

// ProviderConfigField mirrors internal/handler/instance.go
// providerConfigFieldDTO. Kind drives the input renderer; options
// populate selects.
export type ProviderConfigField = {
  key: string
  label: string
  kind: "text" | "password" | "select" | "textarea"
  placeholder?: string
  help?: string
  options?: Array<{ value: string; label: string }>
}

export type ProviderInfo = {
  id: string
  name: string
  enabled: boolean
  external: boolean
  priority?: number
  // Stored provider-specific config blob. Keys align with `schema`.
  config?: Record<string, unknown>
  // Declared form fields — absent when the provider has no config.
  schema?: Array<ProviderConfigField>
  // Health telemetry — RFC3339 timestamps (or empty) set by the
  // enrichment service on each Search call.
  lastSuccessAt?: string
  lastErrorAt?: string
  lastError?: string
}

// Patch payload for PATCH /settings/providers/:id. Any subset of the
// axes may be supplied; the server requires at least one.
// priorityClear distinguishes "leave alone" from "reset to unranked".
export type ProviderPatch = {
  enabled?: boolean
  config?: Record<string, unknown>
  priority?: number
  priorityClear?: boolean
}

export type InstanceInfo = {
  version: string
  goVersion: string
  allowedOrigins: Array<string>
  bookDropPath: string
  dataPath: string
  migrateOnStart: boolean
  enrichmentProviders: Array<ProviderInfo>
  counts: { users: number; libraries: number; books: number }
}

export async function fetchInstanceInfo(): Promise<InstanceInfo> {
  return api<InstanceInfo>("/api/v1/settings/instance")
}

export const instanceInfoQueryKey = ["settings", "instance"] as const

// --- Metadata providers (admin) --------------------------------------------

export async function fetchProviderSettings(): Promise<Array<ProviderInfo>> {
  const { providers } = await api<{ providers: Array<ProviderInfo> }>(
    "/api/v1/settings/providers"
  )
  return providers
}

export async function updateProviderSetting(
  id: string,
  patch: ProviderPatch
): Promise<Array<ProviderInfo>> {
  const { providers } = await api<{ providers: Array<ProviderInfo> }>(
    `/api/v1/settings/providers/${encodeURIComponent(id)}`,
    {
      method: "PATCH",
      body: JSON.stringify(patch),
    }
  )
  return providers
}

export const providerSettingsQueryKey = ["settings", "providers"] as const

// --- Instance-wide metadata switches --------------------------------------

export type MetadataSettings = {
  // When true, bookdrop approvals trigger an enrichment pass so the
  // imported book lands with provider metadata already applied.
  autoEnrich: boolean
}

export async function fetchMetadataSettings(): Promise<MetadataSettings> {
  return api<MetadataSettings>("/api/v1/settings/metadata")
}

export async function updateMetadataSettings(
  body: MetadataSettings
): Promise<MetadataSettings> {
  return api<MetadataSettings>("/api/v1/settings/metadata", {
    method: "PUT",
    body: JSON.stringify(body),
  })
}

export const metadataSettingsQueryKey = ["settings", "metadata"] as const

// Lightweight, non-admin-gated version of InstanceInfo. Rendered in the
// status bar at the bottom of every page, so all signed-in users can call
// it — mirrors /api/v1/instance on the server.
export type InstanceSummary = {
  version: string
  libraries: number
  books: number
}

export async function fetchInstanceSummary(): Promise<InstanceSummary> {
  return api<InstanceSummary>("/api/v1/instance")
}

export const instanceSummaryQueryKey = ["instance", "summary"] as const

// --- Users (admin) ---------------------------------------------------------

export async function fetchSettingsUsers(): Promise<Array<AuthUser>> {
  const { users } = await api<{ users: Array<AuthUser> }>(
    "/api/v1/settings/users"
  )
  return users
}

export async function createSettingsUser(body: {
  email: string
  name: string
  password: string
  role: "admin" | "user"
}): Promise<AuthUser> {
  const { user } = await api<{ user: AuthUser }>("/api/v1/settings/users", {
    method: "POST",
    body: JSON.stringify(body),
  })
  return user
}

export async function updateSettingsUserRole(
  id: string,
  role: "admin" | "user"
): Promise<void> {
  await api<void>(`/api/v1/settings/users/${id}/role`, {
    method: "PATCH",
    body: JSON.stringify({ role }),
  })
}

export async function deleteSettingsUser(id: string): Promise<void> {
  await api<void>(`/api/v1/settings/users/${id}`, { method: "DELETE" })
}

export async function approveSettingsUser(id: string): Promise<AuthUser> {
  const { user } = await api<{ user: AuthUser }>(
    `/api/v1/settings/users/${id}/approve`,
    { method: "POST" }
  )
  return user
}

export async function denySettingsUser(id: string): Promise<AuthUser> {
  const { user } = await api<{ user: AuthUser }>(
    `/api/v1/settings/users/${id}/deny`,
    { method: "POST" }
  )
  return user
}

export const settingsUsersQueryKey = ["settings", "users"] as const
