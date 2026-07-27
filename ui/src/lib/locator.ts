/**
 * The reading-position token vocabulary: encode, decode, label.
 *
 * One column (`user_book_progress.resume_cfi`) and one column
 * (`annotations.locator`) carry a position for every format, so the token
 * is prefix-discriminated and every reader shell, notebook row and book
 * page has to agree about what a given string means. That agreement used
 * to be spread over seven inline encoders and three copies of the
 * decoder, none of which knew about all the kinds the others wrote — an
 * audiobook bookmark reached the notebook as the literal `time:3661.00`
 * because both display copies handled page and CFI and fell through to
 * the raw string.
 *
 * This module is the only place a prefix is spelled. Adding a kind is one
 * file, and every consumer gets its label for free. See
 * `docs/ARCHITECTURE.md` §5.6.1 for the wire format.
 */

const PAGE_PREFIX = "page:"
const TIME_PREFIX = "time:"
const CFI_PREFIX = "epubcfi"

/**
 * A decoded reading position.
 *
 * `page` is the **human** page number — the one a label may print
 * unchanged. Readers with a 0-indexed internal page model (the comic
 * reader) convert at their own boundary rather than storing their
 * indexing in the token, because the token is read by consumers that
 * have no idea which reader wrote it.
 *
 * `unknown` is a real member, not an error case: a token written by a
 * future version, or a corrupted row, must survive a round trip and
 * render as something rather than crash a notebook page.
 */
export type Locator =
  | { kind: "cfi"; cfi: string }
  | { kind: "page"; page: number }
  | { kind: "time"; seconds: number }
  | { kind: "unknown"; raw: string }

/** Renders a locator as the token stored in the database. */
export function encodeLocator(locator: Locator): string {
  switch (locator.kind) {
    case "cfi":
      return locator.cfi
    case "page":
      return `${PAGE_PREFIX}${locator.page}`
    // Two decimal places: audio positions are fractional, and a fixed
    // width keeps the stored tokens comparable by eye.
    case "time":
      return `${TIME_PREFIX}${locator.seconds.toFixed(2)}`
    case "unknown":
      return locator.raw
  }
}

/**
 * Reads a stored token. Returns null for an absent or empty token —
 * "no position recorded" is distinct from "a position we can't read",
 * which decodes to `unknown` and is preserved verbatim.
 *
 * A prefix present but its payload unparseable is `unknown` rather than
 * page 0 / second 0: silently sending a reader to the top of the book is
 * worse than showing the token.
 */
export function decodeLocator(raw: string | null | undefined): Locator | null {
  if (!raw) return null

  if (raw.startsWith(PAGE_PREFIX)) {
    const page = Number.parseInt(raw.slice(PAGE_PREFIX.length), 10)
    return Number.isFinite(page)
      ? { kind: "page", page }
      : { kind: "unknown", raw }
  }
  if (raw.startsWith(TIME_PREFIX)) {
    const seconds = Number.parseFloat(raw.slice(TIME_PREFIX.length))
    return Number.isFinite(seconds)
      ? { kind: "time", seconds }
      : { kind: "unknown", raw }
  }
  // CFIs are required to carry their own prefix — epub.js always emits
  // `epubcfi(...)`. The old decoder treated *any* unrecognised string as
  // a CFI, which left it with no way to say "I don't know what this is".
  if (raw.startsWith(CFI_PREFIX)) return { kind: "cfi", cfi: raw }

  return { kind: "unknown", raw }
}

/**
 * The reader-facing rendering of a stored token, for the notebook row,
 * the book page annotation list and the reader's own notes panel.
 *
 * A CFI is opaque to a human, so it reduces to the format name; there is
 * nothing better to say without resolving it against the book.
 */
export function locatorLabel(raw: string | null | undefined): string {
  const locator = decodeLocator(raw)
  if (!locator) return ""

  switch (locator.kind) {
    case "cfi":
      return "EPUB"
    case "page":
      return `p.${locator.page}`
    case "time":
      return formatHMS(locator.seconds)
    case "unknown":
      return locator.raw
  }
}

/**
 * Renders a seconds count as H:MM:SS (or M:SS for short clips). NaN /
 * negative values render as "—:—".
 *
 * Lives here because it is how a time locator becomes readable, and the
 * audio shell's scrubber and chapter list want the same rendering the
 * label uses — a bookmark at 1:01:01 should read 1:01:01 in both places.
 */
export function formatHMS(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "—:—"
  const total = Math.floor(seconds)
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const pad = (n: number) => n.toString().padStart(2, "0")
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`
}
