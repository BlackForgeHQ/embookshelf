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

## Code Markers

`TODO(author): desc (#issue)` for planned work. `FIXME(author): desc (#issue)` for known bugs. `HACK(author): desc (#issue)` for ugly workarounds (explain the proper fix). `NOTE: desc` for non-obvious context. Owner and issue link required. Never `XXX`, `TEMP`, `REMOVEME`.

## File Organization

- Exports: named over default. One component / type / class per file.
- Function order: public API first, then helpers in call order.

## Generated files — do not edit

- `ui/src/routeTree.gen.ts` (TanStack Router)
- `internal/staticfs/dist/**` (synced from Vite build via `bun scripts/sync-dist.ts`)

Language-specific naming + import conventions live in path-scoped rules: `code-quality-go.md` for `cmd/**` and `internal/**`, `frontend.md` for `ui/**`.
