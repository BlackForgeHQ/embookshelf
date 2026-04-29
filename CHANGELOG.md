# Changelog

## [0.2.5](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.4...v0.2.5) (2026-04-29)


### Features

* **scan:** two-phase scan + reconciliation (Plan C of 8) ([#55](https://github.com/BlackForgeHQ/embookshelf/issues/55)) ([364e8d1](https://github.com/BlackForgeHQ/embookshelf/commit/364e8d1156a16e7fa80094587ba5e2aa482f4c1a))

## [0.2.4](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.3...v0.2.4) (2026-04-29)


### Features

* **db:** storage_v2 schema + content-hash identity (Plan B of 8) ([#54](https://github.com/BlackForgeHQ/embookshelf/issues/54)) ([384270d](https://github.com/BlackForgeHQ/embookshelf/commit/384270debf3ed3f6b1797e68e131b8767e4e87ea))


### Documentation

* **plan:** storage v2 schema + content-hash identity (Plan B of 8) ([#51](https://github.com/BlackForgeHQ/embookshelf/issues/51)) ([5a82d4c](https://github.com/BlackForgeHQ/embookshelf/commit/5a82d4cab35ccb3b0281805e8746f0b4eba1068f))

## [0.2.3](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.2...v0.2.3) (2026-04-29)


### Features

* **storage:** backend-agnostic Storage interface (Plan A of 8) ([#50](https://github.com/BlackForgeHQ/embookshelf/issues/50)) ([32354ae](https://github.com/BlackForgeHQ/embookshelf/commit/32354ae11ee8451b5bc086b6acea5b81bf6d438f))

## [0.2.2](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.1...v0.2.2) (2026-04-29)


### Features

* add feature specifications for CI/CD, file naming patterns, library creation, metadata providers, OIDC settings, and S3 storage ([f27f336](https://github.com/BlackForgeHQ/embookshelf/commit/f27f336607157d67264edecd9fbbe98465c45c3f))
* comic (CBZ) and audiobook (MP3/M4B) readers ([#47](https://github.com/BlackForgeHQ/embookshelf/issues/47)) ([6ca0087](https://github.com/BlackForgeHQ/embookshelf/commit/6ca0087c619b1062487a685b80f7a474544ef5e1))

## [0.2.1](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.0...v0.2.1) (2026-04-29)


### Features

* **db:** enhance ScanStringSlice for PostgreSQL text-array literals ([5c9515f](https://github.com/BlackForgeHQ/embookshelf/commit/5c9515f0884dc8076380db82c0b0297b5a028432))

## [0.2.0](https://github.com/BlackForgeHQ/embookshelf/compare/v0.1.4...v0.2.0) (2026-04-29)


### ⚠ BREAKING CHANGES

* bare-default Postgres connections are no longer attempted. Existing deployments that already set DATABASE_URL explicitly are unaffected. Operators relying on the implicit postgres://localhost:5432/embookshelf default must now set DATABASE_URL explicitly. See README quickstart and architecture.md for the new defaults.

### Features

* SQLite backend — driver, schema, repos, FTS5 (Plan 2A of 4) ([#40](https://github.com/BlackForgeHQ/embookshelf/issues/40)) ([7256c16](https://github.com/BlackForgeHQ/embookshelf/commit/7256c167649b2c74908e9e89d59727b5d1410bc4))
* SQLite CI lanes + e2e + final docs (Plan 4 of 4) ([#43](https://github.com/BlackForgeHQ/embookshelf/issues/43)) ([0d0bbc8](https://github.com/BlackForgeHQ/embookshelf/commit/0d0bbc830f9a7f7bd3bbd4c90f8a9b2026ae49c2))
* SQLite is the default + test matrix harness (Plan 2B of 4) ([#41](https://github.com/BlackForgeHQ/embookshelf/issues/41)) ([4009de1](https://github.com/BlackForgeHQ/embookshelf/commit/4009de109d7364dba0747dd2d9a71ab8c3f2b2af))
* SQLite queue worker (Plan 3 of 4) ([#42](https://github.com/BlackForgeHQ/embookshelf/issues/42)) ([3f7d74d](https://github.com/BlackForgeHQ/embookshelf/commit/3f7d74d1a79cf9d8e782aabcedfd37059041e36e))
* **ui:** add sidebar toggle button to TopBar and enhance BookDrop layout ([4c3b1e9](https://github.com/BlackForgeHQ/embookshelf/commit/4c3b1e9bb3d78ba8a0c616967634efeab55c26ff))
* **ui:** enhance StarRating component with interactivity and rating mutation ([2235c3d](https://github.com/BlackForgeHQ/embookshelf/commit/2235c3d2d225615f15c2f2278d2263d9372d2b8f))
* **ui:** rethink edit metadata as two dedicated pages ([#44](https://github.com/BlackForgeHQ/embookshelf/issues/44)) ([f305c42](https://github.com/BlackForgeHQ/embookshelf/commit/f305c42a12bd86b9372a79a366a65de34b6ad3da))


### Bug Fixes

* **ci:** scan correct image tag for SBOM generation ([#38](https://github.com/BlackForgeHQ/embookshelf/issues/38)) ([205939f](https://github.com/BlackForgeHQ/embookshelf/commit/205939f4be5e627e9c8fd965fd4f148bc78b859c))

## [0.1.4](https://github.com/BlackForgeHQ/embookshelf/compare/v0.1.3...v0.1.4) (2026-04-27)


### Features

* OIDC admin-approval flow ([#37](https://github.com/BlackForgeHQ/embookshelf/issues/37)) ([1f662d2](https://github.com/BlackForgeHQ/embookshelf/commit/1f662d2bd0d1483139f4bcb786d7e665a9aad18c))
* update logo and manifest for embookshelf ([12eefe3](https://github.com/BlackForgeHQ/embookshelf/commit/12eefe3e47599516bcae7f8a641cad3a2726cb96))

## [0.1.3](https://github.com/BlackForgeHQ/embookshelf/compare/v0.1.2...v0.1.3) (2026-04-27)


### Features

* command-powered search palette and library combobox ([#34](https://github.com/BlackForgeHQ/embookshelf/issues/34)) ([65db034](https://github.com/BlackForgeHQ/embookshelf/commit/65db0347b36a4f3de6eefdc23ac24557696e947f))

## [0.1.2](https://github.com/BlackForgeHQ/embookshelf/compare/v0.1.1...v0.1.2) (2026-04-24)


### Features

* **docs:** add comprehensive guide for Go libraries in book metadata enrichment ([df38e20](https://github.com/BlackForgeHQ/embookshelf/commit/df38e20a36d35805d8a4e43fde757ea603842200))
* **provider:** add per-provider rate limit config to catalog ([ce0d84d](https://github.com/BlackForgeHQ/embookshelf/commit/ce0d84db4bf95e5a980a94de01b1ccbc7d7cf1f9))
* **provider:** add resilient HTTP transport with rate limiting, circuit breaking, and retries ([b57c805](https://github.com/BlackForgeHQ/embookshelf/commit/b57c8057326cd6603734aca786e754836b7b76b0))
* **provider:** add Unicode NFC normalization to match scoring ([4f952b9](https://github.com/BlackForgeHQ/embookshelf/commit/4f952b9a3acdd2146914394a29e5fcc4695e70bd))
* **provider:** resilient enrichment pipeline with per-provider rate limits, circuit breakers, retries, and Unicode scoring ([03a31cb](https://github.com/BlackForgeHQ/embookshelf/commit/03a31cbf443f4f1cf1867810371db4b1672c71e9))
* **provider:** wire per-provider resilient clients through Build() ([f40db75](https://github.com/BlackForgeHQ/embookshelf/commit/f40db75a1ffd00422d57cdb1e7ae7f0306cc643f))


### Bug Fixes

* resolve lint issues (errcheck, gofmt) ([9eae62e](https://github.com/BlackForgeHQ/embookshelf/commit/9eae62e4d21805dd5e8594ad20e07c73d8b267d6))

## [0.1.1](https://github.com/BlackForgeHQ/embookshelf/compare/v0.1.0...v0.1.1) (2026-04-24)


### Bug Fixes

* **ci:** commit internal/staticfs/dist/.gitkeep so embed target exists ([89dca50](https://github.com/BlackForgeHQ/embookshelf/commit/89dca5019e64ccbe8430e80ada2f7a7769846faf))
* **ui:** reconcile lockfile + tsconfig + eslint after dep bumps ([0efa9e9](https://github.com/BlackForgeHQ/embookshelf/commit/0efa9e910cfe9b5aedd312da5235dc0501e39499))

## 0.1.0 (2026-04-24)

Initial public release. Future entries on this file are managed by
[release-please](https://github.com/googleapis/release-please) based on
conventional-commit messages landed on `main`.

### Highlights at 0.1.0

* Self-hosted multi-user digital library — Go backend + React (TanStack
  Start) SPA + Postgres, shipped as a single binary with the SPA embedded
  via `//go:embed`.
* EPUB + PDF readers, full-text search, per-user shelves and annotations.
* BookDrop import queue with polling watcher, metadata enrichment across
  four providers (Google Books, Open Library, Amazon, DuckDuckGo),
  configurable file-naming patterns.
* OIDC / SSO (Google, GitHub, generic) with PKCE and admin-controlled
  provider configuration.
* OPDS 1.2 catalog for e-readers, reMarkable device sync.
* OpenTelemetry export (traces, metrics, logs) via OTLP.
* CI/CD pipeline on GitHub Actions: PR gate, path-gated Playwright e2e,
  multi-arch GHCR image publish with SBOM + SLSA provenance on tag push,
  CodeQL + Trivy + dependency-review security scans, Dependabot, and
  release-please-driven versioning.
