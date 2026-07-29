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

function formatList(names: Array<string>): string {
  const last = names.pop()
  if (last === undefined) return "no"
  if (names.length === 0) return last
  return `${names.join(", ")} and ${last}`
}
