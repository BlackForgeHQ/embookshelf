# embookshelf — dev Makefile
SHELL := /bin/bash

DB_URL ?= postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable

.PHONY: help
help: ## List targets
	@awk 'BEGIN{FS=":.*?## "} /^[a-zA-Z_-]+:.*?## /{printf "  %-18s %s\n",$$1,$$2}' $(MAKEFILE_LIST)

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

.PHONY: ui-install
ui-install: ## Install UI deps
	cd ui && bun install

.PHONY: ui-dev
ui-dev: ## Vite dev server (proxies /api, /opds → Go)
	cd ui && bun run dev

.PHONY: ui-build
ui-build: ## Build UI + sync shell into internal/staticfs/dist/
	cd ui && bun run build

.PHONY: db-up
db-up: ## Start Postgres via compose
	docker compose -f compose.dev.yml up -d postgres

.PHONY: db-down
db-down: ## Stop Postgres
	docker compose -f compose.dev.yml down

# s3-up starts the store AND provisions the bucket with versioning on.
# Versioning is not decoration: the conformance suite's CapVersioning row
# can only assert that deleting an older version leaves the current one
# readable where the store actually keeps versions, and against an
# unversioned bucket a backend that drops the version id looks exactly
# like an honest one (#270). CI calls this target rather than restating
# the steps, so the gate is the same in both places.
#
# `mc` ships inside the MinIO image (its healthcheck uses it), so this
# needs no second image. The alias is set here rather than trusted from
# the image, whose built-in `local` alias carries no credentials.
.PHONY: s3-up
s3-up: ## Start the dev MinIO (S3 API :9000, console :9001), wait, and make the test bucket versioned
	docker compose -f compose.dev.yml up -d --wait --wait-timeout 120 minio
	@docker compose -f compose.dev.yml exec -T minio sh -c '\
		mc alias set embookshelf-test-store http://127.0.0.1:9000 "$$MINIO_ROOT_USER" "$$MINIO_ROOT_PASSWORD" >/dev/null && \
		mc mb --ignore-existing embookshelf-test-store/$(TEST_S3_BUCKET) >/dev/null && \
		mc version enable embookshelf-test-store/$(TEST_S3_BUCKET) >/dev/null' \
		|| { echo "error: could not enable versioning on bucket $(TEST_S3_BUCKET)"; exit 1; }
	@echo "minio ready: bucket $(TEST_S3_BUCKET) is versioned"

.PHONY: s3-down
s3-down: ## Stop the dev MinIO
	docker compose -f compose.dev.yml stop minio

.PHONY: converter-up
converter-up: ## Build + start the converter extension (POST /convert on :6070, ADR-0033)
	docker compose -f compose.dev.yml up -d --build --wait --wait-timeout 300 converter

.PHONY: converter-stop
converter-stop: ## Stop the converter extension
	docker compose -f compose.dev.yml stop converter

.PHONY: converter-test
converter-test: ## Run the converter crate's tests (needs a local Rust toolchain)
	cd extensions/converter && cargo test

.PHONY: obs-up
obs-up: ## Start grafana/otel-lgtm (OTLP :4317/:4318, Grafana UI :3001)
	docker compose -f compose.dev.yml up -d otel-lgtm

.PHONY: obs-down
obs-down: ## Stop grafana/otel-lgtm
	docker compose -f compose.dev.yml stop otel-lgtm

.PHONY: obs-logs
obs-logs: ## Tail grafana/otel-lgtm logs
	docker compose -f compose.dev.yml logs -f otel-lgtm

.PHONY: migrate
migrate: ## Apply all pending migrations
	DATABASE_URL="$(DB_URL)" go run ./cmd/migrate up

.PHONY: migrate-down
migrate-down: ## Revert the most recent migration (add ARGS=-all to revert every one)
	DATABASE_URL="$(DB_URL)" go run ./cmd/migrate down $(ARGS)

.PHONY: migrate-version
migrate-version: ## Show current migration version
	DATABASE_URL="$(DB_URL)" go run ./cmd/migrate version

.PHONY: seed
seed: ## Load dev seed data (runs psql inside the postgres container)
	docker compose -f compose.dev.yml exec -T postgres \
		psql -U embookshelf -d embookshelf < scripts/seed.sql

.PHONY: build
build: ui-build ## Build the server binary (includes embedded SPA)
	go build -o ./tmp/embookshelf ./cmd/embookshelf

.PHONY: release
release: ui-build ## Production build: stripped, version+commit baked in via ldflags
	CGO_ENABLED=0 go build -trimpath \
		-ldflags="-s -w -X main.version=$(IMAGE_VERSION) -X main.commit=$(IMAGE_COMMIT)" \
		-o ./tmp/embookshelf ./cmd/embookshelf

.PHONY: tag
tag: ## Build and run embookshelf-tag (pass ARGS="-dry-run" for dry run)
	go build -o ./tmp/embookshelf-tag ./cmd/embookshelf-tag
	./tmp/embookshelf-tag $(ARGS)

