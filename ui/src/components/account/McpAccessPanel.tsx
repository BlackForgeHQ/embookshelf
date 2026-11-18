import { useState } from "react"
import type { FormEvent } from "react"

import type { APIToken, CreatedAPIToken } from "@/api/mcp"
import {
  TOKEN_PLACEHOLDER,
  accountTokensQuery,
  clientSnippets,
  createAccountToken,
  mcpEndpoint,
  revokeAccountToken,
} from "@/api/mcp"
import { useApiMutation } from "@/api/mutation"
import { useApiQuery } from "@/api/query"
import { appConfigQuery } from "@/api/settings"
import { formatDate, formatDateTime } from "@/lib/format"
import { CopyButton } from "@/components/CopyButton"
import { Icon } from "@/components/Icon"
import { Card, Field, PanelHeader, Select } from "@/components/SettingsShared"
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

const EXPIRY_OPTIONS = [
  { value: "0", label: "Never" },
  { value: "30", label: "30 days" },
  { value: "90", label: "90 days" },
  { value: "365", label: "1 year" },
]

const INTRO = (
  <>
    Connect an assistant such as Claude to your library over the Model Context
    Protocol. It reads as you, and sees only what you can see here.
  </>
)

export function McpAccessPanel() {
  // Tri-state, like the invites panel: undefined is "not loaded yet",
  // which is neither permission to show the controls nor grounds to say
  // MCP is off.
  const mcpEnabled = useApiQuery(appConfigQuery).data?.mcpEnabled

  if (mcpEnabled === undefined) {
    return <PanelHeader title="MCP access">{INTRO}</PanelHeader>
  }
  if (!mcpEnabled) {
    return (
      <>
        <PanelHeader title="MCP access">{INTRO}</PanelHeader>
        <p className="t-small text-muted-foreground italic">
          Your administrator hasn't enabled MCP on this instance.
        </p>
      </>
    )
  }
  return <McpAccessEnabled />
}

function McpAccessEnabled() {
  const tokens = useApiQuery(accountTokensQuery)
  const [creating, setCreating] = useState(false)
  const revokeMut = useApiMutation(revokeAccountToken, {
    successToast: "Token revoked.",
  })

  const endpoint =
    typeof window === "undefined" ? "/mcp" : mcpEndpoint(window.location.origin)
  const rows = tokens.data ?? []

  return (
    <>
      <PanelHeader
        title="MCP access"
        actions={
          <Button type="button" variant="outline" size="sm" onClick={() => setCreating(true)}>
            <Icon name="plus" size={13} /> New token
          </Button>
        }
      >
        {INTRO}
      </PanelHeader>

      <div className="t-label mb-2.5">Connecting a client</div>
      <Card>
        <Field label="Server URL">
          <div className="flex gap-2">
            <Input readOnly value={endpoint} className="mono flex-1" />
            <CopyButton value={endpoint} size="default" />
          </div>
        </Field>
        <p className="t-small">
          Replace <span className="mono">{TOKEN_PLACEHOLDER}</span> with a token
          from <strong>New token</strong>. The token is filled in for you when you
          make one.
        </p>
        <Snippets endpoint={endpoint} token={TOKEN_PLACEHOLDER} />
      </Card>

      <div className="t-label mt-6 mb-2.5">Your tokens</div>

      {tokens.isLoading && (
        <p className="t-small text-muted-foreground italic">Loading tokens…</p>
      )}

      {!tokens.isLoading && rows.length === 0 && (
        <p className="t-small text-muted-foreground italic">
          No tokens yet. Make one to connect a client.
        </p>
      )}

      <div className="flex flex-col gap-2">
        {rows.map((t) => (
          <TokenRow
            key={t.id}
            token={t}
            busy={revokeMut.isPending}
            onRevoke={() => {
              if (
                window.confirm(
                  `Revoke "${t.name}"? Clients using it will be refused immediately.`
                )
              ) {
                revokeMut.mutate(t.id)
              }
            }}
          />
        ))}
      </div>

      <NewTokenDialog
        open={creating}
        onOpenChange={setCreating}
        endpoint={endpoint}
      />
    </>
  )
}

