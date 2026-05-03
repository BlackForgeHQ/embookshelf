# S3 edit-time folder rename via copy + sweeper-deferred delete

S3-backed libraries now follow the same `{Author}/{Title}/` rename-on-edit rule as local libraries (ADR-0003 §6). On a user-driven Author/Title edit, `MetadataWriter` issues server-side `CopyObject` for every key under the Book's old prefix, swaps DB pointers in a single transaction, and enqueues the old keys into a new `pending_orphans` table that a background sweeper drains after a grace window. Supersedes ADR-0003 §7.

## Status

accepted (2026-05-03)

Supersedes ADR-0003 §7 ("S3 backends: layout at place-time, never on edit"). All other sections of ADR-0003 stand.

## Context

ADR-0003 §7 deliberately excluded S3 from edit-time folder rename, with two stated reasons:

1. **Cost.** Rejected eager S3 migration cited "$$$ on S3" in §Considered alternatives.
2. **Atomicity.** S3 has no atomic directory rename; copy + delete is multi-op and partial-failure-prone.

The cost premise was wrong for the backends embookshelf actually targets. Server-side `CopyObject` is **free intra-bucket** on AWS (same region), Cloudflare R2, MinIO, and Tigris. The cost objection was inherited from generic S3 wisdom that doesn't apply to single-bucket, library-prefix deployments. (`internal/storage/s3/s3.go` enforces single-bucket per backend; `internal/storage/s3/methods.go:149` already implements server-side Copy.)

The atomicity objection remains real but is solvable with a deferred-delete sweeper. The "S3 is captured by sidecar full-mirror" fallback in §7's Consequences turned out unsatisfying in practice: external readers (Kobo, KOReader, OPDS clients) browse and bookmark by key path, not by sidecar contents. Drift between approve-time titles and current metadata is user-visible.

## Decision

### 1. Drop the `BackendID == nil` guard around `FolderRename` in `DecideEffects`.

`internal/service/decide_effects.go` currently sets `FolderRename` only when the library has no backend. New shape:

```go
e := Effects{DB: true, Sidecar: true}
if folderChanged {
    e.FolderRename = true   // both backends
}
if handle.Library.BackendID == nil {
    e.InFile = true         // local-only, unchanged
}
```

The trigger gate from ADR-0003 §6 is preserved: only `manual_edit` and `apply_enrichment` can set `FolderRename`. `auto_enrichment`, scan re-extract, and bookdrop approve never trigger a rename — same on both backends.

### 2. `MetadataWriter.renameFolder` branches by backend.

```go
func (w *MetadataWriter) renameFolder(...) (bool, string) {
    if handle.Library.BackendID == nil {
        return w.renameFolderLocal(...)
    }
    return w.renameFolderBackend(...)
}
```

The two arms have different error shapes, different recovery models, and different dependencies (the backend arm needs the orphan-table repo). Hiding the asymmetry behind a `Storage.RenamePrefix` method would mislead callers about atomicity. We keep the split visible.

### 3. Backend rename pipeline.

Per-rename flow on S3:

1. **Enumerate keys to move** (hybrid, ADR-0003 §8/§9 catch-all). Issue `ListObjectsV2(Prefix={old_folder}/)` paginated. Captures the multi-format files, the folder-root sidecar (`metadata.embookshelf.json`), folder-root covers (`cover.{jpg,jpeg,png,webp}`), and any user/system artifact. The DB list (`files.location` rows for the Book) drives the post-move `files.location` UPDATE — only DB-tracked files get a row update; arbitrary co-located keys ride along under the new prefix.

2. **Collision check.** `ListObjectsV2(Prefix={new_folder}/, MaxKeys=1)`. Non-empty → suffix ` (2)`, ` (3)`, re-probe. Same suffix rule as local's `uniqueDirectoryUnless`. Race window is narrow — same hazard as local concurrent edits, accepted.

3. **5GB+ files.** Above the `CopyObject` 5GB hard limit, copy returns the AWS `EntityTooLarge` error; rename aborts cleanly, DB unchanged, sidecar full-mirror still records the metadata. User sees "Title updated; folder rename skipped (file too large)." Multipart-copy (`UploadPartCopy`) deferred until a user actually hits this.

4. **Phase-1: copy-loop.** For each enumerated key, `Copy(srcKey, dstKey)` server-side. Each copy retried with bounded backoff (3× exponential). Failure after retry: roll back. Roll back = insert every successfully-copied **new** key into `pending_orphans` with short grace (5 min) so the sweeper reaps half-rename garbage. DB stays unchanged. Caller sees error.

5. **Phase-2: DB swap.** Single transaction:
   ```sql
   UPDATE files SET location = $new_prefix || substring(location from length($old_prefix)+1)
   WHERE book_id = $1;
   UPDATE books SET folder_path = $2, path = $3 WHERE id = $1;
   INSERT INTO pending_orphans (library_id, key, eligible_at, reason, book_id)
   VALUES ... -- one row per old enumerated key
   ```
   Atomic visibility: sweeper either sees all old or all new. The DB is the canonical point of truth on S3 — there is no filesystem anchor.

6. **Phase-3: deletes happen later, not here.** No inline delete. The sweeper handles all source-key deletes after the grace window. Drops the failure mode where the rename error-path has to clean up after itself.

### 4. `pending_orphans` table (migration 000031).

