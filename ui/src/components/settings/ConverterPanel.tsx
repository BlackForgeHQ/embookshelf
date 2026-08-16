import type { FormEvent } from "react"

import type { ConverterCoverage, ConverterSettings } from "@/api/converter"
import {
  converterCoverageQuery,
  converterHealthQuery,
  converterSettingsQuery,
  startBulkConversion,
  updateConverterSettings,
} from "@/api/converter"
import { useApiMutation } from "@/api/mutation"
import { useApiQuery } from "@/api/query"
import { pollWhile } from "@/lib/artifactRun"
import { ProgressBar } from "@/components/ProgressBar"
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
    (ADR-0033). embookshelf works without it; features that need book text
    for those formats will say the extension is not configured. Start it with{" "}
    <code>make converter-up</code> and point this at{" "}
    <code>http://localhost:6070</code>.
  </>
)

export function ConverterPanel() {
  const draft = useSettingsDraft({
    query: converterSettingsQuery,
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

      {form.enabled && <BulkConversionCard />}
    </>
  )
}

// BulkConversionCard converts every candidate PDF in one click and
// shows progress. Coverage serves both the pre-flight count and the
// progress bar, so the bar survives a reload and a run someone started
// yesterday — same shape as the guide run card. No token estimate:
// conversion costs sidecar CPU, not a metered API.
function BulkConversionCard() {
  const coverage = useApiQuery(converterCoverageQuery, {
    // Poll only while something is converting; an idle instance is
    // never polled. Cadence and predicate shape from lib/artifactRun.
    ...pollWhile<ConverterCoverage>((d) => (d?.converting ?? 0) > 0),
  })

  const runMut = useApiMutation(startBulkConversion, {
    successToast: (res: { queued: number }) =>
      res.queued === 0
        ? "Every convertible book already has a markdown rendition."
        : `Queued ${res.queued} book${res.queued === 1 ? "" : "s"} for conversion.`,
  })

  const cov = coverage.data

  if (!cov) {
    return (
      <Card className="mt-6">
        <h3 className="t-h3 mb-2">Convert the whole library</h3>
        <p className="t-small">Checking what needs conversion…</p>
      </Card>
    )
  }

  const pct = cov.total > 0 ? Math.round((cov.ready / cov.total) * 100) : 0

  return (
    <Card className="mt-6">
      <h3 className="t-h3 mb-2">Convert the whole library</h3>

      {cov.total > 0 && (
        <div className="mb-4">
          <div
            className="mb-1 flex items-baseline justify-between"
            style={{ gap: 12 }}
          >
            <span className="t-small">
              {cov.ready.toLocaleString()} of {cov.total.toLocaleString()}{" "}
              convertible books have a markdown rendition
            </span>
            <span className="t-small tabular-nums">{pct}%</span>
          </div>
          <ProgressBar value={pct / 100} label="Markdown rendition coverage" />
        </div>
      )}

      {cov.converting > 0 && (
        <p className="t-small mb-1">
          Converting {cov.converting.toLocaleString()} book
          {cov.converting === 1 ? "" : "s"}…
        </p>
      )}
      {cov.failed > 0 && (
        <p className="t-small mb-1 text-destructive">
          {cov.failed.toLocaleString()} conversion
          {cov.failed === 1 ? "" : "s"} failed. Each book's guide tab shows
          the reason verbatim. Running again retries them.
        </p>
      )}

      {cov.total === 0 ? (
        <p className="t-small">No convertible books in the library (PDF only today).</p>
      ) : cov.candidates === 0 && cov.converting === 0 ? (
        <p className="t-small">Every convertible book already has a rendition.</p>
      ) : (
        <Button
          variant="outline"
          disabled={runMut.isPending || cov.candidates === 0}
          onClick={() => runMut.mutate(undefined)}
        >
          Convert {cov.candidates.toLocaleString()} book
          {cov.candidates === 1 ? "" : "s"}
        </Button>
      )}
    </Card>
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
        The health check itself failed. See the server log.
      </p>
    )
  }
  switch (health.status) {
    case "not_configured":
      return (
        <p className="text-sm text-muted-foreground">
          Not configured. Enable the extension and set its base URL, then
          save.
        </p>
      )
    case "ok":
      return (
        <p className="text-sm">
          <span className="text-(--color-ok-ink)">Reachable</span>
          {health.version ? (
            <span className="text-muted-foreground">, v{health.version}</span>
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
