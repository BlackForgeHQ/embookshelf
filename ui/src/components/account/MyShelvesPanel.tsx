import { useState } from "react"

import type { Shelf } from "@/api/books"
import { accentColor } from "@/components/AccentPicker"
import { Card } from "@/components/SettingsShared"
import { ShelfIcon } from "@/components/ShelfIcon"
import { ShelfIconPicker } from "@/components/ShelfIconPicker"
import { Button } from "@/components/ui/button"
import {
  deleteShelf,
  publishShelf,
  shelvesQuery,
  updateShelf,
} from "@/api/books"
import { useApiMutation } from "@/api/mutation"
import { useApiQuery } from "@/api/query"
import { useViewer } from "@/lib/affordance"

// MyShelvesPanel — owned shelves overview. Heavy table view per ADR-0019:
// icon · name · accent · kind · visibility · book count · created. Inline
// icon picker on icon click; per-row publish/unpublish for admins, delete
// for everyone. Re-uses the existing mutations so realtime SSE flows
// cover this surface without extra wiring.
export function MyShelvesPanel() {
  const { isAdmin } = useViewer()
  const shelves = useApiQuery(shelvesQuery)

  const updateMut = useApiMutation(updateShelf)
  const deleteMut = useApiMutation(deleteShelf, {
    successToast: "Shelf deleted.",
  })
  const publishMut = useApiMutation(publishShelf)

  const all = shelves.data?.shelves ?? []
  // Owner-only — non-owned public shelves never appear here.
  const owned = all.filter((s) => (s.ownerName ?? "") === "")

  if (shelves.isLoading) {
    return (
      <Card>
        <h2 className="t-h2">My shelves</h2>
        <div className="t-small text-(--color-ink-3)">Loading shelves…</div>
      </Card>
    )
  }

  return (
    <Card>
      <div className="flex flex-col gap-1">
        <h2 className="t-h2">My shelves</h2>
        <p className="t-small text-(--color-ink-3)">
          Pick an icon for each shelf you own. Public shelves use the same
          icon for every viewer.
        </p>
      </div>
      {owned.length === 0 ? (
        <div className="t-small text-(--color-ink-3) italic">
          You don't own any shelves yet. Create one from the sidebar.
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="border-b border-(--color-paper-3) text-left">
                <th className="px-2 py-2 font-medium text-(--color-ink-3)">
                  Icon
                </th>
                <th className="px-2 py-2 font-medium text-(--color-ink-3)">
                  Name
                </th>
                <th className="px-2 py-2 font-medium text-(--color-ink-3)">
                  Accent
                </th>
                <th className="px-2 py-2 font-medium text-(--color-ink-3)">
                  Kind
                </th>
                <th className="px-2 py-2 font-medium text-(--color-ink-3)">
                  Visibility
                </th>
                <th className="px-2 py-2 text-right font-medium text-(--color-ink-3)">
                  Books
                </th>
                <th className="px-2 py-2 text-right font-medium text-(--color-ink-3)">
                  Actions
                </th>
              </tr>
            </thead>
            <tbody>
              {owned.map((s) => (
                <ShelfRow
                  key={s.id}
                  shelf={s}
                  isAdmin={isAdmin}
                  busy={
                    updateMut.isPending ||
                    publishMut.isPending ||
                    deleteMut.isPending
                  }
                  onIconChange={(icon) =>
                    updateMut.mutate({ slug: s.slug, body: { icon } })
                  }
                  onPublishToggle={() =>
                    publishMut.mutate({
                      slug: s.slug,
                      isPublic: !s.isPublic,
                    })
                  }
                  onDelete={() => {
                    if (window.confirm(`Delete shelf "${s.name}"?`)) {
                      deleteMut.mutate(s.slug)
                    }
                  }}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  )
}

function ShelfRow({
  shelf,
  isAdmin,
  busy,
  onIconChange,
  onPublishToggle,
  onDelete,
}: {
  shelf: Shelf
  isAdmin: boolean
  busy: boolean
  onIconChange: (icon: string) => void
  onPublishToggle: () => void
  onDelete: () => void
}) {
  const [pickerIcon, setPickerIcon] = useState(shelf.icon)
  const handlePick = (next: string) => {
    setPickerIcon(next)
    onIconChange(next)
  }

  return (
    <tr className="border-b border-(--color-paper-3) align-middle">
      <td className="px-2 py-2">
        <ShelfIconPicker value={pickerIcon} onChange={handlePick} />
      </td>
      <td className="px-2 py-2 font-medium">{shelf.name}</td>
      <td className="px-2 py-2">
        <span
          aria-label={shelf.accent}
          title={shelf.accent}
          className="inline-block size-4 rounded-full"
          style={{ background: accentColor(shelf.accent) }}
        />
      </td>
      <td className="px-2 py-2 text-(--color-ink-2)">
        {shelf.isSmart ? (
          <span className="inline-flex items-center gap-1">
            <ShelfIcon name="sparkles" size={12} />
            Smart
          </span>
        ) : (
          "Regular"
        )}
      </td>
      <td className="px-2 py-2 text-(--color-ink-2)">
        {shelf.isPublic ? "Shared" : "Private"}
      </td>
      <td className="px-2 py-2 text-right tabular-nums text-(--color-ink-2)">
        {shelf.bookCount}
      </td>
      <td className="px-2 py-2 text-right">
        <div className="flex justify-end gap-1.5">
          {isAdmin && !shelf.isSmart && (
            <Button
              size="sm"
              variant="outline"
              onClick={onPublishToggle}
              disabled={busy}
            >
              {shelf.isPublic ? "Unshare" : "Share"}
            </Button>
          )}
          <Button
            size="sm"
            variant="ghost"
            onClick={onDelete}
            disabled={busy}
          >
            Delete
          </Button>
        </div>
      </td>
    </tr>
  )
}
