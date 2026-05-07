---
alwaysApply: true
---

# Code Quality

## Anti-defaults (counter common Claude tendencies)

- No premature abstractions. Three similar lines beats a helper used once.
- Don't add features or improvements beyond what was asked.
- Don't refactor adjacent code while fixing a bug.
- No dead code or commented-out blocks. Git has history.
- WHY comments, never WHAT. If code needs a "what" comment, rename instead.
- API docs at module boundaries only, not every internal function.

## Naming

### Go (`cmd/`, `internal/`)

- Files: `snake_case.go` (e.g. `metadata_writer.go`, `device_remarkable.go`). Tests next to source as `*_test.go`.
- Packages: short, lowercase, no underscores (`fileproc`, `coverstore`).
- Exported identifiers: `PascalCase`. Unexported: `camelCase`. Acronyms stay uppercase: `ID`, `URL`, `HTTP`, `OIDC`, so `userID`, not `userId`.
- Errors: variable names end in `Err` (`var ErrNotFound = errors.New(...)`).

### Frontend (`ui/`)

- Component files: `PascalCase.tsx` (`AccentPicker.tsx`). Hooks: `useThing.ts`. Utilities: `kebab-case.ts`.
- Booleans: `is` / `has` / `should` / `can` prefix. Functions: verb-first (`getUser`). Handlers: `handle*` internal, `on*` as props.
- Acronyms as words in TS: `userId`, not `userID` (TS convention; differs from Go above).
- Constants: `SCREAMING_SNAKE`.

## Code Markers

`TODO(author): desc (#issue)` for planned work. `FIXME(author): desc (#issue)` for known bugs. `HACK(author): desc (#issue)` for ugly workarounds (explain the proper fix). `NOTE: desc` for non-obvious context. Owner and issue link required. Never `XXX`, `TEMP`, `REMOVEME`.

## File Organization

- Go imports: stdlib, blank line, external, blank line, project (`github.com/blackforge/embookshelf/...`). `goimports` enforces this.
- TS imports: builtins, external, internal (`@/...`), relative, types. Blank line between groups.
- Exports: named over default. One component / type / class per file.
- Function order: public API first, then helpers in call order.

## Generated files — do not edit

- `ui/src/routeTree.gen.ts` (TanStack Router)
- `internal/staticfs/dist/**` (synced from Vite build via `bun scripts/sync-dist.ts`)
