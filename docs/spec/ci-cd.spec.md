# CI/CD — Feature Specification

> Automate verification (lint, typecheck, unit + integration + e2e tests, container build) for every pull request, publish a multi-arch image on each merge to `main`, and cut tagged release images with provenance and an SBOM on Git tags. Single-repo pipeline on GitHub Actions against `github.com/BlackForgeHQ/embookshelf`.

- **Status:** Draft
- **Scope:** `.github/workflows/` + repo-level config (CODEOWNERS, branch protection, Dependabot); no changes to application runtime code
- **Permission required:** repo `admin` to land the workflows and configure branch-protection / secrets
- **Entry point:** GitHub Actions — triggered on `pull_request`, `push` to `main`, and `push` of `v*` tags

---

## 1. Purpose

embookshelf today ships with a Dockerfile, a Makefile, and a growing test pyramid (Go unit tests under [internal/](internal), a Playwright suite under [e2e/](e2e), a React SPA under [ui/](ui)), but there is no CI: no workflow files exist in `.github/workflows/`, and merges to `main` depend on the contributor's local machine having run `make test` / `make build` correctly.

This spec defines the pipeline that replaces that trust with automation. Three triggers, four workflows, one container registry:

- **`pull_request`** → fast gate (≤ 10 min wall clock): Go lint + unit tests, UI typecheck + lint + unit tests, Go + UI build, migrations sanity. Merge blocks on failure.
- **`pull_request` (labeled `e2e` or touching e2e-sensitive paths)** → Playwright against the built binary + Postgres. Separate workflow so a UI-only typo doesn't pay the full e2e cost.
- **`push` to `main`** → everything above + Docker image build & publish to GHCR with `main` and `sha-<shortsha>` tags.
- **`push` tag `v*`** → semver-tagged multi-arch image (`linux/amd64` + `linux/arm64`) to GHCR with an SBOM (syft) and provenance attestation (SLSA v1).

Design choices worth flagging up front:

