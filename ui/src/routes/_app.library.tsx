import { useMemo, useState } from "react"
import { createFileRoute, useNavigate } from "@tanstack/react-router"

import type { Book } from "@/api/books"
import { booksQuery, librariesQuery, shelvesQuery } from "@/api/books"
import { useApiQuery } from "@/api/query"
import { Cover, StarRating } from "@/components/Cover"
import { Icon } from "@/components/Icon"
import { ProgressBar } from "@/components/ProgressBar"
import { NotebookEmpty } from "@/components/SettingsShared"
import { ShelfIcon } from "@/components/ShelfIcon"
import { TopBar } from "@/components/TopBar"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

type Layout = "shelf" | "grid" | "list"
type SortKey = "added" | "title" | "author" | "rating"

type LibrarySearch = {
  shelf?: string
  library?: string
  layout?: Layout
  unshelved?: "1"
  sort?: SortKey
}

export const Route = createFileRoute("/_app/library")({
  validateSearch: (raw: Record<string, unknown>): LibrarySearch => ({
    shelf: typeof raw.shelf === "string" ? raw.shelf : undefined,
    library: typeof raw.library === "string" ? raw.library : undefined,
    layout:
      raw.layout === "shelf" || raw.layout === "grid" || raw.layout === "list"
        ? raw.layout
        : undefined,
    // `?unshelved=1` is the canonical truthy form. Anything else drops to
    // undefined so the URL stays clean when the filter is off.
    unshelved: raw.unshelved === "1" ? "1" : undefined,
    sort:
      raw.sort === "added" ||
      raw.sort === "title" ||
      raw.sort === "author" ||
      raw.sort === "rating"
        ? raw.sort
        : undefined,
  }),
  component: LibraryView,
})

// sortKeyToApi maps our local UI sort terms onto the backend's vocabulary.
// The backend uses "recent" for created_at DESC; the UI calls it "added".
function sortKeyToApi(k: SortKey): "title" | "author" | "recent" | "rating" {
  return k === "added" ? "recent" : k
}

