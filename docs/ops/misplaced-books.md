# Book files written outside their library (v0.3.1 – v0.6.2)

A bug in releases **v0.3.1 through v0.6.2** could write an approved book's
file to the **filesystem root** instead of into its library. The catalog
recorded the correct location, so the book looks fine in the UI and never
opens.

`embookshelf recover-misplaced` finds those files and moves them where the
catalog already says they are. It is a dry run by default.

```bash
# report only — changes nothing
DATABASE_URL='postgres://…' embookshelf recover-misplaced

# move what it found
DATABASE_URL='postgres://…' embookshelf recover-misplaced --apply
```

Run it with the server stopped, or at least with nobody approving books,
and take a backup of the database first ([backups.md](backups.md)).

## Is my install affected?

Run the command. It reads only the catalog, touches only paths a book
already names, and prints `Nothing found.` on a healthy install.

You are in the affected set only if **all** of these are true:

| | |
| --- | --- |
| **Version** | You ran some release between v0.3.1 and v0.6.2. |
| **Age of the install** | The install predates the storage-v2 migration — i.e. it existed before v0.3.0 and was upgraded onto one of those releases. Libraries created *after* that migration were never affected. |
| **Process user** | The server ran **as root**. |

That last row is the one people get wrong, so it is worth stating plainly:

- **Running as a non-root user** (the official images, and any compose
  file that does not override `user:`) — the misplaced write failed with
  a permission error on `mkdir`, the approve refused loudly, and the file
  stayed in BookDrop. Annoying, but **nothing was misplaced**. There is
  nothing for this tool to find.
- **Running as root** — bare-metal installs, and compose files that set
  `user: root` or `user: "0:0"` — the write **succeeded silently**. Those
  books are in the catalog, never open, never scan, and after 24 hours
  the missing-file sweeper deletes their file record too. These are the
  ones to recover.

## What it does

For every book in a local library that carries a storage-backend row —
the shape the storage-v2 migration created, and the only shape that could
be affected — it takes the location the catalog already holds and checks
the one place the bug would have put the bytes: that same location
resolved against `/`.

It never walks the filesystem. Nothing that no book names is examined.

A file is moved only when **all** of the following hold:

1. The book's real location inside the library is **empty**.
2. A regular file sits at the `/`-rooted path.
3. Its bytes match the recorded content hash — or, for a record the hash
   backfill never reached, its size.

If the missing-file sweeper has already deleted the book's file record,
the tool recreates it after the move (a library scan will not: a scan is
drift detection, never an ingest path — ADR-0018). Without that record
the bytes would land in the right folder with nothing pointing at them
and the book would still not open.

## What it refuses to do

It **never deletes anything** under the filesystem root, whatever the
hashes say. Files it will not move are reported as strays with their full
path, for you to remove deliberately:

- **destination already occupied** — you re-imported the book after the
  failure, so the copy at the root is a duplicate. The good copy is left
  untouched.
- **contents do not match the catalog** — something else is at that path.
  It is not this book's file and is left alone.
- **claimed by more than one book** — two books name the same
  author/title key, so one file at the root cannot be attributed.

## Repeat runs

Re-running after `--apply` finds nothing: there is no longer a file at
the root for any record to claim. Running the dry form as often as you
like costs one query per library plus a `stat` per book.

## Background

- Issue [#265](https://github.com/BlackForgeHQ/embookshelf/issues/265) — the
  dispatch bug. The placer chose its adapter from `libraries.backend_id`,
  the storage-v2 migration set that column on every pre-existing library
  with a path, and every local backend is constructed rooted at `/`
  (ADR-0030 §1). A library-relative key therefore resolved against `/`.
- Issue [#272](https://github.com/BlackForgeHQ/embookshelf/issues/272) —
  this recovery.
- `docs/adr/0003-book-per-folder-library-layout.md` — the
  `{Author}/{Title}/` layout the misplaced paths mirror.
