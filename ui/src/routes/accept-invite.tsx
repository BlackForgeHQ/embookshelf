import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Link, createFileRoute, useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import type { FormEvent } from "react"

import { AuthShell } from "@/components/AuthShell"
import { acceptInvite, meQueryKey } from "@/api/auth"
import { useApiMutation } from "@/api/mutation"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

type AcceptInviteSearch = { token?: string }

export const Route = createFileRoute("/accept-invite")({
  validateSearch: (raw: Record<string, unknown>): AcceptInviteSearch => ({
    token: typeof raw.token === "string" ? raw.token : undefined,
  }),
  component: AcceptInvitePage,
})

function AcceptInvitePage() {
  const { token } = Route.useSearch()
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const [name, setName] = useState("")
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")

  // Server sets the session cookie on success; prime the cache with the
  // returned user so the dashboard renders without an extra round-trip.
  const acceptMut = useApiMutation(acceptInvite, {
    successToast: "Welcome aboard.",
    onSuccess: (user) => {
      queryClient.setQueryData(meQueryKey, user)
      void navigate({ to: "/", replace: true })
    },
    errorToast: (err) => err.message || "Could not accept invite.",
  })

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    if (!token) return
    if (password !== confirm) {
      toast.error("Passwords do not match.")
      return
    }
    acceptMut.mutate({ token, name: name.trim(), password })
  }

  return (
    <AuthShell>
          <h1 className="t-h2" style={{ marginBottom: 4 }}>
            Accept your invite
          </h1>
          <p
            className="t-small"
            style={{ marginBottom: 24, fontStyle: "italic" }}
          >
            You have been invited to join this embookshelf instance. Set your
            display name and password to finish creating your account.
          </p>

          {!token ? (
            <>
              <p
                className="t-small"
                style={{ marginBottom: 16, fontStyle: "italic" }}
              >
                This invite link is missing its token. Ask your administrator
                for a fresh invite.
              </p>
              <Button asChild className="w-full">
                <Link to="/login">Back to sign in</Link>
              </Button>
            </>
          ) : (
            <form
              onSubmit={onSubmit}
              style={{ display: "flex", flexDirection: "column", gap: 14 }}
            >
              <label
                style={{ display: "flex", flexDirection: "column", gap: 6 }}
              >
                <span className="t-label">Display name</span>
                <Input
                  autoComplete="name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                />
              </label>
              <label
                style={{ display: "flex", flexDirection: "column", gap: 6 }}
              >
                <span className="t-label">Password</span>
                <Input
                  type="password"
                  autoComplete="new-password"
                  minLength={8}
                  required
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
              </label>
              <label
                style={{ display: "flex", flexDirection: "column", gap: 6 }}
              >
                <span className="t-label">Confirm password</span>
                <Input
                  type="password"
                  autoComplete="new-password"
                  minLength={8}
                  required
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                />
              </label>
              <Button
                type="submit"
                className="mt-1 w-full"
                disabled={acceptMut.isPending}
              >
                {acceptMut.isPending ? "Working…" : "Create account"}
              </Button>
            </form>
          )}
    </AuthShell>
  )
}
