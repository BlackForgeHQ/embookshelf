import { useMemo, useState } from "react"

import type { EmailSettings, EmailTLS } from "@/api/email"
import {
  emailSettingsQuery,
  sendEmailTest,
  updateEmailSettings,
} from "@/api/email"
import { useConnectionTest } from "@/hooks/useConnectionTest"
import { useSettingsDraft } from "@/hooks/useSettingsDraft"
import type { Viewer } from "@/lib/affordance"
import { messageForCode } from "@/lib/affordance"
import { Icon } from "@/components/Icon"
import {
  Card,
  ConnectionTestReport,
  Field,
  PanelHeader,
  PanelLoading,
  Select,
  Toggle,
} from "@/components/SettingsShared"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

const TLS_OPTIONS: ReadonlyArray<{ value: EmailTLS; label: string }> = [
  { value: "starttls", label: "STARTTLS" },
  { value: "tls", label: "Implicit TLS (465)" },
  { value: "none", label: "None (insecure)" },
]

// emptyForm is the initial shape rendered before the GET completes — also
// used as the "not configured yet" baseline. Port 587 + STARTTLS matches
// the most common provider default (Gmail, Mailgun, SES STARTTLS, …).
const emptyForm: EmailSettings = {
  enabled: false,
  smtp: { host: "", port: 587, username: "", password: "", tls: "starttls" },
  from: { address: "", name: "" },
  publicUrl: "",
  passwordSet: false,
}

const INTRO = `SMTP configuration powers password resets, admin invites, and Send-to-Kindle. Disable to silence all outbound mail without losing the credentials.`

// Only admins reach any /settings section, so the affordance module's
// viewer is not in question here.
const SETTINGS_VIEWER: Viewer = { isAdmin: true }