# ---- Docker image ----------------------------------------------------------
# Mirrors the shape of .github/workflows/image.yml: VERSION + COMMIT get
# baked into the binary via -ldflags and into OCI labels. Override
# IMAGE_NAME / IMAGE_PLATFORMS from the CLI for ad-hoc builds.
IMAGE_NAME       ?= embookshelf
IMAGE_VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
IMAGE_COMMIT     ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
IMAGE_SHA_SHORT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

.PHONY: docker-build
docker-build: ## Build the container image for the host arch and load it into docker
	docker buildx build \
		--build-arg VERSION=$(IMAGE_VERSION) \
		--build-arg COMMIT=$(IMAGE_COMMIT) \
		--tag $(IMAGE_NAME):dev \
		--tag $(IMAGE_NAME):sha-$(IMAGE_SHA_SHORT) \
		--load \
		.

.PHONY: docker-build-multi
docker-build-multi: ## Multi-arch (amd64+arm64) build matching CI publish; doesn't load (buildx limitation)
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--build-arg VERSION=$(IMAGE_VERSION) \
		--build-arg COMMIT=$(IMAGE_COMMIT) \
		--tag $(IMAGE_NAME):dev \
		--tag $(IMAGE_NAME):sha-$(IMAGE_SHA_SHORT) \
		.

.PHONY: docker-run
docker-run: ## Run the last-built :dev image against the dev Postgres (expects `make db-up`)
	docker run --rm -it \
		--network host \
		-e DATABASE_URL=postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable \
		-e EMBOOKSHELF_PORT=6060 \
		-v $(CURDIR)/data:/data \
		-v $(CURDIR)/bookdrop:/bookdrop \
		-e DATA_PATH=/data \
		-e BOOKDROP_PATH=/bookdrop \
		$(IMAGE_NAME):dev

.PHONY: run
run: ## Run the server (expects SPA already built)
	go run ./cmd/embookshelf

.PHONY: dev
dev: ## Live-reload backend via `go tool air`
	go tool air

.PHONY: up
up: ## Run backend (air) + UI (Vite) together — backend first, UI once :6060 is up
	@trap 'kill 0' EXIT INT TERM; \
	$(MAKE) --no-print-directory dev & \
	backend_pid=$$!; \
	printf 'waiting for backend on :6060'; \
	for i in $$(seq 1 60); do \
		if curl -fsS http://localhost:6060/api/v1/healthcheck >/dev/null 2>&1; then \
			printf ' ✓\n'; \
			break; \
		fi; \
		if ! kill -0 $$backend_pid 2>/dev/null; then \
			printf '\nbackend exited before becoming ready\n'; \
			exit 1; \
		fi; \
		printf '.'; \
		sleep 0.5; \
	done; \
	if ! curl -fsS http://localhost:6060/api/v1/healthcheck >/dev/null 2>&1; then \
		printf '\nbackend healthcheck did not respond within 30s — aborting\n'; \
		exit 1; \
	fi; \
	$(MAKE) --no-print-directory ui-dev & \
	wait

.PHONY: up-otlp
up-otlp: obs-up ## Same as `up` but exports OTLP from backend AND browser to grafana/otel-lgtm (UI on :3001)
	@trap 'kill 0' EXIT INT TERM; \
	OTEL_ENABLED=true \
	OTEL_SERVICE_NAME=embookshelf \
	OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317 \
	OTEL_EXPORTER_OTLP_PROTOCOL=grpc \
	OTEL_EXPORTER_OTLP_INSECURE=true \
	$(MAKE) --no-print-directory dev & \
	backend_pid=$$!; \
	printf 'waiting for backend on :6060'; \
	for i in $$(seq 1 60); do \
		if curl -fsS http://localhost:6060/api/v1/healthcheck >/dev/null 2>&1; then \
			printf ' ✓\n'; \
			break; \
		fi; \
		if ! kill -0 $$backend_pid 2>/dev/null; then \
			printf '\nbackend exited before becoming ready\n'; \
			exit 1; \
		fi; \
		printf '.'; \
		sleep 0.5; \
	done; \
	if ! curl -fsS http://localhost:6060/api/v1/healthcheck >/dev/null 2>&1; then \
		printf '\nbackend healthcheck did not respond within 30s — aborting\n'; \
		exit 1; \
	fi; \
	VITE_OTEL_ENABLED=true \
	VITE_OTEL_SERVICE_NAME=embookshelf-ui \
	$(MAKE) --no-print-directory ui-dev & \
	wait

TEST_PG_DSN ?= postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable

.PHONY: test
test: test-db ## Run Go tests (starts the dev Postgres if needed)
	TEST_DATABASE_URL='$(TEST_PG_DSN)' go test ./...

