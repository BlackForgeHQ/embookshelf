# Changelog

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
