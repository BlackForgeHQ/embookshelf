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
migrate-down: ## Revert the most recent migration
	DATABASE_URL="$(DB_URL)" go run ./cmd/migrate down

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

.PHONY: test
test: ## Run Go tests
	go test ./...

GOLANGCI_LINT_VERSION ?= v2.11.4

.PHONY: go-lint
go-lint: ## Run golangci-lint (pinned version fetched via `go run` if not on PATH)
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --timeout=5m; \
	else \
		echo "golangci-lint not on PATH; running $(GOLANGCI_LINT_VERSION) via go run"; \
		go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run --timeout=5m; \
	fi

.PHONY: ui-lint
ui-lint: ## Lint the UI (ESLint)
	cd ui && bun run lint

.PHONY: ui-typecheck
ui-typecheck: ## Typecheck the UI (tsc --noEmit)
	cd ui && bun run typecheck

.PHONY: ui-test
ui-test: ## Run UI unit tests (Vitest)
	cd ui && bun run test

.PHONY: ci-local
ci-local: go-lint test ui-install ui-lint ui-typecheck ui-test ui-build build ## Run the same checks CI runs on a PR

.PHONY: e2e-install
e2e-install: ## Install Playwright deps + Chromium
	cd e2e && npm install && npx playwright install --with-deps chromium

.PHONY: e2e
e2e: ## Run Playwright specs against the running dev stack (needs `make up`)
	cd e2e && npm test

.PHONY: e2e-ui
e2e-ui: ## Playwright UI mode for iterating on specs
	cd e2e && npm run test:ui
