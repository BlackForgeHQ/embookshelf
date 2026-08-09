import type { FormEvent } from "react"

import type { ConverterSettings } from "@/api/converter"
import {
  converterHealthQuery,
  converterSettingsQuery,
  updateConverterSettings,
} from "@/api/converter"
import { useApiQuery } from "@/api/query"
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

// Mirrors the server's DefaultConverterConfig — disabled, no URL.
const emptyForm: ConverterSettings = {
  enabled: false,
  baseUrl: "",
}

const INTRO = (
  <>
    The converter extension is a separately deployed sidecar that turns
    PDF, Word, RTF and OpenDocument books into Markdown for AI features
    (ADR-0033). embookshelf works without it — features that need book text
    for those formats will say the extension is not configured. Start it with{" "}
    <code>make converter-up</code> and point this at{" "}
    <code>http://localhost:6070</code>.
  </>
)

export function ConverterPanel() {
  const draft = useSettingsDraft({
    queryKey: converterSettingsQuery.key,
    queryFn: converterSettingsQuery.fn,
    initial: emptyForm,
    save: updateConverterSettings,
    successToast: "Converter settings saved.",
    toPayload: (form) => form,
  })
  const health = useApiQuery(converterHealthQuery)

  const form = draft.value
  const enabledWithoutUrl = form.enabled && form.baseUrl.trim() === ""

  function onSave(e: FormEvent) {
    e.preventDefault()
    if (enabledWithoutUrl) return
    draft.save()
  }

  if (draft.loading) {
    return (
      <>
        <PanelHeader title="Converter">{INTRO}</PanelHeader>
        <PanelLoading />
      </>
    )
  }

  return (
    <>
      <PanelHeader title="Converter">{INTRO}</PanelHeader>

      <form onSubmit={onSave} className="flex flex-col gap-4">
        <Card>
          <Toggle
            label="Enable the converter extension"
            hint="When off, no conversion is attempted and conversion-dependent features report the extension as not configured."
            checked={form.enabled}
            onChange={(v) => draft.patch("enabled", v)}
          />
          <Field label="Base URL">
            <Input
              type="url"
              value={form.baseUrl}
              onChange={(e) => draft.patch("baseUrl", e.target.value)}
              placeholder="http://converter:6070"
              spellCheck={false}
            />
          </Field>
          {enabledWithoutUrl && (
            <p className="text-[13px] text-destructive">
              A base URL is required when the converter is enabled.
            </p>
          )}
        </Card>

        <Card>
          <h3 className="t-h3" style={{ marginBottom: 4 }}>
            Status
          </h3>
          <ConverterStatus
            health={health.data}
            loading={health.isPending}
            failed={health.isError}
          />
          <div>
            <Button
              type="button"
              variant="outline"
              onClick={() => void health.refetch()}
              disabled={health.isFetching}
            >
              {health.isFetching ? "Checking…" : "Check now"}
            </Button>
          </div>
        </Card>

        <div className="flex justify-end">
          <Button type="submit" disabled={draft.saving || enabledWithoutUrl}>
            {draft.saving ? "Saving…" : "Save"}
          </Button>
        </div>
      </form>
    </>
  )
}

// ConverterStatus renders the three health states plus the two the
// query itself can be in. "Unreachable" carries the dial error verbatim
// — ADR-0033's loud-failure rule; an admin debugging a compose network
// needs the real string, not a summary of it.
function ConverterStatus({
  health,
  loading,
  failed,
}: {
  health?: import("@/api/converter").ConverterHealth
  loading: boolean
  failed: boolean
}) {
  if (loading) {
    return <p className="text-sm text-muted-foreground">Checking…</p>
  }
  if (failed || !health) {
    return (
      <p className="text-sm text-destructive">
        The health check itself failed — see the server log.
      </p>
    )
  }
  switch (health.status) {
    case "not_configured":
      return (
        <p className="text-sm text-muted-foreground">
          Not configured — enable the extension and set its base URL, then
          save.
        </p>
      )
    case "ok":
      return (
        <p className="text-sm">
          <span className="text-emerald-600 dark:text-emerald-400">
            Reachable
          </span>
          {health.version ? (
            <span className="text-muted-foreground"> — v{health.version}</span>
          ) : null}
        </p>
      )
    case "unreachable":
      return (
        <p className="text-sm text-destructive">
          Unreachable: {health.error ?? "no further detail"}
        </p>
      )
  }
}
