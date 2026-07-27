import type { ReactNode } from "react"
import { useEffect, useId, useRef, useState } from "react"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

// The type-to-confirm module.
//
// Destructive actions in embookshelf are gated by making the operator
// type something back — the library's name, the book's title, the word
// `bookdrop` — before the confirm button arms. ADR-0014 mandates the gate
// for the BookDrop wipe and ADR-0025 §4 mandates it again for narration
// spend, independently, which is how a project ends up with four copies
// of the same dialog.
//
// It had three. Each owned its own input state, its own reset-on-close
// effect (carrying the *same* pasted four-line comment), its own match
// check and its own disabled rule — and no two match checks agreed. The
// library dialog compared a trimmed input against an untrimmed name, the
// book dialog trimmed both sides, the BookDrop dialog trimmed against a
// literal. A rule written three times is a rule nobody owns.
//
// A caller supplies the phrase to type, the consequence copy, and what to
// do on confirm. Everything between — the box, the gate, the reset, the
// busy state — is here, so the fourth gate is a call, not a copy.

/**
 * The match rule, and the only one.
 *
 * Both sides are trimmed. Trimming the input is politeness: a phrase
 * copied out of the UI often arrives with a space attached, and refusing
 * it teaches nothing. Trimming the *phrase* is the fix for a real
 * defect — a library named `"Sci-Fi "` was undeletable, because the
 * dialog compared a trimmed input against the raw name and no keystroke
 * could bridge the gap.
 *
 * Everything else stays strict. Case matters, interior whitespace
 * matters, and an empty phrase matches nothing at all: a gate an empty
 * box satisfies would arm itself the moment the dialog opened, which is
 * precisely the click this whole mechanism exists to prevent.
 */
export function matchesConfirmPhrase(input: string, phrase: string): boolean {
  const wanted = phrase.trim()
  if (wanted === "") return false
  return input.trim() === wanted
}

/**
 * An optional slot below the input, for switches that ride along with the
 * confirmation — the S3 purge toggle on library deletion is the existing
 * case. The dialog owns the value so it resets with everything else, and
 * hands it to `onConfirm`; a caller that keeps such a toggle in its own
 * state has re-imported the reset bug this module deletes.
 */
export type ConfirmExtras<E> = {
  initial: E
  render: (value: E, set: (next: E) => void) => ReactNode
}

export type ConfirmPhraseDialogProps<E> = {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Dialog heading. */
  title: ReactNode
  /** What this action destroys, stated plainly. */
  description: ReactNode
  /** The text the operator must reproduce to arm the confirm button. */
  phrase: string
  /**
   * The instruction above the input. Defaults to naming the phrase, which
   * is right when the phrase is short enough to read back; a caller with
   * a long phrase (a book title) can point at it instead.
   */
  prompt?: ReactNode
  /** Placeholder for the input, when showing the phrase there helps. */
  placeholder?: string
  confirmLabel: string
  /** Label while the action is in flight — "Deleting…", "Wiping…". */
  busyLabel: string
  busy: boolean
  extras?: ConfirmExtras<E>
  onConfirm: (extras: E) => void
}

export function ConfirmPhraseDialog<E = void>({
  open,
  onOpenChange,
  title,
  description,
  phrase,
  prompt,
  placeholder,
  confirmLabel,
  busyLabel,
  busy,
  extras,
  onConfirm,
}: ConfirmPhraseDialogProps<E>) {
  const inputId = useId()
  const [typed, setTyped] = useState("")
  const [extrasValue, setExtrasValue] = useState<E>(extras?.initial as E)

  // `extras` is built inline at every call site, so it is a new object on
  // every render and cannot go in a dependency array. The reset only ever
  // needs the value it was declared with.
  const initialExtras = useRef(extras?.initial as E)
  initialExtras.current = extras?.initial as E

  useEffect(() => {
    if (open) return
    // Deliberate: setState inside an effect, syncing React state from an
    // external source. Was suppressed via react-hooks/set-state-in-effect;
    // Biome has no equivalent rule yet, so there is nothing to suppress.
    //
    // This is the copy that used to live in three files. A dialog closed
    // half-typed must not reopen with a primed button.
    setTyped("")
    setExtrasValue(initialExtras.current)
  }, [open])

  const armed = matchesConfirmPhrase(typed, phrase) && !busy

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[460px]">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-2">
          <Label htmlFor={inputId}>
            {prompt ?? (
              <>
                Type <span className="mono">{phrase}</span> to confirm.
              </>
            )}
          </Label>
          <Input
            id={inputId}
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            placeholder={placeholder}
            autoFocus
          />
        </div>

        {extras?.render(extrasValue, setExtrasValue)}

        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            onClick={() => onOpenChange(false)}
            disabled={busy}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            onClick={() => onConfirm(extrasValue)}
            disabled={!armed}
            className="active:translate-y-[1px]"
          >
            {busy ? busyLabel : confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
