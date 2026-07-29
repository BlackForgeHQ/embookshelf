import { useState } from "react"

import { createInvite, invitesQuery, revokeInvite } from "@/api/email"
import { useApiMutation } from "@/api/mutation"
import { useApiQuery } from "@/api/query"
import { appConfigQuery } from "@/api/settings"
import { affordanceFor } from "@/lib/affordance"
import { Icon } from "@/components/Icon"
import { Card, Field, Select } from "@/components/SettingsShared"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

export function InvitesPanel() {
  // Read from the app config every screen already holds, not from the
  // admin SMTP settings: the question is one boolean, and answering it
  // by fetching host, port, username and from-address is a second,
  // heavier source for a fact the first one states.
  const cfg = useApiQuery(appConfigQuery)

  // Every invites endpoint answers 503 EMAIL_DISABLED while SMTP is off.
  // This is that same rule, read once and used for both consequences:
  // don't ask (TanStack Query would retry-storm the server with failed
  // GETs), and say why. Deliberately tri-state — `undefined` means the
  // config hasn't loaded, which is neither permission to fetch nor
  // grounds to claim email is disabled.
  const emailEnabled = cfg.data?.emailEnabled

  const invites = useApiQuery(invitesQuery, {
    enabled: emailEnabled === true,
  })

  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState<{
    email: string
    role: "admin" | "user"
  }>({ email: "", role: "user" })

  const createMut = useApiMutation(createInvite, {
    successToast: (_, vars) => `Invite sent to ${vars.email}.`,
    onSuccess: () => {
      setOpen(false)
      setDraft({ email: "", role: "user" })
    },
    errorToast: (err) => err.message || "Could not send invite.",
  })

  const revokeMut = useApiMutation(revokeInvite, {
    successToast: "Invite revoked.",
    errorToast: (err) => err.message || "Could not revoke invite.",
  })

  if (emailEnabled === false) {
    // The sentence comes from the affordance module so this panel and
    // the Send-to-Kindle button cannot describe the same refusal two
    // ways. Only admins reach /settings at all (SETTINGS_SECTIONS marks
    // every section adminOnly), so the viewer is known here. The fix it
    // names is the "Email delivery" section one tab away — that panel is
    // this route's local state, so there is no link to render.
    const refusal = affordanceFor("EMAIL_DISABLED", { isAdmin: true })
    const why = refusal.kind === "hidden" ? "" : refusal.reason

    return (
      <>
        <div className="mb-4 flex items-center justify-between">
          <h2 className="font-serif text-xl font-medium text-foreground">
            Invites
          </h2>
        </div>
        <p className="text-sm text-muted-foreground italic">
          {why} Invitations go out by email, so there is nothing to send
          until it is on.
        </p>
      </>
    )
  }

  const rows = invites.data ?? []

  return (
    <>
      <div className="mb-4 flex items-center justify-between">
        <h2 className="font-serif text-xl font-medium text-foreground">
          Invites
        </h2>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => setOpen((v) => !v)}
        >
          <Icon name="plus" size={13} /> New invite
        </Button>
      </div>
      <p className="mb-6 text-sm text-muted-foreground italic">
        Pending invites land here until the recipient accepts. Revoking
        invalidates the token immediately.
      </p>

      {open && (
        <Card>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              createMut.mutate({
                email: draft.email.trim(),
                role: draft.role,
              })
            }}
            className="flex flex-col gap-4"
          >
            <Field label="Email">
              <Input
                type="email"
                value={draft.email}
                onChange={(e) => setDraft({ ...draft, email: e.target.value })}
                required
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <Field label="Role">
              <Select
                value={draft.role}
                onChange={(v) =>
                  setDraft({ ...draft, role: v as "user" | "admin" })
                }
                options={[
                  { value: "user", label: "User" },
                  { value: "admin", label: "Admin" },
                ]}
              />
            </Field>
            <div className="mt-2 flex items-center justify-end gap-2">
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => setOpen(false)}
              >
                Cancel
              </Button>
              <Button type="submit" size="sm" disabled={createMut.isPending}>
                {createMut.isPending ? "Sending…" : "Send invite"}
              </Button>
            </div>
          </form>
        </Card>
      )}

      {invites.isLoading && (
        <div className="text-sm text-muted-foreground italic">
          Loading invites…
        </div>
      )}

      {!invites.isLoading && rows.length === 0 && (
        <div className="text-sm text-muted-foreground italic">
          No pending invites.
        </div>
      )}

      <div className="mt-4 flex flex-col gap-2">
        {rows.map((row) => (
          <div
            key={row.id}
            className="flex items-center gap-4 rounded-lg border border-border bg-card p-3 shadow-sm"
          >
            <div className="min-w-0 flex-1">
              <div className="truncate text-[14px] leading-snug font-medium">
                {row.email}
              </div>
              <div className="mt-0.5 truncate text-xs text-muted-foreground">
                {row.role === "admin" ? "Admin" : "User"}
                {row.invitedByName && ` · invited by ${row.invitedByName}`}
                {" · expires "}
                {new Date(row.expiresAt).toLocaleDateString(undefined, {
                  month: "short",
                  day: "numeric",
                  year: "numeric",
                })}
              </div>
            </div>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => {
                if (
                  window.confirm(
                    `Revoke invite for ${row.email}? The link will stop working immediately.`
                  )
                ) {
                  revokeMut.mutate(row.id)
                }
              }}
              disabled={revokeMut.isPending}
              className="text-destructive hover:bg-destructive/10 hover:text-destructive"
            >
              Revoke
            </Button>
          </div>
        ))}
      </div>
    </>
  )
}
