import type { Shelf } from "@/api/books"

// shelfGroups is the one spelling of how a viewer's shelves split
// (#352). The rule was written twice — the book page's picker and the
// Sidebar — with different splits, and the two had already drifted on
// admin-owned Shared shelves. The facts it encodes are CONTEXT.md's:
// mutations are owner-only (ADR-0017), a shared shelf appears in the
// sidebar's SHARED group for everyone but is curatable only by its
// owner, and smart shelves are query-time — never curatable anywhere.
//
// `ownerName` is empty exactly when the viewer owns the row: the wire
// shape only names an owner on somebody else's shared shelf.
export type ShelfGroups = {
  /** Regular shelves the viewer owns and has not shared. */
  ownPrivate: Shelf[]
  /** Every shared shelf — the viewer's own first (their edits propagate). */
  shared: Shelf[]
  /** Rule-driven; membership is computed, never assigned. */
  smart: Shelf[]
  /** What an "Add to shelf" picker may offer: regular shelves the viewer owns, published or not. */
  curatable: Shelf[]
}

export function shelfGroups(shelves: Shelf[]): ShelfGroups {
  const owned = (s: Shelf) => (s.ownerName ?? "") === ""
  const regular = shelves.filter((s) => !s.isSmart)
  const ownShared = regular.filter((s) => s.isPublic && owned(s))
  const otherShared = regular.filter((s) => s.isPublic && !owned(s))
  return {
    ownPrivate: regular.filter((s) => !s.isPublic),
    shared: [...ownShared, ...otherShared],
    smart: shelves.filter((s) => s.isSmart),
    curatable: regular.filter((s) => !s.isPublic || owned(s)),
  }
}
