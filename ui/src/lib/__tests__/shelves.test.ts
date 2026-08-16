import { describe, expect, it } from "vitest"

import type { Shelf } from "@/api/books"
import { shelfGroups } from "@/lib/shelves"

const shelf = (over: Partial<Shelf>): Shelf =>
  ({
    id: over.slug ?? "id",
    name: over.slug ?? "Shelf",
    slug: "s",
    accent: "",
    icon: "library",
    bookCount: 0,
    isSmart: false,
    isPublic: false,
    createdAt: "2026-01-01T00:00:00Z",
    ...over,
  }) as Shelf

describe("shelfGroups — the one curation rule (#352)", () => {
  const mine = shelf({ slug: "mine" })
  const mineShared = shelf({ slug: "mine-shared", isPublic: true })
  const theirs = shelf({ slug: "top-picks", isPublic: true, ownerName: "admin" })
  const smart = shelf({ slug: "unread", isSmart: true })
  const groups = shelfGroups([mine, mineShared, theirs, smart])

  it("splits private / shared / smart the way the sidebar renders them", () => {
    expect(groups.ownPrivate.map((s) => s.slug)).toEqual(["mine"])
    expect(groups.shared.map((s) => s.slug)).toEqual(["mine-shared", "top-picks"])
    expect(groups.smart.map((s) => s.slug)).toEqual(["unread"])
  })

  it("the picker offers only what the viewer curates: owned regular shelves, shared included", () => {
    // A shared shelf owned by someone else is read-only (ADR-0017), and
    // a smart shelf is never assigned — neither may appear in a picker.
    expect(groups.curatable.map((s) => s.slug)).toEqual(["mine", "mine-shared"])
  })

  it("the viewer's own shared shelves lead the shared group — their edits propagate", () => {
    expect(groups.shared[0]?.slug).toBe("mine-shared")
  })
})
