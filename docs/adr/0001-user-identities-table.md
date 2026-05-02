# ADR-0001: Move OIDC identities into `user_identities`

- Status: Accepted (2026-05-02)
- Deciders: Bohdan Shaparenko (@shbodya)

## Context

Migration `000014_oidc.up.sql` (2025) added two nullable columns to
the `users` table:

```sql
ALTER TABLE users ADD COLUMN oidc_subject TEXT, ADD COLUMN oidc_issuer TEXT;
CREATE UNIQUE INDEX users_oidc_identity ON users (oidc_issuer, oidc_subject)
    WHERE oidc_subject IS NOT NULL;
```

That schema permits exactly one OIDC identity per user. The login
callback can either look an identity up or attach one via
`UserRepo.LinkOIDC` (an `UPDATE` over the columns).

We are adding three user-facing capabilities to the account panel:

1. show how the signed-in user authenticates
2. let the user link additional providers from the panel
3. let the user unlink a provider with a lockout guard

Capability 2 implies a user can link Google and GitHub to the same
account. The current schema cannot represent that — the second link
would overwrite the first.

## Decision

Replace the two columns on `users` with a dedicated table:

```sql
CREATE TABLE user_identities (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider      TEXT NOT NULL,                 -- "google" | "github" | "generic"
    issuer        TEXT NOT NULL,
    subject       TEXT NOT NULL,
    email         TEXT,                          -- last-seen, informational
    linked_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login_at TIMESTAMPTZ,
    UNIQUE (issuer, subject),                    -- one identity → one user
    UNIQUE (user_id, provider)                   -- one Google per user, one GitHub, …
);

CREATE INDEX user_identities_user_id ON user_identities (user_id);
```

Existing rows with non-null `oidc_subject` migrate into the new
table in the same migration; the columns are dropped at the end.
The migration is forward-only — no dual-write window — matching the
rest of `internal/migrator/migrations`.

`provider` is stored explicitly rather than derived at read time
from the issuer URL. Login flows use the slug for routing
(`/api/v1/auth/oidc/:slug`), and the UI uses it for icon + label
selection. Deriving it at every read costs a string match and
couples the read path to issuer-URL trivia (Google host changed once
already).

`UNIQUE (user_id, provider)` enforces one identity per provider per
user. Multi-account-per-provider (work + personal Google) is
explicitly out of scope; if it ever becomes a real ask the
constraint relaxes to `UNIQUE (issuer, subject)` only and the UI
grows a per-row label.

## Consequences

Positive:

- Multi-provider linking representable without further schema work.
- Login `Exchange` flow becomes a clean lookup against one table
  instead of nullable columns on `users`.
- `last_login_at` per identity gives a useful audit signal that the
  current schema can't carry.

Negative:

- Forward-only migration. A bad rollout means restoring the columns
  by hand from the new table — feasible but manual.
- One more table to keep in sync between Postgres and SQLite
  dialects. SQLite's consolidated `0000_init.up.sql` will need the
  same definition added; the numbered SQLite migration will perform
  the data move on existing installs.

Neutral:

- Auto-link semantics (login-time email match, gated by
  `AllowLocalAccountLinking`) are preserved unchanged, only ported
  to the new table. See CONTEXT.md → "Auto-link".

## Alternatives considered

**Keep the columns, add the table, dual-write.** Rejected: two
sources of truth invite drift bugs; nothing in this codebase needs
the rollback window that dual-write would provide.

**Add the table, keep the columns deprecated.** Rejected: same
drift risk plus permanent dead code.

**No multi-provider support; just expose linked/not-linked in the
panel.** Rejected by the product decision to support panel-driven
linking of multiple providers.
