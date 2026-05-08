# Send-to-Kindle accepts EPUB and PDF only, no in-binary conversion

The Send-to-Kindle action attaches the book's primary file as-is and emails it to the user's `kindle_email`. Eligible formats are EPUB and PDF only — the intersection of embookshelf's supported set (EPUB, PDF, CBZ, CBR, MOBI, AZW3, FB2, M4B, MP3) and Amazon's current Send-to-Kindle ingestion list (EPUB, PDF, DOC/DOCX, RTF, TXT, HTML, JPEG/PNG). The button is disabled with a tooltip on every other format. There is no in-binary conversion step (no calibre, no kindlegen, no MOBI/AZW3 builder).

## Status

accepted (2026-05-08)

## Considered options

- **Ship EPUB and PDF as-is, disable on others.** Picked. Amazon converts EPUB and PDF to KFX server-side; both render natively in the Kindle reader. A 50 MB cap matches Amazon's per-attachment limit; we reject above that with a clear error rather than letting Amazon bounce silently. Subject line is the book title (Amazon uses the subject as the library entry's title), attachment filename is `{Title} - {Author}.{ext}` sanitised through the existing layout helper, body is empty (Amazon strips it).
- **Bundle calibre / kindlegen and convert MOBI / CBZ / FB2 → EPUB on the fly.** Rejected. calibre is ~250 MB on disk and depends on Qt / Python; kindlegen has been deprecated by Amazon since 2021 and is no longer redistributable. Either path destroys the single-binary deployment promise (`make build` produces ~30 MB today). Conversion is also lossy and slow — a 100 MB CBZ → EPUB pipeline would tie up the queue for tens of seconds per book and produce a worse reading experience than keeping the original format on a non-Kindle reader.
- **Accept any format and let Amazon decide.** Rejected. Amazon silently rejects unsupported formats with a generic "we couldn't process this file" email back to the sender — which is our server-wide From address, not the user. The user sees nothing; we see nothing actionable. Pre-flight format check + disabled button gives the user the truth synchronously: "Kindle doesn't accept CBZ; export to EPUB first."
- **Convert EPUB → MOBI for older Kindles.** Rejected. Amazon ended new MOBI delivery in August 2022; modern Kindles handle EPUB natively. Converting wastes CPU on a non-problem and would regress on Kindles that prefer EPUB.
- **Per-user "preferred attachment format" with conversion fallback.** Rejected. Same calibre / kindlegen blast radius. The right answer for the 5 % of users with mismatched formats is "store the EPUB version in your library", not "make embookshelf a conversion service".

The asymmetry: shipping the original file is one `storage.Open` + one SMTP attachment + one filename sanitisation. Converting is a second binary, a second build pipeline, a per-format conversion fidelity story, a cancellation story for slow jobs, and a permanent CVE surface (calibre has had several over the years). The 5 % of books that fall outside EPUB/PDF route around the feature without breaking it.

## Consequences

- Eligible-format set is checked at three points: (1) the `<SendToKindleButton>` reads `book.format` (the primary format cache) and disables on miss; (2) the `POST /api/v1/books/:id/send-to-kindle` handler re-checks server-side and returns 415 `{"error":{"code":"FORMAT_NOT_SUPPORTED","message":"Send-to-Kindle accepts EPUB and PDF only"}}`; (3) the `task.SendToKindle` worker re-validates after `LibraryHandle.For` so a race with a re-import doesn't ship a stale row's format.
- Size cap is 50 MB enforced via `storage.Source.Size()` before opening the byte stream. Reject with 413 `{"error":{"code":"FILE_TOO_LARGE"}}`. We do not chunk or split — Amazon does not accept multi-part attachments.
- Job lives in the existing `queue.Client` interface (`EnqueueSendToKindle(ctx, bookID, userID)` added alongside `EnqueueBookDrop` and `EnqueueLibraryScan`). River backend on Postgres, polling worker on SQLite — no new transport. Failure surface: job row stores `last_error`; SSE event `kindle.sent` / `kindle.failed` rides the existing `/events` channel; toast shown to the originating user only.
- Per-user `users.kindle_email TEXT` column added with regex validation `^[a-z0-9._-]+@kindle\.com$`. Empty means user has not set up Send-to-Kindle; button shows "Set Kindle email" link to account panel instead.
- From-address is the server-wide `EMAIL.from.address`. Onboarding doc tells admins to instruct users to add this address to their Amazon "approved senders" list once. We do not attempt per-user From spoofing.
- Rate limit: per-user 10/hour, enforced in the handler before enqueue. Prevents accidental bulk-send loops and matches Amazon's own rough throttling.
- Reversibility: dropping the feature is one migration to drop `users.kindle_email`, one queue job type removal, and the UI button. Adding format conversion later means adding a `Converter` seam upstream of the attachment build — a localised change inside `task.SendToKindle`. We do not commit to that path.
- Tail risk: Amazon changes the eligible-format list. Today's set has been stable for 3+ years; if it shifts, the eligibility check is one constant in `internal/email/` (and the disabled-button copy in the UI).

## Companion artifacts

- `internal/migrator/migrations/postgres/000036_kindle_email.up.sql` and `…/sqlite/000036_kindle_email.up.sql` — add `users.kindle_email TEXT` column. Down migrations drop it.
- `internal/repo/users.go` — `User.KindleEmail string` field; `UpdateKindleEmail(ctx, userID, email)`.
- `internal/handler/account.go` — `PUT /api/v1/account/kindle-email` with regex validation.
- `internal/handler/books.go` (or new `kindle.go`) — `POST /api/v1/books/:id/send-to-kindle`; format + size pre-check; rate-limit; enqueue.
- `internal/queue/queue.go` — `EnqueueSendToKindle` on `Client` interface; River and SQLite implementations.
- `internal/task/send_to_kindle.go` — worker: `LibraryHandle.For` → `Storage.Open` → format/size re-check → `email.Sender.Send` with attachment → SSE event.
- `internal/service/notifier.go` — `SendToKindle(ctx, book, user, source) error` builds `email.Message` with subject = title, filename = `{Title} - {Author}.{ext}`.
- `ui/src/components/SendToKindleButton.tsx` — eligibility gate, kindle-email-set check, mutation + toast.
- `ui/src/components/account/KindleEmailField.tsx` — account panel input + save.
- `CONTEXT.md` — "Send-to-Kindle", "Kindle email", "Eligible format" glossary entries.
