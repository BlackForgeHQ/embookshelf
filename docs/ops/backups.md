# Backups

embookshelf has no built-in backup job. Durable state lives in three
places, and a restore only works if you captured all three from roughly
the same moment.

| What | Where | Lose it and… |
| --- | --- | --- |
| Postgres database | `DATABASE_URL` | everything relational is gone: books, metadata, shelves, users, sessions, settings |
| Book files (+ sidecars) | library paths / S3 bucket | the actual EPUBs, PDFs, CBZs and generated audiobooks |
| `EMBOOKSHELF_SECRET_KEY` | your env / secret manager | every encrypted secret in the DB is unreadable — see below |

Everything under `DATA_PATH` other than managed libraries is a cache,
and `BOOKDROP_PATH` is a transient inbox. Neither needs backing up.

## 1. Postgres

Everything relational — books, metadata, reading progress, annotations,
users, shelves, `app_settings`, `storage_backends`, audiobook runs, the
River job queue.

```bash
pg_dump --format=custom --file=embookshelf-$(date +%F).dump "$DATABASE_URL"
```

With the compose stack, dump from inside the container:

```bash
docker compose exec -T postgres-embookshelf \
  pg_dump --format=custom -U embookshelf embookshelf \
  > embookshelf-$(date +%F).dump
```

Ship it to a blob store on a cron. Restore with `pg_restore` into an
empty database; the app applies any pending migrations on boot when
`MIGRATE_ON_START=true` (the default).

## 2. Book files

Where the bytes live depends on how each library was created:

- **Local library pointing at an existing folder** — that folder. You
  chose the path; back it up like any other data directory.
- **Managed local library** — `${DATA_PATH}/libraries/<slug>/`. In the
  compose stack that is the `embookshelf_data` volume.
- **S3 library** — the bucket named by `EMBOOKSHELF_S3_BUCKET`. Use
  bucket versioning plus cross-region replication rather than pulling
  objects down. If a lifecycle rule tiers cold objects to Glacier
  (see [s3-lifecycle.md](s3-lifecycle.md)), account for restore latency.

Metadata edits are written back to a JSON/OPF sidecar next to each book
file (ADR-0001), so a book-files backup carries user metadata edits even
without the database. `scan/reattach.go` reads those sidecars on rescan.
That makes book files the only backup that can partially rebuild a lost
database — but only partially: reading progress, annotations, users and
shelves exist nowhere but Postgres.

Generated audiobooks are stored as ordinary book files, so they are
covered here. The per-segment staging under `${DATA_PATH}/audiobooks/`
is scratch and gets swept.

## 3. The secret key

`EMBOOKSHELF_SECRET_KEY` encrypts provider API keys, the reading-guide
LLM key, text-to-speech engine keys, OIDC client secrets and stored
cookies with AES-256-GCM (ADR-0010). It is **not** in the database —
losing it means a restored dump boots with unreadable secrets, and every
affected credential has to be re-entered by hand.

Store it wherever you keep the rest of your deployment secrets, and
verify it is there before you need it.

## What you can skip

- `${DATA_PATH}/covers/` — extracted from book files; regenerated on rescan.
- `${DATA_PATH}/audiobooks/` — narration staging, swept after finalize.
- `BOOKDROP_PATH` — the watched inbox. Files here are not yet in the
  library; anything mid-import can be dropped in again.

## Restore checklist

1. Restore Postgres from the dump.
2. Make sure `EMBOOKSHELF_SECRET_KEY` is set to the **same** value as before.
3. Restore or re-mount book files at the same paths the `libraries` rows
   record; for S3, point at the same bucket.
4. Boot. Migrations apply on start.
5. Trigger a rescan per library and confirm book counts and cover art.
