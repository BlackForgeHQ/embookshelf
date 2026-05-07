import { createContext, useContext, useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { ShelfCreatorDialog } from "./ShelfCreatorDialog"
import type { ReactNode } from "react"

import { createShelf, fetchShelves, shelvesQueryKey } from "@/api/books"
import { useApiMutation } from "@/api/mutation"

type ShelfDraftDialogContextValue = {
  open: () => void
}

const ShelfDraftDialogContext =
  createContext<ShelfDraftDialogContextValue | null>(null)

export function useShelfDraftDialog(): ShelfDraftDialogContextValue {
  const ctx = useContext(ShelfDraftDialogContext)
  if (!ctx) {
    throw new Error(
      "useShelfDraftDialog must be used inside <ShelfDraftProvider>"
    )
  }
  return ctx
}

/**
 * Hosts the "create a regular shelf" dialog and exposes `open()` via
 * context so the sidebar header button and the command palette can both
 * trigger it without threading props through the tree.
 */
export function ShelfDraftProvider({ children }: { children: ReactNode }) {
  const [isOpen, setOpen] = useState(false)

  // Pull the shelf list to power duplicate-name validation in the dialog.
  // Reuses the same shelvesQueryKey the Sidebar already fetches, so this
  // hook subscribes to the cached result without an extra request.
  const shelves = useQuery({
    queryKey: shelvesQueryKey,
    queryFn: fetchShelves,
  })

  const createShelfMut = useApiMutation(createShelf, {
    onSuccess: () => setOpen(false),
  })

  const value = useMemo<ShelfDraftDialogContextValue>(
    () => ({ open: () => setOpen(true) }),
    []
  )

  return (
    <ShelfDraftDialogContext.Provider value={value}>
      {children}
      <ShelfCreatorDialog
        open={isOpen}
        onOpenChange={(open) => {
          if (!open) createShelfMut.reset()
          setOpen(open)
        }}
        existingNames={(shelves.data?.shelves ?? []).map((s) => s.name)}
        busy={createShelfMut.isPending}
        error={createShelfMut.error?.message ?? null}
        onSubmit={(draft) => createShelfMut.mutate(draft)}
      />
    </ShelfDraftDialogContext.Provider>
  )
}
