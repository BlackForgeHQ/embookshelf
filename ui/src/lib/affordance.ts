import type { ApiErrorCode } from "@/api/client"
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
 * refusal.
 */
export function affordanceFor(code: ApiErrorCode, viewer: Viewer): Affordance {
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

    case "AUDIOBOOKS_DISABLED":
      return instanceWide(viewer, {
        panel: "audiobooks",
        adminReason:
          "No text-to-speech engine is configured. Set one up to enable narration.",
        label: "Configure narration",
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
 */
export function messageForCode(
  code: ApiErrorCode | undefined,
  serverMessage: string,
  viewer: Viewer,
): string {
  if (!code) return serverMessage
  const affordance = affordanceFor(code, viewer)
  if (affordance.kind === "hidden") return serverMessage
  return affordance.reason || serverMessage
}
