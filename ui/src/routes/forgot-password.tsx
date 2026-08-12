import { useState } from "react"
import { Link, createFileRoute } from "@tanstack/react-router"
import type { FormEvent } from "react"

import { AuthShell } from "@/components/AuthShell"
import { requestPasswordReset } from "@/api/auth"
import { useApiMutation } from "@/api/mutation"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

export const Route = createFileRoute("/forgot-password")({
  component: ForgotPasswordPage,
})

function ForgotPasswordPage() {
  const [email, setEmail] = useState("")
  const [submitted, setSubmitted] = useState(false)

  // No success toast — the server returns 202 regardless of whether the
  // email matches a real account. Showing the same flash either way keeps
  // the page from leaking which addresses are registered.
  const requestMut = useApiMutation(requestPasswordReset, {
    onSuccess: () => setSubmitted(true),
    errorToast: (err) => err.message || "Could not request a reset link.",
  })

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    requestMut.mutate(email.trim())
  }

  return (
    <AuthShell>
          <h1 className="t-h2" style={{ marginBottom: 4 }}>
            Reset your password
          </h1>
          <p
            className="t-small"
            style={{ marginBottom: 24, fontStyle: "italic" }}
          >
            We will email you a link if an account is registered with that
            address.
          </p>

          {submitted ? (
            <div
              style={{
                padding: "12px 16px",
                border: "1px solid var(--color-rule-soft)",
                background: "var(--color-paper-1)",
                borderRadius: 2,
                fontSize: 13,
                marginBottom: 16,
                lineHeight: 1.55,
              }}
            >
              If an account exists for <strong>{email}</strong>, a reset link
              is on its way. Check your inbox (and spam folder).
            </div>
          ) : (
            <form
              onSubmit={onSubmit}
              style={{ display: "flex", flexDirection: "column", gap: 14 }}
            >
              <label style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                <span className="t-label">Email</span>
                <Input
                  type="email"
                  autoComplete="email"
                  required
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                />
              </label>
              <Button
                type="submit"
                className="mt-1 w-full"
                disabled={requestMut.isPending}
              >
                {requestMut.isPending ? "Sending…" : "Send reset link"}
              </Button>
            </form>
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