function LibraryView() {
  const navigate = useNavigate()
  const {
    shelf: activeShelf,
    library: activeLibrary,
    layout: layoutSearch,
    unshelved: unshelvedSearch,
    sort: sortSearch,
  } = Route.useSearch()
  const layout: Layout = layoutSearch ?? "grid"
  const isUnshelved = unshelvedSearch === "1"
  const sortBy: SortKey = sortSearch ?? "title"

  const [filterFormat, setFilterFormat] = useState<string | null>(null)

  const setSortBy = (next: SortKey) => {
    void navigate({
      to: "/library",
      search: (prev) => ({
        ...prev,
        // Drop the param when the user picks the default — keeps URLs clean.
        sort: next === "title" ? undefined : next,
      }),
    })
  }

  // Shelf filter takes precedence on the server; library + q + format are
  // merged as additional filters otherwise. `unshelved` is orthogonal:
  // it stacks with library/q/format. When `shelf` is also set, the
  // server lets `shelf` win — the sidebar already clears the conflicting
  // one client-side, so we don't need to defend against it here.
  const queryParams = {
    shelf: activeShelf || undefined,
    library: activeLibrary || undefined,
    format: filterFormat ? [filterFormat] : undefined,
    sort: sortKeyToApi(sortBy),
    unshelved: isUnshelved || undefined,
  }

  const books = useApiQuery(booksQuery(queryParams), {
    // Hold the previous grid while a filter changes rather than blanking
    // the page — the key carries the filter, so this is a different query.
    placeholderData: (prev) => prev,
  })

  const libraries = useApiQuery(librariesQuery)
  const shelves = useApiQuery(shelvesQuery)

  const rows = books.data?.books ?? []

  const { shelfTitle, subtitle, shelfIcon } = useMemo(() => {
    if (isUnshelved) {
      const n = shelves.data?.unshelvedCount ?? 0
      return {
        shelfTitle: "Unshelved",
        subtitle:
          n === 0
            ? "All books are shelved."
            : `${n} ${n === 1 ? "book" : "books"} waiting to be shelved.`,
        shelfIcon: undefined,
      }
    }
    if (activeShelf) {
      const s = shelves.data?.shelves.find((x) => x.slug === activeShelf)
      if (s) {
        return {
          shelfTitle: s.name,
          subtitle:
            activeShelf === "reading"
              ? "Books with a ribbon still in them."
              : activeShelf === "finished"
                ? "Shelved, loved, occasionally revisited."
                : `${s.bookCount} volumes on this shelf.`,
          shelfIcon: s.icon,
        }
      }
      return { shelfTitle: "Shelf", subtitle: "", shelfIcon: undefined }
    }
    if (activeLibrary) {
      const lib = libraries.data?.find((x) => x.slug === activeLibrary)
      if (lib) {
        return {
          shelfTitle: lib.name,
          subtitle: `${lib.bookCount} volumes in this library.`,
          shelfIcon: undefined,
        }
      }
    }
    return {
      shelfTitle: "All Books",
      subtitle: "Your complete collection across every library.",
      shelfIcon: undefined,
    }
  }, [activeShelf, activeLibrary, isUnshelved, shelves.data, libraries.data])

  const setLayout = (next: Layout) => {
    void navigate({
      to: "/library",
      search: (prev) => ({ ...prev, layout: next }),
    })
  }

  const openBook = (id: string) =>
    void navigate({ to: "/book/$id", params: { id } })

  const layoutBtn = (name: Layout, label: string) => (
    <Button
      variant={layout === name ? "default" : "ghost"}
      className={layout === name ? "shadow-sm" : ""}
      size="icon-sm"
      onClick={() => setLayout(name)}
      title={label}
      aria-label={`${label} layout`}
      aria-pressed={layout === name}
    >
      <Icon
        name={name === "shelf" ? "shelf" : name === "grid" ? "grid" : "list"}
        size={13}
      />
    </Button>
  )

  return (
    <div className="fade-in">
      <TopBar
        title={
          shelfIcon ? (
            <span className="inline-flex items-center gap-2">
              <ShelfIcon name={shelfIcon} size={22} />
              <span>{shelfTitle}</span>
            </span>
          ) : (
            shelfTitle
          )
        }
        subtitle={subtitle}
        right={
          <div className="flex items-center gap-1 rounded-md border border-border bg-muted p-1">
            {layoutBtn("shelf", "Shelf")}
            {layoutBtn("grid", "Grid")}
            {layoutBtn("list", "List")}
          </div>
        }
      />

      {/* Filter rail */}
      <div className="flex flex-wrap items-center gap-3 border-b border-border bg-muted/50 px-4 py-3 md:px-8">
        <span className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">
          Filter
        </span>
        {[null, "EPUB", "PDF", "CBZ", "M4B"].map((f) => (
          <button
            key={f ?? "all"}
            className={`cursor-pointer rounded-full px-3 py-1.5 text-xs font-medium transition-colors ${filterFormat === f ? "bg-primary text-primary-foreground" : "bg-transparent text-muted-foreground hover:bg-muted"}`}
            onClick={() => setFilterFormat(f)}
            style={{ border: "none" }}
          >
            {f ?? "All formats"}
          </button>
        ))}
        <div className="grow" />
        <span className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">
          Sort
        </span>
        <Select value={sortBy} onValueChange={(v) => setSortBy(v as SortKey)}>
          <SelectTrigger size="sm" className="w-auto">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="added">Recently added</SelectItem>
            <SelectItem value="title">Title</SelectItem>
            <SelectItem value="author">Author</SelectItem>
            <SelectItem value="rating">Rating</SelectItem>
          </SelectContent>
        </Select>
        <span
          className="mono"
          style={{ fontSize: 11, color: "var(--color-ink-3)", marginLeft: 8 }}
        >
          {books.isLoading ? "loading…" : `${rows.length} volumes`}
        </span>
      </div>

      {/* Content. Keyed on the loading flag so the skeleton→covers swap
          crossfades (120ms, opacity only) instead of popping in one
          frame — the skeleton's grid geometry already matches, so
          nothing shifts. */}
      <div className="flex-1 p-4 pb-20 md:p-8">
        <div key={books.isLoading ? "skeleton" : "content"} className="fade-in">
          {books.isError ? (
            <ErrorPanel message="Failed to load books." />
          ) : books.isLoading ? (
            <LoadingGrid />
          ) : rows.length === 0 ? (
            <EmptyPanel unshelved={isUnshelved} />
          ) : layout === "shelf" ? (
            <ShelfLayout books={rows} onOpen={openBook} />
          ) : layout === "grid" ? (
            <GridLayout books={rows} onOpen={openBook} />
          ) : (
            <ListLayout books={rows} onOpen={openBook} />
          )}
        </div>
      </div>
    </div>
  )
}

function ShelfLayout({
  books,
  onOpen,
}: {
  books: Array<Book>
  onOpen: (id: string) => void
}) {
  const chunks: Array<Array<Book>> = []
  const perRow = 8
  for (let i = 0; i < books.length; i += perRow)
    chunks.push(books.slice(i, i + perRow))
  return (
    <div className="flex flex-col gap-10">
      {chunks.map((row, ri) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: rows are fixed-size chunks of the book list — the chunk index is the row
        <div key={ri} className="shelf-row">
          <div className="relative w-full max-w-full overflow-hidden">
            <div className="flex snap-x gap-4 overflow-x-auto pb-4">
              {row.map((b) => (
                <Cover
                  key={b.id}
                  book={b}
                  size="md"
                  onClick={() => onOpen(b.id)}
                />
              ))}
            </div>
          </div>
          <div className="shelf-plank" />
        </div>
      ))}
    </div>
  )
}

