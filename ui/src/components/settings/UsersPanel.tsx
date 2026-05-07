import { useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"

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
import { useApiMutation } from "@/api/mutation"
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

  const createMut = useApiMutation(createSettingsUser, {
    successToast: "User created.",
    onSuccess: () => {
      setCreateOpen(false)
      setDraft({ email: "", name: "", password: "", role: "user" })
    },
  })

  const roleMut = useApiMutation(updateSettingsUserRole, {
    successToast: (_, { role }) =>
      role === "admin"
        ? "User promoted to admin."
        : "User demoted to regular user.",
  })

  const deleteMut = useApiMutation(deleteSettingsUser, {
    successToast: "User deleted.",
  })

  const approveMut = useApiMutation(approveSettingsUser, {
    successToast: "User approved.",
  })

  const denyMut = useApiMutation(denySettingsUser, {
    successToast: "User denied.",
  })

  if (!isAdmin) return <AdminGate label="Users & roles" />

  return (
    <>
      <div className="mb-4 flex items-center justify-between">
        <h2 className="font-serif text-xl font-medium text-foreground">
          Users &amp; roles
        </h2>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => setCreateOpen((v) => !v)}
        >
          <Icon name="plus" size={13} /> New user
        </Button>
      </div>
      <p className="mb-6 text-sm text-muted-foreground italic">
        Admins see every settings pane; regular users see only Account, Reading
        preferences, Device sync, and About.
      </p>

      {createOpen && (
        <Card>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              createMut.mutate(draft)
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
            <div className="mt-2 flex items-center justify-end gap-2">
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
        <div className="text-sm text-muted-foreground italic">
          Loading users…
        </div>
      )}

      <div className="mt-4 flex flex-col gap-2">
        {sortedUsers.map((u) => {
          const isMe = u.id === me?.id
          return (
            <div
              key={u.id}
              data-row="user"
              data-user-id={u.id}
              className="flex items-center gap-4 rounded-lg border border-border bg-card p-3 shadow-sm"
            >
              <Avatar initials={u.initials} size={32} />
              {u.status !== "active" && (
                <span
                  data-row-status={u.status}
                  className={`rounded-full px-2 py-0.5 text-[11px] font-medium ${u.status === "pending" ? "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400" : "bg-muted text-muted-foreground"}`}
                >
                  {u.status === "pending" ? "Pending" : "Denied"}
                </span>
              )}
              <div className="min-w-0 flex-1">
                <div className="truncate text-[14px] leading-snug font-medium">
                  {u.display}{" "}
                  {isMe && (
                    <span className="ml-2 text-[10px] tracking-wider text-muted-foreground uppercase">
                      you
                    </span>
                  )}
                </div>
                <div className="mt-0.5 truncate text-xs text-muted-foreground">
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
                    className="text-destructive hover:bg-destructive/10 hover:text-destructive"
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
