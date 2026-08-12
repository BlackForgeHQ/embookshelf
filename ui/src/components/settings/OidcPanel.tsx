import type { ReactNode } from "react"

import type {
  OidcAdminSettings,
  OidcTestCheck,
  OidcTestResult,
  ProviderSlug,
} from "@/api/oidc"
import {
  oidcAdminSettingsQuery,
  saveOidcAdminSettings,
  testOidcProvider,
} from "@/api/oidc"
import type { SecretField } from "@/hooks/useSettingsDraft"
import { useConnectionTest } from "@/hooks/useConnectionTest"
import { useSettingsDraft } from "@/hooks/useSettingsDraft"
import {
  Card,
  ConnectionTestReport,
  Field,
  PanelHeader,
  PanelLoading,
  Select,
} from "@/components/SettingsShared"
import { SecretInput } from "@/components/settings/SecretInput"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"

// The shape rendered before the first payload lands. Mirrors the server's
// defaults so a fresh instance with no row stored looks the same as one
// that answered.
const emptyForm: OidcAdminSettings = {
  forceOnly: false,
  autoProvision: {
    enableAutoProvisioning: false,
    allowLocalAccountLinking: false,
    defaultRole: "user",
    requireAdminApproval: true,
  },
  google: { enabled: false, clientId: "", clientSecretSet: false },
  github: { enabled: false, clientId: "", clientSecretSet: false },
  generic: {
    enabled: false,
    providerName: "",
    clientId: "",
    clientSecretSet: false,
    issuerUri: "",
    scopes: "openid profile email",
    claimMapping: {
      username: "preferred_username",
      email: "email",
      name: "name",
    },
  },
  redirectUri: "",
}

const INTRO = `Enable Google, GitHub, and a custom OpenID Connect provider independently. The login page shows a button for each one you turn on. Changes take effect on the next login, no restart required.`

