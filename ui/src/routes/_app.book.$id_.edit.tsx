// ui/src/routes/_app.book.$id_.edit.tsx
import { useEffect, useMemo, useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import {
  createFileRoute,
  Link,
  useBlocker,
  useNavigate,
} from "@tanstack/react-router"
import type { ReactNode } from "react"

import type { LockField } from "@/api/books"
import type { FormState } from "@/lib/book-form"
import { bookQuery, bookQueryKey, patchBook } from "@/api/books"
import { useApiMutation } from "@/api/mutation"
import { useApiQuery } from "@/api/query"
import {
  blankForm,
  bookToForm,
  dirtyFieldCount,
  formToPatch,
} from "@/lib/book-form"
import { validateField } from "@/lib/metadata-validators"
import { Icon } from "@/components/Icon"
import { Notice } from "@/components/Notice"
import { ChipEditor } from "@/components/metadata/ChipEditor"
import { CoverPanel } from "@/components/metadata/CoverPanel"
import { FieldLockButton } from "@/components/metadata/FieldLockButton"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"

export const Route = createFileRoute("/_app/book/$id_/edit")({
  component: MetadataEditor,
})

const AGE_RATINGS = ["All ages", "8+", "12+", "16+", "18+"] as const
const CONTENT_RATINGS = ["G", "PG", "PG-13", "R", "NC-17"] as const

const TAG_SUGGESTIONS = [
  "Fiction",
  "Literary",
  "Essays",
  "Poetry",
  "Nonfiction",
  "History",
  "Philosophy",
  "Memoir",
] as const

function MetadataEditor() {
  const { id } = Route.useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const book = useApiQuery(bookQuery(id))

  const [form, setForm] = useState<FormState>(blankForm())
  const baselineRef = useRef<FormState>(blankForm())
  const [errors, setErrors] = useState<
    Partial<Record<keyof FormState, string>>
  >({})
  const [hydrated, setHydrated] = useState(false)
  // Set during save's onSuccess so the route blocker doesn't fire on
  // the redirect — the closure captures the pre-render dirty state and
  // would prompt confirm() before the form re-renders clean.
  const savingRef = useRef(false)

  // Sync form once when the book loads. Subsequent refetches don't
  // overwrite in-flight edits. This is a prop→state sync, not a
  // cascading render — the intended use of setState-in-effect.
  useEffect(() => {
    if (book.data && !hydrated) {
      const seeded = bookToForm(book.data)
      // Deliberate: setState inside an effect, syncing React state from an
      // external source. Was suppressed via react-hooks/set-state-in-effect;
      // Biome has no equivalent rule yet, so there is nothing to suppress.
      setForm(seeded)
      baselineRef.current = seeded
      setHydrated(true)
    }
  }, [book.data, hydrated])

  const dirtyCount = useMemo(
    // Deliberate ref access. Was suppressed via react-hooks/refs; Biome has
    // no equivalent rule yet, so there is nothing to suppress.
    () => dirtyFieldCount(form, baselineRef.current),
    [form]
  )
  const isDirty = dirtyCount > 0

  // beforeUnload guard for tab close / hard refresh. Router-level
  // guard below handles in-app navigation.
  useEffect(() => {
    if (!isDirty) return
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault()
      e.returnValue = ""
    }
    window.addEventListener("beforeunload", handler)
    return () => window.removeEventListener("beforeunload", handler)
  }, [isDirty])

  // TanStack Router blocker — surfaces a confirm() before the route
  // changes when the form is dirty. The savingRef bypass lets the
  // post-save navigate proceed without prompting; setForm/baseline
  // updates won't have flushed by the time navigate fires, so a plain
  // isDirty check still reads dirty.
  useBlocker({
    shouldBlockFn: () => {
      if (savingRef.current) return false
      if (!isDirty) return false
      return !window.confirm("You have unsaved changes. Leave without saving?")
    },
  })

  // Inline: a refused save is reported in the footer beside the button
  // that refused, which turns into "Retry save". The user is still in the
  // form with the edit in hand; a toast would repeat that sentence and
  // then take it away before they had fixed anything.
  const saveMut = useApiMutation(patchBook, {
    reportErrors: "inline",
    onSuccess: (updated) => {
      queryClient.setQueryData(bookQueryKey(id), updated)
      baselineRef.current = bookToForm(updated)
      setForm(bookToForm(updated))
      savingRef.current = true
      void navigate({ to: "/book/$id", params: { id } })
    },
  })

  const set = <TKey extends keyof FormState>(k: TKey, v: FormState[TKey]) => {
    setForm((f) => ({ ...f, [k]: v }))
    // Clear any stale validation error so the red message disappears as
    // soon as the user starts correcting the value (re-validation
    // still runs on blur).
    if (errors[k]) setErrors((e) => ({ ...e, [k]: undefined }))
  }

  const onBlurValidate = (k: keyof FormState) => () => {
    const value = form[k]
    if (typeof value !== "string") return
    const err = validateField(k, value)
    setErrors((e) => ({ ...e, [k]: err ?? undefined }))
  }

  const validateAll = (): boolean => {
    const next: Partial<Record<keyof FormState, string>> = {}
    for (const k of ["isbn13", "isbn10", "year", "pages"] as const) {
      const err = validateField(k, form[k])
      if (err) next[k] = err
    }
    setErrors(next)
    return Object.keys(next).length === 0
  }

  const onSave = () => {
    if (!validateAll()) return
    saveMut.mutate({ id, patch: formToPatch(form) })
  }

  if (book.isLoading) {
    return <EditPageSkeleton />
  }
  if (book.isError || !book.data) {
    return (
      <div className="p-10">
        <p className="t-small">Book not found.</p>
      </div>
    )
  }

  const b = book.data
  const error = saveMut.error

  return (
    <div className="fade-in">
      {/* Sticky top header — minimal toolbar */}
      <header className="sticky top-0 z-20 border-b border-(--color-rule-soft) bg-(--color-paper-1)/92 backdrop-blur">
        <div className="mx-auto flex max-w-[1180px] items-center gap-3 px-8 py-3">
          <Button variant="ghost" size="sm" asChild>
            <Link to="/book/$id" params={{ id }}>
              <Icon name="arrow-left" size={14} /> Back to book
            </Link>
          </Button>
          <div className="grow" />
          <Button variant="outline" size="sm" asChild>
            <Link to="/book/$id/find" params={{ id }}>
              Find metadata online <Icon name="arrow-right" size={13} />
            </Link>
          </Button>
          <Button
            onClick={onSave}
            disabled={!isDirty || saveMut.isPending}
            size="sm"
          >
            {saveMut.isPending ? "Saving…" : "Save changes"}
          </Button>
        </div>
      </header>

      {/* Editorial title block */}
      <div className="border-b border-(--color-rule-soft) bg-(--color-paper-1)">
        <div className="mx-auto max-w-[1180px] px-8 pt-12 pb-10">
          <p className="t-micro mb-3">Editing metadata</p>
          <h1
            className="t-display max-w-[820px] text-(--color-ink-1)"
            style={{ fontWeight: 500 }}
          >
            {b.title}
          </h1>
          {b.author && (
            <p className="t-body mt-3 text-(--color-ink-2) italic">
              by {b.author}
            </p>
          )}
        </div>
      </div>

      {/* Two-column working area */}
      <div className="mx-auto max-w-[1180px] px-8 pt-12 pb-32">
        <div className="grid gap-12 lg:grid-cols-[240px_minmax(0,1fr)] lg:gap-16">
          <aside className="lg:sticky lg:top-[76px] lg:self-start">
            <CoverPanel book={b} />
          </aside>

          <div className="max-w-[640px] min-w-0">
            <FormSection title="Title & author">
              <FieldRow
                label="Title"
                error={errors.title}
                lock={{
                  bookId: b.id,
                  field: "title",
                  locked: !!b.locks?.title,
                }}
              >
                <Input
                  value={form.title}
                  onChange={(e) => set("title", e.target.value)}
                  disabled={!!b.locks?.title}
                  className={b.locks?.title ? "opacity-60" : ""}
                />
              </FieldRow>
              <FieldRow
                label="Subtitle"
                lock={{
                  bookId: b.id,
                  field: "subtitle",
                  locked: !!b.locks?.subtitle,
                }}
              >
                <Input
                  value={form.subtitle}
                  onChange={(e) => set("subtitle", e.target.value)}
                  placeholder="—"
                  disabled={!!b.locks?.subtitle}
                  className={b.locks?.subtitle ? "opacity-60" : ""}
                />
              </FieldRow>
              <FieldRow
                label="Author"
                lock={{
                  bookId: b.id,
                  field: "author",
                  locked: !!b.locks?.author,
                }}
              >
                <Input
                  value={form.author}
                  onChange={(e) => set("author", e.target.value)}
                  disabled={!!b.locks?.author}
                  className={b.locks?.author ? "opacity-60" : ""}
                />
              </FieldRow>
            </FormSection>

            <FormSection title="Description">
              <FieldRow
                label="Description"
                lock={{
                  bookId: b.id,
                  field: "description",
                  locked: !!b.locks?.description,
                }}
              >
                <Textarea
                  rows={8}
                  value={form.description}
                  onChange={(e) => set("description", e.target.value)}
                  disabled={!!b.locks?.description}
                  className={
                    "min-h-[220px] resize-y font-serif text-[15px] leading-relaxed " +
                    (b.locks?.description ? "opacity-60" : "")
                  }
                />
              </FieldRow>
            </FormSection>

            <FormSection title="Publication">
              <FieldRow
                label="Publisher"
                lock={{
                  bookId: b.id,
                  field: "publisher",
                  locked: !!b.locks?.publisher,
                }}
              >
                <Input
                  value={form.publisher}
                  onChange={(e) => set("publisher", e.target.value)}
                  disabled={!!b.locks?.publisher}
                  className={b.locks?.publisher ? "opacity-60" : ""}
                />
              </FieldRow>
              <div className="grid gap-4 md:grid-cols-2">
                <FieldRow
                  label="Publish date"
                  lock={{
                    bookId: b.id,
                    field: "publishDate",
                    locked: !!b.locks?.publishDate,
                  }}
                >
                  <Input
                    type="date"
                    value={form.publishDate}
                    onChange={(e) => set("publishDate", e.target.value)}
                    disabled={!!b.locks?.publishDate}
                    className={b.locks?.publishDate ? "opacity-60" : ""}
                  />
                </FieldRow>
                <FieldRow label="Year" error={errors.year}>
                  <Input
                    inputMode="numeric"
                    value={form.year}
                    onChange={(e) => set("year", e.target.value)}
                    onBlur={onBlurValidate("year")}
                    aria-invalid={!!errors.year}
                  />
                </FieldRow>
                <FieldRow
                  label="Language"
                  lock={{
                    bookId: b.id,
                    field: "language",
                    locked: !!b.locks?.language,
                  }}
                >
                  <Input
                    value={form.language}
                    onChange={(e) => set("language", e.target.value)}
                    placeholder="en"
                    className={
                      `mono ${b.locks?.language ? "opacity-60" : ""}`
                    }
                    disabled={!!b.locks?.language}
                  />
                </FieldRow>
                <FieldRow
                  label="Pages"
                  error={errors.pages}
                  lock={{
                    bookId: b.id,
                    field: "pages",
                    locked: !!b.locks?.pages,
                  }}
                >
                  <Input
                    inputMode="numeric"
                    value={form.pages}
                    onChange={(e) => set("pages", e.target.value)}
                    onBlur={onBlurValidate("pages")}
                    placeholder="—"
                    aria-invalid={!!errors.pages}
                    disabled={!!b.locks?.pages}
                    className={b.locks?.pages ? "opacity-60" : ""}
                  />
                </FieldRow>
              </div>
            </FormSection>

            <FormSection title="Identifiers">
              <div className="grid gap-4 md:grid-cols-2">
                <FieldRow
                  label="ISBN-13"
                  error={errors.isbn13}
                  lock={{
                    bookId: b.id,
                    field: "isbn",
                    locked: !!b.locks?.isbn,
                  }}
                >
                  <Input
                    value={form.isbn13}
                    onChange={(e) => set("isbn13", e.target.value)}
                    onBlur={onBlurValidate("isbn13")}
                    aria-invalid={!!errors.isbn13}
                    disabled={!!b.locks?.isbn}
                    className={`mono ${b.locks?.isbn ? "opacity-60" : ""}`}
                  />
                </FieldRow>
                <FieldRow
                  label="ISBN-10"
                  error={errors.isbn10}
                  lock={{
                    bookId: b.id,
                    field: "isbn10",
                    locked: !!b.locks?.isbn10,
                  }}
                >
                  <Input
                    value={form.isbn10}
                    onChange={(e) => set("isbn10", e.target.value)}
                    onBlur={onBlurValidate("isbn10")}
                    aria-invalid={!!errors.isbn10}
                    disabled={!!b.locks?.isbn10}
                    className={`mono ${b.locks?.isbn10 ? "opacity-60" : ""}`}
                  />
                </FieldRow>
              </div>
            </FormSection>

            <FormSection title="Series">
              <div className="grid grid-cols-1 gap-4 md:grid-cols-[2fr_1fr_1fr]">
                <FieldRow
                  label="Series name"
                  lock={{
                    bookId: b.id,
                    field: "series",
                    locked: !!b.locks?.series,
                  }}
                >
                  <Input
                    value={form.series}
                    onChange={(e) => set("series", e.target.value)}
                    placeholder="—"
                    disabled={!!b.locks?.series}
                    className={b.locks?.series ? "opacity-60" : ""}
                  />
                </FieldRow>
                <FieldRow label="Book #">
                  <Input
                    inputMode="numeric"
                    value={form.seriesNum}
                    onChange={(e) => set("seriesNum", e.target.value)}
                    placeholder="—"
                  />
                </FieldRow>
                <FieldRow label="Total">
                  <Input
                    inputMode="numeric"
                    value={form.seriesTotal}
                    onChange={(e) => set("seriesTotal", e.target.value)}
                    placeholder="—"
                  />
                </FieldRow>
              </div>
            </FormSection>

            <FormSection title="Categories & tags">
              <FieldRow
                label="Genres"
                lock={{
                  bookId: b.id,
                  field: "genres",
                  locked: !!b.locks?.genres,
                }}
              >
                <ChipEditor
                  value={form.genres}
                  onChange={(v) => set("genres", v)}
                  placeholder="Add a genre and press Enter"
                  disabled={!!b.locks?.genres}
                />
              </FieldRow>
              <FieldRow
                label="Moods"
                lock={{
                  bookId: b.id,
                  field: "moods",
                  locked: !!b.locks?.moods,
                }}
              >
                <ChipEditor
                  value={form.moods}
                  onChange={(v) => set("moods", v)}
                  placeholder="Hopeful, reflective…"
                  disabled={!!b.locks?.moods}
                />
              </FieldRow>
              <FieldRow
                label="Tags"
                lock={{ bookId: b.id, field: "tags", locked: !!b.locks?.tags }}
              >
                <ChipEditor
                  value={form.tags}
                  onChange={(v) => set("tags", v)}
                  placeholder="Add a tag and press Enter"
                  disabled={!!b.locks?.tags}
                  suggestions={TAG_SUGGESTIONS}
                />
              </FieldRow>
            </FormSection>

            <FormSection title="Ratings">
              <div className="grid gap-4 md:grid-cols-3">
                <FieldRow label="Age rating">
                  <Select
                    value={form.ageRating}
                    onValueChange={(v) =>
                      set("ageRating", v === "__none" ? "" : v)
                    }
                  >
                    <SelectTrigger>
                      <SelectValue placeholder="—" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="__none">—</SelectItem>
                      {AGE_RATINGS.map((r) => (
                        <SelectItem key={r} value={r}>
                          {r}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </FieldRow>
                <FieldRow label="Content rating">
                  <Select
                    value={form.contentRating}
                    onValueChange={(v) =>
                      set("contentRating", v === "__none" ? "" : v)
                    }
                  >
                    <SelectTrigger>
                      <SelectValue placeholder="—" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="__none">—</SelectItem>
                      {CONTENT_RATINGS.map((r) => (
                        <SelectItem key={r} value={r}>
                          {r}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </FieldRow>
                <FieldRow label="Public reviews">
                  <Select
                    value={
                      form.publicReviews === "" ? "__none" : form.publicReviews
                    }
                    onValueChange={(v) =>
                      set(
                        "publicReviews",
                        (v === "__none" ? "" : v) as FormState["publicReviews"]
                      )
                    }
                  >
                    <SelectTrigger>
                      <SelectValue placeholder="No value" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="__none">No value</SelectItem>
                      <SelectItem value="yes">Allowed</SelectItem>
                      <SelectItem value="no">Blocked</SelectItem>
                    </SelectContent>
                  </Select>
                </FieldRow>
              </div>
            </FormSection>
          </div>
        </div>
      </div>

      {/* Sticky bottom save bar */}
      <footer className="sticky bottom-0 z-20 border-t border-(--color-rule-soft) bg-(--color-paper-1)/92 backdrop-blur">
        <div className="mx-auto flex max-w-[1180px] items-center gap-3 px-8 py-3">
          <div className="flex items-center gap-2">
            <span
              aria-hidden
              className={
                "inline-block h-1.5 w-1.5 rounded-full " +
                (isDirty
                  ? "bg-(--color-editorial-accent)"
                  : "bg-(--color-ink-4)")
              }
            />
            <span className="t-small">
              {isDirty
                ? `${dirtyCount} unsaved change${dirtyCount === 1 ? "" : "s"}`
                : "No changes"}
            </span>
          </div>
          {error && <Notice>{error.message}</Notice>}
          <div className="grow" />
          <Button
            variant="ghost"
            size="sm"
            disabled={!isDirty || saveMut.isPending}
            onClick={() => {
              setForm(baselineRef.current)
              setErrors({})
            }}
          >
            Discard
          </Button>
          <Button
            onClick={onSave}
            disabled={!isDirty || saveMut.isPending}
            size="sm"
          >
            {saveMut.isPending
              ? "Saving…"
              : error
                ? "Retry save"
                : "Save changes"}
          </Button>
        </div>
      </footer>
    </div>
  )
}

function EditPageSkeleton() {
  return (
    <div className="mx-auto max-w-[760px] p-10 fade-in">
      <Skeleton className="mb-6 h-8 w-1/2" />
      <Skeleton className="mb-10 h-[240px] w-[160px]" />
      {[1, 2, 3, 4].map((i) => (
        <div key={i} className="mb-10">
          <Skeleton className="mb-4 h-4 w-32" />
          <Skeleton className="mb-3 h-9 w-full" />
          <Skeleton className="h-9 w-full" />
        </div>
      ))}
    </div>
  )
}

function FormSection({
  title,
  children,
}: {
  title: string
  children: ReactNode
}) {
  return (
    <section className="mb-10 border-b border-(--color-rule-soft) pb-8 last:border-b-0">
      <h2
        className="mb-5 font-serif text-[22px] text-(--color-ink-1)"
        style={{ fontWeight: 500 }}
      >
        {title}
      </h2>
      <div className="flex flex-col gap-4">{children}</div>
    </section>
  )
}

function FieldRow({
  label,
  children,
  lock,
  error,
}: {
  label: string
  children: ReactNode
  lock?: { bookId: string; field: LockField; locked: boolean }
  error?: string
}) {
  return (
    <div>
      <div className="t-label mb-1.5 flex items-center gap-2">
        <span>{label}</span>
        {lock && (
          <FieldLockButton
            bookId={lock.bookId}
            field={lock.field}
            locked={lock.locked}
          />
        )}
      </div>
      {children}
      {error && (
        <p className="t-small mt-1 text-(--color-accent-ink)" role="alert">
          {error}
        </p>
      )}
    </div>
  )
}
