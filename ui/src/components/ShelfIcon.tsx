import { Suspense, lazy } from "react"
import {
  BookMarked,
  BookOpen,
  Bookmark,
  CheckCircle2,
  Clock,
  Flag,
  Flame,
  Folder,
  Hash,
  Heart,
  Library,
  Sparkles,
  Star,
  Tag,
} from "lucide-react"

const DynamicIconLazy = lazy(() =>
  import("lucide-react/dynamic").then((m) => ({ default: m.DynamicIcon }))
)

// Static map covers the suggestion-list glyphs and the migration-backfill
// names so the common case renders synchronously without a dynamic import.
// Tail icons hit the lazy DynamicIcon path. ADR-0019.
const STATIC_SHELF_ICONS: Record<
  string,
  React.ComponentType<{ size?: number; className?: string }>
> = {
  library: Library,
  "book-open": BookOpen,
  "book-marked": BookMarked,
  bookmark: Bookmark,
  star: Star,
  flag: Flag,
  folder: Folder,
  sparkles: Sparkles,
  flame: Flame,
  heart: Heart,
  hash: Hash,
  clock: Clock,
  tag: Tag,
  "check-circle-2": CheckCircle2,
}

type Props = {
  name: string
  size?: number
  className?: string
  "aria-label"?: string
}

export function ShelfIcon({ name, size = 15, className, ...rest }: Props) {
  const Static = STATIC_SHELF_ICONS[name]
  if (Static) {
    return <Static size={size} className={className} {...rest} />
  }
  return (
    <Suspense
      fallback={
        <span
          aria-hidden
          style={{ display: "inline-block", width: size, height: size }}
          className={className}
        />
      }
    >
      <DynamicIconLazy
        name={name as never}
        size={size}
        className={className}
        {...rest}
      />
    </Suspense>
  )
}

export const SHELF_ICON_SUGGESTIONS = [
  "library",
  "book-marked",
  "book-open",
  "bookmark",
  "star",
  "flag",
  "folder",
  "sparkles",
  "flame",
  "heart",
  "hash",
  "clock",
  "tag",
] as const
