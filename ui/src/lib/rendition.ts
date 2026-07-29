import type { Audiobook } from "@/api/audiobooks"
import { readerKindForFormat } from "@/lib/formats"

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
 * request rather than an instruction: a narration that is not ready
 * falls back to the book's own file, which is what a reader who reloads
 * mid-run needs to land on instead of a player with nothing behind it.
 */
export function renditionsFor(
  format: string,
  narration: Audiobook | null | undefined,
  prefer: Rendition,
): RenditionState {
  const available: Array<Rendition> = []

  // A format with no reader has no primary rendition, but it may still
  // have a narration — the audio was paid for and is still playable.
  if (readerKindForFormat(format) !== null) available.push("primary")
  // Ready, not merely present: a run still going has no file behind it.
  // A stale narration stays offered — ADR-0025 §2 surfaces staleness
  // rather than acting on it.
  if (narration?.state === "ready") available.push("narration")

  const selected = available.includes(prefer) ? prefer : (available[0] ?? null)

  return { available, selected, canSwitch: available.length > 1 }
}
