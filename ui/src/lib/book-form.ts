// ui/src/lib/book-form.ts
import type { BookDetail, BookPatch } from "@/api/books"

// FormState mirrors the editor inputs as strings (native form shape);
// numeric fields parse back to numbers in formToPatch. publicReviews is
// tri-state — '' means "No Value" (null), 'yes' / 'no' map to true/false.
export type FormState = {
  title: string
  subtitle: string
  author: string
  description: string
  year: string
  publishDate: string
  language: string
  publisher: string
  isbn13: string
  isbn10: string
  series: string
  seriesNum: string
  seriesTotal: string
  genres: Array<string>
  moods: Array<string>
  tags: Array<string>
  ageRating: string
  contentRating: string
  pages: string
  publicReviews: "" | "yes" | "no"
}

export function blankForm(): FormState {
  return {
    title: "",
    subtitle: "",
    author: "",
    description: "",
    year: "",
    publishDate: "",
    language: "",
    publisher: "",
    isbn13: "",
    isbn10: "",
    series: "",
    seriesNum: "",
    seriesTotal: "",
    genres: [],
    moods: [],
    tags: [],
    ageRating: "",
    contentRating: "",
    pages: "",
    publicReviews: "",
  }
}

export function bookToForm(b: BookDetail): FormState {
  const pr = b.publicReviews
  return {
    title: b.title,
    subtitle: b.subtitle ?? "",
    author: b.author,
    description: b.description ?? "",
    year: b.year ? String(b.year) : "",
    publishDate: b.publishDate ?? "",
    language: b.language ?? "",
    publisher: b.publisher ?? "",
    isbn13: b.isbn ?? "",
    isbn10: b.isbn10 ?? "",
    series: b.series ?? "",
    seriesNum: b.seriesNum ? String(b.seriesNum) : "",
    seriesTotal: b.seriesTotal ? String(b.seriesTotal) : "",
    genres: [...b.genres],
    moods: [...b.moods],
    tags: [...b.tags],
    ageRating: b.ageRating ?? "",
    contentRating: b.contentRating ?? "",
    pages: b.pages ? String(b.pages) : "",
    publicReviews: pr === true ? "yes" : pr === false ? "no" : "",
  }
}

export function splitCsv(raw: string): Array<string> {
  return raw
    .split(",")
    .map((t) => t.trim())
    .filter(Boolean)
}

export function formToPatch(form: FormState): BookPatch {
  const patch: BookPatch = {
    title: form.title.trim(),
    subtitle: form.subtitle.trim(),
    author: form.author.trim(),
    description: form.description,
    language: form.language.trim(),
    publisher: form.publisher.trim(),
    isbn: form.isbn13.trim(),
    isbn10: form.isbn10.trim(),
    series: form.series.trim(),
    ageRating: form.ageRating.trim(),
    contentRating: form.contentRating.trim(),
    publishDate: form.publishDate.trim(),
  }
  const year = Number.parseInt(form.year, 10)
  patch.year = Number.isFinite(year) ? year : 0
  const seriesNum = Number.parseInt(form.seriesNum, 10)
  patch.seriesNum = Number.isFinite(seriesNum) ? seriesNum : 0
  const seriesTotal = Number.parseInt(form.seriesTotal, 10)
  patch.seriesTotal = Number.isFinite(seriesTotal) ? seriesTotal : 0
  const pages = Number.parseInt(form.pages, 10)
  patch.pages = Number.isFinite(pages) ? pages : 0
  patch.genres = form.genres
  patch.moods = form.moods
  patch.tags = form.tags
  if (form.publicReviews === "yes") {
    patch.publicReviews = true
  } else if (form.publicReviews === "no") {
    patch.publicReviews = false
  } else {
    patch.publicReviewsClear = true
  }
  return patch
}

// dirtyFieldCount counts which top-level FormState keys differ from the
// reference baseline. Arrays compare by joined-string identity (case- and
// order-sensitive — order represents the user's preferred display order).
export function dirtyFieldCount(form: FormState, baseline: FormState): number {
  let n = 0
  for (const k of Object.keys(form) as Array<keyof FormState>) {
    const a = form[k]
    const b = baseline[k]
    if (Array.isArray(a) && Array.isArray(b)) {
      if (a.length !== b.length || a.some((v, i) => v !== b[i])) n++
    } else if (a !== b) {
      n++
    }
  }
  return n
}
