import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import type { ApiError } from "@/api/client"
import {
  changePassword,
  fetchMe,
  meQueryKey,
  updateDisplayName,
} from "@/api/auth"
import { Avatar, Card, Field } from "@/components/SettingsShared"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

const AUTH_METHODS: ReadonlyArray<{ n: string; on: boolean; sub: string }> = [
  { n: "Local (session)", on: true, sub: "Username + password" },
  { n: "OIDC", on: false, sub: "Pending" },
]

export function AccountPanel() {
  const queryClient = useQueryClient()
  const me = useQuery({
    queryKey: meQueryKey,
    queryFn: fetchMe,
    staleTime: 60_000,
  })
  const user = me.data

  const [editing, setEditing] = useState(false)
  const [nameDraft, setNameDraft] = useState("")
  const [pwOpen, setPwOpen] = useState(false)
  const [pwCurrent, setPwCurrent] = useState("")
  const [pwNext, setPwNext] = useState("")
  const [pwConfirm, setPwConfirm] = useState("")

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

  const joined = user?.createdAt
    ? new Date(user.createdAt).toLocaleDateString(undefined, {
        month: "short",
        year: "numeric",
      })
    : "—"
  const roleLabel = user?.role === "admin" ? "Admin" : "User"

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
              <Button
                variant="outline"
                size="sm"
                onClick={() => setPwOpen((v) => !v)}
              >
                Change password
              </Button>
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
      </Card>

      <div className="t-label" style={{ marginTop: 24, marginBottom: 10 }}>
        Authentication
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
        {AUTH_METHODS.map((a) => (
          <div
            key={a.n}
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
                background: a.on
                  ? "oklch(0.58 0.12 140)"
                  : "var(--color-ink-4)",
              }}
            />
            <div className="grow">
              <div className="t-item-title">{a.n}</div>
              <div className="t-item-sub">{a.sub}</div>
            </div>
            <span className="t-micro">{a.on ? "enabled" : "disabled"}</span>
          </div>
        ))}
      </div>
    </>
  )
}
