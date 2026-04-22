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
up: ## Run backend (air) + UI (Vite) together — Ctrl-C stops both
	@trap 'kill 0' EXIT INT TERM; \
	$(MAKE) --no-print-directory dev & \
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
	VITE_OTEL_ENABLED=true \
	VITE_OTEL_SERVICE_NAME=embookshelf-ui \
	$(MAKE) --no-print-directory ui-dev & \
	wait

.PHONY: test
test: ## Run Go tests
	go test ./...

.PHONY: e2e-install
e2e-install: ## Install Playwright deps + Chromium
	cd e2e && npm install && npx playwright install --with-deps chromium

.PHONY: e2e
e2e: ## Run Playwright specs against the running dev stack (needs `make up`)
	cd e2e && npm test

.PHONY: e2e-ui
e2e-ui: ## Playwright UI mode for iterating on specs
	cd e2e && npm run test:ui
