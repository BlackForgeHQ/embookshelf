import { useEffect, useMemo, useState } from "react"
import { createFileRoute, Link, redirect } from "@tanstack/react-router"
import type { FormEvent } from "react"

import { AuthShell } from "@/components/AuthShell"
import type { ApiError } from "@/api/client"
import { meQuery, oidcConfigQuery, signupStatusQuery } from "@/api/auth"
import { apiQueryOptions, useApiQuery } from "@/api/query"
import { useLogin } from "@/hooks/useLogin"
import { useSignup } from "@/hooks/useSignup"
import { Notice } from "@/components/Notice"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

type LoginSearch = {
  next?: string
  mode?: "login" | "signup"
  oidcError?: string
  local?: boolean
}

// OIDC error codes the callback handler may redirect with. Kept in one
// place so new failure paths pick up a friendly message automatically.
const OIDC_ERROR_MESSAGES: Record<string, string> = {
  stateMismatch:
    "The SSO login timed out or was tampered with. Please try again.",
  userNotProvisioned:
    "Your SSO account is not authorised for this instance. Contact an administrator.",
  disabled: "SSO is not currently enabled on this instance.",
  notConfigured: "SSO is not configured on this instance.",
  invalidRequest:
    "The provider returned an incomplete response. Please try again.",
  // Names the claim, because this one is an admin's misconfiguration
  // rather than anything the person signing in can retry their way out
  // of: the provider answered, it just did not send an email.
  emailClaimMissing:
    "The identity provider did not return an email claim. An administrator needs to check the claim mapping and scopes in Settings → SSO.",
  unknown: "Sign-in failed. Please try again or use a local account.",
}

export const Route = createFileRoute("/login")({
  validateSearch: (raw: Record<string, unknown>): LoginSearch => ({
    next: typeof raw.next === "string" ? raw.next : undefined,
    mode: raw.mode === "signup" ? "signup" : "login",
    oidcError: typeof raw.oidcError === "string" ? raw.oidcError : undefined,
    local: raw.local === true || raw.local === "true",
  }),
  // Skip rendering the page entirely for already-authenticated users.
  beforeLoad: async ({ context, search }) => {
    const me = await context.queryClient.ensureQueryData(
      apiQueryOptions(meQuery)
    )
    if (me) {
      throw redirect({ to: safeNext(search.next) })
    }
  },
  component: LoginPage,
})

// safeNext rejects external redirects and the login page itself, collapsing
// those back to the dashboard.
function safeNext(raw: string | undefined): string {
  if (!raw) return "/"
  if (!raw.startsWith("/")) return "/"
  if (raw.startsWith("/login")) return "/"
  return raw
}

