import { useMemo, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import type { ProviderConfigField, ProviderInfo } from "@/api/settings"
import {
  metadataSettingsQuery,
  metadataSettingsQueryKey,
  providerSettingsQuery,
  providerSettingsQueryKey,
  updateMetadataSettings,
  updateProviderSetting,
} from "@/api/settings"
import { useApiMutation } from "@/api/mutation"
import { useApiQuery } from "@/api/query"
import { useDraft } from "@/hooks/useSettingsDraft"
import { Icon } from "@/components/Icon"
import { NotebookEmpty, QuillMark } from "@/components/SettingsShared"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { cn } from "@/lib/utils"

export function ProvidersPanel() {
  const queryClient = useQueryClient()
  const providersQuery = useApiQuery(providerSettingsQuery)

  const metaQuery = useApiQuery(metadataSettingsQuery)

  const metaMut = useApiMutation(updateMetadataSettings, {
    successToast: (data) =>
      data.autoEnrich
        ? "Auto-enrich on approve enabled."
        : "Auto-enrich on approve disabled.",
    errorToast: (err) => err.message || "Update failed.",
    onSuccess: (data) => {
      queryClient.setQueryData(metadataSettingsQueryKey, data)
    },
  })

  const patchMut = useApiMutation(updateProviderSetting, {
    errorToast: (err) => err.message || "Update failed.",
    onSuccess: (providers) => {
      queryClient.setQueryData(providerSettingsQueryKey, providers)
    },
  })

  const providers = providersQuery.data ?? []
  const enabledCount = providers.filter((p) => p.enabled).length
  const rankedCount = providers.filter((p) => p.priority != null).length
  const isLoading = providersQuery.isLoading

  // Sorted view for chain-order display. Ranked providers sit on top,
  // unranked fall back to catalog order. Up/Down arrows swap priorities
  // within the ranked portion.
  const ordered = [...providers].sort((a, b) => {
    const ap = a.priority
    const bp = b.priority
    if (ap != null && bp != null) return ap - bp
    if (ap != null) return -1
    if (bp != null) return 1
    return 0
  })

  const swapPriority = (idx: number, dir: -1 | 1) => {
    const target = idx + dir
    if (target < 0 || target >= ordered.length) return
    const a = ordered[idx]
    const b = ordered[target]
    if (!a || !b) return
    const aPrio = a.priority ?? idx
    const bPrio = b.priority ?? target
    patchMut.mutate({ id: a.id, patch: { priority: bPrio } })
    patchMut.mutate({ id: b.id, patch: { priority: aPrio } })
  }

  return (
    <>
      <header className="mb-8 border-b border-(--color-rule-soft) pb-5">
        <div className="t-label mb-3">Settings · Enrichment</div>
        <div className="flex flex-wrap items-end justify-between gap-8">
          <div className="min-w-0">
            <h2 className="t-h2">Metadata providers</h2>
            <p className="t-small mt-2 max-w-[58ch] italic">
              Enrichment queries fan out across enabled providers — toggle any
              row to include or skip it. Priority drives ISBN-lookup chain
              order; the parallel fan-out on the book editor still sorts by
              match confidence.
            </p>
          </div>
          <StatStrip
            enabled={enabledCount}
            total={providers.length}
            ranked={rankedCount}
            loading={isLoading}
          />
        </div>
      </header>

      <section
        className="mb-10 border border-(--color-rule-soft) bg-(--color-paper-0)"
        aria-label="Auto-enrich"
      >
        <label className="flex cursor-pointer items-center gap-5 px-5 py-4">
          <span className="t-micro shrink-0 text-(--color-accent-ink)">
            Auto
          </span>
          <div className="min-w-0 flex-1">
            <div className="t-item-title">Auto-enrich on bookdrop approve</div>
            <div className="t-small mt-1 max-w-[58ch]">
              When enabled, approving a bookdrop item triggers a provider
              fan-out and writes the top match — empty fields only, respecting
              locks.
            </div>
          </div>
          <Switch
            checked={!!metaQuery.data?.autoEnrich}
            disabled={metaQuery.isLoading || metaMut.isPending}
            onCheckedChange={(v) => metaMut.mutate({ autoEnrich: v })}
            aria-label="Toggle auto-enrich on bookdrop approve"
          />
        </label>
      </section>

      <div className="mb-3 flex items-baseline justify-between gap-3">
        <div className="t-label">The provider chain</div>
        <div className="font-mono text-[10.5px] tracking-widest text-(--color-ink-3) uppercase tabular-nums">
          {isLoading
            ? "Loading…"
            : `${rankedCount.toString().padStart(2, "0")} ranked · ${enabledCount.toString().padStart(2, "0")} on`}
        </div>
      </div>

      <div className="divide-y divide-(--color-rule-soft) border border-(--color-rule-soft) bg-(--color-paper-0)">
        {isLoading && <SkeletonRows count={3} />}
        {!isLoading && ordered.length === 0 && <EmptyState />}
        {!isLoading &&
          ordered.map((p, idx) => (
            <ProviderRow
              key={p.id}
              provider={p}
              position={idx}
              total={ordered.length}
              busy={patchMut.isPending}
              onToggle={(enabled) =>
                patchMut.mutate({ id: p.id, patch: { enabled } })
              }
              onSaveConfig={(config) =>
                patchMut.mutate(
                  { id: p.id, patch: { config } },
                  {
                    onSuccess: () => toast.success(`${p.name} config saved.`),
                  }
                )
              }
              onMoveUp={() => swapPriority(idx, -1)}
              onMoveDown={() => swapPriority(idx, 1)}
            />
          ))}
      </div>
    </>
  )
}

function StatStrip({
  enabled,
  total,
  ranked,
  loading,
}: {
  enabled: number
  total: number
  ranked: number
  loading: boolean
}) {
  return (
    <dl
      className="flex items-stretch divide-x divide-(--color-rule-soft) border border-(--color-rule-soft) bg-(--color-paper-0) text-right"
      aria-busy={loading}
    >
      <Stat label="Enabled" value={loading ? "—" : `${enabled}/${total}`} />
      <Stat label="Ranked" value={loading ? "—" : ranked} />
    </dl>
  )
}

function Stat({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="min-w-[88px] px-4 py-2.5">
      <dt className="t-label">{label}</dt>
      <dd className="mt-0.5 font-mono text-[18px] leading-tight text-(--color-ink-1) tabular-nums">
        {value}
      </dd>
    </div>
  )
}

function SkeletonRows({ count }: { count: number }) {
  return (
    <>
      {Array.from({ length: count }).map((_, i) => (
        <div
          key={i}
          className="flex animate-pulse items-center gap-4 px-5 py-4"
        >
          <div className="w-9 shrink-0">
            <div className="mb-2 h-2.5 w-6 bg-(--color-paper-3)" />
            <div className="h-4 w-4 bg-(--color-paper-2)" />
          </div>
          <div className="h-2 w-2 rounded-full bg-(--color-paper-3)" />
          <div className="min-w-0 flex-1">
            <div className="mb-2 h-3.5 w-40 bg-(--color-paper-3)" />
            <div className="h-3 w-56 bg-(--color-paper-2)" />
          </div>
          <div className="h-5 w-9 rounded-full bg-(--color-paper-2)" />
        </div>
      ))}
    </>
  )
}

function EmptyState() {
  return (
    <NotebookEmpty
      mark={<QuillMark />}
      title="No metadata providers detected."
      body="Build flags or runtime configuration removed every catalog entry. Check the binary build tags or restart with the default provider set."
    />
  )
}

function ProviderRow({
  provider,
  position,
  total,
  busy,
  onToggle,
  onSaveConfig,
  onMoveUp,
  onMoveDown,
}: {
  provider: ProviderInfo
  position: number
  total: number
  busy: boolean
  onToggle: (enabled: boolean) => void
  onSaveConfig: (config: Record<string, unknown>) => void
  onMoveUp: () => void
  onMoveDown: () => void
}) {
  const schema = provider.schema ?? []
  const [expanded, setExpanded] = useState(false)

  // The row's payload arrives as a prop — it is a slice of the providers
  // list query, not a query of its own — so it uses the draft core
  // directly. Same rule as every other settings panel: the list refetches
  // whenever any row is toggled, and a refetch must not take away what
  // the admin is typing into a config field.
  const configHash = JSON.stringify(provider.config ?? {})
  // biome-ignore lint/correctness/useExhaustiveDependencies: configHash is the intended trigger; depending on the schema object itself would re-run on every render
  const source = useMemo(
    () => schemaToForm(schema, provider.config ?? {}),
    [configHash]
  )
  const draft = useDraft(source, EMPTY_CONFIG)
  const values = draft.value

  const dirty = draft.dirty
  const hasConfig = schema.length > 0
  const ranked = provider.priority != null

  return (
    <div className="px-5 py-4">
      <div className="flex items-start gap-4">
        <div className="flex w-9 shrink-0 flex-col items-center gap-1.5 pt-0.5">
          <span
            className={cn(
              "font-mono text-[10.5px] tracking-widest tabular-nums",
              ranked
                ? "text-(--color-ink-2)"
                : "text-(--color-ink-4) line-through"
            )}
            aria-label={ranked ? `Position ${position + 1}` : "Unranked"}
            title={ranked ? `Position ${position + 1}` : "Unranked"}
          >
            {String(position + 1).padStart(2, "0")}
          </span>
          <div className="flex flex-col gap-0.5">
            <button
              type="button"
              aria-label="Move up"
              disabled={position === 0 || busy}
              onClick={onMoveUp}
              className={cn(
                "border border-(--color-rule-soft) bg-(--color-paper-0) p-0.5 leading-none text-(--color-ink-2) transition-colors",
                position === 0 || busy
                  ? "cursor-default opacity-30"
                  : "hover:bg-(--color-paper-2) hover:text-(--color-ink-1)"
              )}
            >
              <Icon name="chevron-up" size={12} />
            </button>
            <button
              type="button"
              aria-label="Move down"
              disabled={position === total - 1 || busy}
              onClick={onMoveDown}
              className={cn(
                "border border-(--color-rule-soft) bg-(--color-paper-0) p-0.5 leading-none text-(--color-ink-2) transition-colors",
                position === total - 1 || busy
                  ? "cursor-default opacity-30"
                  : "hover:bg-(--color-paper-2) hover:text-(--color-ink-1)"
              )}
            >
              <Icon name="chevron-down" size={12} />
            </button>
          </div>
        </div>

        <StatusDot
          enabled={provider.enabled}
          successAt={provider.lastSuccessAt}
          errorAt={provider.lastErrorAt}
        />

        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-baseline gap-2">
            <div className="t-item-title">{provider.name}</div>
            <span className="font-mono text-[11px] tracking-wide text-(--color-ink-3)">
              {provider.id}
            </span>
            {provider.external && (
              <span className="t-micro text-(--color-ink-3)">External</span>
            )}
            {ranked && (
              <span className="font-mono text-[10.5px] tracking-widest text-(--color-accent-ink) uppercase tabular-nums">
                P{provider.priority}
              </span>
            )}
          </div>
          <ProviderHealth
            successAt={provider.lastSuccessAt}
            errorAt={provider.lastErrorAt}
            lastError={provider.lastError}
          />
        </div>

        <div className="flex shrink-0 items-center gap-2">
          {hasConfig && (
            <button
              type="button"
              onClick={() => setExpanded((v) => !v)}
              aria-expanded={expanded}
              aria-controls={`provider-config-${provider.id}`}
              className="t-micro flex items-center gap-1 border border-(--color-rule-soft) px-2 py-1 text-(--color-ink-2) transition-colors hover:bg-(--color-paper-2) hover:text-(--color-ink-1)"
            >
              Config
              <Icon name={expanded ? "chevron-up" : "chevron-down"} size={11} />
            </button>
          )}
          <Switch
            id={`provider-${provider.id}`}
            checked={provider.enabled}
            disabled={busy}
            onCheckedChange={onToggle}
            aria-label={`${provider.enabled ? "Disable" : "Enable"} ${provider.name}`}
          />
        </div>
      </div>

      {hasConfig && expanded && (
        <div
          id={`provider-config-${provider.id}`}
          className="mt-4 flex flex-col gap-3 border-t border-dashed border-(--color-rule-soft) pt-4 pl-13"
        >
          {schema.map((field) => (
            <ConfigFieldRow
              key={field.key}
              field={field}
              value={values[field.key] ?? ""}
              onChange={(v) => draft.patch(field.key, v)}
            />
          ))}
          <div className="flex items-center justify-end gap-2 pt-1">
            {dirty && (
              <span className="t-micro mr-auto text-(--color-accent-ink)">
                Unsaved changes
              </span>
            )}
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={!dirty || busy}
              onClick={draft.revert}
            >
              Revert
            </Button>
            <Button
              type="button"
              size="sm"
              disabled={!dirty || busy}
              onClick={() => {
                onSaveConfig(formToConfig(schema, values))
                // Settled on submit rather than on the response: the save
                // belongs to the list, not the row, so the row cannot see
                // it land. A rejected save leaves the typed values on
                // screen, which is what a retry needs.
                draft.settle()
              }}
            >
              Save config
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}

function StatusDot({
  enabled,
  successAt,
  errorAt,
}: {
  enabled: boolean
  successAt?: string
  errorAt?: string
}) {
  const sAt = successAt ? Date.parse(successAt) : 0
  const eAt = errorAt ? Date.parse(errorAt) : 0
  const errorWins = eAt > 0 && eAt > sAt
  const tone = !enabled
    ? "bg-(--color-ink-4)"
    : errorWins
      ? "bg-(--color-accent-ink)"
      : "bg-(--color-cov-forest)"
  const ringTone = errorWins
    ? "bg-(--color-accent-ink)"
    : "bg-(--color-cov-forest)"
  const label = !enabled
    ? "Disabled"
    : errorWins
      ? "Last call failed"
      : "Healthy"
  return (
    <span
      className="relative mt-2 inline-flex h-2 w-2 shrink-0"
      aria-label={label}
      title={label}
    >
      {enabled && (
        <span
          className={cn(
            "absolute inset-0 animate-ping rounded-full opacity-30",
            ringTone
          )}
        />
      )}
      <span className={cn("relative h-2 w-2 rounded-full", tone)} />
    </span>
  )
}

function ConfigFieldRow({
  field,
  value,
  onChange,
}: {
  field: ProviderConfigField
  value: string
  onChange: (v: string) => void
}) {
  const [reveal, setReveal] = useState(false)
  return (
    <div>
      <div className="mb-1 flex items-center gap-2">
        <span className="t-label">{field.label}</span>
        {field.kind === "password" && value && (
          <button
            type="button"
            onClick={() => setReveal((r) => !r)}
            className="t-micro text-(--color-accent-ink) hover:underline"
          >
            {reveal ? "Hide" : "Reveal"}
          </button>
        )}
      </div>
      {field.kind === "select" ? (
        <select
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
        >
          {(field.options ?? []).map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      ) : field.kind === "textarea" ? (
        <textarea
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={field.placeholder}
          rows={3}
          className="flex w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-sm shadow-xs"
        />
      ) : (
        <Input
          type={field.kind === "password" && !reveal ? "password" : "text"}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={field.placeholder}
          className={field.kind === "password" ? "mono" : undefined}
          autoComplete="off"
        />
      )}
      {field.help && (
        <div className="t-small mt-1 text-[11.5px]">{field.help}</div>
      )}
    </div>
  )
}

// ProviderHealth renders a single-line badge under each provider row
// showing "last success Xm ago" or the last error when a failure is
// more recent than the last success. Returns null when neither
// timestamp has been observed.
function ProviderHealth({
  successAt,
  errorAt,
  lastError,
}: {
  successAt?: string
  errorAt?: string
  lastError?: string
}) {
  if (!successAt && !errorAt) return null
  const sAt = successAt ? Date.parse(successAt) : 0
  const eAt = errorAt ? Date.parse(errorAt) : 0
  const errorWins = eAt > sAt
  const ts = errorWins ? eAt : sAt
  const rel = relativeTime(ts)
  return (
    <div
      className={cn(
        "mt-1 font-mono text-[11.5px] leading-tight tabular-nums",
        errorWins ? "text-(--color-accent-ink)" : "text-(--color-cov-forest)"
      )}
      title={errorWins ? lastError : "Last successful fetch"}
    >
      {errorWins
        ? `failed ${rel}${lastError ? ` — ${truncate(lastError, 80)}` : ""}`
        : `ok · ${rel}`}
    </div>
  )
}

function relativeTime(ms: number): string {
  if (!ms) return "—"
  const diff = Date.now() - ms
  if (diff < 0) return "in the future"
  const s = Math.floor(diff / 1000)
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  const d = Math.floor(h / 24)
  return `${d}d ago`
}

function truncate(s: string, n: number): string {
  return s.length > n ? s.slice(0, n - 1) + "…" : s
}

// The pre-payload shape for a config row. Module-level so its identity is
// stable across renders, which is what the draft core expects.
const EMPTY_CONFIG: Record<string, string> = {}

function valueToString(v: unknown): string {
  if (v == null) return ""
  return typeof v === "string" ? v : String(v)
}

function schemaToForm(
  schema: Array<ProviderConfigField> = [],
  config: Record<string, unknown>
): Record<string, string> {
  const out: Record<string, string> = {}
  for (const f of schema) {
    out[f.key] = valueToString(config[f.key])
  }
  return out
}

function formToConfig(
  schema: Array<ProviderConfigField> = [],
  values: Record<string, string>
): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const f of schema) {
    out[f.key] = values[f.key] ?? ""
  }
  return out
}
