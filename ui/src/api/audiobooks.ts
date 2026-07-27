import { api } from "./client"
import { bookQueryKey } from "./books"
import { defineMutation } from "./mutation"

// Mirrors internal/handler/audiobook.go audiobookDTO.
//
// Segment counts rather than a percentage: there is no job-status API, so
// progress is done-over-total on rows that survive a reload and a restart
// — the property a run measured in tens of minutes actually needs
// (ADR-0028 §7).
export type Audiobook = {
  bookId: string
  state: "pending" | "running" | "ready" | "failed" | "canceled"
  engine: string
  voice: string
  model?: string
  error?: string
  segmentsTotal: number
  segmentsDone: number
  segmentsFailed: number
  durationSeconds: number
  // The EPUB changed after this narration was made, so the audio is of
  // the older text. Surfaced, never acted on — discarding hours of audio
  // because someone re-uploaded a better copy would be worse.
  stale: boolean
}

export const bookAudiobookQueryKey = (id: string) => ["book-audiobook", id] as const

// fetchBookAudiobook resolves to null when a book has never been
// narrated. The server answers 404 for that, which is the normal state
// for almost every book rather than an error.
export async function fetchBookAudiobook(id: string): Promise<Audiobook | null> {
  try {
    return await api<Audiobook>(`/api/v1/books/${id}/audiobook`)
  } catch (err) {
    if ((err as { status?: number }).status === 404) return null
    throw err
  }
}

// AudiobookEstimate is the guardrail on a real-money action. Priced from
// an admin-set $/1M characters, so the number is real rather than a stale
// figure we shipped months ago (ADR-0028 §2).
export type AudiobookEstimate = {
  chars: number
  segments: number
  audioSeconds: number
  costUsd: number
  engine: string
  voice: string
}

export async function fetchAudiobookEstimate(id: string): Promise<AudiobookEstimate> {
  return api<AudiobookEstimate>(`/api/v1/books/${id}/audiobook/estimate`)
}

// generateAudiobook returns 202: the work is tens of queued jobs, and the
// page polls the status endpoint from here. Voice and model are optional
// overrides of the instance default (ADR-0026 §6).
export const generateAudiobook = defineMutation({
  fn: (args: { id: string; voice?: string; model?: string }): Promise<{ queued: boolean }> =>
    api<{ queued: boolean }>(`/api/v1/books/${args.id}/audiobook`, {
      method: "POST",
      body: JSON.stringify({ voice: args.voice ?? "", model: args.model ?? "" }),
    }),
  invalidates: (args) => [bookAudiobookQueryKey(args.id)],
})

// cancelAudiobook is the only stop-loss on a run already spending money.
export const cancelAudiobook = defineMutation({
  fn: (id: string): Promise<void> =>
    api<void>(`/api/v1/books/${id}/audiobook/cancel`, { method: "POST" }),
  invalidates: (id) => [bookAudiobookQueryKey(id)],
})

// retryAudiobook re-enqueues only the segments that never finished — the
// completed ones are already paid for.
export const retryAudiobook = defineMutation({
  fn: (id: string): Promise<{ queued: boolean }> =>
    api<{ queued: boolean }>(`/api/v1/books/${id}/audiobook/retry`, { method: "POST" }),
  invalidates: (id) => [bookAudiobookQueryKey(id)],
})

// deleteAudiobook removes the narration and its bytes. The book keeps its
// EPUB, so this also busts the book itself: chapters and duration go with
// the audio.
export const deleteAudiobook = defineMutation({
  fn: (id: string): Promise<void> =>
    api<void>(`/api/v1/books/${id}/audiobook`, { method: "DELETE" }),
  invalidates: (id) => [bookAudiobookQueryKey(id), bookQueryKey(id)],
})

// narrationUrl is the audio rendition's source. books.format names the
// primary format, so the selector is what makes the narration reachable
// at all (ADR-0025 §3).
export function narrationUrl(id: string, opts: { download?: boolean } = {}): string {
  const params = new URLSearchParams({ rendition: "audio" })
  if (opts.download) params.set("download", "1")
  return `/api/v1/books/${id}/file?${params.toString()}`
}

// --- admin ---------------------------------------------------------------

// One engine's row in the settings panel. apiKey is write-only: the
// server returns keySet instead, and sending an empty key means "keep the
// stored one".
export type AudiobookEngine = {
  id: string
  label: string
  enabled: boolean
  baseUrl: string
  apiKey?: string
  keySet: boolean
  model: string
  defaultVoice: string
  pricePerMillionChars: number
  // Catalog facts, read-only: the engine's own per-request cap, and
  // whether it takes a model or needs an operator-chosen endpoint.
  maxRequestChars: number
  needsModel: boolean
  needsBaseUrl: boolean
}

// Mirrors audiobookSettingsDTO. Deliberately no segment cap or timeout —
// ADR-0028 §3 states the cap as a fixed property of the design, and a
// knob would invite a value that breaks it.
export type AudiobookSettings = {
  enabled: boolean
  engine: string
  engines: Array<AudiobookEngine>
}

export const audiobookSettingsQueryKey = ["audiobook-settings"] as const

export async function fetchAudiobookSettings(): Promise<AudiobookSettings> {
  return api<AudiobookSettings>("/api/v1/settings/audiobook")
}

export const saveAudiobookSettings = defineMutation({
  fn: (cfg: AudiobookSettings): Promise<AudiobookSettings> =>
    api<AudiobookSettings>("/api/v1/settings/audiobook", {
      method: "PUT",
      body: JSON.stringify(cfg),
    }),
  invalidates: [audiobookSettingsQueryKey],
})

export type AudiobookVoice = { id: string; label: string }

export const audiobookVoicesQueryKey = ["audiobook-voices"] as const

export async function fetchAudiobookVoices(): Promise<Array<AudiobookVoice>> {
  const res = await api<{ voices: Array<AudiobookVoice> }>(
    "/api/v1/settings/audiobook/voices"
  )
  return res.voices
}

// testAudiobook synthesizes one short phrase, so a wrong key surfaces now
// rather than forty minutes into a run.
export type AudiobookTestResult = { ok: boolean; engine: string; bytes: number }

export const testAudiobook = defineMutation({
  fn: (): Promise<AudiobookTestResult> =>
    api<AudiobookTestResult>("/api/v1/settings/audiobook/test", { method: "POST" }),
  invalidates: [],
})
