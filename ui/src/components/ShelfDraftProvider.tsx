import { createContext, useContext, useMemo, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { ShelfCreatorDialog } from "./ShelfCreatorDialog"
import type { ReactNode } from "react"
import type { ShelfAccent } from "./AccentPicker"

import type { ApiError } from "@/api/client"
import { createShelf, fetchShelves, shelvesQueryKey } from "@/api/books"

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
 * context. Mirrors UserSettingsDialogProvider so the sidebar header
 * button and the command palette can both trigger it.
 */
export function ShelfDraftProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  const [isOpen, setOpen] = useState(false)

  // Pull the shelf list to power duplicate-name validation in the dialog.
  // Reuses the same shelvesQueryKey the Sidebar already fetches, so this
  // hook subscribes to the cached result without an extra request.
  const shelves = useQuery({
    queryKey: shelvesQueryKey,
    queryFn: fetchShelves,
  })

  const createShelfMut = useMutation({
    mutationFn: (args: { name: string; accent: ShelfAccent }) =>
      createShelf(args.name, args.accent),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: shelvesQueryKey })
      setOpen(false)
    },
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
        existingNames={(shelves.data ?? []).map((s) => s.name)}
        busy={createShelfMut.isPending}
        error={(createShelfMut.error as ApiError | null)?.message ?? null}
        onSubmit={(draft) => createShelfMut.mutate(draft)}
      />
    </ShelfDraftDialogContext.Provider>
  )
}
