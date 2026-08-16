import { useMemo } from "react"
import type { FormEvent } from "react"

import type { ForwardAuthSettings } from "@/api/forwardAuth"
import {
  forwardAuthSettingsQuery,
  saveForwardAuthSettings,
} from "@/api/forwardAuth"
import { useSettingsDraft } from "@/hooks/useSettingsDraft"
import {
  Card,
  Field,
  PanelHeader,
  PanelLoading,
  Toggle,
} from "@/components/SettingsShared"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

// emptyForm seeds the form before the GET completes. Mirrors the
// server's DefaultForwardAuthConfig — disabled, Authelia headers.
// ADR-0022.
const emptyForm: ForwardAuthSettings = {
  enabled: false,
  trustedProxyCIDRs: [],
  headers: {
    user: "Remote-User",
    email: "Remote-Email",
    name: "Remote-Name",
    groups: "Remote-Groups",
  },
  logoutUrl: "",
  hideLocalLogin: false,
}

// The intro sits outside the component so the loading state and the form
// open with the same words.
const INTRO = (
  <>
    Trust identity headers from an upstream reverse proxy that already
    terminates SSO (Authelia, oauth2-proxy, Traefik forwardAuth, Cloudflare
    Access). Headers are read only when the request's immediate TCP peer matches
    one of the trusted CIDRs. <code>X-Forwarded-For</code> is ignored.
    ADR-0022.
  </>
)