function TokenRow({
  token,
  busy,
  onRevoke,
}: {
  token: APIToken
  busy: boolean
  onRevoke: () => void
}) {
  return (
    <div className="flex items-center gap-4 rounded-lg border border-border bg-card p-3 shadow-sm">
      <div className="min-w-0 flex-1">
        <div className="truncate text-[14px] leading-snug font-medium">
          {token.name}{" "}
          <span className="mono text-xs text-muted-foreground">{token.prefix}…</span>
        </div>
        <div className="mt-0.5 truncate text-xs text-muted-foreground">
          {"created "}
          {formatDate(token.createdAt)}
          {" · "}
          {token.lastUsedAt
            ? `last used ${formatDateTime(token.lastUsedAt)}`
            : "never used"}
          {" · "}
          {token.expiresAt ? `expires ${formatDate(token.expiresAt)}` : "never expires"}
        </div>
      </div>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={onRevoke}
        disabled={busy}
        className="text-destructive hover:bg-destructive/10 hover:text-destructive"
      >
        Revoke
      </Button>
    </div>
  )
}

function Snippets({ endpoint, token }: { endpoint: string; token: string }) {
  return (
    <div className="flex flex-col gap-3">
      {clientSnippets(endpoint, token).map((s) => (
        <div key={s.id} className="flex flex-col gap-1.5">
          <div className="flex items-center justify-between gap-2">
            <div>
              <span className="t-label">{s.label}</span>
              <span className="t-small ml-2 text-muted-foreground">{s.hint}</span>
            </div>
            <CopyButton value={s.code} />
          </div>
          <pre
            data-testid={`snippet-${s.id}`}
            className="mono overflow-x-auto rounded-md border border-border bg-muted/40 p-3 text-[12px] leading-relaxed whitespace-pre"
          >
            {s.code}
          </pre>
        </div>
      ))}
    </div>
  )
}

// NewTokenDialog has two faces: the form, and — once the server answers —
// the token itself with the snippets filled in. The raw token lives only
// in this component's state; closing the dialog drops it for good.
function NewTokenDialog({
  open,
  onOpenChange,
  endpoint,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  endpoint: string
}) {
  const [name, setName] = useState("")
  const [days, setDays] = useState("0")
  const [created, setCreated] = useState<CreatedAPIToken | null>(null)

  const createMut = useApiMutation(createAccountToken, {
    reportErrors: "inline",
    onSuccess: (result) => setCreated(result),
  })

  function close(next: boolean) {
    if (!next) {
      setName("")
      setDays("0")
      setCreated(null)
      createMut.reset()
    }
    onOpenChange(next)
  }

  function submit(e: FormEvent) {
    e.preventDefault()
    createMut.mutate({ name: name.trim(), expiresInDays: Number(days) })
  }

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="sm:max-w-[560px]">
        {created ? (
          <>
            <DialogHeader>
              <DialogTitle>Token created</DialogTitle>
              <DialogDescription>
                Copy it now. It won't be shown again — if you lose it, revoke it
                and make another.
              </DialogDescription>
            </DialogHeader>
            <Field label={created.meta.name}>
              <div className="flex gap-2">
                <Input
                  readOnly
                  value={created.token}
                  className="mono flex-1"
                  data-testid="new-token"
                />
                <CopyButton value={created.token} size="default" />
              </div>
            </Field>
            <Snippets endpoint={endpoint} token={created.token} />
            <DialogFooter>
              <Button type="button" size="sm" onClick={() => close(false)}>
                Done
              </Button>
            </DialogFooter>
          </>
        ) : (
          <form onSubmit={submit} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>New token</DialogTitle>
              <DialogDescription>
                Name it after where it will live, so you know which to revoke.
              </DialogDescription>
            </DialogHeader>
            <Field label="Name">
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Claude Desktop on my laptop"
                maxLength={100}
                autoFocus
                required
              />
            </Field>
            <Field label="Expires">
              <Select value={days} onChange={setDays} options={EXPIRY_OPTIONS} />
            </Field>
            {createMut.error && (
              <p className="t-small text-destructive" role="alert">
                {createMut.error.message}
              </p>
            )}
            <DialogFooter>
              <Button type="button" variant="ghost" size="sm" onClick={() => close(false)}>
                Cancel
              </Button>
              <Button
                type="submit"
                size="sm"
                disabled={createMut.isPending || !name.trim()}
              >
                {createMut.isPending ? "Creating…" : "Create token"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
