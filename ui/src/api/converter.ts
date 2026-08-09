import { api } from "./client"
import { defineMutation } from "./mutation"
import { defineQuery } from "./query"

// Mirrors internal/handler/converter_settings.go converterSettingsDTO.
// The converter extension (ADR-0033) is the Rust sidecar that turns
// Convertible-format books into Markdown renditions. No secrets in v1,
// so the whole row travels both ways.
export type ConverterSettings = {
  enabled: boolean
  baseUrl: string
}

// Mirrors converterHealthDTO. "not_configured" means no probe was
// attempted; "unreachable" carries the dial error verbatim.
export type ConverterHealth = {
  status: "not_configured" | "ok" | "unreachable"
  version?: string
  error?: string
}

export const converterSettingsQueryKey = ["settings", "converter"] as const
export const converterHealthQueryKey = [
  "settings",
  "converter",
  "health",
] as const

export const converterSettingsQuery = defineQuery({
  key: converterSettingsQueryKey,
  fn: (): Promise<ConverterSettings> =>
    api<ConverterSettings>("/api/v1/settings/converter"),
})

// The probe re-runs after every save: reachability is a property of the
// URL just written, so a stale answer about the previous URL is worse
// than no answer.
export const converterHealthQuery = defineQuery({
  key: converterHealthQueryKey,
  fn: (): Promise<ConverterHealth> =>
    api<ConverterHealth>("/api/v1/settings/converter/health"),
})

export const updateConverterSettings = defineMutation({
  fn: (body: ConverterSettings): Promise<ConverterSettings> =>
    api<ConverterSettings>("/api/v1/settings/converter", {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  invalidates: [converterSettingsQueryKey, converterHealthQueryKey],
})
