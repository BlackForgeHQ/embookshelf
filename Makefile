# embookshelf — dev Makefile
SHELL := /bin/bash

DB_URL ?= postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable

.PHONY: help
help: ## List targets
	@awk 'BEGIN{FS=":.*?## "} /^[a-zA-Z_-]+:.*?## /{printf "  %-18s %s\n",$$1,$$2}' $(MAKEFILE_LIST)

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

.PHONY: frontend-install
frontend-install: ## Install frontend deps
	cd frontend && npm install

.PHONY: frontend-dev
frontend-dev: ## Vite dev server (proxies /api, /opds → Go)
	cd frontend && npm run dev

.PHONY: frontend-build
frontend-build: ## Build frontend + sync shell into internal/staticfs/dist/
	cd frontend && npm run build

.PHONY: db-up
db-up: ## Start Postgres via compose
	docker compose -f compose.dev.yml up -d postgres

.PHONY: db-down
db-down: ## Stop Postgres
	docker compose -f compose.dev.yml down

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
build: frontend-build ## Build the server binary (includes embedded SPA)
	go build -o ./tmp/embookshelf ./cmd/embookshelf

.PHONY: run
run: ## Run the server (expects SPA already built)
	go run ./cmd/embookshelf

.PHONY: dev
dev: ## Live-reload backend via `go tool air`
	go tool air

.PHONY: up
up: ## Run backend (air) + frontend (Vite) together — Ctrl-C stops both
	@trap 'kill 0' EXIT INT TERM; \
	$(MAKE) --no-print-directory dev & \
	$(MAKE) --no-print-directory frontend-dev & \
	wait

.PHONY: test
test: ## Run Go tests
	go test ./...
