# BookDrop wipe is admin-only and serialises against the watcher

The Settings → BookDrop panel exposes two housekeeping ops: **Clear processed** (DB-only sweep of terminal-state rows) and **Wipe BookDrop** (recursive removal of every file under `BOOKDROP_PATH`). Both live under `/api/v1/settings/bookdrop/*` and require the admin role. Wipe takes a write-lock on `BookDropService`; the ingest watcher's enqueue path takes a read-lock. Files referenced by `processing` rows are skipped to avoid killing live extraction. The UI dialog requires the user to type `bookdrop` to enable the confirm button, mirroring `DeleteLibraryDialog`.

## Status

accepted (2026-05-03)

## Decisions bundled here

### 1. Wipe is admin-only; Clear processed is also tightened to admin

`BOOKDROP_PATH` is a single shared staging directory — `bookdrop_items` has no `user_id` column. A wipe initiated by user A will erase user B's pending uploads. That cross-user blast radius is the gating constraint. We chose the admin role over inventing per-uploader scoping (which would require a new `uploaded_by` column and rethinking the watcher) because the staging area is by design a tiny, transient scratch space — not a place worth building tenancy into.

Clear processed (DB-only) is tightened to admin in the same change for symmetry. The two endpoints sit under `/settings/bookdrop/*` and share one mental model: "instance housekeeping for the staging area." A non-admin who could clear queue history but not wipe files is a confusing API contour and not a use case anyone has asked for.

### 2. Recursive directory nuke, not extension-filtered or row-driven

Wipe walks `BOOKDROP_PATH` recursively and deletes every regular file. Three rejected alternatives:

- **Extension filter** (`.epub .pdf .cbz …`) — leaves stray junk, contradicts "clean all files".
- **Row-driven** (delete only files referenced by a `bookdrop_items` row) — leaves a watcher race window where a file dropped seconds before wipe has no row yet and survives.
- **DB-only nuke** (drop all rows including non-terminal) — doesn't match user intent ("clean files").

The user's mental model is "the staging directory is empty after I click this." Anything less is surprising. The directory is documented as staging-only (ADR-0004), so deleting non-bookdrop content a user dumped in there manually is acceptable.

### 3. Files referenced by `processing` rows are skipped

A row in `processing` state has an extractor goroutine actively reading the file. Deleting the file mid-extract produces a surprise EOF, possibly a half-written sidecar, and a row stuck in `processing` until manual intervention. Skipping these files is cheap: the `processing` state is short-lived (seconds), the user can click Wipe again after extraction finishes, and any other state — `discovered`, `ready`, `failed`, `imported`, `rejected` — is safe to delete.

After the file sweep, the same operation drops every `bookdrop_items` row whose path no longer exists, except `processing`. This is the orphan cleanup that keeps the queue UI from showing ghosts.

### 4. Service-level RWMutex coordinates wipe with the watcher

The watcher (`internal/ingest/watcher.go`) is a 5-second polling loop with no pause primitive. Mid-wipe, a tick could observe a file mid-delete and start an ingest, leaving a `processing` row pointing at bytes that vanish a moment later.

Three rejected alternatives:

- **Add `Pause()/Resume()` to the watcher.** More machinery, couples the ingest package to the wipe op.
- **fsnotify-style suspend.** Same coupling; the watcher is intentionally polling-based for NFS/SMB compatibility.
- **No coordination, accept eventual consistency.** Leaves correctness holes — a row can be inserted with state `processing` referencing a file deleted milliseconds later, becoming a permanent broken row needing manual sweep.

We took a `sync.RWMutex` on `BookDropService`. Wipe takes the write-lock for the duration of `snapshot → delete → DB sweep`. The watcher's enqueue entry point takes the read-lock. Wipe completes in seconds; lock contention on a 5s tick is negligible. This is the smallest change that closes the race.

### 5. Type-to-confirm gate, mirror `DeleteLibraryDialog`

The dialog shows the file count, total bytes, and the count of `processing`-skipped files, then requires the user to type the literal `bookdrop` to enable the confirm button. The pattern is copied verbatim from `DeleteLibraryDialog` (LibrariesPanel.tsx:497-500) so the codebase has one shape for "this is destructive, don't muscle-memory click it" instead of two.

We rejected `WIPE` as the token because uppercase ASCII reads as a generic destructive word users have been trained to type without thinking. `bookdrop` forces registration of *what* is being wiped. We rejected the full path string because it's too long for a misclick gate.

### 6. SSE: reuse `bookdrop.cleared`, do not introduce `bookdrop.wiped`

After wipe, the service broadcasts the existing `bookdrop.cleared` SSE event. From the client's perspective, wipe is a bigger Clear: invalidate the queue list, refetch. No client today distinguishes "why" the queue changed, and adding a new event would expand the SSE contract surface for no functional gain. If observability later wants to log wipes distinctly, that's a non-breaking addition.

### 7. UI: `/bookdrop` page keeps the inline finished list, loses the Clear button

The user originally asked to "move Recently processed and clear button" to settings. We move only the Clear control. The inline finished-list section stays on `/bookdrop` because users want a glance at what just imported during a review session, with the per-row detail-pane click-through. Yanking the list mid-workflow forces a settings-page detour the user didn't ask for.

The trade-off: settings becomes the single entry point for both housekeeping ops (Clear processed and Wipe BookDrop), but the queue page remains the place where you watch the queue.

## Companion artifacts

- `internal/handler/bookdrop.go` — `BookDropClearProcessed` (to be moved under `/settings/bookdrop/processed`).
- `internal/handler/bookdrop.go` — new `BookDropFilesPreview` (GET) and `BookDropWipeFiles` (DELETE) under `/settings/bookdrop/files`.
- `internal/service/bookdrop.go` — `ClearProcessed`, new `WipeFiles`, new `RWMutex` field; `BookDropClearProcessed` comment block already documents the no-fs-touch decision for Clear.
- `internal/ingest/watcher.go` — `scan` enqueue path takes the service RLock.
- `internal/handler/router.go` — route registration moves under the existing `admin := authed.Group("/settings")` block.
- `ui/src/components/settings/BookDropPanel.tsx` — new panel; counts + Clear + Wipe buttons.
- `ui/src/routes/_app.bookdrop.tsx` — Clear button removed from the "Recently processed" section header.
- `ui/src/api/bookdrop.ts` — `clearProcessedBookDrop` repointed; new `previewBookDropFiles` and `wipeBookDropFiles`.
- ADR-0001 — sidecar write-back; wipe does NOT touch sidecars in libraries (only files in `BOOKDROP_PATH`).
- ADR-0004 — scan auto-imports BookDrop uploads only; reinforces that `BOOKDROP_PATH` is staging, safe to wipe.
- CONTEXT.md — new glossary entries `Clear processed` and `Wipe BookDrop`.
