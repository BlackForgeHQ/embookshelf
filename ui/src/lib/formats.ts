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
  const names: Array<string> = [...NARRATABLE_FORMATS]
  const last = names.pop()
  if (last === undefined) return "no"
  if (names.length === 0) return last
  return `${names.join(", ")} and ${last}`
}
