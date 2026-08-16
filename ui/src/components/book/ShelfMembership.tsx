import { useMemo, useState } from "react"

import type { BookDetail as BookDetailPayload } from "@/api/books"
import {
  addBookToShelf,
  removeBookFromShelf,
  shelvesQuery,
} from "@/api/books"
import { useApiMutation } from "@/api/mutation"
import { useApiQuery } from "@/api/query"
import { Icon } from "@/components/Icon"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import { shelfGroups } from "@/lib/shelves"

// ShelfMembership renders the book's current shelf memberships and
// exposes an
// inline searchable picker. Manual shelves toggle in-place; smart shelves
// are surfaced read-only with a rule hint so users learn why they can't
// be edited directly. Out of the route file since #352: this is a
// self-contained module, and its central rule — which shelves the
// viewer curates — lives in lib/shelves beside the Sidebar's reading of
// the same split.
export function ShelfMembership({ book }: { book: BookDetailPayload }) {
  const shelves = useApiQuery(shelvesQuery)
  const [pickerOpen, setPickerOpen] = useState(false)

  const addMut = useApiMutation(addBookToShelf, {
    errorToast: (err) => err.message || "Couldn't add to shelf",
  })
  const removeMut = useApiMutation(removeBookFromShelf, {
    errorToast: (err) => err.message || "Couldn't remove from shelf",
  })

  const currentSlugs = useMemo(() => new Set(book.shelves), [book.shelves])
  const all = useMemo(
    () => shelves.data?.shelves ?? [],
    [shelves.data?.shelves]
  )
  // Picker only offers shelves the viewer actually curates — the one
  // spelling of that rule is shelfGroups (#352).
  const manual = useMemo(() => shelfGroups(all).curatable, [all])
  const smartActive = useMemo(
    () => all.filter((s) => s.isSmart && currentSlugs.has(s.slug)),
    [all, currentSlugs]
  )
  const activeManual = useMemo(
    () => manual.filter((s) => currentSlugs.has(s.slug)),
    [manual, currentSlugs]
  )
  const totalActive = activeManual.length + smartActive.length
  const pending = addMut.isPending || removeMut.isPending

  const onToggle = (slug: string) => {
    if (currentSlugs.has(slug))
      removeMut.mutate({ bookId: book.id, shelfSlug: slug })
    else addMut.mutate({ bookId: book.id, shelfSlug: slug })
  }

  return (
    <div
      style={{
        border: "1px solid var(--color-rule-soft)",
        padding: 16,
        background: "var(--color-paper-0)",
        borderRadius: 2,
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          marginBottom: 12,
        }}
      >
        <div className="t-label">Shelves</div>
        <div
          className="mono"
          style={{
            fontSize: 10.5,
            color: "var(--color-ink-3)",
            letterSpacing: "0.05em",
          }}
        >
          {totalActive} · {manual.length} avail
        </div>
      </div>

      {shelves.isPending ? (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
          {[68, 92, 56].map((w) => (
            <span
              key={w}
              aria-hidden
              style={{
                display: "inline-block",
                width: w,
                height: 22,
                borderRadius: 100,
                background:
                  "linear-gradient(90deg, var(--color-paper-2) 0%, var(--color-paper-3) 50%, var(--color-paper-2) 100%)",
                backgroundSize: "200% 100%",
                animation: "shelfShimmer 1400ms ease-in-out infinite",
              }}
            />
          ))}
        </div>
      ) : (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
          {totalActive === 0 && (
            <span
              className="t-small"
              style={{ fontStyle: "italic", color: "var(--color-ink-3)" }}
            >
              Not on any shelves yet.
            </span>
          )}
          {activeManual.map((s) => (
            <button
              key={s.slug}
              type="button"
              className="chip accent group"
              onClick={() =>
                removeMut.mutate({ bookId: book.id, shelfSlug: s.slug })
              }
              disabled={pending}
              title={`Remove from ${s.name}`}
              style={{
                cursor: "pointer",
                transition:
                  "transform 160ms cubic-bezier(0.16,1,0.3,1), opacity 120ms ease",
                opacity: pending ? 0.6 : 1,
              }}
              onMouseDown={(e) => {
                e.currentTarget.style.transform = "translateY(1px)"
              }}
              onMouseUp={(e) => {
                e.currentTarget.style.transform = "translateY(0)"
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.transform = "translateY(0)"
              }}
            >
              {s.name}
              <Icon name="close" size={10} />
            </button>
          ))}
          {smartActive.map((s) => (
            <span
              key={s.slug}
              className="chip"
              title="Auto-matched by rule. Manage on the shelf page"
              style={{
                cursor: "default",
                background: "transparent",
                borderColor: "var(--color-rule-soft)",
                color: "var(--color-ink-3)",
              }}
            >
              <Icon name="sparkle" size={10} />
              {s.name}
            </span>
          ))}

          <Popover open={pickerOpen} onOpenChange={setPickerOpen}>
            <PopoverTrigger asChild>
              <button
                type="button"
                className="chip"
                aria-label="Add to shelf"
                style={{
                  cursor: "pointer",
                  borderStyle: "dashed",
                  background: "transparent",
                }}
              >
                <Icon name="plus" size={10} /> Add
              </button>
            </PopoverTrigger>
            <PopoverContent
              align="start"
              sideOffset={6}
              className="w-72 p-0"
              style={{
                background: "var(--color-paper-0)",
                border: "1px solid var(--color-rule)",
                borderRadius: 4,
                boxShadow:
                  "0 16px 40px -20px rgba(40,30,20,0.18), 0 1px 0 rgba(255,255,255,0.6) inset",
              }}
            >
              <Command
                style={{ background: "transparent" }}
                filter={(value, search) =>
                  value.toLowerCase().includes(search.toLowerCase()) ? 1 : 0
                }
              >
                <div
                  style={{
                    padding: "10px 12px 6px",
                    borderBottom: "1px solid var(--color-rule-soft)",
                  }}
                >
                  <div
                    className="t-micro"
                    style={{ marginBottom: 6, color: "var(--color-ink-3)" }}
                  >
                    Place on shelf
                  </div>
                  <CommandInput
                    placeholder="Search shelves…"
                    className="h-7 px-0 text-[13px]"
                  />
                </div>
                <CommandList style={{ maxHeight: 280, padding: 4 }}>
                  <CommandEmpty>
                    <div
                      style={{
                        padding: "18px 12px",
                        textAlign: "center",
                      }}
                    >
                      <div
                        className="t-small"
                        style={{
                          fontStyle: "italic",
                          color: "var(--color-ink-3)",
                          marginBottom: 4,
                        }}
                      >
                        No shelf matches.
                      </div>
                      <div
                        className="t-micro"
                        style={{ color: "var(--color-ink-3)" }}
                      >
                        Create one from the sidebar
                      </div>
                    </div>
                  </CommandEmpty>

                  {manual.length > 0 && (
                    <CommandGroup heading="Manual">
                      {manual.map((s) => {
                        const active = currentSlugs.has(s.slug)
                        return (
                          <CommandItem
                            key={s.slug}
                            value={`${s.name} ${s.slug}`}
                            onSelect={() => onToggle(s.slug)}
                            disabled={pending}
                            className="rounded-sm"
                            style={{ padding: "8px 10px" }}
                          >
                            <span
                              aria-hidden
                              style={{
                                width: 14,
                                height: 14,
                                display: "inline-flex",
                                alignItems: "center",
                                justifyContent: "center",
                                border: "1px solid var(--color-rule)",
                                borderRadius: 2,
                                background: active
                                  ? "var(--color-ink-1)"
                                  : "transparent",
                                color: "var(--color-paper-0)",
                                transition:
                                  "background 140ms ease, border-color 140ms ease",
                                flexShrink: 0,
                              }}
                            >
                              {active && <Icon name="check" size={10} />}
                            </span>
                            <span
                              style={{
                                fontSize: 13,
                                color: "var(--color-ink-1)",
                                flex: 1,
                              }}
                            >
                              {s.name}
                            </span>
                            <span
                              className="mono"
                              style={{
                                fontSize: 10.5,
                                color: "var(--color-ink-3)",
                                letterSpacing: "0.04em",
                              }}
                            >
                              {s.bookCount}
                            </span>
                          </CommandItem>
                        )
                      })}
                    </CommandGroup>
                  )}

                  {smartActive.length > 0 && (
                    <>
                      <CommandSeparator
                        style={{ background: "var(--color-rule-soft)" }}
                      />
                      <CommandGroup heading="Auto-matched">
                        {smartActive.map((s) => (
                          <CommandItem
                            key={s.slug}
                            value={`${s.name} ${s.slug} smart`}
                            disabled
                            className="rounded-sm opacity-80"
                            style={{ padding: "8px 10px" }}
                          >
                            <span
                              aria-hidden
                              style={{
                                width: 14,
                                height: 14,
                                display: "inline-flex",
                                alignItems: "center",
                                justifyContent: "center",
                                color: "var(--color-accent-ink)",
                                flexShrink: 0,
                              }}
                            >
                              <Icon name="sparkle" size={11} />
                            </span>
                            <span
                              style={{
                                fontSize: 13,
                                color: "var(--color-ink-2)",
                                flex: 1,
                              }}
                            >
                              {s.name}
                            </span>
                            <span
                              className="t-micro"
                              style={{ color: "var(--color-ink-3)" }}
                            >
                              by rule
                            </span>
                          </CommandItem>
                        ))}
                      </CommandGroup>
                    </>
                  )}
                </CommandList>
                <div
                  style={{
                    display: "flex",
                    justifyContent: "space-between",
                    alignItems: "center",
                    padding: "6px 10px",
                    borderTop: "1px solid var(--color-rule-soft)",
                  }}
                >
                  <span
                    className="t-micro"
                    style={{ color: "var(--color-ink-3)" }}
                  >
                    {pending ? "Saving…" : "Click to toggle"}
                  </span>
                  <span
                    className="mono"
                    style={{
                      fontSize: 10,
                      color: "var(--color-ink-3)",
                      letterSpacing: "0.04em",
                    }}
                  >
                    Esc
                  </span>
                </div>
              </Command>
            </PopoverContent>
          </Popover>
        </div>
      )}
    </div>
  )
}
