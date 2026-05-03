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
// Admin-only — see ADR-0014.
export async function clearProcessedBookDrop(): Promise<number> {
  const { cleared } = await api<{ cleared: number }>(
    "/api/v1/settings/bookdrop/processed",
    { method: "DELETE" }
  )
  return cleared
}

export type BookDropFilesPreview = {
  count: number
  bytes: number
  skippedInFlight: number
}

// previewBookDropFiles returns a snapshot of what wipeBookDropFiles would
// remove. Admin-only.
export async function previewBookDropFiles(): Promise<BookDropFilesPreview> {
  return api<BookDropFilesPreview>("/api/v1/settings/bookdrop/files")
}

export type BookDropWipeResult = {
  deleted: number
  freed: number
  skippedInFlight: number
  orphanRows: number
}

// wipeBookDropFiles recursively removes every file under BOOKDROP_PATH,
// skipping files referenced by 'processing' rows, and drops orphan
// queue rows. Admin-only — cross-user blast radius.
export async function wipeBookDropFiles(): Promise<BookDropWipeResult> {
  return api<BookDropWipeResult>("/api/v1/settings/bookdrop/files", {
    method: "DELETE",
  })
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
    for (const f of files) {
      const safeName = f.name
        .normalize("NFKD")
        .replace(/[^\x20-\x7E]/g, "_")
        .replace(/["\r\n\\]/g, "_")
      form.append("files", f, safeName)
    }

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
        message: body?.error || xhr.statusText || "upload failed",
      })
    }

    xhr.send(form)
  })
}

export const bookdropQueryKey = ["bookdrop"] as const

export const bookdropCoverUrl = (id: string) => `/api/v1/bookdrop/${id}/cover`

// bookdropFileUrl returns the URL serving the staged file bytes for a
// BookDrop item before approval. Used by the preview pane to render a
// client-side cover (e.g. PDF page-1) when the extractor didn't find one.
export const bookdropFileUrl = (id: string) =>
  `/api/v1/bookdrop/${encodeURIComponent(id)}/file`

// putBookDropCover uploads a client-rendered cover image (PNG/JPEG raw
// bytes, <= 5 MB) for a BookDrop item that doesn't yet have a cover.
// 409 (already-present) is treated as success — caller doesn't need to
// distinguish "we wrote it" from "someone else already did".
export async function putBookDropCover(
  id: string,
  blob: Blob
): Promise<void> {
  const res = await fetch(`/api/v1/bookdrop/${encodeURIComponent(id)}/cover`, {
    method: "PUT",
    body: blob,
    headers: { "content-type": blob.type || "image/jpeg" },
    credentials: "include",
  })
  if (res.status === 409) return
  if (!res.ok) {
    throw new Error(`putBookDropCover ${res.status}`)
  }
}
