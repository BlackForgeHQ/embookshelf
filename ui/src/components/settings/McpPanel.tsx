import type { FormEvent } from "react"

import type { MCPSettings } from "@/api/mcp"
import {
  mcpEndpoint,
  mcpSettingsQuery,
  mcpTokensQuery,
  revokeMCPToken,
  saveMCPSettings,
} from "@/api/mcp"
import { useApiMutation } from "@/api/mutation"
import { useApiQuery } from "@/api/query"
import { formatDate, formatDateTime } from "@/lib/format"
import { useSettingsDraft } from "@/hooks/useSettingsDraft"
import { CopyButton } from "@/components/CopyButton"
import { Panel, SaveRow } from "@/components/settings/Panel"
import { Card, Field, Toggle } from "@/components/SettingsShared"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

const emptyForm: MCPSettings = { enabled: false }

const INTRO = (
  <>
    Let assistants such as Claude read this library over the Model Context
    Protocol. Each user connects with their own access token, made under
    Account → MCP access, and sees only what they can see here. ADR-0039.
  </>
)

export function McpPanel() {
  const draft = useSettingsDraft({
    query: mcpSettingsQuery,
    initial: emptyForm,
    save: saveMCPSettings,
    successToast: "MCP settings saved.",
    toPayload: (form) => form,
  })

  // The endpoint follows what is saved, not the unsaved switch: it is
  // only worth copying once clients can actually reach it.
  const live = draft.server?.enabled === true
  const endpoint =
    typeof window === "undefined" ? "/mcp" : mcpEndpoint(window.location.origin)

  function onSave(e: FormEvent) {
    e.preventDefault()
    draft.save()
  }

  return (
    <Panel title="MCP" intro={INTRO} loading={draft.loading}>
      <form onSubmit={onSave} className="flex flex-col gap-4">
        <Card>
          <Toggle
            label="Enable the MCP server"
            hint="When off, every client is refused and no new token can be made. Existing tokens are kept and work again when it is turned back on."
            checked={draft.value.enabled}
            onChange={(v) => draft.patch("enabled", v)}
          />
          {live && (
            <Field label="Server URL">
              <div className="flex gap-2">
                <Input readOnly value={endpoint} className="mono flex-1" />
                <CopyButton value={endpoint} size="default" />
              </div>
            </Field>
          )}
        </Card>
        <SaveRow draft={draft} />
      </form>

      <TokensOverview />
    </Panel>
  )
}

function TokensOverview() {
  const tokens = useApiQuery(mcpTokensQuery)
  const revokeMut = useApiMutation(revokeMCPToken, {
    successToast: "Token revoked.",
  })
  const rows = tokens.data ?? []

  return (
    <section className="mt-8">
      <div className="t-label mb-2.5">Access tokens</div>
      <p className="t-small mb-4 italic">
        Every user's tokens. Revoking one stops it working on the next request.
      </p>

      {tokens.isLoading && (
        <p className="t-small text-muted-foreground italic">Loading tokens…</p>
      )}

      {!tokens.isLoading && rows.length === 0 && (
        <p className="t-small text-muted-foreground italic">
          No one has made a token yet.
        </p>
      )}

      <div className="flex flex-col gap-2">
        {rows.map((t) => (
          <div
            key={t.id}
            className="flex items-center gap-4 rounded-lg border border-border bg-card p-3 shadow-sm"
          >
            <div className="min-w-0 flex-1">
              <div className="truncate text-[14px] leading-snug font-medium">
                {t.name} <span className="mono text-xs text-muted-foreground">{t.prefix}…</span>
              </div>
              <div className="mt-0.5 truncate text-xs text-muted-foreground">
                {t.owner.name || t.owner.email}
                {" · created "}
                {formatDate(t.createdAt)}
                {" · "}
                {t.lastUsedAt ? `last used ${formatDateTime(t.lastUsedAt)}` : "never used"}
                {t.expiresAt && ` · expires ${formatDate(t.expiresAt)}`}
              </div>
            </div>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => {
                if (
                  window.confirm(
                    `Revoke "${t.name}" for ${t.owner.email}? Clients using it will be refused immediately.`
                  )
                ) {
                  revokeMut.mutate(t.id)
                }
              }}
              disabled={revokeMut.isPending}
              className="text-destructive hover:bg-destructive/10 hover:text-destructive"
            >
              Revoke
            </Button>
          </div>
        ))}
      </div>
    </section>
  )
}
