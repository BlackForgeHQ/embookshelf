// ui/src/routes/_app.book.$id_.find.tsx
import { useEffect, useMemo, useRef, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  Link,
  createFileRoute,
  useNavigate,
} from "@tanstack/react-router"
import { toast } from "sonner"

import type { ApiError } from "@/api/client"
import type { EnrichMatch } from "@/api/enrich"
import type { ProviderState } from "@/components/metadata/ProviderStatusChips"
import { applyCoverFromUrl, streamEnrichment } from "@/api/enrich"
import { bookQueryKey, fetchBook } from "@/api/books"
import { Icon } from "@/components/Icon"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { CompareApplyPanel } from "@/components/metadata/CompareApplyPanel"
import { MatchCard } from "@/components/metadata/MatchCard"
import { ProviderStatusChips } from "@/components/metadata/ProviderStatusChips"

export const Route = createFileRoute("/_app/book/$id_/find")({
  component: FindMetadata,
})

// Default provider IDs the UI surfaces as chips even before the server
// reports back. Must stay in sync with PROVIDER_LABELS keys in
// ui/src/api/enrich.ts. Disabled providers fade out as soon as `done`
// arrives and they're missing from the providers list.
const KNOWN_PROVIDER_IDS = [
  "google_books",
  "open_library",
  "hardcover",
  "goodreads",
  "amazon",
  "duckduckgo",
] as const

