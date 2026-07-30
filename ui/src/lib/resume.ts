/**
 * Where a reader left off, per Rendition.
 *
 * A book carries two stored positions — one for the text Rendition, one
 * for the audio Rendition — because a CFI and a timestamp are different
 * currencies until the alignment map bridges them, and one column meant
 * each shell overwrote the other on every Read → Listen → Read round
 * trip (#200). This module is the only client code that names that pair.
 *
 * It owns two halves of one rule:
 *
 *   - **the routing rule** (`renditionForLocator`), the client's mirror
 *     of `internal/repo/progress.go`: a written locator lands in the
 *     field its own kind implies. The client never sends a flag saying
 *     which position it means, so the kind *is* the address.
 *   - **the reading rule** (`resumeLocator`), which is that rule run
 *     backwards. A stored token is offered to a Rendition only if it
 *     would have routed there, so a `time:` token stranded in the text
 *     field by a pre-#200 install is no text position, and a garbled
 *     audio token is no audio position rather than second zero.
 *
 * Before this, four sites in `read.$id.tsx` decoded a field and narrowed
 * it by hand, and the test for the pair had to declare its own stored
 * shape and its own copy of the server's rule because there was nothing
 * to import (#245).
 *
 * ## The off-by-one
 *
 * The comic shell resumes to `page - 1` and no other site converts. That
 * is not a bug and it deliberately does not live here: a page token
 * carries the **human** page number (see `lib/locator`), `PdfReader`
 * takes a 1-based `initialPage` and `ComicReader` a 0-based one, so the
 * subtraction is one renderer's indexing and belongs at that renderer's
 * boundary. Absorbing it here would mean this module returning a
 * different page number depending on who asked, which is exactly the
 * confusion that made `page:7` mean p.8 in the reader chrome and p.7 in
 * the notebook. `resumePage` returns the human page; the comic shell
 * converts in one line, next to the reader that needs it.
 *
 * ## Room for the bridge
 *
 * ADR-0029 §4 adds a fifth locator kind — a character offset — as the
 * one currency both Renditions convert to. It attaches here rather than
 * in `lib/locator`: the kind itself is just another token, but "a char
 * offset answers *both* Renditions" is a statement about routing, and
 * routing is this module. `renditionForLocator` gains a case and
 * `resumeLocator` gains a conversion; no shell learns the other exists.
 */

import type { Rendition } from "@/lib/formats"
import type { Locator } from "@/lib/locator"
import { decodeLocator } from "@/lib/locator"

/**
 * The two stored positions, as they reach the client on the book DTO.
 *
 * Structural rather than `BookDetail`, so the module is testable with a
 * two-field literal and usable from anything that has the pair.
 *
 * `resumeCfi` is a misnomer inherited from the column that predates
 * page and time locators; it holds whichever token the *text* Rendition
 * last wrote, CFI or page.
 */
export type ResumePositions = {
  resumeCfi?: string
  resumeAudio?: string
}

/**
 * Which stored field a Rendition's position lives in.
 *
 * The one place the two field names are spelled against the two
 * Renditions, which is what stops the mapping being restated at every
 * read.
 */
export function resumeFieldFor(rendition: Rendition): keyof ResumePositions {
  return rendition === "narration" ? "resumeAudio" : "resumeCfi"
}

/**
 * Which Rendition's position a locator becomes when it is written.
 *
 * Mirrors `ProgressRepo.Set`: a timestamp is a position inside the
 * narration, and everything else — CFI, page, and anything unreadable —
 * is a text position. Unreadable falls to text on purpose; the server's
 * branch has the same shape, and it means a token from a future version
 * is stored somewhere rather than dropped.
 */
export function renditionForLocator(locator: Locator): Rendition {
  return locator.kind === "time" ? "narration" : "primary"
}

/**
 * The position to resume this Rendition from, or null when there is
 * none to resume from.
 *
 * Null covers both "nothing stored" and "what is stored belongs to the
 * other Rendition" — neither is a position this shell can open, and a
 * caller that had to tell them apart would be re-deciding the routing.
 */
export function resumeLocator(
  positions: ResumePositions,
  rendition: Rendition
): Locator | null {
  const locator = decodeLocator(positions[resumeFieldFor(rendition)])
  if (!locator) return null
  return renditionForLocator(locator) === rendition ? locator : null
}

/**
 * The CFI the EPUB shell resumes from, if the text position is one.
 *
 * The three readings below hand back a primitive rather than a
 * `Locator`, and deliberately: each feeds a renderer prop that sits in
 * an effect's dependency list, and a decoded object would be a fresh
 * identity every render — re-booting epub.js on each one. A resume
 * token of another kind reads as "start from the beginning" rather than
 * a coerced position, so each returns undefined and lets the shell name
 * its renderer's own default.
 */
export function resumeCfi(positions: ResumePositions): string | undefined {
  const locator = resumeLocator(positions, "primary")
  return locator?.kind === "cfi" ? locator.cfi : undefined
}

/**
 * The human page number the paged shells resume from, if the text
 * position is one. Human, not any reader's index — see the module
 * header on the off-by-one.
 */
export function resumePage(positions: ResumePositions): number | undefined {
  const locator = resumeLocator(positions, "primary")
  return locator?.kind === "page" ? locator.page : undefined
}

/**
 * The offset the audio shell resumes from, in seconds.
 *
 * Clamped at zero: a negative timestamp decodes fine and seeks nowhere.
 */
export function resumeSeconds(positions: ResumePositions): number | undefined {
  const locator = resumeLocator(positions, "narration")
  return locator?.kind === "time" ? Math.max(0, locator.seconds) : undefined
}
