import { useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"

import { FieldLockButton } from "./FieldLockButton"

import type { ApiError } from "@/api/client"
import type { BookDetail } from "@/api/books"
import { bookQueryKey } from "@/api/books"
import { applyCoverFromUrl } from "@/api/enrich"
import { Cover } from "@/components/Cover"
import { Icon } from "@/components/Icon"
import { Button } from "@/components/ui/button"

export function CoverPanel({ book }: { book: BookDetail }) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  const removeCoverMut = useMutation({
    // Empty URL clears the cover server-side.
    mutationFn: () => applyCoverFromUrl(book.id, ""),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: bookQueryKey(book.id) })
      queryClient.invalidateQueries({ queryKey: ["books"] })
      toast.success("Cover removed.")
    },
    onError: (err) =>
      toast.error(
        (err as unknown as ApiError).message || "Cover update failed."
      ),
  })

  const onPickFile = () => {
    // File-picker upload arrives in a follow-up. The find page is the
    // primary path for cover changes today.
    toast.info(
      "File-picker upload arrives in a follow-up. Use Find covers online for now."
    )
  }

  return (
    <section className="flex gap-6 border-b border-(--color-rule-soft) pb-6 mb-8">
      <div className="flex flex-col gap-2">
        <div className="t-label flex items-center gap-2">
          <span>Cover</span>
          <FieldLockButton
            bookId={book.id}
            field="cover"
            locked={!!book.locks?.cover}
          />
        </div>
        <div className="w-[160px] h-[240px]">
          <Cover book={book} size="hero" />
        </div>
      </div>
      <div className="flex flex-col gap-2 self-end">
        <Button variant="outline" size="sm" onClick={onPickFile}>
          <Icon name="upload" size={13} /> Replace from file…
        </Button>
        <Button
          variant="outline"
          size="sm"
          onClick={() =>
            void navigate({ to: "/book/$id/find", params: { id: book.id } })
          }
        >
          <Icon name="search" size={13} /> Find covers online
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="text-(--color-accent-ink) hover:text-(--color-accent-ink)"
          disabled={removeCoverMut.isPending || !book.hasCover}
          onClick={() => removeCoverMut.mutate()}
        >
          <Icon name="close" size={13} />{" "}
          {removeCoverMut.isPending ? "Removing…" : "Remove cover"}
        </Button>
      </div>
    </section>
  )
}
