# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

This is a **single-context** repo: one `CONTEXT.md` at the root, one shared `docs/adr/` tree.

## Before exploring, read these

- **`CONTEXT.md`** at the repo root — the live domain glossary, sectioned by area (Users & identity, Library curation, BookDrop housekeeping, Storage layer, Content identity, Workers, Service layer, Library layout, Metadata enrichment, Vocabulary discipline). Skim the section relevant to the area you're about to work in.
- **`docs/adr/`** — read ADRs that touch the area you're about to work in. Numbered chronologically (`0001-…` through whatever's current); skim titles to find the relevant ones.

If any of these files don't exist, **proceed silently**. Don't flag their absence; don't suggest creating them upfront. The producer skill (`/grill-with-docs`) creates them lazily when terms or decisions actually get resolved.

## File structure (this repo)

```
/
├── CONTEXT.md                  ← authoritative domain glossary
├── docs/
│   └── adr/                    ← all architectural decisions
│       ├── 0001-edit-side-metadata-write-back.md
│       ├── …
│       └── 0017-shared-shelves.md
└── …
```

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `CONTEXT.md`. Don't drift to synonyms the glossary explicitly avoids (e.g. say "Shared shelf", not "broadcast shelf"; "Unshelved", not "Unfiled").

If the concept you need isn't in the glossary yet, that's a signal — either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/grill-with-docs`).

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> _Contradicts ADR-0017 (shared shelves are public read-only) — but worth reopening because…_
