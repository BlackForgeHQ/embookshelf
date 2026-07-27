import { api } from "./client"
import { bookQueryKey } from "./books"
import { defineMutation } from "./mutation"

// Mirrors internal/handler/reading_guide.go readingGuideDTO.
export type ReadingGuide = {
  about: string
  audience: string
  notFor: string
  problems: string
  // What the guide was written from. A metadata-only guide leans on what
  // the model already believed about the title, so the UI says so
  // (ADR-0024 §2).
  sourceKind: "full_text" | "metadata"
  model: string
  language: string
  generatedAt: string
  editedByUser: boolean
}

export const bookGuideQueryKey = (id: string) => ["book-guide", id] as const

// fetchBookGuide resolves to null when no guide exists yet. The server
// answers 404 for that, which is a normal state rather than an error —
// most books have no guide until someone asks for one.
export async function fetchBookGuide(id: string): Promise<ReadingGuide | null> {
  try {
    const res = await api<{ guide: ReadingGuide }>(`/api/v1/books/${id}/guide`)
    return res.guide
  } catch (err) {
    if ((err as { status?: number }).status === 404) return null
    throw err
  }
}

// generateBookGuide returns 202: the work runs on the queue and the
// `guide.updated` SSE event busts the cache when it lands.
export const generateBookGuide = defineMutation({
  fn: (id: string): Promise<void> =>
    api<void>(`/api/v1/books/${id}/guide`, { method: "POST" }),
  invalidates: (id) => [bookGuideQueryKey(id)],
})

export type ReadingGuideEdit = {
  about: string
  audience: string
  notFor: string
  problems: string
}

// saveBookGuide marks the guide hand-written, which stops bulk runs from
// overwriting it.
export const saveBookGuide = defineMutation({
  fn: (args: { id: string; edit: ReadingGuideEdit }): Promise<{ guide: ReadingGuide }> =>
    api<{ guide: ReadingGuide }>(`/api/v1/books/${args.id}/guide`, {
      method: "PUT",
      body: JSON.stringify(args.edit),
    }),
  invalidates: (args) => [bookGuideQueryKey(args.id), bookQueryKey(args.id)],
})

// --- admin ---------------------------------------------------------------

// Mirrors readingGuideSettingsDTO. apiKey is write-only: the server never
// returns it, and sending an empty one means "keep the stored key".
export type ReadingGuideSettings = {
  enabled: boolean
  baseUrl: string
  model: string
  apiKey?: string
  keySet: boolean
  // "bearer" for OpenAI/Ollama/OpenRouter, "api-key" for Azure.
  authStyle: "bearer" | "api-key"
  language: string
  textCap: number
  requestJsonMode: boolean
}

export const readingGuideSettingsQueryKey = ["reading-guide-settings"] as const

export async function fetchReadingGuideSettings(): Promise<ReadingGuideSettings> {
  return api<ReadingGuideSettings>("/api/v1/settings/reading-guide")
}

export const saveReadingGuideSettings = defineMutation({
  fn: (cfg: ReadingGuideSettings): Promise<ReadingGuideSettings> =>
    api<ReadingGuideSettings>("/api/v1/settings/reading-guide", {
      method: "PUT",
      body: JSON.stringify(cfg),
    }),
  invalidates: [readingGuideSettingsQueryKey],
})

// GuideRunEstimate is a ceiling, not a prediction — it assumes every
// full-text book fills the cap. Shown before a run starts so the cost
// follows visibly from a decision (ADR-0024 §4).
export type GuideRunEstimate = {
  books: number
  fullTextBooks: number
  maxInputTokens: number
  // Library coverage, not run state — a reload does not reset it and a
  // run started before the last restart still shows up.
  totalBooks: number
  booksWithGuide: number
}

export const guideEstimateQueryKey = ["reading-guide-estimate"] as const

export async function fetchGuideEstimate(): Promise<GuideRunEstimate> {
  return api<GuideRunEstimate>("/api/v1/settings/reading-guide/estimate")
}

// testReadingGuide sends one trivial prompt and reports what came back.
// Always resolves 200 — a refused endpoint is a result to display, not a
// request that failed.
export type GuideTestResult = { ok: boolean; reply?: string; error?: string }

export const testReadingGuide = defineMutation({
  fn: (): Promise<GuideTestResult> =>
    api<GuideTestResult>("/api/v1/settings/reading-guide/test", {
      method: "POST",
    }),
  invalidates: [],
})

export const startGuideRun = defineMutation({
  fn: (): Promise<{ queued: number }> =>
    api<{ queued: number }>("/api/v1/settings/reading-guide/run", {
      method: "POST",
    }),
  invalidates: [guideEstimateQueryKey],
})
