# The local backend stays rooted at `/`, and the key shim stays with it

`files.location` is library-relative. The S3 backend is rooted per library, so a library-relative key is exactly what it wants. The local backend is rooted at `/` for the whole instance, so the same key has to be joined onto the library root before it means anything. A private shim does that join at the four places that read bytes.

The question this ADR settles is whether to close that asymmetry by re-rooting the local backend per library — making keys mean one thing for every backend and deleting the shim — or to keep the rooting and make the shim total.

## Status

accepted (2026-07-29)

## What the code actually looks like

Worth stating plainly, because the framing that prompted this was that local keys are absolute and S3 keys are relative:

**`files.location` is already library-relative for both backends**, in every live write path — `LocalPlacer`, `BackendPlacer`, narration placement, the edit-side rename, and the scan's relocate-by-hash all produce `Author/Title/file.epub`. The asymmetry is not in the column. It is in what the two adapters are rooted at.

**Existing rows can nonetheless hold an absolute location**, from exactly one producer: the storage-v2 backfill's `seedFilesFromBooks`, which writes `books.path` verbatim when the library root was unknown at seed time. Its own tests assert that. Such rows are *already* broken today — `scan/differ.go` compares locations by exact string, so an absolute row reads as Missing while the walked file reads as New, rescued only by the content-hash relocate.

**`books.path` is mixed and stays mixed.** Approves since storage-v2 write it library-relative; legacy rows are absolute, and `metadata_writer` already branches on `filepath.IsAbs` in one place.

## Decisions bundled here

### 1. The local backend stays rooted at `/`

Re-rooting per library was tried and reverted. The loader records why, at the point of the decision: it "was an over-application of Plan F's S3 bucket model — S3 needs per-bucket-prefix rooting, but the local filesystem doesn't, and rooting per-library broke every caller that passes absolute paths."

Those callers are still there and still pass absolute paths: the scan worker walks from the absolute library root and un-absolutizes what it finds, the files backfill joins the root before hashing, and `BOOKDROP_PATH` is force-absolutized at config load *specifically* because the local backend is rooted at `/`. Re-rooting means changing all of them, inverting the regression tests that pin the current behaviour, and adding a data migration for the absolute rows above — where `UNIQUE (library_id, location)` means rewriting one can collide with the relative row the hash-relocate already created for the same file.

That is a large, destructive change to buy an aesthetic symmetry. The rejected shape is exactly that: keys library-relative everywhere, local rooted per library, shim deleted.

### 2. The shim is total, and named for what it is

The cost of keeping the rooting is that every path from a stored location to a storage key must go through one function. That was three of four; the fourth, the edit-side write pipeline, was handing `books.path` over raw, so on a local library the in-file embed opened nothing and the sidecar was written at the filesystem root — ADR-0001's write-back quietly off for every locally-approved book, in warnings nobody reads.

The shim also has to be total over both *shapes*, because `books.path` is mixed: an already-absolute key passes through untouched rather than being joined onto the root a second time.

This is the trade being made explicit. A backend rooted globally needs its keys resolved before use; that resolution is a real thing this codebase does, and the honest response is to name it and route everything through it, not to pretend it is a patch.

### 3. Folder rename becomes one operation on the Storage interface

Still to build, and deliberately not settled here. The interface has `Copy` but no rename and no directory concept, so the local arm escapes to `os.Rename` and the S3 arm hand-rolls a copy loop.

The open question is not the signature but who owns failure. The S3 arm retries each copy, schedules already-written keys for reclamation when its transaction fails, and refuses the rename outright when there is no orphan queue to defer the source delete to. The local arm renames a directory atomically and has none of that. A single operation has to say which of those it owns and which stays with the caller — and the answer decides whether the two adapters share a contract or merely a name.

The conformance suite is not ready for it either: it only ever puts single keys, has no multi-key fixture, and its `Copy` test never checks whether the source survived — so the divergence a rename contract would have to pin is currently untested. The interface comment claims `Copy` is `rename(2)`-with-fallback on local; the implementation is a true copy that never unlinks.

## Consequences

- The shim survives, so "keys mean one thing for every backend" is not true of this codebase and this ADR is where a future reader learns it is deliberate.
- `libraries.path` and `libraries.root` are two columns for one thing, read inconsistently by four call sites, and `relativeToRoot` / `Relativize` both fall through to emitting an absolute location when they disagree — a live producer of the very rows a migration would have had to clean up. Reconciling them is worth doing on its own merits and is a prerequisite for ever revisiting §1.
- If §1 is ever revisited, the migration story agreed alongside this ADR was a boot-time, sentinel-guarded backfill in the shape of `BackfillStorageV2`, resolving `UNIQUE (library_id, location)` collisions by deleting the absolute duplicate — the relative row is the live one.

## Companion artifacts

- `docs/adr/0005-s3-edit-time-folder-rename.md` — the copy-plus-orphan rename §3 would have to preserve.
- `CONTEXT.md` — Files row, Backend, Library.
- Issue #168.
