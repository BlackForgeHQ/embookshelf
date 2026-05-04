# embookshelf — Domain glossary

Terms with a specific meaning inside the codebase. When a term here
conflicts with how a teammate is using a word, the term here wins —
push back and align before changing the code.

## Identity

A credential link between an embookshelf user and an OIDC provider
account. Stored in `user_identities` (one row per linked provider).
Distinct from "session" (a logged-in browser) and "account" (the
human-facing user record in `users`).

## Provider

An OIDC identity provider slug used in URLs and DB rows. Three slugs:
`google`, `github`, `generic`. The `generic` slug points at whatever
issuer the admin configured (Authelia, Keycloak, Okta, etc.).
A provider is "enabled" when its admin row in `app_settings` has
`Enabled=true`.

## Linking

The act of attaching an identity to a user. Two ways it happens:

1. **Panel-driven link**: signed-in user clicks Connect Google in the
   account panel. Initiated under `/api/v1/account/oidc/link/:slug`.
   Always explicit, always authed.
2. **Auto-link** (see below): performed by the login callback.

## Auto-link

Login-time linking gated by the admin flag
`AllowLocalAccountLinking`. When the OIDC callback returns an
identity that doesn't match any row but the email claim matches a
local user, the callback attaches the identity to that user instead
of rejecting the login. Relies on the IdP-verified email — GitHub
explicitly rejects unverified emails (`service/oidc.go:486`); Google
and generic OIDC trust the `email_verified` claim.

## Lockout guard

The invariant enforced on every unlink: a user must end the
operation with at least one usable credential — either a password or
a remaining linked identity. A user with no password and exactly one
linked identity must set a password before that identity can be
removed. Enforced at the SQL layer in a single statement so the
check and the delete are race-free.

## Provisioning

Admin policy controlling whether an unknown OIDC identity creates
a new user. Three knobs in `oidc_auto_provision_details`:
`EnableAutoProvisioning`, `RequireAdminApproval`, `DefaultRole`.
Off by default after the first user; the first OIDC login on an
empty instance is always admitted as admin to avoid an unrecoverable
state.

## Pending orphan

A storage key whose Book is no longer referenced by `files.location`
(or sidecar/cover at `{folder_path}/`) and which is queued for
deletion after a grace window. Materialised as a row in
`pending_orphans` (library_id, key, eligible_at, reason, book_id).
Created on every S3 edit-time folder rename: old keys go in with
full grace; new keys from a half-failed rename go in with a short
grace. A background sweeper deletes keys past `eligible_at`. Local
backends do not produce pending orphans — `os.Rename` is atomic.

## Force-only mode

Admin toggle `oidc_force_only_mode` that hides the local-password
form on the login page when exactly one OIDC provider is enabled.
Does not affect API auth or existing local users.

## Clear processed

Housekeeping op that deletes every `bookdrop_items` row in a
terminal state (`imported`, `rejected`) and best-effort sweeps any
pre-approval cover bytes still on disk. The source files under
`BOOKDROP_PATH` are NOT touched — the watcher will re-discover any
file still on disk on its next tick. Distinct from Wipe BookDrop.

## Unshelved

A virtual view of books a user has not manually placed on any
**regular** shelf. System shelves `reading` and `finished` are
excluded from the test (they auto-populate from progress, not
curation), and smart shelves are ignored (their membership is
query-time, not stored). Per-user: a book can be unshelved for
one user and shelved for another. Surfaced as a fixed sidebar
entry under "All Books" and as the `?unshelved=1` library
filter — never a row in `shelves`. Implemented with a single
`NOT EXISTS` subquery over `shelf_books`. Distinct from "All
Books" (every book) and from a smart shelf (rule-driven).

## Wipe BookDrop

Admin-only housekeeping op that recursively removes every file
under `BOOKDROP_PATH` and drops orphaned `bookdrop_items` rows
whose path no longer exists. Files referenced by rows in
`processing` state are skipped to avoid killing live extraction.
Cross-user blast radius: erases other users' pending uploads,
hence the admin gate and the type-to-confirm dialog. Distinct
from Clear processed (DB-only).
