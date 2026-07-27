import { api } from "./client"
import { oidcConfigQueryKey } from "./auth"
import { defineMutation } from "./mutation"
import { defineQuery } from "./query"

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

export const oidcAdminSettingsQuery = defineQuery({
  key: oidcAdminSettingsQueryKey,
  fn: fetchOidcAdminSettings,
})

// A probe against one provider's issuer. Declared like every other write
// even though it changes nothing — `invalidates: []` is the statement
// that it changes nothing, and it keeps the endpoint in the same
// vocabulary as its neighbours. The panel runs it through
// `useConnectionTest`, which reports the verdict inline instead of
// toasting it.
export const testOidcProvider = (slug: ProviderSlug) =>
  defineMutation({
    fn: (body: Record<string, unknown>): Promise<OidcTestResult> =>
      api<OidcTestResult>(`/api/v1/settings/oidc/test/${slug}`, {
        method: "POST",
        body: JSON.stringify(body),
      }),
    invalidates: [],
  })
