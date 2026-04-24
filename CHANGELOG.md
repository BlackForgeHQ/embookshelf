# Changelog

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
