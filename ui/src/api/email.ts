import { api } from "./client"
import { defineMutation } from "./mutation"
import { defineQuery } from "./query"
import { appConfigQueryKey } from "./settings"

// Mirrors internal/handler/settings_email.go emailSettingsDTO. The wire
// shape never carries the SMTP password back to the client — `passwordSet`
// records "yes one is stored" so the UI can render a badge. PUT bodies
// echo the DTO with `smtp.password` populated only when the admin types
// a fresh secret; an empty password is the server's "leave alone" signal
// (mirrors the OIDC client-secret pattern).
export type EmailTLS = "none" | "starttls" | "tls"

export type EmailSettings = {
  enabled: boolean
  smtp: {
    host: string
    port: number
    username: string
    password?: string
    tls: EmailTLS
  }
  from: {
    address: string
    name: string
  }
  publicUrl: string
  passwordSet: boolean
}

export const emailSettingsQueryKey = ["settings", "email"] as const

export async function fetchEmailSettings(): Promise<EmailSettings> {
  return api<EmailSettings>("/api/v1/settings/email")
}

// updateEmailSettings persists the SMTP + identity rows. An empty
// `smtp.password` keeps the existing secret — admins editing host /
// port / from address shouldn't have to retype the password every save.
//
// It also busts the app config: the handler hot-reloads the notifier on
// save (ADR-0020), so GET /api/v1/config's emailEnabled changes with
// this write, and every control gated on it — Send-to-Kindle, the
// account panel's Kindle form, the invites panel — reads that flag. Left
// out, they went on claiming email was off for the rest of the session.
export const updateEmailSettings = defineMutation({
  fn: (body: EmailSettings): Promise<EmailSettings> =>
    api<EmailSettings>("/api/v1/settings/email", {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  invalidates: [emailSettingsQueryKey, appConfigQueryKey],
})

// sendEmailTest fires a one-off message through the live SMTP config to
// the supplied recipient. 502 SMTP_ERROR + the underlying message bubble
// up via the standard ApiError path so the form can render the failure
// inline rather than a generic toast.
export const sendEmailTest = defineMutation({
  fn: (to: string): Promise<void> =>
    api<void>("/api/v1/settings/email/test", {
      method: "POST",
      body: JSON.stringify({ to }),
    }),
  invalidates: [],
})

// Mirrors internal/handler/admin_invites.go inviteDTO. `id` is the hex
// SHA-256 of the issued token — it's an opaque revocation handle, not
// the plaintext (which is gone the moment the email is sent).
export type Invite = {
  id: string
  email: string
  role: "admin" | "user"
  invitedById: string
  invitedByName?: string
  createdAt: string
  expiresAt: string
}

export const invitesQueryKey = ["settings", "invites"] as const

export const emailSettingsQuery = defineQuery({
  key: emailSettingsQueryKey,
  fn: fetchEmailSettings,
})

export const invitesQuery = defineQuery({
  key: invitesQueryKey,
  fn: fetchInvites,
})

export async function fetchInvites(): Promise<Array<Invite>> {
  const { invites } = await api<{ invites: Array<Invite> }>(
    "/api/v1/settings/invites"
  )
  return invites
}

export const createInvite = defineMutation({
  fn: (body: { email: string; role: "admin" | "user" }): Promise<void> =>
    api<void>("/api/v1/settings/invites", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  invalidates: [invitesQueryKey],
})

export const revokeInvite = defineMutation({
  fn: (id: string): Promise<void> =>
    api<void>(`/api/v1/settings/invites/${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),
  invalidates: [invitesQueryKey],
})
