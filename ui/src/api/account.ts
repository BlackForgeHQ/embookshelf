import { api } from "./client"

// Mirrors internal/handler/account_identities.go accountIdentitiesDTO.
export type AccountIdentityProvider = {
  provider: "google" | "github" | "generic"
  displayName: string
  linked: boolean
  email?: string
  linkedAt?: string
  lastLoginAt?: string
}

export type AccountIdentities = {
  hasPassword: boolean
  providers: Array<AccountIdentityProvider>
}

export const accountIdentitiesQueryKey = ["account-identities"] as const

export async function fetchAccountIdentities(): Promise<AccountIdentities> {
  return api<AccountIdentities>("/api/v1/account/identities")
}

// linkOIDC navigates the browser to the link-flow start endpoint.
// The endpoint redirects to the IdP; on return the callback hands
// the user back to /account?linked=...&error=... so the panel can
// render the outcome.
export function linkOIDC(slug: AccountIdentityProvider["provider"]): void {
  window.location.assign(`/api/v1/account/oidc/link/${slug}`)
}

export async function unlinkOIDC(
  provider: AccountIdentityProvider["provider"],
): Promise<void> {
  await api<void>(`/api/v1/account/oidc/${provider}`, { method: "DELETE" })
}

export async function setInitialPassword(next: string): Promise<void> {
  await api<void>("/api/v1/account/password/set", {
    method: "POST",
    body: JSON.stringify({ next }),
  })
}