export function ForwardAuthPanel() {
  const draft = useSettingsDraft({
    query: forwardAuthSettingsQuery,
    initial: emptyForm,
    save: saveForwardAuthSettings,
    successToast: "Forward auth saved.",
    // Blank lines are an editing artefact, not a CIDR. They live in the
    // draft so the textarea round-trips exactly what was typed, and are
    // dropped on the way out.
    toPayload: (form) => ({ ...form, trustedProxyCIDRs: cidrsOf(form) }),
  })

  const form = draft.value

  // The CIDR list is edited as one textarea (admins paste from compose
  // files) and stored as the array the API takes. Splitting on newline in
  // both directions keeps the two in step without a second piece of state
  // to hydrate — including the trailing newline mid-typing, which a
  // filtered round-trip would eat.
  const cidrList = useMemo(() => cidrsOf(form), [form])
  const cidrInvalid = useMemo(
    () => cidrList.filter((c) => !looksLikeCidr(c)),
    [cidrList]
  )
  const enabledWithoutCidrs = form.enabled && cidrList.length === 0

  function update<TKey extends keyof ForwardAuthSettings>(
    key: TKey,
    value: ForwardAuthSettings[TKey]
  ) {
    draft.patch(key, value)
  }
  function updateHeader<TKey extends keyof ForwardAuthSettings["headers"]>(
    key: TKey,
    value: ForwardAuthSettings["headers"][TKey]
  ) {
    draft.set((prev) => ({
      ...prev,
      headers: { ...prev.headers, [key]: value },
    }))
  }

  function onSave(e: FormEvent) {
    e.preventDefault()
    if (enabledWithoutCidrs || cidrInvalid.length > 0) return
    draft.save()
  }

  if (draft.loading) {
    return (
      <>
        <PanelHeader title="Forward auth">{INTRO}</PanelHeader>
        <PanelLoading />
      </>
    )
  }

  return (
    <>
      <PanelHeader title="Forward auth">{INTRO}</PanelHeader>

      <form onSubmit={onSave} className="flex flex-col gap-4">
        <Card>
          <Toggle
            label="Enable forward auth"
            hint="When off, the middleware is a no-op regardless of the rest of this form."
            checked={form.enabled}
            onChange={(v) => update("enabled", v)}
          />
          <Toggle
            label="Hide local login form"
            hint="Replaces the password form on /login with an SSO notice. The form stays reachable at /login?local=true for break-glass admin access."
            checked={form.hideLocalLogin}
            onChange={(v) => update("hideLocalLogin", v)}
          />
        </Card>

        <Card>
          <Field label="Trusted proxy CIDRs (one per line)">
            <textarea
              value={form.trustedProxyCIDRs.join("\n")}
              onChange={(e) =>
                update("trustedProxyCIDRs", e.target.value.split(/\r?\n/))
              }
              rows={4}
              spellCheck={false}
              className="rounded-md border border-input bg-transparent px-3 py-2 font-mono text-[13px] shadow-xs"
              placeholder={"172.16.0.0/12\n127.0.0.1/32"}
            />
          </Field>
          {enabledWithoutCidrs && (
            <p className="text-[13px] text-destructive">
              At least one CIDR is required when forward auth is enabled.
            </p>
          )}
          {cidrInvalid.length > 0 && (
            <p className="text-[13px] text-destructive">
              Not a valid CIDR: {cidrInvalid.join(", ")}
            </p>
          )}
          <p className="text-sm text-muted-foreground">
            List the IP / range of the proxy as the listening socket sees it —
            usually the proxy's IP on the shared Docker network.
          </p>
        </Card>

        <Card>
          <h3 className="t-h3" style={{ marginBottom: 4 }}>
            Headers
          </h3>
          <p className="text-sm text-muted-foreground">
            Defaults match Authelia. For oauth2-proxy use{" "}
            <code>X-Forwarded-User</code> / <code>X-Forwarded-Email</code> etc.
          </p>
          <Field label="User header (required)">
            <Input
              value={form.headers.user}
              onChange={(e) => updateHeader("user", e.target.value)}
              spellCheck={false}
            />
          </Field>
          <Field label="Email header">
            <Input
              value={form.headers.email}
              onChange={(e) => updateHeader("email", e.target.value)}
              spellCheck={false}
            />
          </Field>
          <Field label="Name header">
            <Input
              value={form.headers.name}
              onChange={(e) => updateHeader("name", e.target.value)}
              spellCheck={false}
            />
          </Field>
          <Field label="Groups header">
            <Input
              value={form.headers.groups ?? ""}
              onChange={(e) => updateHeader("groups", e.target.value)}
              spellCheck={false}
            />
          </Field>
        </Card>

        <Card>
          <Field label="Logout URL">
            <Input
              type="url"
              value={form.logoutUrl}
              onChange={(e) => update("logoutUrl", e.target.value)}
              placeholder="https://auth.example.com/logout"
              spellCheck={false}
            />
          </Field>
          <p className="text-sm text-muted-foreground">
            Returned to the SPA on logout so the browser can bounce to the
            proxy's logout endpoint and kill the upstream session too.
          </p>
        </Card>

        <div className="flex justify-end">
          <Button
            type="submit"
            disabled={
              draft.saving || enabledWithoutCidrs || cidrInvalid.length > 0
            }
          >
            {draft.saving ? "Saving…" : "Save"}
          </Button>
        </div>
      </form>
    </>
  )
}

// cidrsOf reads the meaningful entries out of the textarea-backed list —
// trimmed, blanks dropped. What the draft holds is what was typed; this
// is what the rest of the panel and the server reason about.
function cidrsOf(form: ForwardAuthSettings): Array<string> {
  return form.trustedProxyCIDRs.map((s) => s.trim()).filter(Boolean)
}

// looksLikeCidr is a quick "did the admin paste something CIDR-shaped"
// check. Server-side ParseCIDR is authoritative; this only flags
// obvious typos so the Save button can stay disabled until the input
// at least parses to "<v4 or v6>/<n>".
function looksLikeCidr(s: string): boolean {
  const slash = s.indexOf("/")
  if (slash < 1 || slash === s.length - 1) return false
  const host = s.slice(0, slash)
  const mask = Number(s.slice(slash + 1))
  if (!Number.isInteger(mask) || mask < 0) return false
  if (host.includes(":")) return mask <= 128
  const parts = host.split(".")
  if (parts.length !== 4) return false
  for (const p of parts) {
    const n = Number(p)
    if (!Number.isInteger(n) || n < 0 || n > 255) return false
  }
  return mask <= 32
}
