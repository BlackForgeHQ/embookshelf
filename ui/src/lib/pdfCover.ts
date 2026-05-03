import * as pdfjsLib from "pdfjs-dist"
import type { PDFDocumentProxy } from "pdfjs-dist"

// Worker URL is computed once at module load — same shape as PdfReader.
// Vite resolves the URL at build time and emits the worker as a hashed
// asset, so this is safe to set even when the helper is imported by
// non-reader pages (e.g. BookDrop preview).
pdfjsLib.GlobalWorkerOptions.workerSrc = new URL(
  "pdfjs-dist/build/pdf.worker.min.mjs",
  import.meta.url
).toString()

export type RenderCoverOpts = {
  width?: number
  quality?: number
}

// renderPdfPageOneJpeg fetches a PDF and rasters its first page to a
// JPEG blob. Returns null when the document is unreadable or the
// canvas can't allocate. The caller is expected to upload the blob via
// putBookDropCover and discard it afterwards.
//
// When `signal` aborts, the in-flight pdfjs `loadingTask` is destroyed,
// which cancels both the network fetch and the worker-side parsing.
export async function renderPdfPageOneJpeg(
  url: string,
  opts?: RenderCoverOpts & { signal?: AbortSignal }
): Promise<Blob | null> {
  const targetWidth = opts?.width ?? 1200
  const quality = opts?.quality ?? 0.85
  const loadingTask = pdfjsLib.getDocument({ url, withCredentials: true })
  const signal = opts?.signal
  const onAbort = () => {
    void loadingTask.destroy()
  }
  signal?.addEventListener("abort", onAbort, { once: true })
  let doc: PDFDocumentProxy | null = null
  try {
    doc = await loadingTask.promise
    if (signal?.aborted) return null
    const page = await doc.getPage(1)
    const baseViewport = page.getViewport({ scale: 1 })
    const scale = targetWidth / baseViewport.width
    const viewport = page.getViewport({ scale })
    const canvas = document.createElement("canvas")
    canvas.width = Math.ceil(viewport.width)
    canvas.height = Math.ceil(viewport.height)
    const ctx = canvas.getContext("2d")
    if (!ctx) return null
    await page.render({ canvasContext: ctx, viewport, canvas }).promise
    if (signal?.aborted) return null
    return await new Promise<Blob | null>((resolve) =>
      canvas.toBlob((b) => resolve(b), "image/jpeg", quality)
    )
  } catch {
    return null
  } finally {
    signal?.removeEventListener("abort", onAbort)
    if (doc) {
      try {
        await doc.destroy()
      } catch {
        // best-effort
      }
    }
  }
}
