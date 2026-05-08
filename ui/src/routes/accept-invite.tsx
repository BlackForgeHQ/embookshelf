import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Link, createFileRoute, useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import type { FormEvent } from "react"

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
    <main
      style={{
        minHeight: "100vh",
        background: "var(--color-paper-1)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: 32,
      }}
    >
      <div style={{ width: "100%", maxWidth: 420 }}>
        <div
          style={{
            textAlign: "center",
            marginBottom: 24,
            display: "flex",
            flexDirection: "column",
            alignItems: "center",
            gap: 10,
          }}
        >
          <img
            src="/logo.png"
            alt="embookshelf"
            style={{
              width: 40,
              height: 40,
              objectFit: "contain",
              borderRadius: 2,
            }}
          />
          <div
            style={{
              fontFamily: "var(--font-serif)",
              fontSize: 22,
              fontWeight: 500,
              letterSpacing: "-0.01em",
            }}
          >
            embookshelf
          </div>
        </div>

        <div
          style={{
            background: "var(--color-paper-0)",
            border: "1px solid var(--color-rule-soft)",
            padding: 32,
            borderRadius: 3,
            boxShadow: "0 12px 32px -8px oklch(0.2 0.02 60 / 0.14)",
          }}
        >
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
        </div>
      </div>
    </main>
  )
}
