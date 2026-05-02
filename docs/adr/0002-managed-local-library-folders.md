# Managed local library folders under `${DATA_PATH}/libraries/{slug}/`

When a user creates a `kind=local` library, embookshelf auto-derives the filesystem path as `${DATA_PATH}/libraries/{slug}/` and creates the directory itself. The operator does **not** pick the path; the library-create UI no longer exposes a path input. Mirrors the existing `kind=s3` flow which already derives `libraries/{slug}/` as a prefix in the shared bucket.

## Status

accepted (2026-05-02)

## Context

Until now, `kind=local` library creation took an operator-supplied filesystem path. That allowed pointing libraries at external mounts (`/mnt/nas/scifi`), but left every install with a different layout, made backups hard to script, and forced the s3 and local create paths to diverge unnecessarily.

`kind=s3` (Plan F+) already handles this cleanly: operator types a name → service derives prefix `libraries/{slug}/` → done. This decision pulls local into the same shape.

## Decision

For `kind=local`:
1. Operator types a library **name** only.
2. Service slugifies: `slug = slugify(name)`.
3. Path is derived: `path = filepath.Join(cfg.DataPath, "libraries", slug)`.
4. Directory is created at library-create time via `os.MkdirAll(path, 0o755)` — idempotent. Pre-staged folders (operator pre-copied books) are re-adopted on first scan.
5. DB unique on `libraries.slug` prevents two libraries pointing at the same managed folder.

For library deletion:
- Default: row delete only; files preserved.
- `?purge=true`: row delete + recursive folder removal. Mirrors s3 case.

For migration:
- Existing libraries with non-`${DATA_PATH}` paths keep their explicit paths. Only new libraries are managed. No automatic move.

## Considered alternatives

- **Operator-supplied path (status quo).** Loses NAS/external-mount use case but trades operator agency for layout consistency, simpler UX, scriptable backups, and parity with s3.
- **Default-with-override.** UI shows the derived path as a prefilled placeholder, operator can override. Rejected: keeps two code paths, defeats the simplification, and the override is what we're trying to drop.
- **New `LibraryKind=managed` alongside `local`.** Three-axis enum. Rejected: `local` already implies "filesystem" — splitting hairs between operator-path-local and managed-local doesn't earn its keep.

## Consequences

**Positive:**
- One layout convention. Backup scripts target `${DATA_PATH}` and capture covers + bookdrop + libraries together.
- Symmetry with s3: one mental model for "where does library X live."
- Library-create UX is one input (name) instead of two.
- Pre-staging works: `mkdir ${DATA_PATH}/libraries/scifi && cp books/*.epub ${DATA_PATH}/libraries/scifi/ && create library "scifi"` → scan picks up files.

**Negative / surprising:**
- New libraries cannot point at external mounts. Operators with that need either:
  - Symlink: `ln -s /mnt/nas/scifi ${DATA_PATH}/libraries/scifi` before create.
  - Bind-mount the external storage at `${DATA_PATH}/libraries/...`.
  - Mount `${DATA_PATH}` itself on the external volume.
- Existing libraries with explicit external paths keep working but become a divergent layout class until manually migrated. Documented in release notes; no automatic move.
- Disk-failure mode at create-time: if `MkdirAll` fails (read-only FS, permission denied), library creation fails before the DB row is inserted. Operator sees the error in the UI.
