import { useEffect, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import type { ReactNode } from "react"

import type { ApiError } from "@/api/client"
import type {
  OidcAdminSettings,
  OidcTestCheck,
  OidcTestResult,
  ProviderSlug,
} from "@/api/oidc"
import {
  fetchOidcAdminSettings,
  oidcAdminSettingsQueryKey,
  saveOidcAdminSettings,
  testOidcProvider,
} from "@/api/oidc"
import { useApiMutation } from "@/api/mutation"
import { Card, Field, Select } from "@/components/SettingsShared"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"

export function OidcPanel() {
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: oidcAdminSettingsQueryKey,
    queryFn: fetchOidcAdminSettings,
  })

  const [draft, setDraft] = useState<OidcAdminSettings | null>(null)
  // Per-provider "secret was touched" flags so an empty secret field
  // only clears the stored secret when the admin explicitly typed in
  // it (or clicked the clear button).
  const [secretTouched, setSecretTouched] = useState<
    Record<ProviderSlug, boolean>
  >({
    google: false,
    github: false,
    generic: false,
  })

  useEffect(() => {
    if (query.data && !draft) {
      // Prop→state sync on first load; not a cascading render.
      // Deliberate: setState inside an effect, syncing React state from an
      // external source. Was suppressed via react-hooks/set-state-in-effect;
      // Biome has no equivalent rule yet, so there is nothing to suppress.
      setDraft(query.data)
    }
  }, [query.data, draft])

  const saveMut = useApiMutation(saveOidcAdminSettings, {
    successToast: "OIDC settings saved.",
    onSuccess: (data) => {
      queryClient.setQueryData(oidcAdminSettingsQueryKey, data)
      setDraft(data)
      setSecretTouched({ google: false, github: false, generic: false })
    },
  })

  if (query.isLoading || !draft) {
    return (
      <>
        <h2 className="t-h2" style={{ marginBottom: 8 }}>
          OIDC / SSO
        </h2>
        <p className="t-small" style={{ fontStyle: "italic" }}>
          Loading…
        </p>
      </>
    )
  }

  const someEnabled =
    (draft.google.enabled &&
      draft.google.clientId !== "" &&
      (draft.google.clientSecretSet ||
        (draft.google.clientSecret ?? "") !== "")) ||
    (draft.github.enabled &&
      draft.github.clientId !== "" &&
      (draft.github.clientSecretSet ||
        (draft.github.clientSecret ?? "") !== "")) ||
    (draft.generic.enabled &&
      draft.generic.clientId !== "" &&
      draft.generic.issuerUri !== "")
  const canForceOnly = someEnabled

  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 8 }}>
        OIDC / SSO
      </h2>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: "italic" }}>
        Enable Google, GitHub, and a custom OpenID Connect provider
        independently — the login page shows a button for each one you turn on.
        Changes take effect on the next login, no restart required.
      </p>

      <Card>
        <div style={{ display: "flex", alignItems: "center", gap: 14 }}>
          <div className="grow">
            <div className="t-item-title">Force SSO (hide local login)</div>
            <div className="t-item-sub">
              Hides the password form. Escape hatch:{" "}
              <span className="mono">/login?local=true</span>. Requires at least
              one provider enabled.
            </div>
          </div>
          <Switch
            checked={draft.forceOnly}
            disabled={!canForceOnly}
            onCheckedChange={(v) => setDraft({ ...draft, forceOnly: v })}
            aria-label="Force OIDC"
          />
        </div>
      </Card>

      <GooglePanel
        value={draft.google}
        onChange={(next) => setDraft({ ...draft, google: next })}
        redirectUri={draft.redirectUri}
        secretTouched={secretTouched.google}
        onSecretTouch={(v) => setSecretTouched({ ...secretTouched, google: v })}
      />

      <GitHubPanel
        value={draft.github}
        onChange={(next) => setDraft({ ...draft, github: next })}
        redirectUri={draft.redirectUri}
        secretTouched={secretTouched.github}
        onSecretTouch={(v) => setSecretTouched({ ...secretTouched, github: v })}
      />

      <GenericOidcPanel
        value={draft.generic}
        onChange={(next) => setDraft({ ...draft, generic: next })}
        redirectUri={draft.redirectUri}
        secretTouched={secretTouched.generic}
        onSecretTouch={(v) =>
          setSecretTouched({ ...secretTouched, generic: v })
        }
      />

      <h3 className="t-h3" style={{ marginTop: 24, marginBottom: 8 }}>
        Auto provisioning
      </h3>
      <Card>
        <div style={{ display: "flex", flexDirection: "column", gap: 14 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 14 }}>
            <div className="grow">
              <div className="t-item-title">
                Auto-create users on first login
              </div>
              <div className="t-item-sub">
                When off, unknown SSO users are rejected unless linked to an
                existing local account.
              </div>
            </div>
            <Switch
              checked={draft.autoProvision.enableAutoProvisioning}
              onCheckedChange={(v) =>
                setDraft({
                  ...draft,
                  autoProvision: {
                    ...draft.autoProvision,
                    enableAutoProvisioning: v,
                  },
                })
              }
            />
          </div>
          <div style={{ display: "flex", alignItems: "center", gap: 14 }}>
            <div className="grow">
              <div className="t-item-title">Link by email</div>
              <div className="t-item-sub">
                Permits linking an existing local account to an SSO identity on
                first login when emails match.
              </div>
            </div>
            <Switch
              checked={draft.autoProvision.allowLocalAccountLinking}
              onCheckedChange={(v) =>
                setDraft({
                  ...draft,
                  autoProvision: {
                    ...draft.autoProvision,
                    allowLocalAccountLinking: v,
                  },
                })
              }
            />
          </div>
          <div style={{ display: "flex", alignItems: "center", gap: 14 }}>
            <div className="grow">
              <div className="t-item-title">Require admin approval</div>
              <div className="t-item-sub">
                New SSO users are created in a pending state and cannot sign in
                until an admin approves them in Users &amp; roles. Disabling
                this later does not auto-promote already-pending users.
              </div>
            </div>
            <Switch
              checked={draft.autoProvision.requireAdminApproval}
              disabled={!draft.autoProvision.enableAutoProvisioning}
              onCheckedChange={(v) =>
                setDraft({
                  ...draft,
                  autoProvision: {
                    ...draft.autoProvision,
                    requireAdminApproval: v,
                  },
                })
              }
            />
          </div>
          <Field label="Default role for new users">
            <Select
              value={draft.autoProvision.defaultRole}
              onChange={(v) =>
                setDraft({
                  ...draft,
                  autoProvision: {
                    ...draft.autoProvision,
                    defaultRole: v === "admin" ? "admin" : "user",
                  },
                })
              }
              options={[
                { value: "user", label: "User" },
                { value: "admin", label: "Admin" },
              ]}
            />
          </Field>
        </div>
      </Card>

      <div style={{ display: "flex", gap: 10, marginTop: 20 }}>
        <Button
          onClick={() => saveMut.mutate(draft)}
          disabled={saveMut.isPending}
        >
          {saveMut.isPending ? "Saving…" : "Save all"}
        </Button>
      </div>
    </>
  )
}

