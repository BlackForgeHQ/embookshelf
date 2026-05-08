# SMTP-only email delivery, no provider catalog

embookshelf sends transactional mail (password reset, admin invite, Send-to-Kindle) through a single `email.Sender` interface with one real implementation: an SMTP adapter built on `github.com/wneessen/go-mail`. Admins configure host / port / username / password / from / TLS in `app_settings.EMAIL`; password is encrypted per-field per ADR-0010. There is **no** provider catalog mirroring `internal/provider` — no Resend, SES, Mailgun adapter, no `kind` discriminator, no `email_settings` rows seeded from a `Catalog`.

## Status

accepted (2026-05-08)

## Considered options

- **SMTP-only with `wneessen/go-mail`.** Picked. One transport covers Brevo, Mailjet, Amazon SES (SMTP endpoint), Postmark, Mailgun, Gmail app-passwords, and operator-run Postfix. `wneessen/go-mail` is zero-dep, RFC-compliant, context-aware, supports DKIM and attachments — the only piece we actually need beyond stdlib `net/smtp` is multipart + attachment handling for Send-to-Kindle. One adapter is real, the others are hypothetical. Per the project rule "two implementations is real, one is hypothetical", no interface farm.
- **Provider catalog mirroring `internal/provider/catalog.go`.** Define `email.Provider` interface, declare `Catalog = []Info{SMTP, Resend, SES}`, store config keyed by provider id. Rejected: metadata providers actually differ in shape (search, scoring, ISBN priority, schema-driven config UI per ADR-0008). Email providers all expose `Send(msg)`. The catalog pattern was justified by genuine per-provider behavior; replicating it for email would be cargo-cult. Adds settings-UI polymorphism, encrypted-config fan-out, and per-provider rate-limit math for no win.
- **Resend HTTP adapter alongside SMTP.** Rejected for v1. The case is "PaaS host blocks port 25/587 outbound" (Heroku, some Render tiers). embookshelf's deployment shape is "single binary on a box / Docker / NAS"; outbound SMTP is not blocked there. If a real user hits the wall, a `ResendSender` implementation behind the same `email.Sender` interface is a localised add — the seam is in place.
- **stdlib `net/smtp` only.** Rejected. No multipart/alternative builder, no attachment helpers, no STARTTLS handshake helpers worth using directly. Send-to-Kindle needs a 50 MB attachment with a sanitised filename; rolling that on top of `net/smtp` is a maintenance liability for no gain over a 600-LoC focused dep.
- **`go-gomail/gomail`.** Rejected. Unmaintained since 2016, no Go modules, no context support. Ergonomically familiar but a dead branch.

The asymmetry: a catalog buys a settings-UI affordance ("pick your provider") that admins resolve once and never revisit, in exchange for permanent code surface across handler / encryption / boot / tests. The SMTP-only model puts the picking step on the admin's side (they already chose Brevo / SES / etc when they signed up there) and keeps embookshelf's surface flat.

## Consequences

- New module `internal/email/` holds `Sender` interface, `SMTPSender` (wneessen/go-mail wrapping), `NoopSender` (dev fallback when `EMAIL.enabled=false`), `Message{To, Subject, Text, HTML, Attachments}`, and `templates/` with `//go:embed`.
- Service-layer orchestration lives in `internal/service/notifier.go`. `Notifier` knows about reset tokens, invite tokens, Send-to-Kindle attachment build via `LibraryHandle`, and the `publicUrl` for link rendering. The transport (`Sender`) does not know about embookshelf domain.
- `app_settings.EMAIL` JSON: `{enabled, smtp:{host,port,username,password,tls}, from:{address,name}, publicUrl}`. `smtp.password` AES-GCM encrypted in place per ADR-0010 — generalises `transformConfigFields` from `provider_settings` to `app_settings` (the OIDC ClientSecret gap stays open for a separate change; tracked as a follow-up).
- Feature flag: when `enabled=false` or row missing, login hides "Forgot password", account panel disables Send-to-Kindle with tooltip "Email not configured by admin", admin invite UI redirects to email settings, and the affected APIs return 503 `{"error":{"code":"EMAIL_DISABLED"}}`.
- New routes: `POST /api/v1/auth/password-reset/request|confirm`, `GET .../verify`, `POST /api/v1/admin/invites` (+ list / revoke), `POST /api/v1/auth/invites/accept`, `POST /api/v1/books/:id/send-to-kindle`, `PUT /api/v1/account/kindle-email`, admin email settings CRUD with a "send test email" action.
- Reversibility: adding a second `Sender` adapter later is a localised change (new file in `internal/email/`, config field `smtp` becomes one of `{smtp|resend|...}`). Migrating from a catalog *back* to a flat config is the painful direction; staying flat is the cheaper bet.
- Deliverability concern is pushed to the operator. Documentation in `docs/ops/` recommends Brevo (300/day free, simplest), Resend (3 k/mo free, dev-friendly), and Amazon SES (cheapest at scale). Self-hosted Postfix is explicitly discouraged for non-relayed use because cloud / residential IPs are blocklisted by default and major providers silently drop.

## Companion artifacts

- `internal/email/sender.go` — `Sender` interface, `SMTPSender`, `NoopSender`.
- `internal/email/message.go` — `Message`, `Attachment`.
- `internal/email/templates.go` + `internal/email/templates/*.{html,txt}` — embedded HTML+text templates parsed once at boot.
- `internal/service/notifier.go` — `Notifier{sender, repos, libStore, cipher, publicUrl}`; `SendPasswordReset`, `SendAdminInvite`, `SendToKindle`.
- `internal/repo/app_settings.go` — `SettingEmail = "EMAIL"` key, `EmailConfig` struct, password-field encryption walk lifted from `provider_settings`.
- `internal/handler/settings_email.go` — admin GET/PUT + test-send endpoint.
- `cmd/embookshelf/main.go` — wire `Sender` from config at boot; fall back to `NoopSender` when disabled.
- `go.mod` — add `github.com/wneessen/go-mail`.
