import { useEffect, useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"

import type { EmailSettings, EmailTLS } from "@/api/email"
import {
  emailSettingsQueryKey,
  fetchEmailSettings,
  sendEmailTest,
  updateEmailSettings,
} from "@/api/email"
import { useApiMutation } from "@/api/mutation"
import { Icon } from "@/components/Icon"
import {
  Card,
  Field,
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

export function EmailPanel() {
  const settings = useQuery({
    queryKey: emailSettingsQueryKey,
    queryFn: fetchEmailSettings,
  })

  const [form, setForm] = useState<EmailSettings>(emptyForm)
  const [pwDraft, setPwDraft] = useState("")
  const [testTo, setTestTo] = useState("")

  // Sync server state into the form whenever a new payload lands. The
  // password field is intentionally left out of `form` — it lives in
  // `pwDraft` so an empty submit means "leave alone" without us
  // accidentally clobbering a value the server never sent us.
  useEffect(() => {
    if (settings.data) {
      // Deliberate: setState inside an effect, syncing React state from an
      // external source. Was suppressed via react-hooks/set-state-in-effect;
      // Biome has no equivalent rule yet, so there is nothing to suppress.
      setForm({ ...settings.data, smtp: { ...settings.data.smtp, password: "" } })
      setPwDraft("")
    }
  }, [settings.data])

  const saveMut = useApiMutation(updateEmailSettings, {
    successToast: "Email settings saved.",
    errorToast: (err) => err.message || "Could not save email settings.",
  })

  const testMut = useApiMutation(sendEmailTest, {
    successToast: (_, to) => `Test email sent to ${to}.`,
    errorToast: (err) => err.message || "Test email failed.",
  })

  const publicUrlInvalid = useMemo(() => {
    const value = form.publicUrl.trim()
    if (value === "") return false
    return !/^https?:\/\/\S+/i.test(value)
  }, [form.publicUrl])

  function update<TKey extends keyof EmailSettings>(
    key: TKey,
    value: EmailSettings[TKey]
  ) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }
  function updateSMTP<TKey extends keyof EmailSettings["smtp"]>(
    key: TKey,
    value: EmailSettings["smtp"][TKey]
  ) {
    setForm((prev) => ({ ...prev, smtp: { ...prev.smtp, [key]: value } }))
  }
  function updateFrom<TKey extends keyof EmailSettings["from"]>(
    key: TKey,
    value: EmailSettings["from"][TKey]
  ) {
    setForm((prev) => ({ ...prev, from: { ...prev.from, [key]: value } }))
  }

  function onSave(e: React.FormEvent) {
    e.preventDefault()
    if (publicUrlInvalid) return
    // Empty password = "leave existing alone" by server contract.
    saveMut.mutate({
      ...form,
      smtp: { ...form.smtp, password: pwDraft },
    })
  }

  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 8 }}>
        Email delivery
      </h2>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: "italic" }}>
        SMTP configuration powers password resets, admin invites, and
        Send-to-Kindle. Disable to silence all outbound mail without losing
        the credentials.
      </p>

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
              value={pwDraft}
              onChange={(e) => setPwDraft(e.target.value)}
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
            Used to build absolute links in outbound mail (reset / invite
            URLs).
          </p>
        </Card>

        <div className="mt-2 flex items-center justify-end gap-2">
          <Button
            type="submit"
            size="sm"
            disabled={saveMut.isPending || publicUrlInvalid}
          >
            {saveMut.isPending ? "Saving…" : "Save email settings"}
          </Button>
        </div>
      </form>

      <div className="t-label" style={{ marginTop: 32, marginBottom: 10 }}>
        Send a test
      </div>
      <Card>
        <p className="t-small" style={{ fontSize: 12, lineHeight: 1.55 }}>
          Save your settings first, then send a probe through the live SMTP
          path to confirm credentials and DNS.
        </p>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            const to = testTo.trim()
            if (!to) return
            testMut.mutate(to)
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
            disabled={testMut.isPending || testTo.trim() === ""}
          >
            <Icon name="upload" size={13} />{" "}
            {testMut.isPending ? "Sending…" : "Send test"}
          </Button>
        </form>
      </Card>
    </>
  )
}
