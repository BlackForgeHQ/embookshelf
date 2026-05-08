import { api } from "./client"
import { defineMutation } from "./mutation"
import type { ApiError } from "./client"

export type UserStatus = "active" | "pending" | "denied"

// Mirrors internal/handler/auth.go userDTO.
export type AuthUser = {
  id: string
  email: string
  name: string
  role: "admin" | "user"
  status: UserStatus
  statusChangedAt?: string
  display: string
  initials: string
  kindleEmail: string
  createdAt: string
  lastSeenAt?: string
}

type UserEnvelope = { user: AuthUser }
type SignupStatus = { enabled: boolean; emailEnabled: boolean }

// fetchMe returns the authenticated user or null when the server responds
// with 401. Keeps useQuery's error state reserved for actual failures
// (network, 5xx, etc.) rather than the "logged out" case.
export async function fetchMe(): Promise<AuthUser | null> {
  try {
    const { user } = await api<UserEnvelope>("/api/v1/me")
    return user
  } catch (e) {
    const err = e as ApiError
    if (err.status === 401) return null
    throw e
  }
}

export async function login(
  email: string,
  password: string
): Promise<AuthUser> {
  const { user } = await api<UserEnvelope>("/api/v1/auth/login", {
    method: "POST",
    body: JSON.stringify({ email, password }),
  })
  return user
}

// LogoutResult mirrors the server's response. When forward-auth is
// enabled and `logoutUrl` is configured, the server returns 200 with
// the URL so the SPA can redirect the browser to the upstream proxy's
// logout endpoint (Authelia /logout, etc.). Otherwise 204 / undefined.
export type LogoutResult = { logoutUrl?: string }

export async function logout(): Promise<LogoutResult> {
  // 204 / 200+JSON are both valid responses. The api helper returns
  // `undefined` typed-as-T for the empty-body case, so widen here and
  // collapse to the empty result.
  const res: LogoutResult | undefined = await api<LogoutResult | undefined>(
    "/api/v1/auth/logout",
    { method: "POST" }
  )
  return res ?? {}
}

export async function signup(
  email: string,
  name: string,
  password: string
): Promise<AuthUser> {
  const { user } = await api<UserEnvelope>("/api/v1/auth/signup", {
    method: "POST",
    body: JSON.stringify({ email, name, password }),
  })
  return user
}

export async function signupStatus(): Promise<SignupStatus> {
  return api<SignupStatus>("/api/v1/auth/signup")
}

// OIDCPublicProvider is one login option surfaced to the login page.
// The browser follows `loginUrl` to kick off the flow for that provider.
export type OIDCPublicProvider = {
  slug: "google" | "github" | "generic"
  name: string
  kind: "google" | "github" | "oidc"
  loginUrl: string
}

// OIDCConfig is the anonymous login-page view. `providers` is one
// entry per enabled OIDC provider; `forceOnly` hides the local form
// when an OIDC provider is in use; the `forwardAuth*` fields advertise
// whether reverse-proxy header SSO is enabled and where to redirect on
// logout. Empty `providers` + disabled forwardAuth means "local login
// only". ADR-0022.
export type OIDCConfig = {
  providers: Array<OIDCPublicProvider>
  forceOnly: boolean
  forwardAuthEnabled: boolean
  hideLocalLogin: boolean
  forwardAuthLogoutUrl: string
}

export async function oidcConfig(): Promise<OIDCConfig> {
  return api<OIDCConfig>("/api/v1/auth/oidc/config")
}

export const oidcConfigQueryKey = ["oidc-config"] as const

// Shared react-query key for the current user. Export so every mutation can
// invalidate it in one line.
export const meQueryKey = ["me"] as const

export const changePassword = defineMutation({
  fn: (args: { current: string; next: string }): Promise<void> =>
    api<void>("/api/v1/me/password", {
      method: "POST",
      body: JSON.stringify(args),
    }),
  invalidates: [meQueryKey],
})

export const updateDisplayName = defineMutation({
  fn: (name: string): Promise<void> =>
    api<void>("/api/v1/me", {
      method: "PATCH",
      body: JSON.stringify({ name }),
    }),
  invalidates: [meQueryKey],
})

// --- Password reset --------------------------------------------------------

// Mirrors internal/handler/auth_password_reset.go passwordResetVerifyResp.
// Server returns the same shape with `valid: false` for unknown / expired /
// consumed tokens so callers can't distinguish failure modes by response code.
export type PasswordResetVerify = {
  valid: boolean
  email?: string
  expiresAt?: string
}

export const requestPasswordReset = defineMutation({
  fn: (email: string): Promise<void> =>
    api<void>("/api/v1/auth/password-reset/request", {
      method: "POST",
      body: JSON.stringify({ email }),
    }),
  invalidates: [],
})

export async function verifyPasswordReset(
  token: string
): Promise<PasswordResetVerify> {
  return api<PasswordResetVerify>(
    `/api/v1/auth/password-reset/verify?token=${encodeURIComponent(token)}`
  )
}

export const confirmPasswordReset = defineMutation({
  fn: (args: { token: string; newPassword: string }): Promise<void> =>
    api<void>("/api/v1/auth/password-reset/confirm", {
      method: "POST",
      body: JSON.stringify(args),
    }),
  invalidates: [],
})

// --- Invite acceptance -----------------------------------------------------

export const acceptInvite = defineMutation({
  fn: async (args: {
    token: string
    name: string
    password: string
  }): Promise<AuthUser> => {
    const { user } = await api<UserEnvelope>("/api/v1/auth/invites/accept", {
      method: "POST",
      body: JSON.stringify(args),
    })
    return user
  },
  invalidates: [meQueryKey],
})
