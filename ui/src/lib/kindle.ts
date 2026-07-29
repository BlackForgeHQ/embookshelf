import { isKindleEligibleFormat, kindleEligibleFormatList } from "@/lib/formats"

/** What the book page should offer for Send-to-Kindle. */
export type KindleAction =
  | { kind: "send" }
  | { kind: "hidden" }
  | { kind: "ineligible"; reason: string }
  | { kind: "needs-address" }

export type KindleState = {
  /** Whether the instance has an email transport configured at all. */
  emailEnabled: boolean
  /** The book's primary format, as the API reports it. */
  format: string
  /** The signed-in user's Kindle address, possibly blank. */
  kindleEmail: string
}

/**
 * Decides the Send-to-Kindle affordance from the same three
 * preconditions the handler checks, in the same order.
 *
 * The order is the point. The server answers a request with one of three
 * outcomes — no transport, 415 for the format, 412 for a missing Kindle
 * address — and the UI mirrors them so what the button says matches what
 * clicking it would have done. That mirroring used to be a chain of
 * early returns inside the book-detail route, restating the handler's
 * branch order and its rejection sentence word for word, where no test
 * could reach it (#193).
 */
export function kindleAction(state: KindleState): KindleAction {
  // Hidden rather than disabled: an instance with no SMTP cannot enable
  // this in the current session, and a permanently greyed button is a
  // tease.
  if (!state.emailEnabled) return { kind: "hidden" }

  if (!isKindleEligibleFormat(state.format)) {
    return {
      kind: "ineligible",
      reason: `Send-to-Kindle accepts ${kindleEligibleFormatList()} only`,
    }
  }

  if (state.kindleEmail.trim() === "") return { kind: "needs-address" }

  return { kind: "send" }
}
