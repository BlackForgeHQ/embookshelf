import { useState } from "react"
import { Command as CommandPrimitive } from "cmdk"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"

import { Icon } from "@/components/Icon"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import { Popover, PopoverAnchor, PopoverContent } from "@/components/ui/popover"
import { useDebounce } from "@/hooks/useDebounce"
import { searchQueryKey, searchSuggest } from "@/api/search"

type Props = {
  value: string
  onSearchChange: (next: string) => void
}

const MIN_QUERY_LENGTH = 2
const DEBOUNCE_MS = 200

export function LibrarySearchCombobox({ value, onSearchChange }: Props) {
  const navigate = useNavigate()
  const [focused, setFocused] = useState(false)
  const debounced = useDebounce(value, DEBOUNCE_MS)
  const enabled = debounced.trim().length >= MIN_QUERY_LENGTH

  const query = useQuery({
    queryKey: searchQueryKey(debounced, 8),
    queryFn: () => searchSuggest(debounced, 8),
    enabled,
    staleTime: 30_000,
  })

  const open = focused && enabled
  const books = query.data?.books ?? []

  return (
    <Popover open={open} onOpenChange={() => undefined}>
      <PopoverAnchor asChild>
        <div style={{ position: "relative", width: 240 }}>
          <Command shouldFilter={false} className="bg-transparent">
            <div className="group flex h-7 items-center gap-2 border-b border-(--color-rule) text-[13px] text-(--color-ink-3) transition-colors focus-within:border-(--color-ink-1) focus-within:text-(--color-ink-1) hover:border-(--color-ink-2)">
              <Icon name="search" size={13} className="text-(--color-ink-3)" />
              <CommandPrimitive.Input
                placeholder="Search the library"
                value={value}
                onValueChange={onSearchChange}
                onFocus={() => setFocused(true)}
                onBlur={() => {
                  // Delay so click on a CommandItem fires before close.
                  setTimeout(() => setFocused(false), 150)
                }}
                className="grow bg-transparent font-serif tracking-tight text-(--color-ink-1) italic outline-none placeholder:text-(--color-ink-3) placeholder:italic"
              />
              <kbd className="font-mono text-[10px] tracking-[0.08em] text-(--color-ink-3) uppercase">
                ⌘K
              </kbd>
            </div>
          </Command>
        </div>
      </PopoverAnchor>
      <PopoverContent
        align="start"
        sideOffset={6}
        className="w-[320px] rounded-sm border-(--color-rule-soft) bg-(--color-paper-0) p-0 shadow-[0_8px_24px_-12px_rgba(40,30,20,0.18)]"
        onOpenAutoFocus={(e) => e.preventDefault()}
      >
        <Command shouldFilter={false}>
          <CommandList>
            {query.isLoading ? (
              <div className="px-3 py-4 text-xs text-(--color-ink-3)">
                Searching…
              </div>
            ) : books.length === 0 ? (
              <CommandEmpty>No matches</CommandEmpty>
            ) : (
              <CommandGroup heading="Books">
                {books.map((b) => (
                  <CommandItem
                    key={b.id}
                    value={`${b.id} ${b.title} ${b.author}`}
                    onSelect={() => {
                      void navigate({ to: "/book/$id", params: { id: b.id } })
                    }}
                  >
                    {b.cover ? (
                      <img
                        src={b.cover}
                        alt=""
                        width={24}
                        height={32}
                        style={{ objectFit: "cover", borderRadius: 2 }}
                      />
                    ) : (
                      <div
                        style={{
                          width: 24,
                          height: 32,
                          borderRadius: 2,
                          background: "var(--color-rule-soft)",
                        }}
                      />
                    )}
                    <div className="flex min-w-0 flex-col">
                      <span className="truncate">{b.title}</span>
                      <span className="text-xs text-(--color-ink-3)">
                        {b.author}
                      </span>
                    </div>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
