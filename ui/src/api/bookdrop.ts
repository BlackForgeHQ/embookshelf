import { api } from "./client"
import type { BookDetail } from "./books"

// Mirrors internal/handler/bookdrop.go bookdropDTO.
export type BookDropState =
  | "discovered"
  | "processing"
  | "ready"
  | "failed"
  | "imported"
  | "rejected"

export type BookDropItem = {
  id: string
  filename: string
  path: string
  fileSize: number
  format: string
  state: BookDropState
  progress: number
  errorMsg?: string
  title?: string
  author?: string
  description?: string
  language?: string
  hasCover: boolean
  coverMime?: string
  bookId?: string
  discoveredAt: string
  updatedAt: string
}

export async function fetchBookDrop(): Promise<Array<BookDropItem>> {
  const { items } = await api<{ items: Array<BookDropItem> }>(
    "/api/v1/bookdrop"
  )
  return items
}

export async function approveBookDrop(
  id: string,
  libraryId?: string
): Promise<BookDetail> {
  const { book } = await api<{ book: BookDetail }>(
    `/api/v1/bookdrop/${id}/approve`,
    {
      method: "POST",
      body: libraryId ? JSON.stringify({ libraryId }) : undefined,
    }
  )
  return book
}

export async function rejectBookDrop(id: string): Promise<void> {
  await api<void>(`/api/v1/bookdrop/${id}/reject`, { method: "POST" })
}

// clearProcessedBookDrop drops every imported/rejected row from the queue
// so "Recently processed" empties out. In-flight rows are left alone.
export async function clearProcessedBookDrop(): Promise<number> {
  const { cleared } = await api<{ cleared: number }>(
    "/api/v1/bookdrop/processed",
    { method: "DELETE" }
  )
  return cleared
}

export type BookDropUploadResult = {
  filename: string
  item?: BookDropItem
  error?: string
}

// Streams multipart upload progress via XHR so the drop zone can render a
// live percent bar. fetch() doesn't expose upload progress yet, so XHR is
// the right tool here.
export function uploadBookDrop(
  files: Array<File>,
  onProgress?: (loaded: number, total: number) => void
): Promise<Array<BookDropUploadResult>> {
  return new Promise((resolve, reject) => {
    const form = new FormData()
    for (const f of files) form.append("files", f, f.name)

    const xhr = new XMLHttpRequest()
    xhr.open("POST", "/api/v1/bookdrop/upload")
    xhr.withCredentials = true
    xhr.responseType = "json"

    if (onProgress) {
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable) onProgress(e.loaded, e.total)
      }
    }

    xhr.onerror = () => reject({ status: 0, message: "network error" })
    xhr.onload = () => {
      const body = xhr.response as {
        results?: Array<BookDropUploadResult>
        error?: string
      } | null
      // 201 = all good, 400 = every file failed but the request itself is
      // valid and we still want to surface the per-file errors.
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(body?.results ?? [])
        return
      }
      if (xhr.status === 400 && body?.results) {
        resolve(body.results)
        return
      }
      reject({
        status: xhr.status,
        message: body?.error ?? xhr.statusText ?? "upload failed",
      })
    }

    xhr.send(form)
  })
}

export const bookdropQueryKey = ["bookdrop"] as const

export const bookdropCoverUrl = (id: string) => `/api/v1/bookdrop/${id}/cover`