# The S3 arm of the storage conformance suite (ADR-0030 §3). `make test`
# leaves TEST_S3_ENDPOINT unset, so the arm skips there and a developer
# with no object store is never hard-failed; this target starts one and
# sets the variable, which is exactly what CI does. Point it elsewhere
# with `make test-s3 TEST_S3_ENDPOINT=… TEST_S3_AK=… TEST_S3_SK=…`.
TEST_S3_ENDPOINT ?= http://localhost:9000
TEST_S3_BUCKET   ?= embookshelf-test
TEST_S3_AK       ?= embookshelf
TEST_S3_SK       ?= embookshelf

# Whole storage tree, not one -run: the S3 arm is the conformance suite
# *and* the streaming measurements next to it, and CI runs `go test ./...`
# with these variables set, so anything narrower here is a local gate that
# checks less than the remote one — the drift #227 set out to close.
#
# STORAGETEST_VERSIONED_STORE says the bucket s3-up just provisioned keeps
# versions, which is what makes the suite's versioning row assert
# something a version-id-dropping backend can fail.
.PHONY: test-s3
test-s3: s3-up ## Run the storage suite against S3 (starts the dev MinIO, versioned bucket)
	TEST_S3_ENDPOINT='$(TEST_S3_ENDPOINT)' \
	TEST_S3_BUCKET='$(TEST_S3_BUCKET)' \
	TEST_S3_AK='$(TEST_S3_AK)' \
	TEST_S3_SK='$(TEST_S3_SK)' \
	STORAGETEST_VERSIONED_STORE=1 \
	go test -race -count=1 ./internal/storage/...

# Repo tests need a real Postgres — they refuse to skip, because a
# skipped integration test is an unrun one (ADR-0023).
.PHONY: test-db
test-db: ## Start the dev Postgres and wait for it to accept connections
	@if ! docker compose -f compose.dev.yml ps postgres --status running -q >/dev/null 2>&1 \
	   || [ -z "$$(docker compose -f compose.dev.yml ps postgres --status running -q 2>/dev/null)" ]; then \
		echo "starting postgres (compose.dev.yml) …"; \
		docker compose -f compose.dev.yml up -d postgres >/dev/null 2>&1 || { \
			echo ""; \
			echo "error: could not start Postgres, which repo tests require."; \
			echo "  Start one yourself and re-run with:"; \
			echo "    make test TEST_PG_DSN='postgres://…'"; \
			exit 1; \
		}; \
	fi
	@for i in $$(seq 1 30); do \
		docker compose -f compose.dev.yml exec -T postgres pg_isready -U embookshelf >/dev/null 2>&1 && exit 0; \
		sleep 1; \
	done; \
	echo "error: Postgres did not become ready in 30s"; exit 1

# The one place the linter version is written. CI reads it from here via
# `make print-golangci-version` rather than restating it, and CLAUDE.md
# points at this line rather than naming a version — a pin restated by
# hand is a pin that drifts, which is how the docs came to name v2.11.4
# while this said v2.12.2 (#187).
GOLANGCI_LINT_VERSION ?= v2.12.2

.PHONY: print-golangci-version
print-golangci-version: ## Print the pinned golangci-lint version (used by CI)
	@echo $(GOLANGCI_LINT_VERSION)

.PHONY: go-lint
go-lint: ## Run golangci-lint (pinned version fetched via `go run` if not on PATH)
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --timeout=5m; \
	else \
		echo "golangci-lint not on PATH; running $(GOLANGCI_LINT_VERSION) via go run"; \
		go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run --timeout=5m; \
	fi

.PHONY: ui-lint
ui-lint: ## Lint the UI (Biome)
	cd ui && bun run lint

.PHONY: ui-typecheck
ui-typecheck: ## Typecheck the UI (tsc --noEmit)
	cd ui && bun run typecheck

.PHONY: ui-test
ui-test: ## Run UI unit tests (Vitest)
	cd ui && bun run test

# test-s3 is in here because ci-local is what a developer runs before
# pushing, and the workflow's go-test job sets TEST_S3_ENDPOINT — so
# without it the local gate and the remote gate check different things,
# which is the drift #227 closed and #270 found reopened one layer up.
# `test` leaves the S3 arm skipping, so the two targets together are the
# workflow's job: everything once, plus the storage tree again with a
# real object store behind it. It costs a docker compose up and a few
# seconds.
.PHONY: ci-local
ci-local: go-lint test test-s3 ui-install ui-lint ui-typecheck ui-test ui-build build ## Run the same checks CI runs on a PR

.PHONY: e2e-install
e2e-install: ## Install Playwright deps + Chromium
	cd e2e && npm install && npx playwright install --with-deps chromium

.PHONY: e2e
e2e: ## Run Playwright specs against the running dev stack (needs `make up`)
	cd e2e && npm test

.PHONY: e2e-ui
e2e-ui: ## Playwright UI mode for iterating on specs
	cd e2e && npm run test:ui