function LoginPage() {
  const { next, mode: modeSearch, oidcError, local } = Route.useSearch()

  // /auth/signup reports whether the first-run bootstrap is still available.
  // Shown as a toggle at the bottom of the card when it is.
  const signupOpen = useApiQuery(signupStatusQuery)

  const oidc = useApiQuery(oidcConfigQuery)

  const [mode, setMode] = useState<"login" | "signup">(modeSearch ?? "login")
  const [email, setEmail] = useState("")
  const [name, setName] = useState("")
  const [password, setPassword] = useState("")

  const target = safeNext(next)
  const loginMut = useLogin(target)
  const signupMut = useSignup(target)

  const active = mode === "signup" ? signupMut : loginMut
  const error = active.error as ApiError | null

  // Force-only mode: when exactly one provider is enabled we can
  // auto-redirect to it. When multiple are enabled we still show the
  // login card (but hide the local form) so the user picks one. The
  // `?local=true` escape hatch and any OIDC error bail out to the
  // normal card — matches the spec recovery path.
  // Memoized so the useEffect dep array below doesn't see a new array
  // reference on every render (the `?? []` fallback allocates fresh).
  const providers = useMemo(() => oidc.data?.providers ?? [], [oidc.data])
  const canAutoRedirect =
    oidc.data?.forceOnly === true &&
    providers.length === 1 &&
    !local &&
    !oidcError
  useEffect(() => {
    if (canAutoRedirect && mode === "login" && providers[0]) {
      window.location.href = providers[0].loginUrl
    }
  }, [canAutoRedirect, mode, providers])

  // Two paths can hide the local form:
  // - OIDC force-only: at least one OIDC provider enabled and admin
  //   flipped the "force SSO" toggle.
  // - Forward-auth: upstream proxy gates the deployment and admin
  //   asked to hide the form.
  // The `?local=true` escape hatch and any OIDC error always show the
  // form so admins can recover from a misconfiguration.
  const fwdAuthHidesLocal =
    oidc.data?.forwardAuthEnabled === true &&
    oidc.data.hideLocalLogin === true &&
    !local &&
    !oidcError
  const oidcHidesLocal =
    oidc.data?.forceOnly === true &&
    providers.length > 0 &&
    !local &&
    !oidcError
  const hideLocalForm = oidcHidesLocal || fwdAuthHidesLocal

  const oidcErrorMessage = oidcError
    ? (OIDC_ERROR_MESSAGES[oidcError] ?? OIDC_ERROR_MESSAGES.unknown)
    : null

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    if (mode === "signup") {
      signupMut.reset()
      signupMut.mutate({ email, name, password })
    } else {
      loginMut.reset()
      loginMut.mutate({ email, password })
    }
  }

  const showSignupToggle = signupOpen.data?.enabled

  return (
    <AuthShell
      footer={
        showSignupToggle ? (
          <div style={{ textAlign: "center", marginTop: 16 }}>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => {
                setMode((m) => (m === "signup" ? "login" : "signup"))
                loginMut.reset()
                signupMut.reset()
              }}
            >
              {mode === "signup"
                ? "I already have an account"
                : "First-run? Create the admin account"}
            </Button>
          </div>
        ) : null
      }
    >
          <h1 className="t-h2" style={{ marginBottom: 4 }}>
            {mode === "signup" ? "Create the first account" : "Sign in"}
          </h1>
          <p
            className="t-small"
            style={{ marginBottom: 24, fontStyle: "italic" }}
          >
            {mode === "signup"
              ? "You are the first user. This account will be the admin."
              : "Enter your credentials to continue."}
          </p>

          {oidcErrorMessage && (
            <Notice className="mb-4">{oidcErrorMessage}</Notice>
          )}

          {error && <Notice className="mb-4">{error.message}</Notice>}

          {fwdAuthHidesLocal && (
            <div
              style={{
                padding: "14px 16px",
                border: "1px solid var(--color-rule-soft)",
                background: "var(--color-paper-1)",
                borderRadius: 3,
                fontSize: 13,
                lineHeight: 1.5,
                color: "var(--color-ink-2)",
                marginBottom: 16,
              }}
              role="status"
            >
              This deployment uses upstream SSO via your reverse proxy. Sign in
              through your provider. embookshelf trusts the session it
              forwards.
            </div>
          )}

          <form
            onSubmit={onSubmit}
            style={{ display: "flex", flexDirection: "column", gap: 14 }}
          >
            {!hideLocalForm && (
              <>
                {mode === "signup" && (
                  <Field label="Name">
                    <Input
                      autoComplete="name"
                      value={name}
                      onChange={(e) => setName(e.target.value)}
                    />
                  </Field>
                )}
                <Field label="Email">
                  <Input
                    type="email"
                    autoComplete="email"
                    required
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                  />
                </Field>
                <Field label="Password">
                  <Input
                    type="password"
                    autoComplete={
                      mode === "signup" ? "new-password" : "current-password"
                    }
                    required
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                  />
                </Field>

                {/*
                  The one place email-enabled is read from something
                  other than appConfigQuery, and it has to be: GET
                  /api/v1/config sits behind the auth middleware
                  (router.go), so this page would get a 401 asking it.
                  signup-status is the pre-auth source. Don't "unify"
                  this into the config query — see #171.
                */}
                {mode === "login" && signupOpen.data?.emailEnabled && (
                  <div style={{ marginTop: -4, textAlign: "right" }}>
                    <Link
                      to="/forgot-password"
                      className="t-small"
                      style={{
                        color: "var(--color-ink-2)",
                        textDecoration: "underline",
                        fontSize: 12,
                      }}
                    >
                      Forgot password?
                    </Link>
                  </div>
                )}

                <Button
                  type="submit"
                  className="mt-1 w-full"
                  disabled={active.isPending}
                >
                  {active.isPending
                    ? "Working…"
                    : mode === "signup"
                      ? "Create account"
                      : "Sign in"}
                </Button>
              </>
            )}

            {mode === "login" && providers.length > 0 && (
              <>
                {!hideLocalForm && (
                  <div
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 12,
                      marginTop: 4,
                    }}
                  >
                    <div
                      style={{
                        flex: 1,
                        height: 1,
                        background: "var(--color-rule-soft)",
                      }}
                    />
                    <span
                      className="t-small"
                      style={{ color: "var(--color-ink-3)" }}
                    >
                      or
                    </span>
                    <div
                      style={{
                        flex: 1,
                        height: 1,
                        background: "var(--color-rule-soft)",
                      }}
                    />
                  </div>
                )}
                {providers.map((p) => (
                  <Button
                    key={p.slug}
                    asChild
                    variant="outline"
                    className="w-full"
                  >
                    <a href={p.loginUrl}>Sign in with {p.name}</a>
                  </Button>
                ))}
              </>
            )}
          </form>
    </AuthShell>
  )
}

function Field({
  label,
  children,
}: {
  label: string
  children: React.ReactNode
}) {
  return (
    <label style={{ display: "flex", flexDirection: "column", gap: 6 }}>
      <span className="t-label">{label}</span>
      {children}
    </label>
  )
}
