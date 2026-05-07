import { useQueryClient } from "@tanstack/react-query"

import type { LockField } from "@/api/books"
import { bookQueryKey, toggleBookFieldLocks } from "@/api/books"
import { useApiMutation } from "@/api/mutation"
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
  const mut = useApiMutation(toggleBookFieldLocks, {
    errorToast: (err) => err.message || "Lock update failed.",
    onSuccess: (fresh) => {
      queryClient.setQueryData(bookQueryKey(bookId), fresh)
    },
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
      onClick={() => mut.mutate({ id: bookId, locks: { [field]: !locked } })}
      className={cn(
        "inline-flex h-5 w-5 cursor-pointer items-center justify-center bg-transparent leading-none disabled:cursor-not-allowed disabled:opacity-50",
        locked
          ? "text-(--color-accent-ink)"
          : "text-(--color-ink-3) hover:text-(--color-ink-1)",
        className
      )}
    >
      <Icon name={locked ? "lock" : "unlock"} size={size} />
    </button>
  )
}