function FindMetadata() {
  const { id } = Route.useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const book = useQuery({
    queryKey: bookQueryKey(id),
    queryFn: () => fetchBook(id),
  })

  const [searchTitle, setSearchTitle] = useState("")
  const [searchAuthor, setSearchAuthor] = useState("")
  const [searchIsbn, setSearchIsbn] = useState("")
  const [hydrated, setHydrated] = useState(false)

  // Seed search inputs from book once on first load. This is a prop→state
  // sync on first hydration — the intended use of setState-in-effect.
  useEffect(() => {
    if (book.data && !hydrated) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setSearchTitle(book.data.title)
      setSearchAuthor(book.data.author)
      setSearchIsbn(book.data.isbn ?? "")
      setHydrated(true)
    }
  }, [book.data, hydrated])

  const [matches, setMatches] = useState<Array<EnrichMatch>>([])
  const [streaming, setStreaming] = useState(false)
  const [providerStates, setProviderStates] = useState<Array<ProviderState>>(
    KNOWN_PROVIDER_IDS.map((p) => ({ id: p, status: "pending" }))
  )
  const [tab, setTab] = useState<"matches" | "covers">("matches")
  const [selected, setSelected] = useState<EnrichMatch | null>(null)
  const cancelRef = useRef<() => void>(undefined)
  const autoStartedRef = useRef(false)

  // Cancel-on-unmount preserves the existing SSE wiring contract.
  useEffect(() => () => cancelRef.current?.(), [])

  const startSearch = () => {
    cancelRef.current?.()
    setMatches([])
    setSelected(null)
    setProviderStates(
      KNOWN_PROVIDER_IDS.map((p) => ({ id: p, status: "active" }))
    )
    setStreaming(true)
    const cancel = streamEnrichment(
      id,
      { title: searchTitle, author: searchAuthor, isbn: searchIsbn },
      (ev) => {
        if (ev.type === "match") {
          setMatches((prev) => {
            if (
              prev.some(
                (m) =>
                  m.source === ev.match.source && m.sourceId === ev.match.sourceId
              )
            ) {
              return prev
            }
            const next = [...prev, ev.match]
            next.sort((a, b) => b.confidence - a.confidence)
            return next
          })
          setProviderStates((prev) =>
            prev.map((p) =>
              p.id === ev.match.source ? { ...p, status: "active" } : p
            )
          )
        } else if (ev.type === "provider-error") {
          setProviderStates((prev) =>
            prev.map((p) =>
              p.id === ev.provider ? { ...p, status: "error", error: ev.error } : p
            )
          )
        } else {
          // ev.type === "done"
          setStreaming(false)
          const enabled = new Set(ev.providers)
          setProviderStates((prev) =>
            prev.map((p) => {
              if (!enabled.has(p.id)) return { ...p, status: "disabled" }
              if (p.status === "error") return p
              return { ...p, status: "done" }
            })
          )
        }
      }
    )
    cancelRef.current = cancel
  }

  // Auto-start the first search once the book and inputs hydrate.
  // The autoStartedRef latch prevents re-firing if hydrated ever flips
  // back-and-forth (it doesn't, but the ref makes the intent explicit).
  useEffect(() => {
    if (hydrated && !autoStartedRef.current) {
      autoStartedRef.current = true
      startSearch()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hydrated])

  const coverMut = useMutation({
    mutationFn: (url: string) => applyCoverFromUrl(id, url),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: bookQueryKey(id) })
      queryClient.invalidateQueries({ queryKey: ["books"] })
      toast.success("Cover updated.")
    },
    onError: (err) =>
      toast.error((err as unknown as ApiError).message || "Cover import failed."),
  })

  const uniqueCovers = useMemo(() => {
    const seen = new Set<string>()
    const out: Array<{ url: string; source: string; title: string }> = []
    for (const m of matches) {
      if (m.coverUrl && !seen.has(m.coverUrl)) {
        seen.add(m.coverUrl)
        out.push({ url: m.coverUrl, source: m.source, title: m.title })
      }
    }
    return out
  }, [matches])

  if (book.isLoading) {
    return <FindPageSkeleton />
  }
  if (book.isError || !book.data) {
    return (
      <div className="p-10">
        <p className="t-small">Book not found.</p>
      </div>
    )
  }

  const b = book.data
  const errored = providerStates.filter((p) => p.status === "error")

  return (
    <div className="fade-in flex flex-col">
      <header className="border-b border-(--color-rule-soft) px-8 py-3 flex items-center gap-3">
        <Button variant="ghost" size="sm" asChild>
          <Link to="/book/$id/edit" params={{ id }}>
            <Icon name="arrow-left" size={14} /> Back to edit
          </Link>
        </Button>
        <div>
          <h1 className="font-serif text-[20px] leading-tight" style={{ fontWeight: 500 }}>
            Find metadata online
          </h1>
          <p className="t-small italic">
            for <em>{b.title}</em> — by {b.author}
          </p>
        </div>
        <div className="grow" />
        <span className="t-small">
          {streaming
            ? "Searching providers…"
            : `${matches.length} result${matches.length === 1 ? "" : "s"} · done`}
        </span>
        <Button
          size="sm"
          variant="outline"
          onClick={() => void navigate({ to: "/book/$id", params: { id } })}
        >
          Done
        </Button>
      </header>

      <div className="grid xl:grid-cols-[280px_minmax(0,1fr)_320px] lg:grid-cols-[280px_minmax(0,1fr)] grid-cols-1">
        {/* Left rail — search refinement */}
        <aside className="lg:sticky lg:top-0 lg:self-start lg:h-screen border-r border-(--color-rule-soft) p-5 flex flex-col gap-4">
          <div>
            <div className="t-label mb-1.5">Title</div>
            <Input
              value={searchTitle}
              onChange={(e) => setSearchTitle(e.target.value)}
            />
          </div>
          <div>
            <div className="t-label mb-1.5">Author</div>
            <Input
              value={searchAuthor}
              onChange={(e) => setSearchAuthor(e.target.value)}
            />
          </div>
          <div>
            <div className="t-label mb-1.5">ISBN</div>
            <Input
              value={searchIsbn}
              onChange={(e) => setSearchIsbn(e.target.value)}
              className="mono"
            />
          </div>
          <div>
            <div className="t-label mb-2">Providers</div>
            <ProviderStatusChips providers={providerStates} />
          </div>
          <Button onClick={startSearch} disabled={streaming}>
            <Icon name="refresh" size={13} />{" "}
            {streaming ? "Searching…" : "Search again"}
          </Button>
        </aside>

        {/* Center — results */}
        <main className="p-6 min-w-0">
          <Tabs value={tab} onValueChange={(v) => setTab(v as "matches" | "covers")}>
            <TabsList variant="line" className="h-9 w-full justify-start gap-4 border-b border-(--color-rule-soft) px-0 mb-5">
              <TabsTrigger value="matches" className="flex-none px-3">
                Matches{matches.length > 0 && ` (${matches.length})`}
              </TabsTrigger>
              <TabsTrigger value="covers" className="flex-none px-3">
                Covers{uniqueCovers.length > 0 && ` (${uniqueCovers.length})`}
              </TabsTrigger>
            </TabsList>

            <TabsContent value="matches">
              {errored.length > 0 && (
                <div className="mb-4 flex flex-wrap gap-2">
                  {errored.map((p) => (
                    <span
                      key={p.id}
                      className="inline-flex items-center gap-2 rounded-[3px] border border-(--color-accent-soft) bg-(--color-accent-soft) px-2 py-1 text-[12px] text-(--color-accent-ink)"
                    >
                      {p.id}: {p.error}
                      <button
                        type="button"
                        className="underline-offset-2 hover:underline"
                        onClick={startSearch}
                        disabled={streaming}
                      >
                        retry
                      </button>
                    </span>
                  ))}
                </div>
              )}

              {matches.length === 0 && !streaming ? (
                <EmptyMatches />
              ) : (
                <div className="grid gap-4 lg:grid-cols-2">
                  {matches.map((m) => (
                    <MatchCard
                      key={`${m.source}:${m.sourceId}`}
                      match={m}
                      onCompare={() => setSelected(m)}
                      onUseCover={() => coverMut.mutate(m.coverUrl ?? "")}
                      busy={coverMut.isPending}
                    />
                  ))}
                  {streaming && (
                    <>
                      <Skeleton className="h-[220px] w-full" />
                      <Skeleton className="h-[220px] w-full" />
                    </>
                  )}
                </div>
              )}
            </TabsContent>

            <TabsContent value="covers">
              {uniqueCovers.length === 0 ? (
                <p className="t-small italic">
                  Covers from providers will appear here once results stream in.
                </p>
              ) : (
                <div
                  className="grid gap-5"
                  style={{ gridTemplateColumns: "repeat(auto-fill, minmax(160px, 1fr))" }}
                >
                  {uniqueCovers.map((c) => (
                    <button
                      key={c.url}
                      type="button"
                      onClick={() => coverMut.mutate(c.url)}
                      disabled={coverMut.isPending}
                      className="flex flex-col items-center gap-1.5 group"
                    >
                      <img
                        src={c.url}
                        alt=""
                        loading="lazy"
                        className="h-[240px] w-[160px] object-cover bg-(--color-paper-2) group-hover:ring-2 group-hover:ring-(--color-accent-ink)"
                      />
                      <span className="t-micro text-center">{c.source}</span>
                    </button>
                  ))}
                </div>
              )}
            </TabsContent>
          </Tabs>
        </main>

        {/* Right rail — Compare & apply */}
        <div className="hidden xl:block">
          {selected && (
            <CompareApplyPanel
              book={b}
              match={selected}
              onClose={() => setSelected(null)}
              onApplied={() => setSelected(null)}
            />
          )}
        </div>
      </div>
    </div>
  )
}