```sql
CREATE TABLE pending_orphans (
    id           BIGSERIAL PRIMARY KEY,
    library_id   UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    key          TEXT NOT NULL,
    eligible_at  TIMESTAMPTZ NOT NULL,
    reason       TEXT NOT NULL,
    book_id      UUID,                                 -- FK-less; book may delete later
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (library_id, key)
);
CREATE INDEX pending_orphans_due ON pending_orphans (eligible_at);
```

`reason` future-proofs for non-rename uses (`delete`, `library-removed`).
`UNIQUE (library_id, key)` prevents double-insert if a rename re-runs on the same Book.
`book_id` nullable + no FK so book deletion doesn't block sweeper.

### 5. Sweeper: `LoopOrphanedKeys` ticker.

New goroutine in `internal/task/orphaned_keys.go`, mirrors `task.LoopMissingPurge` shape. Wakes hourly. Each pass:

```sql
SELECT id, library_id, key FROM pending_orphans
WHERE eligible_at <= now()
ORDER BY eligible_at
LIMIT 500;
```

For each row: resolve the library's `Storage`, call `Delete(ctx, key)`, then `DELETE FROM pending_orphans WHERE id = $1`. `storage.ErrNotFound` is treated as success. Other errors leave the row for the next pass.

Bounded work per pass — O(rename count), not O(library size). Skips full library list-prefix scans entirely.

### 6. Grace window.

`max(2 × cfg.PresignTTL, 1h)`. Default `PresignTTL` is 10min so default grace is 1h. New env `EMBOOKSHELF_S3_RENAME_GRACE` overrides for operators with longer presign TTLs.

The grace window is the contract with already-issued presigned URLs: any URL handed to a client for the old key is honored for at least `2 × PresignTTL`, which exceeds the URL's natural expiry.

### 7. Existing-drift backfill: none.

Mirrors ADR-0003 §5 lazy migration. S3 books whose stored `folder_path` doesn't match `{Author}/{Title}` at ship-time stay drifted until the user touches each one. No boot-time reconcile, no auto-relocate. A future operator-triggered reconcile job is buildable but not in scope.

## Considered alternatives

- **Per-file rename with per-file DB tx.** Each file: copy, update `files.location`, delete. Failure mid-loop leaves rows pointing at mixed prefixes. Rejected — defeats "folder = Book" identity.
- **Inline phase-2 delete after DB swap.** Cleaner mental model (rename returns when fully done), but breaks already-issued presigned URLs immediately and requires the rename code path to retry deletes itself. Rejected — sweeper-deferred delete decouples concerns and naturally bounds blast radius.
- **`Storage.RenamePrefix(ctx, old, new) error` interface method.** Tempting one-liner at the caller. Rejected — local impl is `os.Rename` (atomic), S3 impl is multi-stage with deferred delete; the asymmetry matters to callers and shouldn't be hidden behind a uniform signature.
- **River job per rename instead of inline.** Operator-visible job state, retries for free. Rejected for now — current per-Book file count fits inside an HTTP handler timeout. Revisit if multi-chapter audiobook renames start blocking the request thread.
- **Tag-on-copy with `x-amz-meta-embookshelf-pending` instead of `pending_orphans` table.** Sweeper would list-prefix per library and check tags. Rejected — full library scan per pass vs. bounded DB query; also couples to a per-backend metadata feature.
- **Multipart-copy for >5GB files.** Real edge case but real implementation cost. Deferred to first user complaint per ADR-0003's "fully captured in sidecar" fallback.

## Consequences

**Positive:**
- S3 + local converge behaviorally on edit-time rename; one mental model across backends.
- External readers / OPDS bookmarks / download URLs reflect current Author/Title for actively-edited books (after grace period).
- The `pending_orphans` table gives a clear audit trail: when did each key get abandoned, why, by which book.

**Negative / surprising:**
- `MetadataWriter` now depends on a `PendingOrphansRepo`. Wiring change in the service constructor.
- New env var (`EMBOOKSHELF_S3_RENAME_GRACE`), new background goroutine (`LoopOrphanedKeys`).
- Bucket versioning growth: every rename produces N new version chains and N delete-markers under the old prefix. Lifecycle policy on the bucket is the operator's job; we warn on missing versioning at startup but don't manage retention.
- Half-failed renames briefly leave orphans under the *new* prefix that get reaped within 5 min. A user listing the bucket directly during that window will see ghost folders. Internal code never sees them — DB doesn't reference them.
- Glacier-class objects can't be `CopyObject`'d without restore. Same failure mode as 5GB+: copy fails, rename aborts cleanly, sidecar carries the metadata.
- Existing-drift S3 libraries stay drifted; pre-ship edits do not retroactively fix.

## Implementation phasing

1. Migration 000031: `pending_orphans` table + `pending_orphans_due` index.
2. `internal/repo/pending_orphans.go`: insert / select-due / delete.
3. `internal/task/orphaned_keys.go`: `LoopOrphanedKeys` ticker + `RunOrphanedKeysOnce` for tests.
4. `internal/service/decide_effects.go`: drop the `BackendID == nil` guard around `FolderRename`.
5. `internal/service/metadata_writer.go`: add `renameFolderBackend`, branch in `renameFolder`. New deps: `PendingOrphansRepo`, grace duration.
6. `cmd/embookshelf`: wire deps, start `LoopOrphanedKeys` alongside `LoopMissingPurge`. Read `EMBOOKSHELF_S3_RENAME_GRACE`.
7. Update `decide_effects_test.go` and `metadata_writer_test.go`: flip `TestMetadataWriter_FolderRename_S3SkipsRename` into `TestMetadataWriter_FolderRename_S3Renames`; add tests for partial-failure rollback, collision-suffix, grace-window deferral, sweeper draining.