- **One image, one runtime**: we already ship a single self-contained binary with the SPA embedded via `//go:embed` ([README.md:57](README.md#L57)). The pipeline produces one `ghcr.io/blackforgehq/embookshelf` image; no separate UI image, no sidecar, no "API-only" variant.
- **GHCR, not Docker Hub**: GitHub-native, free for public/org packages, and auth is a built-in `GITHUB_TOKEN` — no extra secret plumbing.
- **No release train, tags are the release**: pushing `v1.2.3` publishes `ghcr.io/.../embookshelf:1.2.3`, `:1.2`, `:1`, and `:latest`. A Release note is generated from `git log` against the previous tag; no manual changelog file is required (see §11 for the open question on changelog tooling).
- **No deployment step**: we're not running a hosted SaaS. The pipeline publishes an image; operators pull it. "Continuous deployment" is out of scope for this spec — see §11.
- **Integration / e2e do not gate `main`-push alone**: the `main` workflow re-runs everything the PR ran plus the image build. Branch protection is what enforces the PR-level gates, not a separate `main`-push re-run.
- **Go toolchain versions are a single source of truth** — pulled from `go.mod`'s `go` directive ([go.mod:3](go.mod#L3), currently `1.25.0`) via `actions/setup-go@v5`'s `go-version-file`. Bumping Go once in `go.mod` updates CI + Dockerfile together (the Dockerfile is updated alongside in the same PR).

Non-goals (out of scope for this spec):

- Deployment to a managed environment (Fly.io / Kubernetes / ECS). No `deploy.yml`.
- Mobile app builds (no mobile app exists).
- Cross-repo orchestration. This is the only repo.
- Merge-queue / "bulldozer"-style auto-merge. Branch protection + required reviews is the whole policy.
- Release notes curation beyond `git log --format`. A later spec can add `release-please` or `changesets`.

---

## 2. User Stories

| # | As a … | I want to … | So that … |
|---|--------|-------------|-----------|
| 1 | Contributor | See lint + unit test results on my PR within a few minutes | I find out I broke something before context-switching away |
| 2 | Maintainer | Block merges that fail checks | `main` stays green without manual discipline |
| 3 | Reviewer | See a preview container image tagged with the PR's SHA | I can run the PR locally without a rebuild |
| 4 | Operator | Pull `ghcr.io/blackforgehq/embookshelf:1.2.3` for a specific release | I can pin a deployment to a known version |
| 5 | Operator | Pull `:latest` / `:main` for always-current builds | I can track `main` without tagging a release |
| 6 | Security lead | Get a CodeQL report + CVE alerts on dependencies | supply-chain issues surface before users file them |
| 7 | Contributor | Run the same commands locally that CI runs | my local green matches CI green |
| 8 | Release manager | Cut a release with one `git tag && git push --tags` | the image, SBOM, and release notes happen without extra steps |

---

## 3. Pipeline Topology

Four workflows, two shared reusable workflows, one registry. All files live under `.github/workflows/`.

### 3.1 Workflow inventory

| File | Trigger | Duration budget | Outcome |
|---|---|---|---|
| [ci.yml](.github/workflows/ci.yml) | `pull_request`, `push` to `main` | ≤ 10 min | Gate: go + ui lint/typecheck/unit/build + migration sanity |
| [e2e.yml](.github/workflows/e2e.yml) | `pull_request` (gated by paths + `e2e` label), `push` to `main` | ≤ 20 min | Gate: Playwright against built binary |
| [image.yml](.github/workflows/image.yml) | `push` to `main`, `push` tag `v*`, `workflow_dispatch` | ≤ 15 min | Publish: multi-arch image to GHCR |
| [security.yml](.github/workflows/security.yml) | `pull_request`, `push` to `main`, `schedule` (weekly Mon 06:00 UTC) | ≤ 15 min | Report: CodeQL (Go + JS/TS), Trivy scan of the built image, dependency review on PRs |

| File | Purpose |
|---|---|
| [_reusable-go.yml](.github/workflows/_reusable-go.yml) | Go setup + `go mod download` + test + build, cached. Called by `ci.yml` and `e2e.yml`. |
| [_reusable-ui.yml](.github/workflows/_reusable-ui.yml) | Bun setup + `bun install --frozen-lockfile` + lint / typecheck / unit test / build, cached. Called by `ci.yml` and `image.yml`. |

### 3.2 Trigger matrix

```
┌──────────────────────────┬─────┬──────┬──────────┬──────────┐
│ Trigger                  │ ci  │ e2e  │ image    │ security │
├──────────────────────────┼─────┼──────┼──────────┼──────────┤
│ pull_request (any)       │  ✓  │   △  │    —     │    ✓     │
│ push main                │  ✓  │   ✓  │   ✓      │    ✓     │
│ push tag v*              │  —  │   —  │   ✓ (rel)│    —     │
│ schedule (weekly)        │  —  │   —  │    —     │    ✓     │
│ workflow_dispatch        │  —  │   —  │   ✓      │    —     │
└──────────────────────────┴─────┴──────┴──────────┴──────────┘

△ = runs only when the PR touches paths in `paths:` filter (§4.2) OR carries the `e2e` label.
```

### 3.3 Concurrency

Every workflow sets `concurrency` keyed on the ref so a new push to the same branch cancels in-flight runs:

```yaml
concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: true
```

Exception: `image.yml` on a tag push does **not** cancel (each tag is uniquely named; cancellation would abandon a release mid-publish).

---

## 4. Workflows

### 4.1 `ci.yml` — fast PR gate

Runs on every PR and every `main` push. Fail-fast disabled so both Go and UI failures surface in the same run.

**Jobs** (parallel unless otherwise noted):

| Job | Runner | Key steps |
|---|---|---|
| `go-lint` | `ubuntu-latest` | `golangci-lint run` (v1.65+). Uses `golangci/golangci-lint-action@v6` with the `.golangci.yml` added alongside this spec (see §8). |
| `go-test` | `ubuntu-latest` | `go test -race -coverprofile=coverage.out ./...`. Uploads coverage as an artifact; no coverage gate yet (see §11). Go unit tests today live in [internal/provider/isbn_test.go](internal/provider/isbn_test.go), [internal/pattern/](internal/pattern), [internal/crypto/cipher_test.go](internal/crypto/cipher_test.go). |
| `go-build` | `ubuntu-latest` | `go build -o /dev/null ./...` — confirms every package compiles (including the two `cmd/` entry points). |
| `ui-lint` | `ubuntu-latest` | `bun run lint` in [ui/](ui). |
| `ui-typecheck` | `ubuntu-latest` | `bun run typecheck` in [ui/](ui). |
| `ui-test` | `ubuntu-latest` | `bun run test` (Vitest, when the first suite lands — today `bun run test` is wired in [ui/package.json](ui/package.json) but no specs ship yet). Allowed to pass with zero tests. |
| `ui-build` | `ubuntu-latest` | `bun run build`. Uploads the resulting `internal/staticfs/dist/` as an artifact so `go-build` in the same run can optionally pick it up for a binary smoke-build (see next row). |
| `binary-smoke` | `ubuntu-latest`, depends on `ui-build` | Downloads the SPA artifact, runs `make build`, confirms `./tmp/embookshelf --help` exits 0. Matches what the Dockerfile does, minus the container layer. |
| `migrations-sanity` | `ubuntu-latest`, service: `postgres:16-alpine` | Runs `go run ./cmd/migrate up` against an empty DB, then `go run ./cmd/migrate down` until version 0, asserting both directions execute without error. Uses the same DSN shape as [Makefile:4](Makefile#L4). |

All nine jobs must pass for the workflow to succeed.

### 4.2 `e2e.yml` — Playwright against the built binary

**Trigger filter** (PR-only gate; `main`-push always runs):

```yaml
on:
  pull_request:
    paths:
      - 'ui/**'
      - 'internal/handler/**'
      - 'internal/service/**'
      - 'internal/auth/**'
      - 'internal/migrator/migrations/**'
      - 'e2e/**'
      - 'Dockerfile'
      - 'Makefile'
      - '.github/workflows/e2e.yml'
  push:
    branches: [main]
```

PRs that don't match any `paths` entry skip the workflow entirely. A manual override is available via the `e2e` label on the PR (handled by a `if: contains(github.event.pull_request.labels.*.name, 'e2e')` condition on a duplicate `on.pull_request` stanza).

**Single job**: `playwright`, `ubuntu-latest`, 20 min timeout.

Sequence mirrors [e2e/playwright.config.ts](e2e/playwright.config.ts) + [e2e/global-setup.ts](e2e/global-setup.ts):

1. Checkout.
2. Set up Go (from `go.mod`), Bun (`oven-sh/setup-bun@v2`, pinned to `1.x`).
3. Start Postgres as a services container (`postgres:16-alpine`, matches [compose.dev.yml:3](compose.dev.yml#L3)), wait for `pg_isready`.
4. `make migrate` against the service DB.
5. `make seed` — the seed file [.github/seed.sql](.github/seed.sql) is the same one [Makefile:60](Makefile#L60) loads. Apply via `psql` directly (no docker-compose exec in CI).
6. `cd ui && bun install --frozen-lockfile && bun run build`.
7. `make build` → `./tmp/embookshelf`.
8. Start the binary in the background (`./tmp/embookshelf &`), wait for `/api/v1/healthcheck` on `:6060` (same readiness logic as [Makefile:80](Makefile#L80)).
9. `cd e2e && npm ci && npx playwright install --with-deps chromium`.
10. `npm test` (the Playwright config already flips to `reporter: [['github'], ['html']]` and `retries: 1` when `CI` is set, per [e2e/playwright.config.ts:13](e2e/playwright.config.ts#L13)).
11. On failure: upload `e2e/playwright-report/` and `e2e/test-results/` as artifacts; annotate via the `github` reporter.

Env the binary needs during e2e (mirrors [.env.example](.env.example)):

```
DATABASE_URL=postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable
EMBOOKSHELF_PORT=6060
MIGRATE_ON_START=false        # we already migrated in step 4
BOOKDROP_PATH=./bookdrop
DATA_PATH=./data
LOG_LEVEL=warn                # keep the CI log slim
OTEL_ENABLED=false
```

### 4.3 `image.yml` — publish to GHCR

**Triggers**:

- `push` to `main` → build + push `ghcr.io/blackforgehq/embookshelf:main` and `:sha-<shortsha>`. Not a release.
- `push` tag matching `v*` → build + push semver tags `:X.Y.Z`, `:X.Y`, `:X`, `:latest`. Generates SBOM + provenance. Creates a GitHub Release with autogenerated notes.
- `workflow_dispatch` → manual rebuild of `main` (useful when a base-image CVE drops and we want a rebuild without a new commit).

**Single job**: `docker`, `ubuntu-latest`.

Steps:

1. Checkout at the triggering ref.
2. `docker/setup-qemu-action@v3` (for arm64 cross-build).
3. `docker/setup-buildx-action@v3`.
4. `docker/login-action@v3` against `ghcr.io` using `GITHUB_TOKEN` (package: write perm).
5. `docker/metadata-action@v5` computes tags + OCI labels from the trigger. Rules:
   - `type=ref,event=branch` → `main`
   - `type=sha,prefix=sha-,format=short` → `sha-<7chars>` (PR builds could consume this if we extended §4.1 with a publish step, see §11).
   - `type=semver,pattern={{version}}` / `{{major}}.{{minor}}` / `{{major}}` — only on tag pushes.
   - `type=raw,value=latest,enable={{is_default_branch_semver_tag}}` — compute via `enable` expression to skip `latest` on pre-release tags (`v1.2.3-rc.1`).
6. `docker/build-push-action@v6` with:
   - `platforms: linux/amd64,linux/arm64`
   - `cache-from: type=gha`, `cache-to: type=gha,mode=max`
   - `provenance: true` (tag builds only), `sbom: true` (tag builds only)
   - `build-args: VERSION=${{ github.ref_name }} COMMIT=${{ github.sha }}` — Dockerfile doesn't consume these today but we add matching `ARG` + `LABEL` lines in the same PR so image metadata carries the version.
7. On tag push: `anchore/sbom-action@v0` produces a `spdx-json` SBOM as a release asset (the inline `sbom: true` SBOM is OCI-embedded; the release-attached one is for humans / scanners).
8. On tag push: `softprops/action-gh-release@v2` creates the GitHub Release with `generate_release_notes: true`.

### 4.4 `security.yml` — CodeQL + image scan + deps

Three parallel jobs:

| Job | Runs | Action |
|---|---|---|
| `codeql-go` | PR + main + weekly | `github/codeql-action/init@v3` with `languages: go`, then `autobuild`, then `analyze`. SARIF uploaded to code scanning. |
| `codeql-ts` | PR + main + weekly | Same, `languages: javascript-typescript`. Only scans [ui/src](ui/src) + [e2e/tests](e2e/tests) — excludes `node_modules` and `ui/dist`. |
| `dependency-review` | PR only | `actions/dependency-review-action@v4` — fails the PR on high-severity vulns in newly added deps. |
| `trivy-image` | main push + weekly | Builds the image (reusing the same Dockerfile, single-arch amd64 for speed), scans with `aquasecurity/trivy-action@master` at `severity: CRITICAL,HIGH`, uploads SARIF. |

`trivy-image` is allowed to run in parallel with `image.yml` on `main`-push; they don't share artifacts. CodeQL results are uploaded even on PRs but don't block the PR — we surface issues as checks + alerts, not gates.

---

## 5. Caching Strategy

Cache hits are the difference between a 3 min CI run and a 10 min one. The pipeline caches at three layers:

### 5.1 Go

Use `actions/setup-go@v5` with `cache: true` and `cache-dependency-path: go.sum` — this handles `GOMODCACHE` (`~/go/pkg/mod`) + `GOCACHE` (`~/.cache/go-build`) keyed on `go.sum` hash. No hand-rolled `actions/cache` needed.

### 5.2 UI (Bun)

`oven-sh/setup-bun@v2` does not cache automatically. Add:

```yaml
- uses: actions/cache@v4
  with:
    path: ~/.bun/install/cache
    key: bun-${{ runner.os }}-${{ hashFiles('ui/bun.lock') }}
    restore-keys: bun-${{ runner.os }}-
```

### 5.3 Docker

`docker/build-push-action@v6` with `cache-from: type=gha` / `cache-to: type=gha,mode=max`. GHA cache is scoped per-branch; `main`-built cache is read by PR builds of the same layer set, which is the hot path for `go mod download` + `bun install`.

### 5.4 Playwright browsers

`actions/cache@v4` on `~/.cache/ms-playwright` keyed on `e2e/package-lock.json` hash. Saves ~30s per run.

---

## 6. Branch Protection & CODEOWNERS

Not part of a workflow file, but part of this spec because the workflows are only as useful as the protection rules that require them.

**`main` branch protection** (configured in Repo → Settings → Branches):

- **Require a pull request** before merging: ✓, 1 approving review, dismiss stale reviews on new commits.
- **Require status checks** before merging: ✓ — required list:
  - `ci / go-lint`
  - `ci / go-test`
  - `ci / go-build`
  - `ci / ui-lint`
  - `ci / ui-typecheck`
  - `ci / ui-build`
  - `ci / binary-smoke`
  - `ci / migrations-sanity`
  - `security / codeql-go`
  - `security / codeql-ts`
  - `security / dependency-review`
- **`e2e / playwright`** is **not** in the required list — it's conditional on paths. Requiring it would block PRs that skip it entirely. The `e2e` label (§4.2) lets reviewers demand an e2e run when a PR looks dangerous despite touching "safe" paths.
- **Require linear history**: ✓. Squash or rebase only; no merge commits.
- **Require signed commits**: ✗ (not today; see §11).
- **Require conversation resolution**: ✓.
- **Include administrators**: ✓ — admins don't bypass CI either.
- **Allow force pushes**: ✗.
- **Allow deletions**: ✗.

**[.github/CODEOWNERS](.github/CODEOWNERS)** (new):

```
*                       @BlackForgeHQ/maintainers
/.github/               @BlackForgeHQ/maintainers
/internal/migrator/     @BlackForgeHQ/maintainers   # migrations are load-bearing; require a human pair of eyes
/spec/                  @BlackForgeHQ/maintainers
```

---

## 7. Secrets, Permissions & Environments

### 7.1 Token permissions

All workflows declare minimum `permissions:` at the workflow level:

```yaml
permissions:
  contents: read           # checkout
```

Jobs that need more escalate locally:

- `image.yml` → `packages: write` (push to GHCR), `id-token: write` (provenance), `attestations: write` (provenance).
- `security.yml` → `security-events: write` (upload SARIF), `actions: read`.
- Tag-push release job → additionally `contents: write` (create Release).

No long-lived PATs. The only GHCR credential is the built-in `GITHUB_TOKEN`; `packages: write` is granted by setting the repo's Actions → General → Workflow permissions to "Read and write" + allowing GHCR attach on the package page after the first push.

### 7.2 Secrets

None at launch. The design is intentionally secret-free until someone asks for:

- Code-signing (Sigstore keyless already covers container signing without secrets; see §11).
- A private container registry mirror.
- OTEL exporter targeting an external collector from CI. Not needed — CI runs are short-lived, traces aren't interesting.

### 7.3 Environments

One environment: `release` (used by `image.yml` on tag push). Gives us a dashboard of releases in the UI and a place to add an approval gate later without rewiring the workflow.

---

## 8. Local Parity — what CI runs that a dev can run

The pipeline commits to reusing the Makefile instead of growing a parallel set of `yarn run` / ad-hoc steps. Every CI step maps to a Make target or a documented one-liner, so `make ci-local` (new target) reproduces the PR gate:

```makefile
.PHONY: ci-local
ci-local: ## Run the same checks CI runs on a PR
	$(MAKE) go-lint
	$(MAKE) go-test
	$(MAKE) ui-install
	$(MAKE) ui-lint
	$(MAKE) ui-typecheck
	$(MAKE) ui-build
	$(MAKE) build
```

New sub-targets added in the same PR:

- `go-lint`: `go tool golangci-lint run` (added to the `tool` directive in [go.mod:111](go.mod#L111) — the existing pattern for `air`).
- `ui-lint`: `cd ui && bun run lint`.
- `ui-typecheck`: `cd ui && bun run typecheck`.
- `ui-test`: `cd ui && bun run test`.

[.golangci.yml](.golangci.yml) (new, repo root) configures a conservative linter set — `errcheck`, `govet`, `ineffassign`, `staticcheck`, `unused`, `gosimple`, `gofmt`, `goimports`, `misspell`. No opinionated style linters (no `revive`, no `stylecheck`) in the first pass; the codebase has its own conventions and we're not retrofitting.

---

## 9. Container Image Strategy

### 9.1 Registry

Single registry: `ghcr.io/blackforgehq/embookshelf`. Visibility matches the repo (public once the org flips the repo to public). Packages tab of the repo lists every tag.

### 9.2 Tag scheme

| Source ref | Tags applied |
|---|---|
| `main` push | `main`, `sha-<7>` |
| `v1.2.3` tag | `1.2.3`, `1.2`, `1`, `latest` |
| `v1.2.3-rc.1` tag | `1.2.3-rc.1` only (no `:latest`, no `:1.2`, no `:1`) |
| `workflow_dispatch` on `main` | `main`, `sha-<7>` (same as a normal main push) |

`:latest` tracks the newest stable release, not `main`. Operators who want the bleeding edge pull `:main`.

### 9.3 Architectures

`linux/amd64` + `linux/arm64`. No 386, no armv7 — the runtime distroless base (`gcr.io/distroless/static-debian12:nonroot`, [Dockerfile:26](Dockerfile#L26)) supports both, and both are what our deployment targets run.

### 9.4 Labels & metadata

`docker/metadata-action@v5` fills OCI annotations (`org.opencontainers.image.revision`, `.version`, `.source`, `.created`, `.licenses`, `.title`, `.description`) from repo metadata. The Dockerfile grows matching `ARG VERSION` / `ARG COMMIT` / `LABEL` lines so `docker inspect` on a running image shows which commit it was built from.

### 9.5 Provenance & SBOM

- **Provenance** (SLSA v1): emitted by `build-push-action`'s `provenance: true` on tag builds. Verifiable via `cosign` or `slsa-verifier`.
- **SBOM**: SPDX JSON, two copies:
  - Inline (OCI-embedded) via `build-push-action`'s `sbom: true`.
  - Standalone, attached to the GitHub Release via `anchore/sbom-action@v0` so operators can diff SBOMs without pulling the image.

### 9.6 Image signing

Keyless Sigstore signing with `cosign` is a §11 follow-up; not in the first iteration. Provenance + SBOM cover the supply-chain ask today.

---

## 10. Release Process

A release is a single push of a `v`-prefixed tag. Everything else is automation.

### 10.1 Happy path

```bash
git switch main && git pull
# semver pick:
git tag -s v1.2.3 -m "v1.2.3"
git push origin v1.2.3
```

What happens next, in order, automatically:

1. `image.yml` triggers on the tag.
2. `docker/metadata-action` derives `1.2.3`, `1.2`, `1`, `latest`.
3. `docker/build-push-action` builds multi-arch, pushes all four tags to GHCR, emits provenance + embedded SBOM.
4. `anchore/sbom-action` attaches `embookshelf_1.2.3_spdx.json` to the release.
5. `softprops/action-gh-release` creates the GitHub Release with `generate_release_notes: true` — GitHub walks `git log` from the previous tag and groups PRs by label (`feat`, `fix`, etc.) per `.github/release.yml` (new).

### 10.2 Pre-releases

A tag like `v1.3.0-rc.1` publishes only `:1.3.0-rc.1`. The Release is created with `prerelease: true`. `:latest`, `:1.3`, and `:1` are **not** updated.

### 10.3 Re-releasing a bad tag

If `v1.2.3` ships broken:

1. Delete the Release and the tag (GitHub UI or `git push origin :refs/tags/v1.2.3`).
2. Deleting the tag does **not** remove the GHCR image — `:1.2.3` stays, intentionally (deleting pulled-from images breaks downstream deployments).
3. Cut `v1.2.4` with the fix.

We never reuse a version number; semver is append-only.

### 10.4 Hotfix branches

Not modeled in this spec. Hotfixes cherry-pick to `main` and tag `v1.2.4`. A long-lived `release/1.2` branch + back-porting is a §11 follow-up when we actually need it.

---

## 11. Open / Future Work

1. **CD to a staging environment** — `deploy.yml` that SSHes / kubectl-applies the new image against a staging cluster on `main`-push, with a manual gate to promote to prod. Out of scope until we host an instance we control.
2. **Release-please / changesets** — autogenerated notes from `git log` are fine for now but don't enforce conventional commits or structured changelogs. Revisit once PR volume grows.
3. **Coverage gate** — `go-test` already emits `coverage.out`. Adding a floor (e.g., no regression from `main`) needs a coverage service (Codecov / Coveralls) or a hand-rolled diff action.
4. **Sigstore keyless container signing** — `cosign sign` on tag-push plus a verification doc. Natural follow-on to §9.5.
5. **Preview images on PRs** — extend `ci.yml` or `image.yml` to push `ghcr.io/.../embookshelf:pr-<N>` for every PR. Costs GHCR storage; opt-in behind a `preview` label.
6. **Performance regression gate** — benchmark the hot paths (search, scan, BookDrop ingest) and fail PRs that regress > X%. Needs a stable runner; GitHub's shared runners are too noisy.
7. **Fuzz testing in CI** — `go test -fuzz` on [internal/pattern](internal/pattern) and [internal/provider](internal/provider) for a bounded duration. Today we don't run fuzz at all.
8. **Signed commits enforcement** — currently off to keep the bar low for contributors. Once we grow past three maintainers, flip on and document the GPG/SSH-key setup.
9. **Upgrade of `compose.dev.yml` migrate image** — [compose.dev.yml:21](compose.dev.yml#L21) pins `golang:1.24-alpine` while `go.mod` and the production Dockerfile are on `1.25`. CI should surface this drift; an added job `compose-go-version-drift` can grep the compose file and fail when it doesn't match `go.mod`.
10. **Self-hosted runners** — if GHA costs become the bottleneck on arm64 builds (the largest step in `image.yml`), a small arm64 runner on Hetzner would halve tag-push time. Deferred until pain justifies the ops overhead.

---

## 12. Testing the Pipeline Itself

CI for CI is a real problem — a broken workflow file can't stop itself from breaking. Three protections:

1. **`actionlint`** — `rhysd/actionlint` runs as a step inside `ci.yml` (`go-lint` job, trailing step) against every `.github/workflows/*.yml`. Catches missing `needs:` refs, typoed expressions, undocumented actions.
2. **`shellcheck`** — against every heredoc / multi-line `run:` block via `reviewdog/action-shellcheck@v1`. Cheap, catches quoting bugs.
3. **Dry-run on workflow edits** — any PR touching `.github/workflows/**` runs its own modified workflows (that's how GitHub Actions works), so a broken `ci.yml` in a PR fails `ci.yml` in that same PR before it lands. The corollary is that changes to `image.yml` don't get tested on PR — only on `main`-push + tag. Mitigation: use `act` locally (documented in the repo's [docs/](docs) folder alongside this spec) for image.yml dry runs, and add a `workflow_dispatch` with a `dry_run` input that skips the final push step.

---

## 13. Validation Summary

| Layer | Rule |
|---|---|
| PR gate | `ci.yml` must pass; `e2e.yml` must pass when triggered by paths or `e2e` label |
| Branch protection | `main` merges require review + the nine required status checks from §6 |
| Release gate | Tag push triggers `image.yml`; failure leaves the tag without an image, and the cut can be retried via `workflow_dispatch` |
| Secrets | No workflow can read repo secrets; `GITHUB_TOKEN` only, scoped per-job |
| Permissions | Default read-only; escalations are job-local |
| Image | Multi-arch (amd64 + arm64), distroless runtime, SBOM + provenance on tags |

---

## 14. Security Considerations

- **Pinned actions**: all third-party actions are pinned to a major version (`@v5`, `@v6`) rather than `@latest`. A §11 follow-up is to pin to full commit SHAs + Dependabot-update them, which is the belt-and-braces supply-chain stance. Today: major-tag pinning + Dependabot watching `.github/workflows`.
- **Dependabot**: enabled for `gomod`, `npm` (ui + e2e), `docker`, and `github-actions` ecosystems via [.github/dependabot.yml](.github/dependabot.yml) (new). Weekly cadence; auto-merges on green CI require a human approval (no automerge in the first iteration).
- **Secret leakage**: zero secrets configured; even `GITHUB_TOKEN` is scoped per-workflow. Dependency-review-action blocks PRs that add a high-severity CVE dep.
- **CodeQL**: Go + JS/TS. Weekly scheduled runs ensure we catch newly disclosed rules even when `main` is quiet.
- **Trivy image scan**: runs weekly against the latest `main` image so base-image CVEs (the `distroless/static-debian12` line) surface without waiting for a release.
- **Third-party runner**: all jobs run on `ubuntu-latest` GitHub-hosted runners. No self-hosted runners, no runner-group secrets.
- **Tag protection**: `v*` tags should be protected via repo Settings → Tags so a compromised contributor account can't cut a rogue release. Flagged under §11 item 8 adjacent (signed tags).

---

## 15. Cross-feature Interactions

- **Migrations** ([internal/migrator/](internal/migrator)): `migrations-sanity` job (§4.1) executes every up + down, which means a broken down-migration fails CI even when the server's `MIGRATE_ON_START=true` path would never exercise it in production. Keeps the reversibility claim honest.
- **Seed** ([.github/seed.sql](.github/seed.sql), referenced by [Makefile:60](Makefile#L60)): the e2e job applies this seed to get the `admin@local` user the Playwright global-setup ([e2e/global-setup.ts:17](e2e/global-setup.ts#L17)) logs in as. Changes to the seed that rename the admin or change the password break e2e — by design.
- **OTEL** ([docs/architecture.md](docs/architecture.md)): CI runs with `OTEL_ENABLED=false`; traces aren't useful and the exporter would fail to connect.
- **Playwright config** ([e2e/playwright.config.ts:13](e2e/playwright.config.ts#L13)): already reads `process.env.CI`, so the GHA default (`CI=true`) flips `forbidOnly`, `retries=1`, and the `github` reporter without any changes.

---

## 16. Key References

- Build: [Makefile](Makefile), [Dockerfile](Dockerfile), [compose.dev.yml](compose.dev.yml)
- Go module: [go.mod](go.mod)
- UI package: [ui/package.json](ui/package.json)
- E2E package: [e2e/package.json](e2e/package.json), [e2e/playwright.config.ts](e2e/playwright.config.ts), [e2e/global-setup.ts](e2e/global-setup.ts), [e2e/fixtures/constants.ts](e2e/fixtures/constants.ts)
- Env contract: [.env.example](.env.example)
- Seed: [.github/seed.sql](.github/seed.sql)
- Docs: [docs/architecture.md](docs/architecture.md), [docs/adr/0006-playwright-e2e-against-built-binary.md](docs/adr/0006-playwright-e2e-against-built-binary.md), [README.md](README.md)
- GitHub Actions docs: <https://docs.github.com/actions> (branch protection: Settings → Branches; Actions permissions: Settings → Actions → General)
- Related specs: [spec/library-creation.spec.md](spec/library-creation.spec.md), [spec/s3-storage.spec.md](spec/s3-storage.spec.md)

---

## 17. Glossary

- **Gate workflow** — a workflow whose failure blocks merge. `ci.yml` is one; `e2e.yml` is one when triggered; `image.yml` is never one (it runs *after* merge).
- **Publish workflow** — a workflow that produces a release artifact (container image, SBOM). `image.yml`.
- **GHCR** — GitHub Container Registry, `ghcr.io/<org>/<repo>`, authenticated with the built-in `GITHUB_TOKEN`.
- **Reusable workflow** — a `.yml` file under `.github/workflows/` invoked by another workflow via `uses: ./.github/workflows/_reusable-x.yml`. Lets `ci.yml` and `e2e.yml` share Go setup without copy-paste.
- **Provenance** — SLSA v1 attestation describing how the image was built (source commit, workflow file, runner). Verifiable post-pull.
- **SBOM** — Software Bill of Materials; SPDX JSON listing every Go module and npm package in the image. One embedded in the image, one attached to the Release.
- **Required status check** — a GitHub branch-protection concept: a named check (e.g. `ci / go-test`) that must pass on the PR's head commit before the Merge button enables.
