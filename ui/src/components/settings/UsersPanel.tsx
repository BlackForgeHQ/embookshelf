import { useMemo, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import type { ApiError } from "@/api/client"
import type { AuthUser } from "@/api/auth"
import {
  approveSettingsUser,
  createSettingsUser,
  deleteSettingsUser,
  denySettingsUser,
  fetchSettingsUsers,
  settingsUsersQueryKey,
  updateSettingsUserRole,
} from "@/api/settings"
import { Icon } from "@/components/Icon"
import {
  AdminGate,
  Avatar,
  Card,
  Field,
  Select,
} from "@/components/SettingsShared"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

export function UsersPanel({
  isAdmin,
  me,
}: {
  isAdmin: boolean
  me: AuthUser | null
}) {
  const queryClient = useQueryClient()
  const users = useQuery({
    queryKey: settingsUsersQueryKey,
    queryFn: fetchSettingsUsers,
    enabled: isAdmin,
  })

  const sortedUsers = useMemo(() => {
    const all = users.data ?? []
    const rank = (s: AuthUser["status"]) =>
      s === "pending" ? 0 : s === "active" ? 1 : 2
    return [...all].sort((a, b) => {
      const r = rank(a.status) - rank(b.status)
      if (r !== 0) return r
      if (a.status === "pending" && b.status === "pending") {
        return (
          new Date(a.statusChangedAt ?? a.createdAt).getTime() -
          new Date(b.statusChangedAt ?? b.createdAt).getTime()
        )
      }
      return a.email.localeCompare(b.email)
    })
  }, [users.data])

  const [createOpen, setCreateOpen] = useState(false)
  const [draft, setDraft] = useState({
    email: "",
    name: "",
    password: "",
    role: "user" as "user" | "admin",
  })

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: settingsUsersQueryKey })

  const createMut = useMutation({
    mutationFn: () => createSettingsUser(draft),
    onSuccess: () => {
      invalidate()
      setCreateOpen(false)
      setDraft({ email: "", name: "", password: "", role: "user" })
      toast.success("User created.")
    },
    onError: (e) => toast.error((e as unknown as ApiError).message),
  })

  const roleMut = useMutation({
    mutationFn: ({ id, role }: { id: string; role: "admin" | "user" }) =>
      updateSettingsUserRole(id, role),
    onSuccess: (_data, { role }) => {
      invalidate()
      toast.success(
        role === "admin"
          ? "User promoted to admin."
          : "User demoted to regular user."
      )
    },
    onError: (e) => toast.error((e as unknown as ApiError).message),
  })

  const deleteMut = useMutation({
    mutationFn: (id: string) => deleteSettingsUser(id),
    onSuccess: () => {
      invalidate()
      toast.success("User deleted.")
    },
    onError: (e) => toast.error((e as unknown as ApiError).message),
  })

  const approveMut = useMutation({
    mutationFn: (id: string) => approveSettingsUser(id),
    onSuccess: () => {
      invalidate()
      toast.success("User approved.")
    },
    onError: (e) => toast.error((e as unknown as ApiError).message),
  })

  const denyMut = useMutation({
    mutationFn: (id: string) => denySettingsUser(id),
    onSuccess: () => {
      invalidate()
      toast.success("User denied.")
    },
    onError: (e) => toast.error((e as unknown as ApiError).message),
  })

  if (!isAdmin) return <AdminGate label="Users & roles" />

  return (
    <>
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-xl font-serif font-medium text-foreground">Users &amp; roles</h2>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => setCreateOpen((v) => !v)}
        >
          <Icon name="plus" size={13} /> New user
        </Button>
      </div>
      <p className="text-sm italic text-muted-foreground mb-6">
        Admins see every settings pane; regular users see only Account, Reading
        preferences, Device sync, and About.
      </p>

      {createOpen && (
        <Card>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              createMut.mutate()
            }}
            className="flex flex-col gap-4"
          >
            <Field label="Email">
              <Input
                type="email"
                value={draft.email}
                onChange={(e) => setDraft({ ...draft, email: e.target.value })}
                required
              />
            </Field>
            <Field label="Display name">
              <Input
                value={draft.name}
                onChange={(e) => setDraft({ ...draft, name: e.target.value })}
              />
            </Field>
            <Field label="Initial password">
              <Input
                type="password"
                value={draft.password}
                onChange={(e) =>
                  setDraft({ ...draft, password: e.target.value })
                }
                minLength={8}
                required
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
            <div
              className="flex items-center gap-2 justify-end mt-2"
            >
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => setCreateOpen(false)}
              >
                Cancel
              </Button>
              <Button type="submit" size="sm" disabled={createMut.isPending}>
                {createMut.isPending ? "Creating…" : "Create user"}
              </Button>
            </div>
          </form>
        </Card>
      )}

      {users.isLoading && (
        <div className="text-sm italic text-muted-foreground">Loading users…</div>
      )}

      <div
        className="flex flex-col gap-2 mt-4"
      >
        {sortedUsers.map((u) => {
          const isMe = u.id === me?.id
          return (
            <div
              key={u.id}
              data-row="user"
              data-user-id={u.id}
              className="flex items-center gap-4 p-3 bg-card border border-border rounded-lg shadow-sm"
            >
              <Avatar initials={u.initials} size={32} />
              {u.status !== "active" && (
                <span
                  data-row-status={u.status}
                  className={`px-2 py-0.5 rounded-full text-[11px] font-medium ${u.status === "pending" ? "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400" : "bg-muted text-muted-foreground"}`}
                >
                  {u.status === "pending" ? "Pending" : "Denied"}
                </span>
              )}
              <div className="flex-1 min-w-0">
                <div className="text-[14px] font-medium leading-snug truncate">
                  {u.display} {isMe && <span className="text-[10px] uppercase tracking-wider text-muted-foreground ml-2">you</span>}
                </div>
                <div className="text-xs text-muted-foreground truncate mt-0.5">
                  {u.email} · joined{" "}
                  {new Date(u.createdAt).toLocaleDateString(undefined, {
                    month: "short",
                    year: "numeric",
                  })}
                  {u.lastSeenAt &&
                    ` · last seen ${new Date(u.lastSeenAt).toLocaleDateString()}`}
                </div>
              </div>
              {u.status === "active" && (
                <>
                  <Select
                    value={u.role}
                    onChange={(v) =>
                      roleMut.mutate({ id: u.id, role: v as "admin" | "user" })
                    }
                    options={[
                      { value: "user", label: "User" },
                      { value: "admin", label: "Admin" },
                    ]}
                    disabled={isMe || roleMut.isPending}
                    triggerClassName="w-[110px] shrink-0"
                  />
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    disabled={isMe || deleteMut.isPending}
                    onClick={() => {
                      if (
                        window.confirm(
                          `Delete ${u.display}? This cannot be undone.`
                        )
                      ) {
                        deleteMut.mutate(u.id)
                      }
                    }}
                    className="text-destructive hover:text-destructive hover:bg-destructive/10"
                    aria-label="Delete user"
                    title={isMe ? "You can't delete yourself" : "Delete user"}
                  >
                    <Icon name="close" size={12} />
                  </Button>
                </>
              )}
              {u.status === "pending" && (
                <>
                  <Button
                    type="button"
                    size="sm"
                    onClick={() => approveMut.mutate(u.id)}
                    disabled={approveMut.isPending}
                  >
                    Approve
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    onClick={() => denyMut.mutate(u.id)}
                    disabled={denyMut.isPending || isMe}
                  >
                    Deny
                  </Button>
                </>
              )}
              {u.status === "denied" && (
                <Button
                  type="button"
                  size="sm"
                  onClick={() => approveMut.mutate(u.id)}
                  disabled={approveMut.isPending}
                >
                  Approve
                </Button>
              )}
            </div>
          )
        })}
      </div>
    </>
  )
}
