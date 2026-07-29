import type { ApiErrorCode } from "@/api/client"
import type { Affordance, Viewer } from "@/lib/affordance"
import { affordanceFor } from "@/lib/affordance"
import { isKindleEligibleFormat } from "@/lib/formats"

/**
 * What the book page should offer for Send-to-Kindle.
 *
 * Everything except `send` is an {@link Affordance}: this module decides
 * *which refusal* the server would return, and `lib/affordance.ts`
 * decides what the UI does about it. Splitting it that way is the point
 * of #171 — the sentence, and the hide-versus-explain-versus-navigate
 * call, are one rule for every feature rather than one per control.
 */
export type KindleAction = { kind: "send" } | Affordance

export type KindleState = {
  /** Whether the instance has an email transport configured at all. */
  emailEnabled: boolean
  /**
   * The book's **primary** format, and deliberately that one.
   *
   * Send-to-Kindle ships the file the library ingested, so the question
   * here is "what is this book's own file", not "what is the reader
   * currently showing". Those diverge for a narrated book: the page
   * above this control can be playing a narration while the file that
   * would be sent is still the EPUB (ADR-0025 §3, and the drift it
   * predicted — several call sites branch on the primary format and are
   * answering a subtly different question than they appear to).
   *
   * If Send-to-Kindle ever offers the narration, this becomes a
   * Rendition question and moves to `lib/rendition.ts`.
   */
  format: string
  /** The signed-in user's Kindle address, possibly blank. */
  kindleEmail: string
  /** Who is looking. An admin can clear an instance-wide obstacle. */
  viewer: Viewer
}

/**
 * Decides the Send-to-Kindle affordance from the same three
 * preconditions the handler checks, in the same order.
 *
 * The order is the point. The server answers a request with one of three
 * outcomes and the UI predicts which, so what the button says matches
 * what clicking it would have done. That mirroring used to be a chain of
 * early returns inside the book-detail route, restating the handler's
 * branch order and its rejection sentence word for word, where no test
 * could reach it (#193).
 *
 * What it no longer does is decide what to *do* about the outcome. It
 * names the code and hands it to `affordanceFor`, so the button and
 * every other refusal in the app obey one rule and quote one sentence.
 */
export function kindleAction(state: KindleState): KindleAction {
  const refusal = refusalCode(state)
  if (refusal === null) return { kind: "send" }
  return affordanceFor(refusal, state.viewer)
}

/**
 * The code `POST /books/:id/kindle` would answer with, or null if it
 * would accept.
 *
 * The branch order is Handler.SendToKindle's, and deliberately so: the
 * handler checks the transport, then the caller's address, then the
 * book's format (internal/handler/kindle.go). Reading them in any other
 * order predicts a refusal the server would not have sent — which is
 * what this did before #171, telling a user with no Kindle address about
 * a format rule the server would never have reached.
 */
function refusalCode(state: KindleState): ApiErrorCode | null {
  if (!state.emailEnabled) return "EMAIL_DISABLED"
  if (state.kindleEmail.trim() === "") return "KINDLE_EMAIL_UNSET"
  if (!isKindleEligibleFormat(state.format)) return "FORMAT_NOT_SUPPORTED"
  return null
}
