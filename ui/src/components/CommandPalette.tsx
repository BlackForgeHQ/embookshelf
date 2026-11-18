import { useEffect, useState } from "react"

import { useApiQuery } from "@/api/query"
import { searchQuery } from "@/api/search"
import { Icon } from "@/components/Icon"
import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command"
import { useDebounce } from "@/hooks/useDebounce"
import { parseLibrarySearch, paths } from "@/lib/routes"
import { useAppRouter } from "@/lib/navigation"

type Props = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const MIN_QUERY_LENGTH = 2
const DEBOUNCE_MS = 200

export function CommandPalette({ open, onOpenChange }: Props) {
  const router = useAppRouter()

  const [input, setInput] = useState("")
  const [selected, setSelected] = useState("")
  const debounced = useDebounce(input, DEBOUNCE_MS)
  const enabled = debounced.trim().length >= MIN_QUERY_LENGTH

  const query = useApiQuery(searchQuery(debounced, 8), {
    enabled: open && enabled,
  })

  function close() {
    onOpenChange(false)
    setInput("")
    setSelected("")
  }

  function run(handler: () => void) {
    handler()
    close()
  }

  const data = query.data
  const hasSearchResults =
    enabled &&
    !!data &&
    (data.books.length > 0 ||
      data.shelves.length > 0 ||
      data.libraries.length > 0)

  // Jump highlight to first search result the moment async data lands.
  // cmdk auto-selects on mount, but it doesn't re-select when items
  // appear later — without this, the highlight stays on a Navigation
  // row while books/shelves are visibly the only matches. This is a
  // remote-data → controlled-selection sync, the intended use of
  // setState-in-effect.
  useEffect(() => {
    if (!enabled || !data) return
    const b = data.books[0]
    if (b) {
      // Deliberate: setState inside an effect, syncing React state from an
      // external source. Was suppressed via react-hooks/set-state-in-effect;
      // Biome has no equivalent rule yet, so there is nothing to suppress.
      setSelected(`book ${b.id} ${b.title} ${b.author}`)
      return
    }
    const s = data.shelves[0]
    if (s) {
      setSelected(`shelf ${s.slug} ${s.name}`)
      return
    }
    const l = data.libraries[0]
    if (l) setSelected(`library ${l.id} ${l.name}`)
  }, [enabled, data])

  return (
    <CommandDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Command palette"
      description="Search the library or run a command."
      // duration-0 kills the dialog zoom/fade: the palette is
      // keyboard-summoned dozens of times a day, and any open animation
      // reads as input lag (the Raycast rule).
      className="duration-0 sm:max-w-[640px]"
    >
      <Command value={selected} onValueChange={setSelected}>
        <CommandInput
          placeholder="Search books, shelves, or run a command…"
          value={input}
          onValueChange={setInput}
          autoFocus
        />
        <CommandList>
          {enabled && !query.isLoading && !hasSearchResults && (
            <CommandEmpty>No matches</CommandEmpty>
          )}

          {enabled && data && data.books.length > 0 && (
            <CommandGroup heading="Books">
              {data.books.map((b) => (
                <CommandItem
                  key={b.id}
                  value={`book ${b.id} ${b.title} ${b.author}`}
                  onSelect={() =>
                    run(() => { router.push(paths.book(b.id)) })
                  }
                >
                  <Icon name="book" size={14} />
                  <span>{b.title}</span>
                  <span className="ml-auto text-xs text-(--color-ink-3)">
                    {b.author}
                  </span>
                </CommandItem>
              ))}
            </CommandGroup>
          )}

          {enabled && data && data.shelves.length > 0 && (
            <CommandGroup heading="Shelves">
              {data.shelves.map((s) => (
                <CommandItem
                  key={s.slug}
                  value={`shelf ${s.slug} ${s.name}`}
                  onSelect={() =>
                    run(() =>
                      router.push(
                        paths.library({ ...currentLibrarySearch(), shelf: s.slug })
                      )
                    )
                  }
                >
                  <Icon name="shelf" size={14} />
                  {s.name}
                </CommandItem>
              ))}
            </CommandGroup>
          )}

          {enabled && data && data.libraries.length > 0 && (
            <CommandGroup heading="Libraries">
              {data.libraries.map((l) => (
                <CommandItem
                  key={l.id}
                  value={`library ${l.id} ${l.name}`}
                  onSelect={() =>
                    run(() =>
                      router.push(
                        paths.library({ ...currentLibrarySearch(), library: l.slug })
                      )
                    )
                  }
                >
                  <Icon name="library" size={14} />
                  {l.name}
                </CommandItem>
              ))}
            </CommandGroup>
          )}

          {hasSearchResults && <CommandSeparator />}

          <CommandGroup heading="Navigation">
            <CommandItem
              value="library all books"
              onSelect={() => run(() => { router.push(paths.library()) })}
            >
              <Icon name="library" size={14} />
              Library
            </CommandItem>
            <CommandItem
              value="unshelved books no shelf inbox"
              onSelect={() =>
                run(() => {
                  router.push(paths.library({ unshelved: "1" }))
                })
              }
            >
              <Icon name="inbox" size={14} />
              Unshelved
            </CommandItem>
            <CommandItem
              value="bookdrop"
              onSelect={() => run(() => { router.push(paths.bookdrop) })}
            >
              <Icon name="upload" size={14} />
              Bookdrop
            </CommandItem>
            <CommandItem
              value="notebook annotations highlights"
              onSelect={() => run(() => { router.push(paths.notebook) })}
            >
              <Icon name="note" size={14} />
              Notebook
            </CommandItem>
            <CommandItem
              value="stats reading"
              onSelect={() => run(() => { router.push(paths.stats) })}
            >
              <Icon name="chart" size={14} />
              Stats
            </CommandItem>
            <CommandItem
              value="settings"
              onSelect={() => run(() => { router.push(paths.settings) })}
            >
              <Icon name="settings" size={14} />
              Settings
            </CommandItem>
          </CommandGroup>
        </CommandList>
      </Command>
    </CommandDialog>
  )
}

// The library filters already in the URL, so picking a shelf or library
// from the palette narrows the current view rather than resetting it.
// Read at selection time; on any other page this is empty.
function currentLibrarySearch() {
  return parseLibrarySearch(new URLSearchParams(window.location.search))
}
