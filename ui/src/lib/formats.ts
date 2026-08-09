import type { RunView } from "@/lib/audiobookRun"

// What a book format can do, declared once for the client.
//
// The server declares the same facts in internal/model/format.go, and
// internal/model/format_parity_test.go holds the two sets equal in both
// directions. Neither side fetches this at runtime — asking the server
// whether EPUB has text would be absurd — so the parity test is what
// stops them drifting, the same arrangement the SSE catalog and the API
// error-code union already use.
//
// Keep the list one entry per line: the Go test parses it by exact
// anchor and fails loudly if it reads zero members.
export const NARRATABLE_FORMATS = [
  "EPUB",
] as const

/**
 * The formats Amazon's Send-to-Kindle service accepts (ADR-0021).
 *
 * A separate list from NARRATABLE_FORMATS, not a reuse of it. They
 * overlap at EPUB and answer different questions — "does Amazon take
 * this" versus "is there text to read aloud" — and one list standing for
 * both would make PDF narratable the day Amazon changed its mind.
 */
export const KINDLE_ELIGIBLE_FORMATS = [
  "EPUB",
  "PDF",
] as const

/**
 * The formats the converter extension turns into Markdown renditions
 * (ADR-0033): what anydoc converts minus what native extraction already
 * serves. EPUB is deliberately absent — routing it through an optional
 * sidecar would be the regression ADR-0033 §2 rejects.
 */
export const CONVERTIBLE_FORMATS = [
  "PDF",
] as const

/** Which reading surface opens a format. */
export type ReaderKind = "text" | "comic" | "audio"

/**
 * Format → reading surface.
 *
 * Distinct from the Rendition the user picks inside that surface: an
 * EPUB with a narration is still "text" here, and the text-or-audio
 * choice happens after (ADR-0025 §3). books.format stays the
 * primary-format cache.
 *
 * A format absent from this map has no reader — it downloads and nothing
 * else. The Go parity test reads this by exact anchor, so keep it one
 * entry per line.
 */
export const FORMAT_READERS: Record<string, ReaderKind> = {
  EPUB: "text",
  PDF: "text",
  CBZ: "comic",
  MP3: "audio",
  M4B: "audio",
}

/** The surface that opens this book, or null when nothing reads it. */
export function readerKindForFormat(format: string): ReaderKind | null {
  return FORMAT_READERS[format.trim().toUpperCase()] ?? null
}

/**
 * Whether this book's format carries text a speech engine can read.
 *
 * The first of the Narratable format's three gates — the handler and the
 * segment worker hold the other two, because a re-import can change a
 * book's format between them.
 */
export function isNarratableFormat(format: string): boolean {
  const want = format.trim().toUpperCase()
  return NARRATABLE_FORMATS.some((f) => f === want)
}

/**
 * The narratable formats as a sentence fragment: "EPUB", or "EPUB and
 * PDF" if a second one ever qualifies. Every user-facing sentence about
 * narration builds from this rather than spelling EPUB out, which is how
 * the same claim came to be written in five places.
 */
export function narratableFormatList(): string {
  return formatList([...NARRATABLE_FORMATS])
}

/**
 * Whether Send-to-Kindle will take this book's format. Mirrors
 * service.IsKindleEligible so the UI can disable the action up front
 * instead of round-tripping for a 415.
 */
export function isKindleEligibleFormat(format: string): boolean {
  const want = format.trim().toUpperCase()
  return KINDLE_ELIGIBLE_FORMATS.some((f) => f === want)
}

/** "EPUB and PDF", for the sentence explaining why the button is off. */
export function kindleEligibleFormatList(): string {
  return formatList([...KINDLE_ELIGIBLE_FORMATS])
}

/**
 * Whether the converter extension accepts this book's format. The first
 * of the Convertible format's three gates — the handler and the worker
 * hold the other two.
 */
export function isConvertibleFormat(format: string): boolean {
  const want = format.trim().toUpperCase()
  return CONVERTIBLE_FORMATS.some((f) => f === want)
}

/** "PDF", for the sentence explaining why conversion is refused. */
export function convertibleFormatList(): string {
  return formatList([...CONVERTIBLE_FORMATS])
}

function formatList(names: Array<string>): string {
  const last = names.pop()
  if (last === undefined) return "no"
  if (names.length === 0) return last
  return `${names.join(", ")} and ${last}`
}

// -------------------------------------------------------------------
// Renditions
//
// Adjacent to everything above and about the same book: which reader
// opens a format is a question this module already answers, and which
// Renditions a book has is the next one. Lived in its own file until it
// had a second consumer to justify the hop; it never got one (#213).
// -------------------------------------------------------------------

/**
 * One of the ways a single Book can be consumed (ADR-0025).
 *
 * `primary` is the book's own file — text, comic or audio, whichever it
 * was ingested as. `narration` is the generated audiobook. Both are
 * `files` rows on the same `books` row: narrating a book produces
 * another artifact of the same work, not another work.
 *
 * Deliberately not one value per format. Which renderer opens the
 * primary file is a different question, answered by
 * `readerKindForFormat`, and conflating the two is the drift ADR-0025 §3
 * predicted: "several call sites branch on book.format and are, after
 * this, answering a subtly different question than the one they think
 * they are."
 */
export type Rendition = "primary" | "narration"

export type RenditionState = {
  /** What this book can be consumed as, in offer order. */
  available: Array<Rendition>
  /** What to open. Null when nothing can open this book. */
  selected: Rendition | null
  /** Whether there is a choice to present. */
  canSwitch: boolean
}

/**
 * Which Renditions a book has, and which one to open.
 *
 * `prefer` is what the reader asked for — the Listen toggle — and is a
 * request rather than an instruction: a narration that is not playable
 * falls back to the book's own file, which is what a reader who reloads
 * mid-run needs to land on instead of a player with nothing behind it.
 *
 * `narration` is the run *view*, not the wire row, and null when the
 * book has never been narrated. This module used to take the row and
 * compare `state === "ready"` itself, which put "is this narration
 * playable" in two modules and meant a new terminal state had to be
 * found in both (#243). `lib/audiobookRun` owns that question now, and
 * with it the only read of `Audiobook.state` in the UI.
 *
 * It answers for a non-narratable format too — a re-import can turn a
 * narrated EPUB into something else and the audio is still paid for and
 * still playable — but the reader gates its narration query on
 * `isNarratableFormat`, so that combination never reaches here today and
 * is deliberately not tested. Widen the gate and those cases become
 * reachable, and worth a test again (#213).
 */
export function renditionsFor(
  format: string,
  narration: RunView | null | undefined,
  prefer: Rendition,
): RenditionState {
  const available: Array<Rendition> = []

  // A format with no reader has no primary rendition, but it may still
  // have a narration — the audio was paid for and is still playable.
  if (readerKindForFormat(format) !== null) available.push("primary")
  if (narration?.playable) available.push("narration")

  const selected = available.includes(prefer) ? prefer : (available[0] ?? null)

  return { available, selected, canSwitch: available.length > 1 }
}
