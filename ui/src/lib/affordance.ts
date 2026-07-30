import type { AuthUser } from "@/api/auth"
import { meQuery } from "@/api/auth"
import type { ApiErrorCode } from "@/api/client"
import { useApiQuery } from "@/api/query"
import type { SettingsSectionKey } from "@/components/settings/sections"
import { kindleEligibleFormatList, narratableFormatList } from "@/lib/formats"

/**
 * Every code the server declares, mirroring handler.AllErrorCodes.
 *
 * Held equal to the Go list by the parity test in internal/handler, the
 * same arrangement the SSE catalog uses. Listed here so a new code
 * cannot be added on one side and silently fall through to a raw
 * message on the other.
 */
export const ALL_ERROR_CODES = [
  "EMAIL_DISABLED",
  "KINDLE_EMAIL_UNSET",
  "FORMAT_NOT_SUPPORTED",
  "EMAIL_RELOAD_FAILED",
  "SMTP_ERROR",
  "GUIDES_DISABLED",
  "AUDIOBOOKS_DISABLED",
  "FORMAT_NOT_NARRATABLE",
] as const satisfies ReadonlyArray<ApiErrorCode>

/** Who is looking, which is what decides what an obstacle looks like. */
export type Viewer = { isAdmin: boolean }

/**
 * Who is looking, from the current user.
 *
 * The one place the role vocabulary is read. Every screen that shows a
 * refusal needs a viewer, so before this each built one by comparing
 * `role === "admin"` itself — ten spellings of the word across seven
 * components, and no way to change what admin means without finding
 * them all. `Viewer` is this module's interface, so deciding who counts
 * as one is this module's job.
 *
 * No current user — signed out, or `/me` still in flight — is not an
 * admin. That is what every call site already did, and it is the right
 * way round: a control that appears a beat late is better than one that
 * flashes into view and then vanishes.
 */
export function viewerOf(user: AuthUser | null | undefined): Viewer {
  return { isAdmin: user?.role === "admin" }
}

/**
 * Who is looking, for a component that has no other use for the user.
 *
 * Most callers want the viewer and nothing else, and this saves them
 * naming the query at all. A component that needs the user's own
 * fields as well — their Kindle address, their display name — reads
 * `meQuery` once and passes the result to `viewerOf`, rather than
 * subscribing to the same query twice.
 */
export function useViewer(): Viewer {
  return viewerOf(useApiQuery(meQuery).data)
}

/** Where the obstacle is cleared. The caller navigates; it holds the router. */
export type Fix =
  | { where: "account" }
  // Settings panels are local state rather than a route param, so a
  // caller can only land on /settings and the sentence has to name the
  // panel. Deep-linking would need a search param on that route.
  | { where: "settings"; panel: SettingsSectionKey }

export type Affordance =
  /** Not this viewer's to act on and not theirs to fix — say nothing. */
  | { kind: "hidden" }
  /** Visible, refused, and explained. */
  | { kind: "explain"; reason: string }
  /** Visible, and it leads to the thing that clears it. */
  | { kind: "navigate"; reason: string; label: string; fix: Fix }
  /** Not a precondition at all: something that already went wrong. */
  | { kind: "report"; reason: string }

/**
 * What the UI should do about a server error code.
 *
 * The rule is who can clear the obstacle, and where:
 *
 * - About *this book* and nobody can change it — a format the feature
 *   does not take — the control stays visible and explains itself. The
 *   feature exists and the user should learn that, along with why it
 *   does not apply here.
 * - Instance-wide and this viewer cannot fix it — email is off and they
 *   are not an admin — the control is hidden. A permanently dead button
 *   teases a feature nobody in this session can enable.
 * - Fixable by whoever is looking — their own missing Kindle address,
 *   or an admin's unconfigured engine — the control stays and leads to
 *   the fix.
 *
 * Before this, one disabled-feature rule was expressed four
 * incompatible ways: email-off hid the Send-to-Kindle button, replaced
 * the account panel's Kindle form with a paragraph, hid the login link,
 * and on /forgot-password did nothing until the request failed (#171).
 *
 * The sentences live here too, so the server's message and the client's
 * explanation cannot drift into saying different things about the same
 * refusal — except where the client cannot know which thing to say, and
 * that is what `cause` is for.
 *
 * `cause` is the server's own message for this refusal, when there is
 * one. A code stands for an obstacle, not for a reason: one code can
 * cover several causes that a client would clear the same way, and where
 * it does, the *what* is the server's to state and only the *where* is
 * this module's. Deciding both is how `AUDIOBOOKS_DISABLED` came to tell
 * an admin who had switched narration off that no engine was configured
 * — a sentence the server could contradict, pointing at the wrong fix
 * (#271). Codes are what a client branches on; messages are for display
 * and free to change (CONTEXT §Error envelope).
 */
