import { api } from "./client"
import { oidcConfigQueryKey } from "./auth"
import { defineMutation } from "./mutation"

// ForwardAuthHeaders mirrors repo.ForwardAuthHeaders. Operator points
// each field at whatever the upstream proxy emits (Authelia
// `Remote-*`, oauth2-proxy `X-Forwarded-*`, …). ADR-0022.
export type ForwardAuthHeaders = {
  user: string
  email: string
  name: string
  groups?: string
}

// ForwardAuthSettings mirrors repo.ForwardAuthConfig. No secrets at
// this seam — trust gate is the IP allowlist, not a shared secret.
export type ForwardAuthSettings = {
  enabled: boolean
  trustedProxyCIDRs: Array<string>
  headers: ForwardAuthHeaders
  logoutUrl: string
  hideLocalLogin: boolean
}

export const forwardAuthSettingsQueryKey = ["forward-auth-settings"] as const

export async function fetchForwardAuthSettings(): Promise<ForwardAuthSettings> {
  return api<ForwardAuthSettings>("/api/v1/settings/forward-auth")
}

export const saveForwardAuthSettings = defineMutation({
  fn: (body: ForwardAuthSettings): Promise<ForwardAuthSettings> =>
    api<ForwardAuthSettings>("/api/v1/settings/forward-auth", {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  invalidates: [forwardAuthSettingsQueryKey, oidcConfigQueryKey],
})