function EmptyMatches() {
  return (
    <div className="border border-dashed border-(--color-rule-soft) rounded-[3px] p-10 text-center">
      <Icon name="search" size={20} />
      <h3 className="font-serif mt-3 text-[18px]" style={{ fontWeight: 500 }}>
        No matches found
      </h3>
      <p className="t-small mt-2 max-w-md mx-auto">
        Try adjusting the title, author, or ISBN, or{" "}
        <Link
          to="/settings"
          className="text-(--color-accent-ink) underline-offset-2 hover:underline"
        >
          enable more providers in Settings
        </Link>
        .
      </p>
    </div>
  )
}

function FindPageSkeleton() {
  return (
    <div className="grid xl:grid-cols-[280px_minmax(0,1fr)_320px] lg:grid-cols-[280px_minmax(0,1fr)] gap-0 fade-in">
      <aside className="border-r border-(--color-rule-soft) p-5 flex flex-col gap-4">
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-9 w-full" />
      </aside>
      <main className="p-6 grid grid-cols-2 gap-4">
        <Skeleton className="h-[220px] w-full" />
        <Skeleton className="h-[220px] w-full" />
        <Skeleton className="h-[220px] w-full" />
        <Skeleton className="h-[220px] w-full" />
      </main>
    </div>
  )
}
