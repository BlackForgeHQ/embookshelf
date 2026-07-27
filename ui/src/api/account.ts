import { api } from "./client"
import { meQueryKey } from "./auth"
import { defineMutation } from "./mutation"
import { defineQuery } from "./query"

// Mirrors internal/handler/account_identities.go accountIdentitiesDTO.
// `proxy` is the forward-auth slug — managed by the upstream reverse
// proxy, surfaced read-only with `managed=true`. ADR-0022.
export type AccountIdentityProvider = {
  provider: "google" | "github" | "generic" | "proxy"
  displayName: string
  linked: boolean
  email?: string
  linkedAt?: string
  lastLoginAt?: string
  managed?: boolean
}

export type AccountIdentities = {
  hasPassword: boolean
  providers: Array<AccountIdentityProvider>
}

export const accountIdentitiesQueryKey = ["account-identities"] as const

export const accountIdentitiesQuery = defineQuery({
  key: accountIdentitiesQueryKey,
  fn: fetchAccountIdentities,
})

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

export const unlinkOIDC = defineMutation({
  fn: (provider: AccountIdentityProvider["provider"]): Promise<void> =>
    api<void>(`/api/v1/account/oidc/${provider}`, { method: "DELETE" }),
  invalidates: [accountIdentitiesQueryKey],
})

export const setInitialPassword = defineMutation({
  fn: (next: string): Promise<void> =>
    api<void>("/api/v1/account/password/set", {
      method: "POST",
      body: JSON.stringify({ next }),
    }),
  invalidates: [accountIdentitiesQueryKey],
})

// updateKindleEmail sets (or clears, with empty string) the user's
// Send-to-Kindle target. Server validates the `^[a-z0-9._-]+@kindle.com$`
// shape so a typo here doesn't leak the file to a stranger's address.
export const updateKindleEmail = defineMutation({
  fn: (email: string): Promise<void> =>
    api<void>("/api/v1/account/kindle-email", {
      method: "PUT",
      body: JSON.stringify({ email }),
    }),
  invalidates: [meQueryKey],
})
