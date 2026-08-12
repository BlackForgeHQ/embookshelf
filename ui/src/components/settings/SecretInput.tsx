import { useId } from "react"

import type { SecretField } from "@/hooks/useSettingsDraft"
import { Input } from "@/components/ui/input"

// SecretInput is the one control for a write-only secret across the admin
// settings panels: the SMTP password, the reading-guide key, each
// audiobook engine key, each OIDC client secret.
//
// It exists because a stored secret has nothing to render. The server
// never sends one back (ADR-0010), so the field shows either what the
// admin typed this session or nothing at all — and four panels each
// invented their own way of saying "there is one, you just can't see it":
// a pill badge, a parenthetical in the label, a row of dots, an empty
// placeholder. Worse, none of them offered any way to remove the thing
// they were describing, though `useSettingsDraft` has modelled the clear
// case since it was written and the server has honoured it since #218.
//
// Three decisions are worth stating, because they are the ones a future
// panel will be tempted to make differently:
//
//  1. The remove control sits on the *label* row, right-aligned, not
//     beside or below the input. The input is where a replacement is
//     typed; removing concerns the stored value, which is what the label
//     and its badge describe. Keeping it there also means the field's
//     vertical rhythm (label / input / note) never shifts.
//
//  2. There is no confirmation dialog. These are draft forms — the clear
//     lands on save like every other edit — so the pending state below
//     *is* the confirmation, and it is undoable until the admin saves.
//
//  3. The input stays enabled while a clear is pending. Typing a new
//     secret is the other way out, and `set` cancels the clear on its
//     own, so the two escape hatches are "Undo" and "just type one".

export function SecretInput({
  label,
  secret,
  stored,
  noun = "secret",
  placeholder,
}: {
  label: string
  secret: SecretField
  // Whether the server holds a secret right now — the draft's copy of the
  // set-flag, never the typed value. It is what decides whether there is
  // anything to remove.
  stored: boolean
  // What the remove control names, for panels where "secret" is not the
  // word: "password", "key", "ElevenLabs key". Reaches the accessible
  // name; the visible button stays short because the badge next to it
  // already says what is being removed.
  noun?: string
  // What the input suggests when nothing is stored. A stored secret says
  // "leave blank to keep" instead — that sentence is the same on every
  // surface, so no panel words it.
  placeholder?: string
}) {
  const id = useId()
  const cleared = secret.cleared
  // Nothing to remove when nothing is stored, and nothing to remove
  // *from* once the admin has typed a replacement — that save already
  // overwrites the stored secret.
  const canClear = stored && !secret.touched

  return (
    <div className="flex flex-col gap-1.5">
      <span className="t-label flex items-center gap-2">
        <label htmlFor={id}>{label}</label>
        <Badge stored={stored} cleared={cleared} />
        {canClear && (
          <button
            type="button"
            className="ml-auto cursor-pointer text-xs font-medium text-muted-foreground underline underline-offset-2 hover:text-foreground"
            aria-label={`Remove stored ${noun}`}
            onClick={secret.clear}
          >
            Remove
          </button>
        )}
      </span>

      <Input
        id={id}
        type="password"
        autoComplete="new-password"
        value={secret.value}
        onChange={(e) => secret.set(e.target.value)}
        placeholder={
          cleared ? "" : stored ? "leave blank to keep" : (placeholder ?? "")
        }
      />

      {cleared && (
        <p className="t-small flex items-center gap-2 text-xs" role="status">
          <span className="text-(--color-accent-ink)">
            The stored {noun} is removed when you save.
          </span>
          <button
            type="button"
            className="cursor-pointer font-medium underline underline-offset-2"
            // Undo is `set("")`, not a separate move: an empty value the
            // admin *did* touch is exactly "keep what is stored", which is
            // where the field was before the click.
            onClick={() => secret.set("")}
          >
            Undo
          </button>
        </p>
      )}
    </div>
  )
}

function Badge({ stored, cleared }: { stored: boolean; cleared: boolean }) {
  const [text, tone] = cleared
    ? ["Removing on save", "bg-muted text-(--color-accent-ink)"]
    : stored
      ? ["Stored", "bg-(--color-ok-soft) text-(--color-ok-ink)"]
      : ["Not set", "bg-muted text-muted-foreground"]
  return (
    <span
      className={`rounded-full px-1.5 py-0.5 text-[10px] font-medium ${tone}`}
    >
      {text}
    </span>
  )
}