type OAuthPresetValue = OidcAdminSettings["google"]

function PresetProviderPanel({
  title,
  slug,
  value,
  onChange,
  redirectUri,
  registerUrl,
  intro,
  secretTouched,
  onSecretTouch,
}: {
  title: string
  slug: ProviderSlug
  value: OAuthPresetValue
  onChange: (next: OAuthPresetValue) => void
  redirectUri: string
  registerUrl: string
  intro: ReactNode
  secretTouched: boolean
  onSecretTouch: (v: boolean) => void
}) {
  const [testResult, setTestResult] = useState<OidcTestResult | null>(null)
  const testMut = useMutation({
    mutationFn: () =>
      testOidcProvider(slug, {
        [slug]: {
          clientId: value.clientId,
          clientSecret: value.clientSecret ?? "",
        },
      }),
    onSuccess: (res) => {
      setTestResult(res)
      if (res.success) {
        toast.success("All critical checks passed.")
      } else {
        toast.error("One or more checks failed.")
      }
    },
    onError: (e) => toast.error((e as unknown as ApiError).message),
  })

  return (
    <>
      <h3 className="t-h3" style={{ marginTop: 24, marginBottom: 8 }}>
        {title}
      </h3>
      <Card>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 14,
            marginBottom: 10,
          }}
        >
          <div className="grow">
            <div className="t-item-title">Enable</div>
            <div className="t-item-sub">{intro}</div>
          </div>
          <Switch
            checked={value.enabled}
            disabled={
              value.clientId === "" ||
              (!value.clientSecretSet && (value.clientSecret ?? "") === "")
            }
            onCheckedChange={(v) => onChange({ ...value, enabled: v })}
          />
        </div>
        <p
          className="t-small"
          style={{ marginBottom: 10, fontStyle: "italic" }}
        >
          Register an OAuth app at{" "}
          <a href={registerUrl} target="_blank" rel="noreferrer">
            {registerUrl}
          </a>
          , set its redirect URL to{" "}
          <span className="mono">{redirectUri || "(set APP_URL)"}</span>, then
          paste the Client ID and Secret below.
        </p>
        <Field label="Client ID">
          <Input
            value={value.clientId}
            onChange={(e) => onChange({ ...value, clientId: e.target.value })}
          />
        </Field>
        <Field
          label={`Client secret${value.clientSecretSet ? " (stored — leave blank to keep)" : ""}`}
        >
          <Input
            type="password"
            autoComplete="new-password"
            placeholder={value.clientSecretSet ? "••••••••" : ""}
            onChange={(e) => {
              onSecretTouch(true)
              onChange({
                ...value,
                clientSecret: e.target.value,
                clientSecretSet: e.target.value !== "" || value.clientSecretSet,
              })
            }}
          />
          {value.clientSecretSet && !secretTouched && (
            <button
              type="button"
              className="t-small"
              style={{
                marginTop: 4,
                background: "none",
                border: "none",
                padding: 0,
                cursor: "pointer",
                color: "var(--color-accent)",
                alignSelf: "flex-start",
              }}
              onClick={() => {
                onSecretTouch(true)
                onChange({ ...value, clientSecret: "", clientSecretSet: false })
              }}
            >
              Clear stored secret
            </button>
          )}
        </Field>
        <div style={{ marginTop: 10 }}>
          <Button
            variant="outline"
            onClick={() => testMut.mutate()}
            disabled={testMut.isPending}
          >
            {testMut.isPending ? "Testing…" : "Test connection"}
          </Button>
        </div>
        {testResult && <TestResultBlock result={testResult} />}
      </Card>
    </>
  )
}