export function affordanceFor(
  code: ApiErrorCode,
  viewer: Viewer,
  cause = "",
): Affordance {
  switch (code) {
    case "FORMAT_NOT_SUPPORTED":
      return {
        kind: "explain",
        reason: `Send-to-Kindle accepts ${kindleEligibleFormatList()} only`,
      }

    case "FORMAT_NOT_NARRATABLE":
      return {
        kind: "explain",
        reason: `Only ${narratableFormatList()} books can be narrated`,
      }

    case "KINDLE_EMAIL_UNSET":
      return {
        kind: "navigate",
        reason: "Set your @kindle.com address in account settings first",
        label: "Set Kindle email",
        fix: { where: "account" },
      }

    case "EMAIL_DISABLED":
      return instanceWide(viewer, {
        panel: "email",
        adminReason: "Email delivery is not configured. Set up SMTP to enable it.",
        label: "Configure email",
      })

    case "GUIDES_DISABLED":
      return instanceWide(viewer, {
        panel: "readingGuides",
        adminReason:
          "Reading guides are not configured. Point them at an LLM endpoint to enable them.",
        label: "Configure reading guides",
      })

    // One branch, three causes: the admin has generation switched off,
    // narration is not wired into this deployment at all, and the
    // settings name an engine the catalog does not have. All three are
    // "narration is unavailable here", all three are the admin's to
    // clear, and all three are cleared in the same panel — so they are
    // one code and one affordance, and what differs is only the
    // sentence. The server is the one that knows which, so it supplies
    // it — and for the third it is also the only one that knows *which
    // engine*, which no client-side copy could have said. The fallback
    // holds for a caller that builds this refusal from the code alone,
    // and is true of any of them.
    case "AUDIOBOOKS_DISABLED":
      return instanceWide(viewer, {
        panel: "audiobooks",
        adminReason: `${asSentence(cause) || "Narration is unavailable on this instance."} See narration settings.`,
        label: "Open narration settings",
      })

    // Outcomes of an action the admin just took, not preconditions on a
    // control. There is nothing to gate; the report belongs where the
    // action happened, which is why these are their own kind rather
    // than being forced into the three above.
    case "EMAIL_RELOAD_FAILED":
      return {
        kind: "report",
        reason: "The settings saved, but the mail sender could not be rebuilt from them.",
      }

    case "SMTP_ERROR":
      return { kind: "report", reason: "The mail server refused the test message." }

    default:
      // A code this build has never heard of means the server has moved
      // ahead of the client. Reporting is the only honest answer —
      // guessing would hide or disable a control on the strength of a
      // string nobody has read.
      return { kind: "report", reason: "" }
  }
}

/**
 * The server's message as a sentence a toast can start with.
 *
 * Server messages are Go error strings — lowercase, unpunctuated by
 * convention — and this module's own copy is prose. Where the two are
 * joined the seam should not show. Empty in, empty out, so a caller can
 * test it for "was there anything to say".
 */
function asSentence(message: string): string {
  const text = message.trim()
  if (!text) return ""
  const capitalized = text.charAt(0).toUpperCase() + text.slice(1)
  return /[.!?]$/.test(capitalized) ? capitalized : `${capitalized}.`
}

function instanceWide(
  viewer: Viewer,
  spec: { panel: SettingsSectionKey; adminReason: string; label: string },
): Affordance {
  if (!viewer.isAdmin) return { kind: "hidden" }
  return {
    kind: "navigate",
    reason: spec.adminReason,
    label: spec.label,
    fix: { where: "settings", panel: spec.panel },
  }
}

/**
 * The sentence to show when a request has already failed.
 *
 * `errorToast` is `(err) => string`: it cannot hide a control, navigate
 * or change severity, so after a failure the only thing a code can
 * contribute is what it says. Falls back to the server's own message —
 * for an unknown code that is all there is, and for a known one it is
 * still the more specific text where this module has none.
 *
 * The message also goes *in*, as the cause: a request that failed is the
 * one place the server's account of why is available, and a branch that
 * covers more than one cause needs it to say which.
 */
export function messageForCode(
  code: ApiErrorCode | undefined,
  serverMessage: string,
  viewer: Viewer,
): string {
  if (!code) return serverMessage
  const affordance = affordanceFor(code, viewer, serverMessage)
  if (affordance.kind === "hidden") return serverMessage
  return affordance.reason || serverMessage
}
