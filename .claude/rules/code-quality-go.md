---
paths:
  - "cmd/**"
  - "internal/**"
---

# Code Quality — Go

## Naming

- Files: `snake_case.go` (e.g. `metadata_writer.go`, `device_remarkable.go`). Tests next to source as `*_test.go`.
- Packages: short, lowercase, no underscores (`fileproc`, `coverstore`).
- Exported identifiers: `PascalCase`. Unexported: `camelCase`. Acronyms stay uppercase: `ID`, `URL`, `HTTP`, `OIDC`, so `userID`, not `userId`.
- Errors: variable names end in `Err`, package-level sentinels prefixed `Err` (`var ErrNotFound = errors.New(...)`).

## Imports

Three groups, blank line between each, `goimports` enforces order:

1. stdlib
2. external
3. project (`github.com/blackforge/embookshelf/...`)
