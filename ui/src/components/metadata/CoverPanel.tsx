import { useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"

import { FieldLockButton } from "./FieldLockButton"

import type { ApiError } from "@/api/client"
import type { BookDetail } from "@/api/books"
import { bookQueryKey } from "@/api/books"
import { removeCover } from "@/api/enrich"
import { Cover } from "@/components/Cover"
import { Icon } from "@/components/Icon"
import { Button } from "@/components/ui/button"

const COVER_W = 240

export function CoverPanel({ book }: { book: BookDetail }) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  const removeCoverMut = useMutation({
    mutationFn: () => removeCover(book.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: bookQueryKey(book.id) })
      queryClient.invalidateQueries({ queryKey: ["books"] })
      toast.success("Cover removed.")
    },
    onError: (err) =>
      toast.error(
        (err as unknown as ApiError).message || "Cover update failed.",
      ),
  })

  const onPickFile = () => {
    // File-picker upload arrives in a follow-up. The find page is the
    // primary path for cover changes today.
    toast.info(
      "File-picker upload arrives in a follow-up. Use Find covers online for now.",
    )
  }

  return (
    <div className="flex flex-col gap-5" style={{ width: COVER_W }}>
      <div className="t-label flex items-center justify-between">
        <span>Cover artwork</span>
        <FieldLockButton
          bookId={book.id}
          field="cover"
          locked={!!book.locks?.cover}
        />
      </div>

      <div
        className="relative shrink-0"
        style={{ width: COVER_W, height: 360 }}
      >
        <Cover book={book} size="hero" />
      </div>

      <div className="flex flex-col gap-1">
        <Button
          variant="outline"
          size="sm"
          onClick={onPickFile}
          className="w-full justify-start"
        >
          <Icon name="upload" size={13} />
          Replace from file…
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="w-full justify-start"
          onClick={() =>
            void navigate({ to: "/book/$id/find", params: { id: book.id } })
          }
        >
          <Icon name="search" size={13} />
          Find covers online
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="w-full justify-start text-(--color-accent-ink) hover:bg-(--color-accent-soft) hover:text-(--color-accent-ink)"
          disabled={removeCoverMut.isPending || !book.hasCover}
          onClick={() => removeCoverMut.mutate()}
        >
          <Icon name="close" size={13} />
          {removeCoverMut.isPending ? "Removing…" : "Remove cover"}
        </Button>
      </div>
    </div>
  )
}
