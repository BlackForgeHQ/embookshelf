---
paths:
  - "internal/handler/**"
  - "internal/auth/**"
  - "internal/service/**"
  - "internal/crypto/**"
  - "internal/opds/**"
  - "internal/sse/**"
  - "internal/provider/**"
---

# Security

- Validate all user input at the system boundary. Never trust request parameters.
- Use parameterized queries (pgx args / `database/sql` placeholders). Never concatenate user input into SQL or shell commands.
- Sanitize output to prevent XSS. Use framework-provided escaping.
- Auth is session-cookie based with bcrypt + CSRF middleware. Cookies must stay `HttpOnly` + `SameSite`. Never log session IDs, tokens, passwords, or PII.
- Use constant-time comparison (`crypto/subtle`) for secrets and tokens.
- Provider API keys, cookies, and OIDC client secrets must go through `internal/crypto` (AES-256-GCM, ADR-0010) before persistence — never store plaintext in DB columns marked encrypted.
- Set appropriate CORS, CSP, and security headers.
- Rate-limit authentication endpoints.