function GooglePanel(props: {
  value: OAuthPresetValue
  onChange: (v: OAuthPresetValue) => void
  redirectUri: string
  secretTouched: boolean
  onSecretTouch: (v: boolean) => void
}) {
  return (
    <PresetProviderPanel
      title="Google"
      slug="google"
      registerUrl="https://console.cloud.google.com/apis/credentials"
      intro="Lets users sign in with their Google account. Scopes and claims are baked in."
      {...props}
    />
  )
}

function GitHubPanel(props: {
  value: OAuthPresetValue
  onChange: (v: OAuthPresetValue) => void
  redirectUri: string
  secretTouched: boolean
  onSecretTouch: (v: boolean) => void
}) {
  return (
    <PresetProviderPanel
      title="GitHub"
      slug="github"
      registerUrl="https://github.com/settings/developers"
      intro="Lets users sign in with their GitHub account. Endpoints, scopes, and the user API are baked in."
      {...props}
    />
  )
}

function GenericOidcPanel({
  value,
  onChange,
  redirectUri,
  secretTouched,
  onSecretTouch,
}: {
  value: OidcAdminSettings["generic"]
  onChange: (v: OidcAdminSettings["generic"]) => void
  redirectUri: string
  secretTouched: boolean
  onSecretTouch: (v: boolean) => void
}) {
  const [testResult, setTestResult] = useState<OidcTestResult | null>(null)
  const testMut = useMutation({
    mutationFn: () =>
      testOidcProvider("generic", {
        generic: {
          clientId: value.clientId,
          clientSecret: value.clientSecret ?? "",
          issuerUri: value.issuerUri,
          scopes: value.scopes,
          claimMapping: value.claimMapping,
        },
      }),
    onSuccess: (res) => {
      setTestResult(res)
      if (res.success) {
        toast.success("All critical checks passed.")
      } else {
        toast.error("One or more checks failed.")
      }
    },
    onError: (e) => toast.error((e as unknown as ApiError).message),
  })

  const canEnable =
    value.clientId.trim() !== "" &&
    value.issuerUri.trim() !== "" &&
    value.claimMapping.username.trim() !== "" &&
    value.claimMapping.email.trim() !== "" &&
    value.claimMapping.name.trim() !== ""

  return (
    <>
      <h3 className="t-h3" style={{ marginTop: 24, marginBottom: 8 }}>
        Custom OIDC provider
      </h3>
      <Card>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 14,
            marginBottom: 10,
          }}
        >
          <div className="grow">
            <div className="t-item-title">Enable</div>
            <div className="t-item-sub">
              Authentik, Authelia, Keycloak, Pocket ID, or any OpenID Connect
              provider with a{" "}
              <span className="mono">/.well-known/openid-configuration</span>{" "}
              document.
            </div>
          </div>
          <Switch
            checked={value.enabled}
            disabled={!canEnable}
            onCheckedChange={(v) => onChange({ ...value, enabled: v })}
          />
        </div>
        <Field label="Provider display name">
          <Input
            value={value.providerName}
            onChange={(e) =>
              onChange({ ...value, providerName: e.target.value })
            }
            placeholder="Authentik"
          />
        </Field>
        <Field label="Issuer URI">
          <Input
            value={value.issuerUri}
            onChange={(e) => onChange({ ...value, issuerUri: e.target.value })}
            placeholder="https://auth.example.com/application/o/embookshelf/"
          />
        </Field>
        <Field label="Client ID">
          <Input
            value={value.clientId}
            onChange={(e) => onChange({ ...value, clientId: e.target.value })}
          />
        </Field>
        <Field
          label={`Client secret${value.clientSecretSet ? " (stored — leave blank to keep)" : ""}`}
        >
          <Input
            type="password"
            autoComplete="new-password"
            placeholder={value.clientSecretSet ? "••••••••" : ""}
            onChange={(e) => {
              onSecretTouch(true)
              onChange({
                ...value,
                clientSecret: e.target.value,
                clientSecretSet: e.target.value !== "" || value.clientSecretSet,
              })
            }}
          />
          {value.clientSecretSet && !secretTouched && (
            <button
              type="button"
              className="t-small"
              style={{
                marginTop: 4,
                background: "none",
                border: "none",
                padding: 0,
                cursor: "pointer",
                color: "var(--color-accent)",
                alignSelf: "flex-start",
              }}
              onClick={() => {
                onSecretTouch(true)
                onChange({ ...value, clientSecret: "", clientSecretSet: false })
              }}
            >
              Clear stored secret
            </button>
          )}
        </Field>
        <Field label="Scopes (space-separated)">
          <Input
            value={value.scopes}
            onChange={(e) => onChange({ ...value, scopes: e.target.value })}
            placeholder="openid profile email"
          />
        </Field>
        <div className="t-label" style={{ marginTop: 12 }}>
          Claim mapping
        </div>
        <Field label="Username claim">
          <Input
            value={value.claimMapping.username}
            onChange={(e) =>
              onChange({
                ...value,
                claimMapping: {
                  ...value.claimMapping,
                  username: e.target.value,
                },
              })
            }
          />
        </Field>
        <Field label="Email claim">
          <Input
            value={value.claimMapping.email}
            onChange={(e) =>
              onChange({
                ...value,
                claimMapping: { ...value.claimMapping, email: e.target.value },
              })
            }
          />
        </Field>
        <Field label="Display name claim">
          <Input
            value={value.claimMapping.name}
            onChange={(e) =>
              onChange({
                ...value,
                claimMapping: { ...value.claimMapping, name: e.target.value },
              })
            }
          />
        </Field>
        <p className="t-small" style={{ marginTop: 10, fontStyle: "italic" }}>
          Redirect URI:{" "}
          <span className="mono">{redirectUri || "(set APP_URL)"}</span>
        </p>
        <div style={{ marginTop: 10 }}>
          <Button
            variant="outline"
            onClick={() => testMut.mutate()}
            disabled={testMut.isPending}
          >
            {testMut.isPending ? "Testing…" : "Test connection"}
          </Button>
        </div>
        {testResult && <TestResultBlock result={testResult} />}
      </Card>
    </>
  )
}

function TestResultBlock({ result }: { result: OidcTestResult }) {
  return (
    <div style={{ marginTop: 14 }}>
      <div
        style={{
          marginTop: 10,
          display: "flex",
          flexDirection: "column",
          gap: 6,
        }}
      >
        {result.checks.map((c: OidcTestCheck, i: number) => (
          <div
            key={i}
            style={{
              display: "grid",
              gridTemplateColumns: "70px 1fr",
              gap: 10,
              fontSize: 13,
              padding: "6px 10px",
              border: "1px solid var(--color-rule-soft)",
              borderRadius: 2,
              background: "var(--color-paper-0)",
            }}
          >
            <span
              className="mono"
              style={{
                color:
                  c.status === "PASS"
                    ? "oklch(0.58 0.12 140)"
                    : c.status === "WARN"
                      ? "oklch(0.72 0.14 70)"
                      : "oklch(0.62 0.22 25)",
                fontWeight: 600,
              }}
            >
              {c.status}
            </span>
            <div>
              <div style={{ fontWeight: 500 }}>{c.name}</div>
              <div className="t-item-sub">{c.message}</div>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Backups (informational)
// ---------------------------------------------------------------------------
