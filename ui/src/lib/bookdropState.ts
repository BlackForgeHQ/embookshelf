import type { BookDropState } from "@/api/bookdrop"

/**
 * What a queue row's state is called in the interface.
 *
 * The wire words and the shown words differ deliberately — "discovered"
 * is what the scanner did, "queued" is what the user sees waiting — so
 * this map is the only place either vocabulary meets the other. It lived
 * inside the queue route, which is why the settings panel could not
 * reach it and recomputed what it needed instead (#198).
 */
export const BOOKDROP_STATE_LABEL: Record<BookDropState, string> = {
  discovered: "queued",
  processing: "processing",
  ready: "ready",
  failed: "failed",
  imported: "imported",
  rejected: "discarded",
}

/**
 * Terminal rows are done with: imported into the library, or discarded.
 * They are what the settings panel's cleanup sweeps and what the queue
 * hides from its active list.
 */
export function isTerminalState(state: BookDropState): boolean {
  return state === "imported" || state === "rejected"
}

/** Still in the queue: anything not yet finished with. */
export function isActiveState(state: BookDropState): boolean {
  return !isTerminalState(state)
}

/**
 * Whether a row can be approved into the library.
 *
 * Failed rows are approvable on purpose: extraction failing means the
 * metadata is poor, not that the file is unusable, and the user can fill
 * the gaps in by hand rather than losing the book.
 */
export function isApprovableState(state: BookDropState): boolean {
  return state === "ready" || state === "failed"
}

/**
 * Whether a pre-approval cover can still be uploaded for this row.
 *
 * The server enforces the same three states in BookDropPutCover
 * (internal/handler/bookdrop.go) and refuses the rest with a 409 — this
 * is the client half of that pair, declared once so the two can be read
 * against each other. Failed rows are excluded here even though they are
 * approvable: the upload exists to supply a cover the extractor could
 * not find, and a failed extraction has no page to render one from.
 */
export function acceptsCoverUpload(state: BookDropState): boolean {
  return state === "discovered" || state === "processing" || state === "ready"
}
