import { useEffect, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useNavigate, useSearch } from "@tanstack/react-router"
import { toast } from "sonner"

import type { ApiError } from "@/api/client"
import type {AccountIdentityProvider} from "@/api/account";
import {
  
  accountIdentitiesQueryKey,
  fetchAccountIdentities,
  linkOIDC,
  setInitialPassword,
  unlinkOIDC
} from "@/api/account"
import {
  changePassword,
  fetchMe,
  meQueryKey,
  updateDisplayName,
} from "@/api/auth"
import { Avatar, Card, Field } from "@/components/SettingsShared"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

const LINK_ERROR_MESSAGES: Record<string, string> = {
  session_expired: "Your session expired before the link finished. Sign in and try again.",
  already_linked_elsewhere:
    "That sign-in method is already linked to another account.",
  provider_already_linked:
    "Another sign-in for this provider is already linked. Unlink it first.",
}

export function AccountPanel() {
  const queryClient = useQueryClient()
  const navigate = useNavigate({ from: "/account" })
  const search = useSearch({ from: "/_app/account" })

  // Toast and clear redirect-back signals from the OIDC link callback.
  // Running once per (linked, error) pair keeps refresh from re-firing
  // the toast.
  useEffect(() => {
    if (!search.linked && !search.error) return
    if (search.linked) {
      toast.success(`${prettyProvider(search.linked)} connected.`)
    }
    if (search.error) {
      toast.error(LINK_ERROR_MESSAGES[search.error] ?? "Linking failed.")
    }
    navigate({
      to: "/account",
      search: search.section ? { section: search.section } : {},
      replace: true,
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [search.linked, search.error])

  const me = useQuery({
    queryKey: meQueryKey,
    queryFn: fetchMe,
    staleTime: 60_000,
  })
  const user = me.data
  const identities = useQuery({
    queryKey: accountIdentitiesQueryKey,
    queryFn: fetchAccountIdentities,
    staleTime: 30_000,
  })

  const [editing, setEditing] = useState(false)
  const [nameDraft, setNameDraft] = useState("")
  const [pwOpen, setPwOpen] = useState(false)
  const [pwCurrent, setPwCurrent] = useState("")
  const [pwNext, setPwNext] = useState("")
  const [pwConfirm, setPwConfirm] = useState("")

  // First-password modal for OIDC-only users. Separate from the
  // change-password form because it skips the "current password" step.
  const [initPwOpen, setInitPwOpen] = useState(false)
  const [initPwNext, setInitPwNext] = useState("")
  const [initPwConfirm, setInitPwConfirm] = useState("")

  const nameMut = useMutation({
    mutationFn: (next: string) => updateDisplayName(next),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: meQueryKey })
      setEditing(false)
      toast.success("Display name updated.")
    },
    onError: (e) => toast.error((e as unknown as ApiError).message),
  })

  const pwMut = useMutation({
    mutationFn: ({ current, next }: { current: string; next: string }) =>
      changePassword(current, next),
    onSuccess: () => {
      setPwOpen(false)
      setPwCurrent("")
      setPwNext("")
      setPwConfirm("")
      toast.success("Password updated.")
    },
    onError: (e) => toast.error((e as unknown as ApiError).message),
  })

  const initPwMut = useMutation({
    mutationFn: (next: string) => setInitialPassword(next),
    onSuccess: () => {
      setInitPwOpen(false)
      setInitPwNext("")
      setInitPwConfirm("")
      queryClient.invalidateQueries({ queryKey: accountIdentitiesQueryKey })
      toast.success("Password set. You can now unlink sign-in methods.")
    },
    onError: (e) => toast.error((e as unknown as ApiError).message),
  })

  const unlinkMut = useMutation({
    mutationFn: (provider: AccountIdentityProvider["provider"]) =>
      unlinkOIDC(provider),
    onSuccess: (_, provider) => {
      queryClient.invalidateQueries({ queryKey: accountIdentitiesQueryKey })
      toast.success(`${prettyProvider(provider)} disconnected.`)
    },
    onError: (e) => toast.error((e as unknown as ApiError).message),
  })

  const joined = user?.createdAt
    ? new Date(user.createdAt).toLocaleDateString(undefined, {
        month: "short",
        year: "numeric",
      })
    : "—"
  const roleLabel = user?.role === "admin" ? "Admin" : "User"

  const data = identities.data
  const linkedCount = data?.providers.filter((p) => p.linked).length ?? 0
  const hasPassword = data?.hasPassword ?? false

  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 24 }}>
        Account
      </h2>

      <Card>
        <div style={{ display: "flex", alignItems: "center", gap: 16 }}>
          <Avatar initials={user?.initials} />
          <div style={{ flex: 1, minWidth: 0 }}>
            {editing ? (
              <form
                onSubmit={(e) => {
                  e.preventDefault()
                  nameMut.mutate(nameDraft.trim())
                }}
                style={{ display: "flex", gap: 8, alignItems: "center" }}
              >
                <Input
                  autoFocus
                  value={nameDraft}
                  onChange={(e) => setNameDraft(e.target.value)}
                  placeholder="Display name"
                  className="flex-1"
                />
                <Button type="submit" size="sm" disabled={nameMut.isPending}>
                  Save
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => setEditing(false)}
                >
                  Cancel
                </Button>
              </form>
            ) : (
              <>
                <div style={{ fontSize: 15, fontWeight: 500 }}>
                  {user?.display ?? "…"}
                </div>
                <div className="t-small" style={{ fontSize: 12 }}>
                  {user?.email ?? "—"} · {roleLabel} · joined {joined}
                </div>
              </>
            )}
          </div>
          {!editing && (
            <>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setNameDraft(user?.name ?? "")
                  setEditing(true)
                }}
              >
                Edit name
              </Button>
              {hasPassword ? (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setPwOpen((v) => !v)}
                >
                  Change password
                </Button>
              ) : (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setInitPwOpen(true)}
                >
                  Set password
                </Button>
              )}
            </>
          )}
        </div>

        {pwOpen && (
          <form
            onSubmit={(e) => {
              e.preventDefault()
              if (pwNext !== pwConfirm) {
                toast.error("New passwords do not match.")
                return
              }
              pwMut.mutate({ current: pwCurrent, next: pwNext })
            }}
            style={{
              marginTop: 16,
              paddingTop: 16,
              borderTop: "1px dashed var(--color-rule-soft)",
              display: "flex",
              flexDirection: "column",
              gap: 10,
            }}
          >
            <Field label="Current password">
              <Input
                type="password"
                value={pwCurrent}
                onChange={(e) => setPwCurrent(e.target.value)}
                autoComplete="current-password"
                required
              />
            </Field>
            <Field label="New password">
              <Input
                type="password"
                value={pwNext}
                onChange={(e) => setPwNext(e.target.value)}
                autoComplete="new-password"
                minLength={8}
                required
              />
            </Field>
            <Field label="Confirm new password">
              <Input
                type="password"
                value={pwConfirm}
                onChange={(e) => setPwConfirm(e.target.value)}
                autoComplete="new-password"
                minLength={8}
                required
              />
            </Field>
            <div
              style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}
            >
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => setPwOpen(false)}
              >
                Cancel
              </Button>
              <Button type="submit" size="sm" disabled={pwMut.isPending}>
                {pwMut.isPending ? "Updating…" : "Update password"}
              </Button>
            </div>
          </form>
        )}

        {initPwOpen && (
          <form
            onSubmit={(e) => {
              e.preventDefault()
              if (initPwNext !== initPwConfirm) {
                toast.error("Passwords do not match.")
                return
              }
              initPwMut.mutate(initPwNext)
            }}
            style={{
              marginTop: 16,
              paddingTop: 16,
              borderTop: "1px dashed var(--color-rule-soft)",
              display: "flex",
              flexDirection: "column",
              gap: 10,
            }}
          >
            <div className="t-small" style={{ fontSize: 12 }}>
              You signed up via a sign-in provider, so you don't have a
              password yet. Setting one lets you sign in directly and
              unlink providers without losing access.
            </div>
            <Field label="New password">
              <Input
                type="password"
                value={initPwNext}
                onChange={(e) => setInitPwNext(e.target.value)}
                autoComplete="new-password"
                minLength={8}
                required
              />
            </Field>
            <Field label="Confirm password">
              <Input
                type="password"
                value={initPwConfirm}
                onChange={(e) => setInitPwConfirm(e.target.value)}
                autoComplete="new-password"
                minLength={8}
                required
              />
            </Field>
            <div
              style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}
            >
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => setInitPwOpen(false)}
              >
                Cancel
              </Button>
              <Button type="submit" size="sm" disabled={initPwMut.isPending}>
                {initPwMut.isPending ? "Saving…" : "Set password"}
              </Button>
            </div>
          </form>
        )}
      </Card>

      <div className="t-label" style={{ marginTop: 24, marginBottom: 10 }}>
        Sign-in methods
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
        <SignInRow
          title="Local password"
          subtitle={hasPassword ? "Set" : "Not set"}
          enabled={hasPassword}
        />
        {data?.providers.map((p) => {
          // Lockout: user with no password and exactly one linked
          // identity cannot remove their last credential.
          const isLastCredential =
            p.linked && !hasPassword && linkedCount === 1
          return (
            <SignInRow
              key={p.provider}
              title={p.displayName}
              subtitle={p.linked ? (p.email ?? "Linked") : "Not connected"}
              enabled={p.linked}
              action={
                p.linked ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={isLastCredential || unlinkMut.isPending}
                    title={
                      isLastCredential
                        ? "Set a password before unlinking your last sign-in method."
                        : undefined
                    }
                    onClick={() => unlinkMut.mutate(p.provider)}
                  >
                    Unlink
                  </Button>
                ) : (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => linkOIDC(p.provider)}
                  >
                    Connect
                  </Button>
                )
              }
            />
          )
        })}
        {identities.isLoading && (
          <div className="t-small" style={{ fontSize: 12, padding: 8 }}>
            Loading sign-in methods…
          </div>
        )}
      </div>
    </>
  )
}

function SignInRow({
  title,
  subtitle,
  enabled,
  action,
}: {
  title: string
  subtitle: string
  enabled: boolean
  action?: React.ReactNode
}) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 14,
        padding: "10px 14px",
        border: "1px solid var(--color-rule-soft)",
        background: "var(--color-paper-0)",
      }}
    >
      <span
        style={{
          width: 8,
          height: 8,
          borderRadius: "50%",
          background: enabled ? "oklch(0.58 0.12 140)" : "var(--color-ink-4)",
        }}
      />
      <div className="grow">
        <div className="t-item-title">{title}</div>
        <div className="t-item-sub">{subtitle}</div>
      </div>
      {action}
    </div>
  )
}

function prettyProvider(slug: string): string {
  switch (slug) {
    case "google":
      return "Google"
    case "github":
      return "GitHub"
    case "generic":
      return "Single sign-on"
    default:
      return slug
  }
}
