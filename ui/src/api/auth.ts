import { api } from "./client"
import type { ApiError } from "./client"

// Mirrors internal/handler/auth.go userDTO.
export type AuthUser = {
  id: string
  email: string
  name: string
  role: "admin" | "user"
  display: string
  initials: string
  createdAt: string
  lastSeenAt?: string
}

type UserEnvelope = { user: AuthUser }
type SignupStatus = { enabled: boolean }

// fetchMe returns the authenticated user or null when the server responds
// with 401. Keeps useQuery's error state reserved for actual failures
// (network, 5xx, etc.) rather than the "logged out" case.
export async function fetchMe(): Promise<AuthUser | null> {
  try {
    const { user } = await api<UserEnvelope>("/api/v1/me")
    return user
  } catch (e) {
    const err = e as ApiError
    if (err?.status === 401) return null
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

export async function logout(): Promise<void> {
  await api<void>("/api/v1/auth/logout", { method: "POST" })
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

// OIDCConfig is the anonymous login-page view: one entry per enabled
// provider plus the force-only flag. Empty `providers` means "local
// login only".
export type OIDCConfig = {
  providers: Array<OIDCPublicProvider>
  forceOnly: boolean
}

export async function oidcConfig(): Promise<OIDCConfig> {
  return api<OIDCConfig>("/api/v1/auth/oidc/config")
}

export const oidcConfigQueryKey = ["oidc-config"] as const

// Shared react-query key for the current user. Export so every mutation can
// invalidate it in one line.
export const meQueryKey = ["me"] as const

export async function changePassword(
  current: string,
  next: string
): Promise<void> {
  await api<void>("/api/v1/me/password", {
    method: "POST",
    body: JSON.stringify({ current, next }),
  })
}

export async function updateDisplayName(name: string): Promise<void> {
  await api<void>("/api/v1/me", {
    method: "PATCH",
    body: JSON.stringify({ name }),
  })
}
