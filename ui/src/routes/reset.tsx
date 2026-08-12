import { useState } from "react"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import type { FormEvent } from "react"

import { AuthShell } from "@/components/AuthShell"
import { confirmPasswordReset, passwordResetVerifyQuery } from "@/api/auth"
import { useApiMutation } from "@/api/mutation"
import { useApiQuery } from "@/api/query"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

type ResetSearch = { token?: string }

export const Route = createFileRoute("/reset")({
  validateSearch: (raw: Record<string, unknown>): ResetSearch => ({
    token: typeof raw.token === "string" ? raw.token : undefined,
  }),
  component: ResetPasswordPage,
})

function ResetPasswordPage() {
  const { token } = Route.useSearch()
  const navigate = useNavigate()

  // Pre-flight verify so we render an "expired link" panel without
  // making the user fill the form twice. Disabled until we actually
  // have a token in the URL — the same component handles the "no
  // token at all" case below.
  const verify = useApiQuery(passwordResetVerifyQuery(token ?? ""), {
    enabled: !!token,
  })

  const [next, setNext] = useState("")
  const [confirm, setConfirm] = useState("")

  const confirmMut = useApiMutation(confirmPasswordReset, {
    successToast: "Password updated. Sign in with your new password.",
    onSuccess: () => {
      void navigate({ to: "/login", replace: true })
    },
    errorToast: (err) => err.message || "Could not reset password.",
  })

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    if (next !== confirm) {
      toast.error("Passwords do not match.")
      return
    }
    if (!token) return
    confirmMut.mutate({ token, newPassword: next })
  }

  const isInvalid = !token || verify.data?.valid === false

  return (
    <AuthShell>
          <h1 className="t-h2" style={{ marginBottom: 4 }}>
            Choose a new password
          </h1>

          {verify.isLoading && (
            <p className="t-small" style={{ fontStyle: "italic" }}>
              Verifying link…
            </p>
          )}

          {!verify.isLoading && isInvalid && (
            <>
              <p
                className="t-small"
                style={{ marginBottom: 16, fontStyle: "italic" }}
              >
                This reset link is invalid or has expired. Request a new one.
              </p>
              <Button asChild className="w-full">
                <Link to="/forgot-password">Request a new link</Link>
              </Button>
            </>
          )}

          {!verify.isLoading && verify.data?.valid && (
            <>
              {verify.data.email && (
                <p
                  className="t-small"
                  style={{ marginBottom: 20, fontStyle: "italic" }}
                >
                  for <strong>{verify.data.email}</strong>
                </p>
              )}
              <form
                onSubmit={onSubmit}
                style={{ display: "flex", flexDirection: "column", gap: 14 }}
              >
                <label
                  style={{ display: "flex", flexDirection: "column", gap: 6 }}
                >
                  <span className="t-label">New password</span>
                  <Input
                    type="password"
                    autoComplete="new-password"
                    minLength={8}
                    required
                    value={next}
                    onChange={(e) => setNext(e.target.value)}
                  />
                </label>
                <label
                  style={{ display: "flex", flexDirection: "column", gap: 6 }}
                >
                  <span className="t-label">Confirm new password</span>
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
                  disabled={confirmMut.isPending}
                >
                  {confirmMut.isPending ? "Updating…" : "Update password"}
                </Button>
              </form>
            </>
          )}

          <div style={{ textAlign: "center", marginTop: 16 }}>
            <Link
              to="/login"
              className="t-small"
              style={{
                color: "var(--color-ink-2)",
                textDecoration: "underline",
                fontSize: 12,
              }}
            >
              Back to sign in
            </Link>
          </div>
    </AuthShell>
  )
}
