import { useMutation, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import type { ApiError } from "@/api/client"
import type { LockField } from "@/api/books"
import { bookQueryKey, toggleBookFieldLocks } from "@/api/books"
import { Icon } from "@/components/Icon"
import { cn } from "@/lib/utils"

export function FieldLockButton({
  bookId,
  field,
  locked,
  size = 11,
  className,
}: {
  bookId: string
  field: LockField
  locked: boolean
  size?: number
  className?: string
}) {
  const queryClient = useQueryClient()
  const mut = useMutation({
    mutationFn: (next: boolean) =>
      toggleBookFieldLocks(bookId, { [field]: next }),
    onSuccess: (fresh) => {
      queryClient.setQueryData(bookQueryKey(bookId), fresh)
    },
    onError: (err) =>
      toast.error(
        (err as unknown as ApiError).message || "Lock update failed."
      ),
  })
  return (
    <button
      type="button"
      title={
        locked
          ? "Field is locked — click to unlock"
          : "Lock this field against auto-refresh"
      }
      aria-label={locked ? `Unlock ${field}` : `Lock ${field}`}
      aria-pressed={locked}
      disabled={mut.isPending}
      onClick={() => mut.mutate(!locked)}
      className={cn(
        "inline-flex h-5 w-5 items-center justify-center bg-transparent leading-none cursor-pointer disabled:cursor-not-allowed disabled:opacity-50",
        locked
          ? "text-(--color-accent-ink)"
          : "text-(--color-ink-3) hover:text-(--color-ink-1)",
        className,
      )}
    >
      <Icon name={locked ? "lock" : "unlock"} size={size} />
    </button>
  )
}
