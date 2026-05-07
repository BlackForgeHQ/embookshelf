---
paths:
  - "internal/handler/**"
  - "internal/service/**"
  - "internal/repo/**"
  - "internal/task/**"
  - "internal/queue/**"
  - "internal/provider/**"
---

# Error Handling

- Wrap errors with context: `fmt.Errorf("scan import %s: %w", id, err)`. Never return a bare `err` that loses caller context.
- Use sentinel errors (`errors.Is`) or typed errors (`errors.As`) for control flow, not string matching.
- Never swallow errors silently. Log via the project logger or return up the stack.
- HTTP responses: consistent shape (`{ "error": { "code", "message" } }`); correct status (400 validation, 401 auth, 403 forbidden, 404 not found, 409 conflict, 500 unexpected).
- Never expose internal paths, stack traces, or raw DB errors to clients in production responses.
- Retry transient errors (network timeouts, 5xx from providers, S3 throttling) with exponential backoff via the resilient client in `internal/provider/`. Fail fast on validation, auth, and 4xx.
- Cancel in-flight outbound HTTP via `ctx` propagation when the client disconnects (matters for SSE endpoints like `/books/:id/enrich/stream`).
- Include request / correlation IDs in error logs when available.
