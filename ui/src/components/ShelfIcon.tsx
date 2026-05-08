import {
  BookMarked,
  BookOpen,
  Bookmark,
  CheckCircle2,
  CheckIcon,
  ChevronDownIcon,
  ChevronRightIcon,
  ChevronUpIcon,
  CircleCheckIcon,
  Clock,
  Flag,
  Flame,
  Folder,
  Hash,
  Heart,
  InfoIcon,
  Library,
  Loader2Icon,
  MoreHorizontalIcon,
  OctagonXIcon,
  PanelLeftIcon,
  Search,
  Sparkles,
  Star,
  Tag,
  TriangleAlertIcon,
  XIcon,
} from "lucide-react"
import { DynamicIcon } from "lucide-react/dynamic"

// Static map covers the suggestion-list glyphs, the migration-backfill
// names, and every icon statically imported elsewhere in the app
// (shadcn primitives, sonner). Picking one of those names through the
// picker would otherwise create a static-vs-dynamic chunk cycle in the
// lucide-react bundle ("createLucideIcon is not a function" at icon
// chunk eval). Routing them through the static map short-circuits
// DynamicIcon entirely. Tail icons hit DynamicIcon, which lazy-loads
// each icon's SVG chunk on demand. ADR-0019.
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
  check: CheckIcon,
  "chevron-down": ChevronDownIcon,
  "chevron-right": ChevronRightIcon,
  "chevron-up": ChevronUpIcon,
  "more-horizontal": MoreHorizontalIcon,
  "panel-left": PanelLeftIcon,
  search: Search,
  x: XIcon,
  // Sonner toast icons + their lucide kebab aliases. Keys must match the
  // slug a picker user could select; otherwise DynamicIcon loads the
  // module dynamically while the same icon is statically re-exported by
  // sonner.tsx through the lucide-react main barrel — chunk cycle, throws
  // "createLucideIcon is not a function".
  "circle-check": CircleCheckIcon,
  info: InfoIcon,
  "loader-circle": Loader2Icon,
  "loader-2": Loader2Icon,
  "octagon-x": OctagonXIcon,
  "x-octagon": OctagonXIcon,
  "triangle-alert": TriangleAlertIcon,
  "alert-triangle": TriangleAlertIcon,
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
    <DynamicIcon
      name={name as never}
      size={size}
      className={className}
      fallback={() => (
        <span
          aria-hidden
          style={{ display: "inline-block", width: size, height: size }}
          className={className}
        />
      )}
      {...rest}
    />
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
