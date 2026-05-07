---
alwaysApply: true
---

# Testing

- Verify behavior, not implementation. Don't assert mock call counts when output values would do.
- Run the specific test file after changes, not the full suite. Faster feedback, fewer tokens.
  - Go: `go test ./internal/fileproc/ -run TestEPUB`
  - UI: `cd ui && bun run test -- <pattern>`
  - E2E: `cd e2e && bun run test -- <file>` (needs `make up`)
- Flaky test? Fix it or delete it. Never retry to make it pass.
- Prefer real implementations. Mock only at system boundaries (network, filesystem, clock, randomness).
  - Go integration tests should hit a real SQLite via `internal/db` test helpers, not mock the repo.
  - Storage tests use the `storage/storagetest` fixture, not hand-rolled stubs.
- Go: prefer table-driven tests. One subtest per case via `t.Run(name, ...)`.
- One assertion per test (or per subtest). Test names describe behavior. Arrange-Act-Assert. No `if` or loops in tests beyond the table loop.
- Never `expect(true)` or check a mock was called without verifying arguments.
