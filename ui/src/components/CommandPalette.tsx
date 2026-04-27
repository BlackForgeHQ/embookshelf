import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"

import { fetchMe, meQueryKey } from "@/api/auth"
import { searchQueryKey, searchSuggest } from "@/api/search"
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command"
import { useSidebar } from "@/components/ui/sidebar"
import { useShelfDraftDialog } from "@/components/ShelfDraftProvider"
import { useUserSettingsDialog } from "@/components/UserSettingsDialog"
import { useDebounce } from "@/hooks/useDebounce"
import { useLogout } from "@/hooks/useLogout"

type Props = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const MIN_QUERY_LENGTH = 2
const DEBOUNCE_MS = 200

export function CommandPalette({ open, onOpenChange }: Props) {
  const navigate = useNavigate()
  const me = useQuery({
    queryKey: meQueryKey,
    queryFn: fetchMe,
    staleTime: 60_000,
  })
  const isAdmin = me.data?.role === "admin"

  const shelfDraft = useShelfDraftDialog()
  const userSettings = useUserSettingsDialog()
  const sidebar = useSidebar()
  const logoutMut = useLogout()

  const [value, setValue] = useState("")
  const debounced = useDebounce(value, DEBOUNCE_MS)
  const enabled = debounced.trim().length >= MIN_QUERY_LENGTH

  const query = useQuery({
    queryKey: searchQueryKey(debounced, 8),
    queryFn: () => searchSuggest(debounced, 8),
    enabled: open && enabled,
    staleTime: 30_000,
  })

  function close() {
    onOpenChange(false)
    setValue("")
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

  return (
    <CommandDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Command palette"
      description="Search the library or run a command."
      className="sm:max-w-[640px]"
    >
      <CommandInput
        placeholder="Search books, shelves, or run a command…"
        value={value}
        onValueChange={setValue}
        autoFocus
      />
      <CommandList>
        {enabled && !query.isLoading && !hasSearchResults && (
          <CommandEmpty>No matches</CommandEmpty>
        )}

        <CommandGroup heading="Quick actions">
          <CommandItem
            value="open bookdrop intake upload"
            onSelect={() => run(() => { void navigate({ to: "/bookdrop" }) })}
          >
            Open Bookdrop intake
          </CommandItem>
          <CommandItem
            value="new shelf create collection"
            onSelect={() => run(() => shelfDraft.open())}
          >
            New shelf
          </CommandItem>
          <CommandItem
            value="open user settings preferences account"
            onSelect={() => run(() => userSettings.open())}
          >
            Open user settings
          </CommandItem>
          <CommandItem
            value="toggle sidebar collapse expand"
            onSelect={() => run(() => sidebar.toggleSidebar())}
          >
            Toggle sidebar
          </CommandItem>
          {isAdmin && (
            <CommandItem
              value="library scan rescan reindex admin"
              onSelect={() => run(() => { void navigate({ to: "/settings" }) })}
            >
              Library scan (Settings → Libraries)
            </CommandItem>
          )}
          <CommandItem
            value="sign out logout"
            onSelect={() => run(() => logoutMut.mutate())}
          >
            Sign out
          </CommandItem>
        </CommandGroup>

        <CommandSeparator />

        <CommandGroup heading="Navigation">
          <CommandItem
            value="library all books"
            onSelect={() => run(() => { void navigate({ to: "/library" }) })}
          >
            Library
          </CommandItem>
          <CommandItem
            value="bookdrop"
            onSelect={() => run(() => { void navigate({ to: "/bookdrop" }) })}
          >
            Bookdrop
          </CommandItem>
          <CommandItem
            value="notebook annotations highlights"
            onSelect={() => run(() => { void navigate({ to: "/notebook" }) })}
          >
            Notebook
          </CommandItem>
          <CommandItem
            value="stats reading"
            onSelect={() => run(() => { void navigate({ to: "/stats" }) })}
          >
            Stats
          </CommandItem>
          <CommandItem
            value="settings"
            onSelect={() => run(() => { void navigate({ to: "/settings" }) })}
          >
            Settings
          </CommandItem>
        </CommandGroup>

        {enabled && data && data.books.length > 0 && (
          <>
            <CommandSeparator />
            <CommandGroup heading="Books">
              {data.books.map((b) => (
                <CommandItem
                  key={b.id}
                  value={`book ${b.id} ${b.title} ${b.author}`}
                  onSelect={() =>
                    run(() => { void navigate({ to: "/book/$id", params: { id: b.id } }) })
                  }
                >
                  <span>{b.title}</span>
                  <span className="ml-auto text-xs text-(--color-ink-3)">
                    {b.author}
                  </span>
                </CommandItem>
              ))}
            </CommandGroup>
          </>
        )}

        {enabled && data && data.shelves.length > 0 && (
          <>
            <CommandSeparator />
            <CommandGroup heading="Shelves">
              {data.shelves.map((s) => (
                <CommandItem
                  key={s.slug}
                  value={`shelf ${s.slug} ${s.name}`}
                  onSelect={() =>
                    run(() =>
                      void navigate({
                        to: "/library",
                        search: (prev: Record<string, unknown>) => ({ ...prev, shelf: s.slug }),
                      })
                    )
                  }
                >
                  {s.name}
                </CommandItem>
              ))}
            </CommandGroup>
          </>
        )}

        {enabled && data && data.libraries.length > 0 && (
          <>
            <CommandSeparator />
            <CommandGroup heading="Libraries">
              {data.libraries.map((l) => (
                <CommandItem
                  key={l.id}
                  value={`library ${l.id} ${l.name}`}
                  onSelect={() =>
                    run(() =>
                      void navigate({
                        to: "/library",
                        search: (prev: Record<string, unknown>) => ({ ...prev, library: l.slug }),
                      })
                    )
                  }
                >
                  {l.name}
                </CommandItem>
              ))}
            </CommandGroup>
          </>
        )}
      </CommandList>
    </CommandDialog>
  )
}
