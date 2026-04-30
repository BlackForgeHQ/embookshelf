import { useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"

import type { Book } from "@/api/books"
import {
  booksQueryKey,
  fetchBooks,
  fetchLibraries,
  fetchShelves,
  librariesQueryKey,
  shelvesQueryKey,
} from "@/api/books"
import { Cover, StarRating } from "@/components/Cover"
import { Icon } from "@/components/Icon"
import { TopBar } from "@/components/TopBar"
import { Button } from "@/components/ui/button"
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
}

export const Route = createFileRoute("/_app/library")({
  validateSearch: (raw: Record<string, unknown>): LibrarySearch => ({
    shelf: typeof raw.shelf === "string" ? raw.shelf : undefined,
    library: typeof raw.library === "string" ? raw.library : undefined,
    layout:
      raw.layout === "shelf" || raw.layout === "grid" || raw.layout === "list"
        ? raw.layout
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
  } = Route.useSearch()
  const layout: Layout = layoutSearch ?? "grid"

  const [search, setSearch] = useState("")
  const [sortBy, setSortBy] = useState<SortKey>("added")
  const [filterFormat, setFilterFormat] = useState<string | null>(null)

  // Shelf filter takes precedence on the server; library + q + format are
  // merged as additional filters otherwise.
  const queryParams = {
    shelf: activeShelf || undefined,
    library: activeLibrary || undefined,
    q: search || undefined,
    format: filterFormat ? [filterFormat] : undefined,
    sort: sortKeyToApi(sortBy),
  }

  const books = useQuery({
    queryKey: booksQueryKey(queryParams),
    queryFn: () => fetchBooks(queryParams),
    placeholderData: (prev) => prev,
  })

  const libraries = useQuery({
    queryKey: librariesQueryKey,
    queryFn: fetchLibraries,
  })
  const shelves = useQuery({ queryKey: shelvesQueryKey, queryFn: fetchShelves })

  const rows = books.data?.books ?? []

  const { shelfTitle, subtitle } = useMemo(() => {
    if (activeShelf) {
      const s = shelves.data?.find((x) => x.slug === activeShelf)
      if (s) {
        return {
          shelfTitle: s.name,
          subtitle:
            activeShelf === "reading"
              ? "Books with a ribbon still in them."
              : activeShelf === "finished"
                ? "Shelved, loved, occasionally revisited."
                : `${s.bookCount} volumes on this shelf.`,
        }
      }
      return { shelfTitle: "Shelf", subtitle: "" }
    }
    if (activeLibrary) {
      const lib = libraries.data?.find((x) => x.slug === activeLibrary)
      if (lib) {
        return {
          shelfTitle: lib.name,
          subtitle: `${lib.bookCount} volumes in this library.`,
        }
      }
    }
    return {
      shelfTitle: "All Books",
      subtitle: "Your complete collection across every library.",
    }
  }, [activeShelf, activeLibrary, shelves.data, libraries.data])

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
        title={shelfTitle}
        subtitle={subtitle}
        search={search}
        setSearch={setSearch}
        searchVariant="command"
        commandHint={false}
        right={
          <div className="flex items-center gap-1 bg-muted p-1 rounded-md border border-border">
            {layoutBtn("shelf", "Shelf")}
            {layoutBtn("grid", "Grid")}
            {layoutBtn("list", "List")}
          </div>
        }
      />

      {/* Filter rail */}
      <div
        className="flex flex-wrap items-center gap-3 px-4 md:px-8 py-3 border-b border-border bg-muted/50"
      >
        <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Filter</span>
        {[null, "EPUB", "PDF", "CBZ", "M4B"].map((f) => (
          <button
            key={f ?? "all"}
            className={`px-3 py-1.5 rounded-full text-xs font-medium transition-colors cursor-pointer ${filterFormat === f ? "bg-primary text-primary-foreground" : "bg-transparent hover:bg-muted text-muted-foreground"}`}
            onClick={() => setFilterFormat(f)}
            style={{ border: "none" }}
          >
            {f ?? "All formats"}
          </button>
        ))}
        <div className="grow" />
        <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Sort</span>
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

      {/* Content */}
      <div className="flex-1 p-4 md:p-8 pb-20">
        {books.isError ? (
          <ErrorPanel message="Failed to load books." />
        ) : rows.length === 0 && !books.isLoading ? (
          <EmptyPanel />
        ) : layout === "shelf" ? (
          <ShelfLayout books={rows} onOpen={openBook} />
        ) : layout === "grid" ? (
          <GridLayout books={rows} onOpen={openBook} />
        ) : (
          <ListLayout books={rows} onOpen={openBook} />
        )}
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
        <div key={ri} className="shelf-row">
          <div className="w-full relative max-w-full overflow-hidden">
            <div className="flex gap-4 overflow-x-auto pb-4 snap-x">
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
    <div
      className="grid grid-cols-[repeat(auto-fill,minmax(110px,1fr))] md:grid-cols-[repeat(auto-fill,minmax(110px,1fr))] md:grid-cols-[repeat(auto-fill,minmax(140px,1fr))] gap-x-6 gap-y-8"
    >
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
    <div
      className="bg-card border border-border rounded-lg shadow-sm overflow-hidden"
    >
      <div
        className="grid grid-cols-[46px_2fr_1.2fr_80px_80px_60px] items-center gap-4 px-4 py-2.5 border-b border-border bg-muted/50 text-sm"
      >
        <span />
        <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Title</span>
        <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Author</span>
        <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Format</span>
        <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Rating</span>
        <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Year</span>
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
        className="group grid grid-cols-[46px_2fr_1.2fr_80px_80px_60px] items-center gap-4 px-4 py-3 text-left border-b border-border hover:bg-muted/30 transition-colors w-full last:border-0"
        aria-label={`Open ${book.title}`}
        
      >
        <Cover book={book} size="xs" />
        <div>
          <div style={{ fontWeight: 500, fontSize: 14 }}>{book.title}</div>
          {book.series && (
            <div className="text-sm italic text-muted-foreground">
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
      className="group flex flex-col gap-3 text-left w-full max-w-[150px] mx-auto transition-transform duration-200 hover:scale-[1.02]"
      aria-label={`Open ${book.title}`}
    >
      <Cover book={book} size="md" />
      <div>
        <div
          className="text-[13px] font-medium leading-snug text-balance mt-1 group-hover:text-primary transition-colors"
        >
          {book.title}
        </div>
        <div className="text-xs italic text-muted-foreground mt-0.5">
          {book.author}
        </div>
        {book.progress > 0 && book.progress < 1 && (
          <div className="progress" style={{ marginTop: 6, height: 2 }}>
            <div style={{ width: `${book.progress * 100}%` }} />
          </div>
        )}
      </div>
    </button>
  )
}

function EmptyPanel() {
  return (
    <div
      className="p-12 text-center border-2 border-dashed border-border rounded-lg bg-card text-muted-foreground"
    >
      <div className="text-lg font-serif font-medium text-foreground" style={{ marginBottom: 8 }}>
        Nothing to show yet.
      </div>
      <div className="text-sm italic text-muted-foreground">
        Drop an EPUB into <span className="mono">/bookdrop</span> or register a
        library path in Settings.
      </div>
    </div>
  )
}

function ErrorPanel({ message }: { message: string }) {
  return (
    <div
      className="p-4 border border-destructive/20 bg-destructive/10 text-destructive rounded-lg text-sm"
    >
      {message}
    </div>
  )
}
