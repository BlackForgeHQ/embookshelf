import { api } from "./client"
import { oidcConfigQueryKey } from "./auth"
import { defineMutation } from "./mutation"

export type ProviderSlug = "google" | "github" | "generic"

export type ClaimMapping = {
  username: string
  email: string
  name: string
  groups?: string
}

export type OAuthPreset = {
  enabled: boolean
  clientId: string
  clientSecret?: string
  clientSecretSet: boolean
}

export type GenericOidc = {
  enabled: boolean
  providerName: string
  clientId: string
  clientSecret?: string
  clientSecretSet: boolean
  issuerUri: string
  scopes: string
  claimMapping: ClaimMapping
}

export type OidcAutoProvision = {
  enableAutoProvisioning: boolean
  allowLocalAccountLinking: boolean
  defaultRole: "admin" | "user"
  requireAdminApproval: boolean
}

export type OidcAdminSettings = {
  forceOnly: boolean
  autoProvision: OidcAutoProvision
  google: OAuthPreset
  github: OAuthPreset
  generic: GenericOidc
  redirectUri: string
}

export type OidcTestCheck = {
  name: string
  status: "PASS" | "FAIL" | "WARN"
  message: string
}

export type OidcTestResult = {
  success: boolean
  checks: Array<OidcTestCheck>
}

export const oidcAdminSettingsQueryKey = ["oidc-admin-settings"] as const

export async function fetchOidcAdminSettings(): Promise<OidcAdminSettings> {
  return api<OidcAdminSettings>("/api/v1/settings/oidc")
}

export const saveOidcAdminSettings = defineMutation({
  fn: (body: OidcAdminSettings): Promise<OidcAdminSettings> =>
    api<OidcAdminSettings>("/api/v1/settings/oidc", {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  invalidates: [oidcAdminSettingsQueryKey, oidcConfigQueryKey],
})

export async function testOidcProvider(
  slug: ProviderSlug,
  body: Record<string, unknown>
): Promise<OidcTestResult> {
  return api<OidcTestResult>(`/api/v1/settings/oidc/test/${slug}`, {
    method: "POST",
    body: JSON.stringify(body),
  })
}