export function EmailPanel() {
  const draft = useSettingsDraft({
    queryKey: emailSettingsQuery.key,
    queryFn: emailSettingsQuery.fn,
    initial: emptyForm,
    save: updateEmailSettings,
    successToast: "Email settings saved.",
    // PUT answers 502 EMAIL_RELOAD_FAILED when the row saved but the
    // mail sender could not be rebuilt from it (settings_email.go). Left
    // unread, that surfaced as a raw SMTP construction error — "dial
    // tcp: lookup smtp.exmaple.com" — in a red toast, right after the
    // save the admin can see went through.
    //
    // Still missing: the handler comment asks for this *inline*, and a
    // toast is not that. The honest surface is a warning beside the save
    // button — the settings did save — which needs useSettingsDraft to
    // expose the failed save rather than only toast it, and every panel
    // to have somewhere to render it. The module's sentence in the toast
    // is the part that fits in this slice.
    //
    // The viewer is fixed because it has to be: every /settings section
    // is adminOnly (SETTINGS_SECTIONS), so nobody else is reading this.
    errorToast: (err) => messageForCode(err.code, err.message, SETTINGS_VIEWER),
    toPayload: (form, secrets) => ({
      ...form,
      smtp: { ...form.smtp, password: secrets.value("smtpPassword") },
      passwordSet: secrets.stillSet("smtpPassword", form.passwordSet),
    }),
  })

  const [testTo, setTestTo] = useState("")
  const test = useConnectionTest({
    test: sendEmailTest,
    // The endpoint answers 204 on success, so the recipient is the only
    // thing worth saying back.
    read: (_result, to) => ({ ok: true, message: `Test email sent to ${to}.` }),
    unreachable: (err) => err.message || "Test email failed.",
  })

  const form = draft.value
  const password = draft.secret("smtpPassword")

  const publicUrlInvalid = useMemo(() => {
    const value = form.publicUrl.trim()
    if (value === "") return false
    return !/^https?:\/\/\S+/i.test(value)
  }, [form.publicUrl])

  function update<TKey extends keyof EmailSettings>(
    key: TKey,
    value: EmailSettings[TKey]
  ) {
    draft.patch(key, value)
  }
  function updateSMTP<TKey extends keyof EmailSettings["smtp"]>(
    key: TKey,
    value: EmailSettings["smtp"][TKey]
  ) {
    draft.set((prev) => ({ ...prev, smtp: { ...prev.smtp, [key]: value } }))
  }
  function updateFrom<TKey extends keyof EmailSettings["from"]>(
    key: TKey,
    value: EmailSettings["from"][TKey]
  ) {
    draft.set((prev) => ({ ...prev, from: { ...prev.from, [key]: value } }))
  }

  function onSave(e: React.FormEvent) {
    e.preventDefault()
    if (publicUrlInvalid) return
    draft.save()
  }

  if (draft.loading) {
    return (
      <>
        <PanelHeader title="Email delivery">{INTRO}</PanelHeader>
        <PanelLoading />
      </>
    )
  }

  return (
    <>
      <PanelHeader title="Email delivery">{INTRO}</PanelHeader>

      <form onSubmit={onSave}>
        <Card>
          <Toggle
            label="Enable email delivery"
            hint="When off, password reset, invite, and Send-to-Kindle endpoints return 503."
            checked={form.enabled}
            onChange={(v) => update("enabled", v)}
          />
        </Card>

        <Card>
          <div className="grid grid-cols-1 gap-4 md:grid-cols-[1fr_120px]">
            <Field label="SMTP host">
              <Input
                value={form.smtp.host}
                onChange={(e) => updateSMTP("host", e.target.value)}
                placeholder="smtp.example.com"
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <Field label="Port">
              <Input
                type="number"
                min={1}
                max={65535}
                value={form.smtp.port}
                onChange={(e) =>
                  updateSMTP("port", Number(e.target.value) || 0)
                }
              />
            </Field>
          </div>

          <Field label="Encryption">
            <Select
              value={form.smtp.tls}
              onChange={(v) => updateSMTP("tls", v as EmailTLS)}
              options={TLS_OPTIONS.map((o) => ({
                value: o.value,
                label: o.label,
              }))}
            />
          </Field>

          <Field label="Username">
            <Input
              value={form.smtp.username}
              onChange={(e) => updateSMTP("username", e.target.value)}
              autoComplete="off"
              spellCheck={false}
            />
          </Field>

          <div className="flex flex-col gap-1.5">
            <span className="t-label flex items-center gap-2">
              Password
              <span
                className={`rounded-full px-1.5 py-0.5 text-[10px] font-medium ${
                  form.passwordSet
                    ? "bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400"
                    : "bg-muted text-muted-foreground"
                }`}
              >
                {form.passwordSet ? "Configured" : "Not set"}
              </span>
            </span>
            <Input
              type="password"
              value={password.value}
              onChange={(e) => password.set(e.target.value)}
              placeholder={
                form.passwordSet
                  ? "Leave blank to keep existing"
                  : "Set a password"
              }
              autoComplete="new-password"
            />
          </div>
        </Card>

        <Card>
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            <Field label="From address">
              <Input
                type="email"
                value={form.from.address}
                onChange={(e) => updateFrom("address", e.target.value)}
                placeholder="bookshelf@example.com"
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <Field label="From name">
              <Input
                value={form.from.name}
                onChange={(e) => updateFrom("name", e.target.value)}
                placeholder="embookshelf"
              />
            </Field>
          </div>

          <Field label="Public URL">
            <Input
              value={form.publicUrl}
              onChange={(e) => update("publicUrl", e.target.value)}
              placeholder="https://books.example.com"
              autoComplete="off"
              spellCheck={false}
            />
          </Field>
          {publicUrlInvalid && (
            <p
              className="t-small"
              style={{ color: "var(--color-accent-ink)", fontSize: 12 }}
            >
              Public URL must start with http:// or https://.
            </p>
          )}
          <p className="t-small" style={{ fontSize: 12 }}>
            Used to build absolute links in outbound mail (reset / invite URLs).
          </p>
        </Card>

        <div className="mt-2 flex items-center justify-end gap-2">
          <Button
            type="submit"
            size="sm"
            disabled={draft.saving || publicUrlInvalid}
          >
            {draft.saving ? "Saving…" : "Save email settings"}
          </Button>
        </div>
      </form>

      <div className="t-label" style={{ marginTop: 32, marginBottom: 10 }}>
        Send a test
      </div>
      <Card>
        <p className="t-small" style={{ fontSize: 12, lineHeight: 1.55 }}>
          Save your settings first, then send a probe through the live SMTP path
          to confirm credentials and DNS.
        </p>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            const to = testTo.trim()
            if (!to) return
            test.run(to)
          }}
          className="flex items-end gap-2"
        >
          <Field label="Recipient">
            <Input
              type="email"
              value={testTo}
              onChange={(e) => setTestTo(e.target.value)}
              placeholder="you@example.com"
              autoComplete="off"
              spellCheck={false}
            />
          </Field>
          <Button
            type="submit"
            size="sm"
            variant="outline"
            disabled={test.running || testTo.trim() === ""}
          >
            <Icon name="upload" size={13} />{" "}
            {test.running ? "Sending…" : "Send test"}
          </Button>
        </form>
        <ConnectionTestReport outcome={test.outcome} />
      </Card>
    </>
  )
}
