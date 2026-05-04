# PDF cover rendered in the browser at BookDrop preview

The PDF processor returns no cover bytes — server-side rasterisation needs a PDF rendering library, and every viable Go option violates a load-bearing repo constraint. We push cover rasterisation into the browser instead: when a user opens the BookDrop preview for a PDF, the existing pdfjs-based reader renders page 1, encodes JPEG q85 at 1200 px wide, and `PUT`s the bytes to `/api/v1/bookdrop/:id/cover`. The server treats the upload as the canonical pre-approval cover; approve promotes it through `coverstore` like any other format.

## Status

accepted (2026-05-03)

## Considered options

- **`go-fitz` (CGO MuPDF bindings).** Best fidelity, but introduces CGO. Repo deliberately runs CGO-free (modernc SQLite chosen for that exact reason); enabling CGO breaks `make build` cross-compile and the distroless `nonroot` image.
- **Exec `mutool draw` / `pdftoppm` from the Go process.** No CGO, but adds a runtime binary dependency. Dents the single-binary distribution and requires a non-distroless image variant.
- **`unidoc/unipdf` pure-Go.** Rasterisation works, but the licence is AGPL or commercial — incompatible with a self-host project that doesn't want viral copyleft on the server binary.
- **`pdfcpu` rasterise.** Rasterisation is not a first-class concern; output is fragile on real-world PDFs.
- **Defer cover entirely.** Status quo. Leaves PDFs without cover bytes for OPDS / Kavita / KOReader / Kobo OPDS clients that don't run pdfjs.

We picked the browser-render path because it preserves the no-CGO, single-binary, licence-clean invariants while still producing a real cover that flows through `coverstore` (SHA-256 dedup) and reaches non-pdfjs OPDS clients on first sync.

## Consequences

- The browser is trusted to produce the cover bytes. The server validates the PNG/JPEG magic and caps the body at 5 MB, but does not verify the bytes truly came from page 1 of the uploaded PDF. Acceptable: the BookDrop reviewer is an authenticated user with admin-or-uploader scope on the item, and the cover is reviewable in the same UI before approve.
- The endpoint is idempotent-on-absence: first call wins, second call returns 409. A reviewer who wants to replace the auto-rendered cover uses the existing post-approve cover-edit flow, not a re-PUT.
- PDFs imported before this change land without a cover. A small admin-side backfill ("regenerate covers") is non-blocking future work; it can either reuse the same client-render endpoint against the imported book or, later, ship a server-side rasteriser as an opt-in build tag.
- The PDF extractor itself remains pure-regex and CGO-free. No PDF library dependency is introduced anywhere in the Go module graph.

## Companion artifacts

- `internal/fileproc/pdf.go` — extractor stays cover-less; `Metadata.HasCover` always false for PDF.
- `internal/handler/bookdrop.go` — new `BookDropPutCover` handler.
- `internal/handler/router.go` — `PUT /api/v1/bookdrop/:id/cover`.
- `internal/service/bookdrop.go` — pre-approval cover write path; reject when item already has a cover or is not in `discovered`/`needs_review`.
- `ui/src/components/PdfReader.tsx` (or BookDrop preview wrapper) — render page 1, `canvas.toBlob('image/jpeg', 0.85)`, PUT.
- ADR-0001 — sidecar write-back: cover bytes still flow through `coverstore` on approve, not through sidecar.
