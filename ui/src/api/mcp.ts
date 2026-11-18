import { api } from "./client"
import { defineMutation } from "./mutation"
import { defineQuery } from "./query"
import { appConfigQueryKey } from "./settings"

// MCP server access (ADR-0039): the admin's instance switch and token
// oversight, and each user's own personal access tokens.

// APIToken mirrors handler.apiTokenDTO. `prefix` is display-only
// ("ebk_abcd1234"); the token itself is never sent back after creation.
export type APIToken = {
  id: string
  name: string
  prefix: string
  createdAt: string
  lastUsedAt: string | null
  expiresAt: string | null
}

export type AdminAPIToken = APIToken & {
  owner: { id: string; email: string; name: string }
}

// Declared up front: a token minted or revoked on either side changes
// both lists, so each side's mutations name the other's key.
export const accountTokensQueryKey = ["account", "tokens"] as const
export const mcpSettingsQueryKey = ["settings", "mcp"] as const
export const mcpTokensQueryKey = ["settings", "mcp", "tokens"] as const

// --- Account: the user's own tokens ----------------------------------------

export const accountTokensQuery = defineQuery({
  key: accountTokensQueryKey,
  fn: async (): Promise<Array<APIToken>> => {
    const { tokens } = await api<{ tokens: Array<APIToken> }>(
      "/api/v1/account/tokens"
    )
    return tokens
  },
})

export type CreatedAPIToken = { token: string; meta: APIToken }

// expiresInDays 0 means the token never expires.
export const createAccountToken = defineMutation({
  fn: (body: { name: string; expiresInDays: number }): Promise<CreatedAPIToken> =>
    api<CreatedAPIToken>("/api/v1/account/tokens", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  invalidates: [accountTokensQueryKey, mcpTokensQueryKey],
})

export const revokeAccountToken = defineMutation({
  fn: (id: string): Promise<void> =>
    api<void>(`/api/v1/account/tokens/${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),
  invalidates: [accountTokensQueryKey, mcpTokensQueryKey],
})

// --- Admin: the instance switch and every user's tokens --------------------

export type MCPSettings = { enabled: boolean }

export const mcpSettingsQuery = defineQuery({
  key: mcpSettingsQueryKey,
  fn: (): Promise<MCPSettings> => api<MCPSettings>("/api/v1/settings/mcp"),
})

// The app config carries the same flag for every user, so it goes stale
// with the row.
export const saveMCPSettings = defineMutation({
  fn: (body: MCPSettings): Promise<MCPSettings> =>
    api<MCPSettings>("/api/v1/settings/mcp", {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  invalidates: [mcpSettingsQueryKey, appConfigQueryKey],
})

export const mcpTokensQuery = defineQuery({
  key: mcpTokensQueryKey,
  fn: async (): Promise<Array<AdminAPIToken>> => {
    const { tokens } = await api<{ tokens: Array<AdminAPIToken> }>(
      "/api/v1/settings/mcp/tokens"
    )
    return tokens
  },
})

export const revokeMCPToken = defineMutation({
  fn: (id: string): Promise<void> =>
    api<void>(`/api/v1/settings/mcp/tokens/${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),
  invalidates: [mcpTokensQueryKey, accountTokensQueryKey],
})

// --- Connecting a client ----------------------------------------------------

// The endpoint as the browser reaches it. The server has no configured
// public URL, and the origin this page loaded from is the one users can
// reach.
export function mcpEndpoint(origin: string): string {
  return `${origin.replace(/\/+$/, "")}/mcp`
}

export const TOKEN_PLACEHOLDER = "<your-token>"

export type ClientSnippet = { id: string; label: string; hint: string; code: string }

// The ready-to-paste setup for each client. `token` is the real token
// right after creation, and the placeholder otherwise.
export function clientSnippets(endpoint: string, token: string): Array<ClientSnippet> {
  const json = {
    mcpServers: {
      embookshelf: {
        type: "http",
        url: endpoint,
        headers: { Authorization: `Bearer ${token}` },
      },
    },
  }
  return [
    {
      id: "claude-code",
      label: "Claude Code",
      hint: "Run in a terminal.",
      code: `claude mcp add --transport http embookshelf ${endpoint} --header "Authorization: Bearer ${token}"`,
    },
    {
      id: "json",
      label: "JSON config",
      hint: "For Claude Desktop, Cursor and other clients that take an mcpServers block.",
      code: JSON.stringify(json, null, 2),
    },
  ]
}
