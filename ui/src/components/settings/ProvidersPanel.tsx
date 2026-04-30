import { useEffect, useMemo, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { toast } from "sonner"
import type { CSSProperties, ReactNode } from "react"

import type { ApiError } from "@/api/client"
import type {
  LibraryKind,
  MetadataSettings,
  ProviderConfigField,
  ProviderInfo,
  ProviderPatch,
  SettingsLibrary,
} from "@/api/settings"
import type {
  OidcAdminSettings,
  OidcTestCheck,
  OidcTestResult,
  ProviderSlug,
} from "@/api/oidc"
import type { AuthUser } from "@/api/auth"
import {
  appConfigQueryKey,
  approveSettingsUser,
  createLibrary,
  createSettingsUser,
  deleteLibrary,
  deleteSettingsUser,
  denySettingsUser,
  fetchAppConfig,
  fetchInstanceInfo,
  fetchMetadataSettings,
  fetchProviderSettings,
  fetchSettingsLibraries,
  fetchSettingsUsers,
  instanceInfoQueryKey,
  metadataSettingsQueryKey,
  prescanLibraryPaths,
  providerSettingsQueryKey,
  rescanLibrary,
  settingsLibrariesQueryKey,
  settingsUsersQueryKey,
  updateMetadataSettings,
  updateProviderSetting,
  updateSettingsUserRole,
} from "@/api/settings"
import {
  fetchOidcAdminSettings,
  oidcAdminSettingsQueryKey,
  saveOidcAdminSettings,
  testOidcProvider,
} from "@/api/oidc"
import { fetchMe, meQueryKey } from "@/api/auth"
import { Icon } from "@/components/Icon"
import {
  AdminGate,
  Avatar,
  Card,
  DefRow,
  Field,
  Select,
  SettingsShell,
} from "@/components/SettingsShared"
import { TopBar } from "@/components/TopBar"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"


export function ProvidersPanel({ isAdmin }: { isAdmin: boolean }) {
  const queryClient = useQueryClient()
  const providersQuery = useQuery({
    queryKey: providerSettingsQueryKey,
    queryFn: fetchProviderSettings,
    enabled: isAdmin,
  })

  const metaQuery = useQuery({
    queryKey: metadataSettingsQueryKey,
    queryFn: fetchMetadataSettings,
    enabled: isAdmin,
  })

  const metaMut = useMutation({
    mutationFn: (body: MetadataSettings) => updateMetadataSettings(body),
    onSuccess: (data) => {
      queryClient.setQueryData(metadataSettingsQueryKey, data)
      toast.success(
        data.autoEnrich
          ? "Auto-enrich on approve enabled."
          : "Auto-enrich on approve disabled."
      )
    },
    onError: (err) =>
      toast.error((err as unknown as ApiError).message || "Update failed."),
  })

  const patchMut = useMutation({
    mutationFn: (args: { id: string; patch: ProviderPatch }) =>
      updateProviderSetting(args.id, args.patch),
    onSuccess: (providers) => {
      queryClient.setQueryData(providerSettingsQueryKey, providers)
      queryClient.invalidateQueries({ queryKey: instanceInfoQueryKey })
    },
    onError: (err) =>
      toast.error((err as unknown as ApiError).message || "Update failed."),
  })

  if (!isAdmin) return <AdminGate label="Metadata providers" />

  const providers = providersQuery.data ?? []
  const enabledCount = providers.filter((p) => p.enabled).length

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
    // The bounds check above guarantees both are defined; the guard
    // keeps noUncheckedIndexedAccess happy without duplicating logic.
    if (!a || !b) return
    const aPrio = a.priority ?? idx
    const bPrio = b.priority ?? target
    patchMut.mutate({ id: a.id, patch: { priority: bPrio } })
    patchMut.mutate({ id: b.id, patch: { priority: aPrio } })
  }

  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 8 }}>
        Metadata providers
      </h2>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: "italic" }}>
        Enrichment queries fan out across enabled providers — toggle any row to
        include or skip it. Priority drives ISBN-lookup chain order; the
        parallel fan-out on the book editor still sorts by match confidence.
      </p>

      <div className="t-label" style={{ marginBottom: 10 }}>
        {providersQuery.isLoading
          ? "Loading providers…"
          : `${enabledCount} of ${providers.length} enabled`}
      </div>

      <label
        style={{
          display: "flex",
          alignItems: "center",
          gap: 14,
          padding: "12px 14px",
          marginBottom: 14,
          border: "1px solid var(--color-rule-soft)",
          background: "var(--color-paper-0)",
          borderRadius: 2,
          cursor: "pointer",
        }}
      >
        <div style={{ flex: 1, minWidth: 0 }}>
          <div className="t-item-title">Auto-enrich on bookdrop approve</div>
          <div className="t-item-sub">
            When enabled, approving a bookdrop item triggers a provider fan-out
            and writes the top match (empty fields only, respecting locks).
          </div>
        </div>
        <Switch
          checked={!!metaQuery.data?.autoEnrich}
          disabled={metaQuery.isLoading || metaMut.isPending}
          onCheckedChange={(v) => metaMut.mutate({ autoEnrich: v })}
          aria-label="Toggle auto-enrich on bookdrop approve"
        />
      </label>

      <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
        {ordered.map((p, idx) => (
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
  const [values, setValues] = useState<Record<string, string>>(() =>
    schemaToForm(schema, provider.config ?? {})
  )
  // Rehydrate when the server payload shifts (e.g. another admin saved).
  // useRef ensures we don't nuke in-flight edits; sync only if the stored
  // config hash changes.
  const configHash = JSON.stringify(provider.config ?? {})
  useEffect(() => {
    // Prop→state rehydration when another admin saves; not a cascading render.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setValues(schemaToForm(schema, provider.config ?? {}))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [configHash])

  const dirty = schema.some(
    (f) => (values[f.key] ?? "") !== valueToString(provider.config?.[f.key])
  )

  return (
    <div
      style={{
        padding: "14px 16px",
        border: "1px solid var(--color-rule-soft)",
        background: "var(--color-paper-0)",
        borderRadius: 2,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
        <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
          <button
            type="button"
            className="btn-icon"
            aria-label="Move up"
            disabled={position === 0 || busy}
            onClick={onMoveUp}
            style={iconBtnStyle(position === 0)}
          >
            <Icon name="chevron-up" size={12} />
          </button>
          <button
            type="button"
            className="btn-icon"
            aria-label="Move down"
            disabled={position === total - 1 || busy}
            onClick={onMoveDown}
            style={iconBtnStyle(position === total - 1)}
          >
            <Icon name="chevron-down" size={12} />
          </button>
        </div>
        <span
          style={{
            width: 8,
            height: 8,
            borderRadius: "50%",
            background: provider.enabled
              ? "oklch(0.58 0.12 140)"
              : "var(--color-ink-4)",
            transition: "background 160ms ease",
          }}
        />
        <div style={{ flex: 1, minWidth: 0 }}>
          <div className="t-item-title">{provider.name}</div>
          <div className="t-item-sub">
            <span className="mono">{provider.id}</span>
            {provider.external && " · external API"}
            {provider.priority != null && ` · priority ${provider.priority}`}
          </div>
          <ProviderHealth
            successAt={provider.lastSuccessAt}
            errorAt={provider.lastErrorAt}
            lastError={provider.lastError}
          />
        </div>
        <Switch
          id={`provider-${provider.id}`}
          checked={provider.enabled}
          disabled={busy}
          onCheckedChange={onToggle}
          aria-label={`${provider.enabled ? "Disable" : "Enable"} ${provider.name}`}
        />
      </div>

      {schema.length > 0 && (
        <div
          style={{
            marginTop: 14,
            paddingTop: 14,
            borderTop: "1px dashed var(--color-rule-soft)",
            display: "flex",
            flexDirection: "column",
            gap: 10,
          }}
        >
          {schema.map((field) => (
            <ConfigFieldRow
              key={field.key}
              field={field}
              value={values[field.key] ?? ""}
              onChange={(v) =>
                setValues((prev) => ({ ...prev, [field.key]: v }))
              }
            />
          ))}
          <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={!dirty || busy}
              onClick={() =>
                setValues(schemaToForm(schema, provider.config ?? {}))
              }
            >
              Revert
            </Button>
            <Button
              type="button"
              size="sm"
              disabled={!dirty || busy}
              onClick={() => onSaveConfig(formToConfig(schema, values))}
            >
              Save config
            </Button>
          </div>
        </div>
      )}
    </div>
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
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          fontSize: 12,
          color: "var(--color-ink-3)",
          marginBottom: 4,
          fontFamily: "var(--font-mono)",
          letterSpacing: "0.04em",
        }}
      >
        <span>{field.label}</span>
        {field.kind === "password" && value && (
          <button
            type="button"
            onClick={() => setReveal((r) => !r)}
            style={{
              padding: 0,
              border: "none",
              background: "transparent",
              cursor: "pointer",
              fontSize: 10,
              color: "var(--color-accent-ink)",
            }}
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
        <div className="t-small" style={{ marginTop: 4, fontSize: 11.5 }}>
          {field.help}
        </div>
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
  const color = errorWins ? "var(--color-accent-ink)" : "oklch(0.48 0.11 140)"
  return (
    <div
      className="t-small"
      style={{ fontSize: 11, marginTop: 4, color }}
      title={errorWins ? lastError : "Last successful fetch"}
    >
      {errorWins
        ? `failed ${rel}${lastError ? ` — ${truncate(lastError, 80)}` : ""}`
        : `ok ${rel}`}
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

function iconBtnStyle(disabled: boolean): CSSProperties {
  return {
    padding: 2,
    border: "1px solid var(--color-rule-soft)",
    background: "transparent",
    cursor: disabled ? "default" : "pointer",
    opacity: disabled ? 0.3 : 1,
    lineHeight: 0,
  }
}

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

// ---------------------------------------------------------------------------
// Email delivery (informational)
// ---------------------------------------------------------------------------

