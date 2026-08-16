import { useState } from "react"

import { createBookmark } from "@/api/annotations"
import { useApiMutation } from "@/api/mutation"
import type { Locator } from "@/lib/locator"

// useReaderShell owns the chrome state every reader shell shares
// (#351). ADR-0029 split the shells so each renderer's quirks stay its
// own; what the split left behind was copied instead of owned — the
// bookmark mutation byte-identical in all four shells, the
// close-then-toggle panel idiom five times, chromeVisible four ways
// with three documented inconsistencies. This module is the owner:
//
//   - chrome visibility. The shared restore gesture is the floating
//     restore button, in every shell including audio (which previously
//     had no chrome collapse at all). Shells keep renderer-shaped
//     extras on top: text and PDF restore on a click in the reading
//     area (an EPUB iframe swallows it, a PDF canvas does not — each
//     says so in place), and the comic double-click-toggles because a
//     single click there is already a page turn. The extras differ;
//     the guaranteed gesture no longer does.
//   - exclusive panel selection: one panel open at a time, toggling a
//     panel closes its siblings — the invariant the per-shell
//     `closePanels()` + toggle pairs re-implemented.
//   - the bookmark mutation, once. A null locator is a renderer that
//     has not reported a position yet; bookmarking it is a no-op, the
//     same null path every paged shell guarded by hand.
//
// The footer deliberately sits OUTSIDE the collapsible chrome in every
// shell: "hide chrome" hides the header, never the reading position.
// That was the majority behaviour with the rule stated per shell; it is
// the module's rule now.
export type ReaderPanel = "toc" | "notes" | "type" | "chapters"

export type ReaderShell = {
  chromeVisible: boolean
  hideChrome: () => void
  showChrome: () => void
  toggleChrome: () => void
  openPanel: ReaderPanel | null
  /** Opens the panel and closes its siblings; toggles closed if open. */
  togglePanel: (panel: ReaderPanel) => void
  closePanels: () => void
  bookmark: (locator: Locator | null) => void
  bookmarkPending: boolean
}

export function useReaderShell(bookId: string): ReaderShell {
  const [chromeVisible, setChromeVisible] = useState(true)
  const [openPanel, setOpenPanel] = useState<ReaderPanel | null>(null)

  const bookmarkMut = useApiMutation(createBookmark, {
    successToast: "Bookmark saved",
    errorToast: (err) => err.message || "Bookmark failed",
  })

  return {
    chromeVisible,
    hideChrome: () => setChromeVisible(false),
    showChrome: () => setChromeVisible(true),
    toggleChrome: () => setChromeVisible((v) => !v),
    openPanel,
    togglePanel: (panel) =>
      setOpenPanel((current) => (current === panel ? null : panel)),
    closePanels: () => setOpenPanel(null),
    bookmark: (locator) => {
      if (locator) bookmarkMut.mutate({ bookId, locator })
    },
    bookmarkPending: bookmarkMut.isPending,
  }
}
