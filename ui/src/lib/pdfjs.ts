// pdf.js, loaded on first use. Its module body touches DOMMatrix and
// friends, which the static prerender (Node) does not have, so nothing
// may import it eagerly.
//
// The worker URL is computed with `new URL(..., import.meta.url)` so the
// bundler emits the worker as a hashed asset and returns its resolved URL
// to the runtime.

let loading: Promise<typeof import("pdfjs-dist")> | null = null

export function loadPdfjs(): Promise<typeof import("pdfjs-dist")> {
  loading ??= import("pdfjs-dist").then((pdfjsLib) => {
    pdfjsLib.GlobalWorkerOptions.workerSrc = new URL(
      "pdfjs-dist/build/pdf.worker.min.mjs",
      import.meta.url
    ).toString()
    return pdfjsLib
  })
  return loading
}