function GridLayout({
  books,
  onOpen,
}: {
  books: Array<Book>
  onOpen: (id: string) => void
}) {
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(110px,1fr))] gap-x-6 gap-y-8 md:grid-cols-[repeat(auto-fill,minmax(110px,1fr))]">
      {books.map((b) => (
        <BookCard key={b.id} book={b} onOpen={onOpen} layout="grid" />
      ))}
    </div>
  )
}

function ListLayout({
  books,
  onOpen,
}: {
  books: Array<Book>
  onOpen: (id: string) => void
}) {
  return (
    <div className="overflow-hidden rounded-lg border border-border bg-card shadow-sm">
      <div className="grid grid-cols-[46px_2fr_1.2fr_80px_80px_60px] items-center gap-4 border-b border-border bg-muted/50 px-4 py-2.5 text-sm">
        <span />
        <span className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">
          Title
        </span>
        <span className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">
          Author
        </span>
        <span className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">
          Format
        </span>
        <span className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">
          Rating
        </span>
        <span className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">
          Year
        </span>
      </div>
      {books.map((b) => (
        <BookCard key={b.id} book={b} onOpen={onOpen} layout="list" />
      ))}
    </div>
  )
}

function BookCard({
  book,
  onOpen,
  layout,
}: {
  book: Book
  onOpen: (id: string) => void
  layout: "grid" | "list"
}) {
  if (layout === "list") {
    return (
      <button
        type="button"
        onClick={() => onOpen(book.id)}
        className="group grid w-full grid-cols-[46px_2fr_1.2fr_80px_80px_60px] items-center gap-4 border-b border-border px-4 py-3 text-left transition-colors last:border-0 hover:bg-muted/30"
        aria-label={`Open ${book.title}`}
      >
        <Cover book={book} size="xs" />
        <div>
          <div style={{ fontWeight: 500, fontSize: 14 }}>{book.title}</div>
          {book.series && (
            <div className="text-sm text-muted-foreground italic">
              {book.series}
              {book.seriesNum ? ` #${book.seriesNum}` : ""}
            </div>
          )}
        </div>
        <div style={{ fontSize: 13, color: "var(--color-ink-2)" }}>
          {book.author}
        </div>
        <div
          className="mono"
          style={{ fontSize: 11, color: "var(--color-ink-3)" }}
        >
          {book.format}
        </div>
        <StarRating rating={book.rating} size={11} />
        <div
          className="mono"
          style={{ fontSize: 11, color: "var(--color-ink-3)" }}
        >
          {book.year}
        </div>
      </button>
    )
  }
  return (
    <button
      type="button"
      onClick={() => onOpen(book.id)}
      className="group mx-auto flex w-full max-w-[150px] flex-col gap-3 text-left transition-transform duration-200 hover:scale-[1.02]"
      aria-label={`Open ${book.title}`}
    >
      {/* Fluid override: the grid cell runs 110-150px, and the class's
          fixed 130px cover overflowed the narrow cells. */}
      <Cover
        book={book}
        size="md"
        style={{ width: "100%", height: "auto", aspectRatio: "2 / 3" }}
      />
      <div>
        <div className="mt-1 text-[13px] leading-snug font-medium text-balance transition-colors group-hover:text-primary">
          {book.title}
        </div>
        <div className="mt-0.5 text-xs text-muted-foreground italic">
          {book.author}
        </div>
        {book.progress > 0 && book.progress < 1 && (
          <ProgressBar
            value={book.progress}
            label={`Progress through ${book.title}`}
            style={{ marginTop: 6, height: 2 }}
          />
        )}
      </div>
    </button>
  )
}

// Cover-shaped placeholders in the same grid as GridLayout, so the page
// doesn't render a blank content area while the query is in flight and
// doesn't jump when covers arrive.
function LoadingGrid() {
  return (
    <div
      aria-hidden
      className="grid grid-cols-[repeat(auto-fill,minmax(110px,1fr))] gap-x-6 gap-y-8"
    >
      {Array.from({ length: 12 }, (_, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: identical static placeholders; position is the identity
        <div key={i} className="flex flex-col gap-2">
          <Skeleton className="aspect-2/3 w-full rounded-[2px]" />
          <Skeleton className="h-3 w-4/5 rounded-[2px]" />
          <Skeleton className="h-3 w-3/5 rounded-[2px]" />
        </div>
      ))}
    </div>
  )
}

function EmptyPanel({ unshelved = false }: { unshelved?: boolean }) {
  if (unshelved) {
    return (
      <NotebookEmpty
        title="Every book has a home."
        body="Nothing to surface here. Every book in your library is already shelved."
      />
    )
  }
  return (
    <NotebookEmpty
      title="An empty stack."
      body={
        <>
          Drop an EPUB into <span className="mono">/bookdrop</span> to stage it
          for review, or create a library in Settings.
        </>
      }
    />
  )
}

function ErrorPanel({ message }: { message: string }) {
  return (
    <div className="rounded-lg border border-destructive/20 bg-destructive/10 p-4 text-sm text-destructive">
      {message}
    </div>
  )
}
