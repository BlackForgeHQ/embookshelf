import type { ApiErrorCode } from "@/api/client"
import { isKindleEligibleFormat } from "@/lib/formats"

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
 *
 * The format is the book's **primary** one, deliberately: Send-to-Kindle
 * ships the file the library ingested, not whichever Rendition the
 * reader last opened. Those diverge for a narrated book (ADR-0025 §3).
 * If it ever offers the narration, this becomes a Rendition question and
 * moves to `renditionsFor`.
 */
export function kindleRefusal(
  emailEnabled: boolean,
  kindleEmail: string,
  format: string
): ApiErrorCode | null {
  if (!emailEnabled) return "EMAIL_DISABLED"
  if (kindleEmail.trim() === "") return "KINDLE_EMAIL_UNSET"
  if (!isKindleEligibleFormat(format)) return "FORMAT_NOT_SUPPORTED"
  return null
}
