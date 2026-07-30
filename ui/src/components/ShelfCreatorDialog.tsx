import { useEffect, useState } from "react"

import { AccentPicker } from "./AccentPicker"
import { ShelfIconPicker } from "./ShelfIconPicker"
import type { ShelfAccent } from "./AccentPicker"
import { Notice } from "@/components/Notice"
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

export type ShelfCreatorDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  existingNames: Array<string>
  busy: boolean
  error: string | null
  onSubmit: (draft: {
    name: string
    accent: ShelfAccent
    icon: string
  }) => void
}

// ShelfCreatorDialog replaces the old window.prompt-based shelf creation.
// Matches the LibraryCreatorDialog shape (controlled open, reset on close)
// so the two creation flows feel like part of the same family.
export function ShelfCreatorDialog({
  open,
  onOpenChange,
  existingNames,
  busy,
  error,
  onSubmit,
}: ShelfCreatorDialogProps) {
  const [name, setName] = useState("")
  const [accent, setAccent] = useState<ShelfAccent>("accent")
  const [icon, setIcon] = useState<string>("library")

  useEffect(() => {
    if (open) return
    // Reset form on close — legitimate use of setState-in-effect
    // (external "prop" state → local state synchronisation).
    // Deliberate: setState inside an effect, syncing React state from an
    // external source. Was suppressed via react-hooks/set-state-in-effect;
    // Biome has no equivalent rule yet, so there is nothing to suppress.
    setName("")
    setAccent("accent")
    setIcon("library")
  }, [open])

  const trimmed = name.trim()
  const collision = existingNames.some(
    (n) => n.toLowerCase() === trimmed.toLowerCase()
  )
  const valid = trimmed !== "" && !collision

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[420px]">
        <DialogHeader>
          <DialogTitle>New shelf</DialogTitle>
          <DialogDescription>
            A place to group books by hand. Smart shelves live alongside these
            and fill themselves from rules.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="shelf-name">Name</Label>
            <Input
              id="shelf-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. To finish"
              autoFocus
            />
            {trimmed !== "" && collision && (
              // Field validation under the input it is about, not a
              // Notice: no box, and it answers a keystroke rather than
              // reporting a request that failed. The role matches the
              // edit form's field errors, which is the one thing the two
              // did not already agree on.
              <div className="t-small text-(--color-accent-ink)" role="alert">
                A shelf with that name already exists.
              </div>
            )}
          </div>

          <div className="flex flex-col gap-2">
            <Label>Accent</Label>
            <AccentPicker value={accent} onChange={setAccent} />
          </div>

          <div className="flex flex-col gap-2">
            <Label>Icon</Label>
            <ShelfIconPicker value={icon} onChange={setIcon} />
          </div>

          {error && <Notice>{error}</Notice>}
        </div>

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
            onClick={() => onSubmit({ name: trimmed, accent, icon })}
            disabled={!valid || busy}
          >
            {busy ? "Creating…" : "Create shelf"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