export function OidcPanel() {
  // Three write-only secrets, one per slug. The panel used to carry a
  // parallel record of "was this one touched" precisely because the draft
  // could not tell an untouched field from an erased one; the module
  // makes that distinction part of the secret itself.
  const draft = useSettingsDraft({
    queryKey: oidcAdminSettingsQuery.key,
    queryFn: oidcAdminSettingsQuery.fn,
    initial: emptyForm,
    save: saveOidcAdminSettings,
    successToast: "OIDC settings saved.",
    toPayload: (form, secrets) => ({
      ...form,
      google: withSecret(
        form.google,
        secrets.value("google"),
        secrets.stillSet("google", form.google.clientSecretSet)
      ),
      github: withSecret(
        form.github,
        secrets.value("github"),
        secrets.stillSet("github", form.github.clientSecretSet)
      ),
      generic: withSecret(
        form.generic,
        secrets.value("generic"),
        secrets.stillSet("generic", form.generic.clientSecretSet)
      ),
    }),
  })

  const form = draft.value

  if (draft.loading) {
    return (
      <>
        <PanelHeader title="OIDC / SSO">{INTRO}</PanelHeader>
        <PanelLoading />
      </>
    )
  }

  // Force-SSO is only safe once at least one provider could actually sign
  // someone in — a stored secret, or one typed but not yet saved.
  const usable = (slug: ProviderSlug) => {
    const p = form[slug]
    return (
      p.enabled &&
      p.clientId !== "" &&
      (p.clientSecretSet || draft.secret(slug).value !== "")
    )
  }
  const canForceOnly =
    usable("google") ||
    usable("github") ||
    (form.generic.enabled &&
      form.generic.clientId !== "" &&
      form.generic.issuerUri !== "")

  return (
    <>
      <PanelHeader title="OIDC / SSO">{INTRO}</PanelHeader>

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
            checked={form.forceOnly}
            disabled={!canForceOnly}
            onCheckedChange={(v) => draft.patch("forceOnly", v)}
            aria-label="Force OIDC"
          />
        </div>
      </Card>

      <GooglePanel
        value={form.google}
        onChange={(next) => draft.patch("google", next)}
        redirectUri={form.redirectUri}
        secret={draft.secret("google")}
      />

      <GitHubPanel
        value={form.github}
        onChange={(next) => draft.patch("github", next)}
        redirectUri={form.redirectUri}
        secret={draft.secret("github")}
      />

      <GenericOidcPanel
        value={form.generic}
        onChange={(next) => draft.patch("generic", next)}
        redirectUri={form.redirectUri}
        secret={draft.secret("generic")}
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
              checked={form.autoProvision.enableAutoProvisioning}
              onCheckedChange={(v) =>
                draft.patch("autoProvision", {
                  ...form.autoProvision,
                  enableAutoProvisioning: v,
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
              checked={form.autoProvision.allowLocalAccountLinking}
              onCheckedChange={(v) =>
                draft.patch("autoProvision", {
                  ...form.autoProvision,
                  allowLocalAccountLinking: v,
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
              checked={form.autoProvision.requireAdminApproval}
              disabled={!form.autoProvision.enableAutoProvisioning}
              onCheckedChange={(v) =>
                draft.patch("autoProvision", {
                  ...form.autoProvision,
                  requireAdminApproval: v,
                })
              }
            />
          </div>
          <Field label="Default role for new users">
            <Select
              value={form.autoProvision.defaultRole}
              onChange={(v) =>
                draft.patch("autoProvision", {
                  ...form.autoProvision,
                  defaultRole: v === "admin" ? "admin" : "user",
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
        <Button onClick={draft.save} disabled={draft.saving}>
          {draft.saving ? "Saving…" : "Save all"}
        </Button>
      </div>
    </>
  )
}

type OAuthPresetValue = OidcAdminSettings["google"]

// withSecret folds a secret draft back into a provider block. The pair
// (`clientSecret`, `clientSecretSet`) is what `resolveSecret` on the
// server reads: non-empty wins, empty-and-set keeps, empty-and-unset
// clears. Written once here rather than three times inline.
function withSecret<TValue extends { clientSecretSet: boolean }>(
  value: TValue,
  clientSecret: string,
  clientSecretSet: boolean
): TValue & { clientSecret: string } {
  return { ...value, clientSecret, clientSecretSet }
}

// useOidcTest is the connection test for one provider. All three probes
// answer the same shape, so the verdict sentence lives here rather than
// in each panel.
function useOidcTest(slug: ProviderSlug) {
  return useConnectionTest({
    test: testOidcProvider(slug),
    read: (res: OidcTestResult) => ({
      ok: res.success,
      message: res.success
        ? "All critical checks passed."
        : "One or more checks failed.",
    }),
  })
}

function PresetProviderPanel({
  title,
  slug,
  value,
  onChange,
  redirectUri,
  registerUrl,
  intro,
  secret,
}: {
  title: string
  slug: ProviderSlug
  value: OAuthPresetValue
  onChange: (next: OAuthPresetValue) => void
  redirectUri: string
  registerUrl: string
  intro: ReactNode
  secret: SecretField
}) {
  const test = useOidcTest(slug)
  // The probe runs against what is on screen, not what is stored — that is
  // the point of testing before saving. An untouched secret sends empty,
  // and the server falls back to the stored one.
  const testBody = {
    [slug]: { clientId: value.clientId, clientSecret: secret.value },
  }

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
              (!value.clientSecretSet && secret.value === "")
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
        <SecretInput
          label="Client secret"
          // Three providers render a "Client secret" field on one page,
          // so the removal control names whose secret it drops.
          noun={`${title} client secret`}
          secret={secret}
          stored={value.clientSecretSet}
        />
        <div style={{ marginTop: 10 }}>
          <Button
            variant="outline"
            onClick={() => test.run(testBody)}
            disabled={test.running}
          >
            {test.running ? "Testing…" : "Test connection"}
          </Button>
        </div>
        <ConnectionTestReport outcome={test.outcome}>
          {test.outcome?.data && <TestChecks result={test.outcome.data} />}
        </ConnectionTestReport>
      </Card>
    </>
  )
}

function GooglePanel(props: {
  value: OAuthPresetValue
  onChange: (v: OAuthPresetValue) => void
  redirectUri: string
  secret: SecretField
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
  secret: SecretField
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
  secret,
}: {
  value: OidcAdminSettings["generic"]
  onChange: (v: OidcAdminSettings["generic"]) => void
  redirectUri: string
  secret: SecretField
}) {
  const test = useOidcTest("generic")
  const testBody = {
    generic: {
      clientId: value.clientId,
      clientSecret: secret.value,
      issuerUri: value.issuerUri,
      scopes: value.scopes,
      claimMapping: value.claimMapping,
    },
  }

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
        <SecretInput
          label="Client secret"
          noun={`${value.providerName || "custom provider"} client secret`}
          secret={secret}
          stored={value.clientSecretSet}
        />
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
            onClick={() => test.run(testBody)}
            disabled={test.running}
          >
            {test.running ? "Testing…" : "Test connection"}
          </Button>
        </div>
        <ConnectionTestReport outcome={test.outcome}>
          {test.outcome?.data && <TestChecks result={test.outcome.data} />}
        </ConnectionTestReport>
      </Card>
    </>
  )
}

// TestChecks is the OIDC-specific detail under the shared report: one
// row per probe the server ran, so a failure names which step failed.
function TestChecks({ result }: { result: OidcTestResult }) {
  return (
    <div>
      <div
        style={{
          display: "flex",
          flexDirection: "column",
          gap: 6,
        }}
      >
        {result.checks.map((c: OidcTestCheck, i: number) => (
          <div
            // biome-ignore lint/suspicious/noArrayIndexKey: the probe returns a fixed ordered checklist; position is the check
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
                    ? "var(--color-ok)"
                    : c.status === "WARN"
                      ? "var(--color-warn)"
                      : "var(--color-accent-ink)",
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
