# embookshelf — dev Makefile
SHELL := /bin/bash

DB_URL ?= postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable

.PHONY: help
help: ## List targets
	@awk 'BEGIN{FS=":.*?## "} /^[a-zA-Z_-]+:.*?## /{printf "  %-18s %s\n",$$1,$$2}' $(MAKEFILE_LIST)

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

.PHONY: generate
generate: templ ## Run all code generators

.PHONY: templ
templ: ## Generate *_templ.go files
	go tool templ generate

.PHONY: css
css: ## Build Tailwind CSS once
	npx @tailwindcss/cli -i web/src/styles.css -o internal/staticfs/static/app.css --minify

.PHONY: css-watch
css-watch: ## Watch & rebuild Tailwind CSS
	npx @tailwindcss/cli -i web/src/styles.css -o internal/staticfs/static/app.css --watch

.PHONY: htmx
htmx: ## Download htmx.min.js into internal/staticfs/static
	curl -fsSL https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js -o internal/staticfs/static/htmx.min.js

.PHONY: epubjs
epubjs: ## Download epub.js + jszip for the EPUB reader
	curl -fsSL https://unpkg.com/jszip@3.10.1/dist/jszip.min.js -o internal/staticfs/static/jszip.min.js
	curl -fsSL https://unpkg.com/epubjs@0.3.93/dist/epub.min.js -o internal/staticfs/static/epub.min.js

.PHONY: pdfjs
pdfjs: ## Download pdf.js for the PDF reader
	mkdir -p internal/staticfs/static/pdfjs
	curl -fsSL https://unpkg.com/pdfjs-dist@3.11.174/legacy/build/pdf.min.js -o internal/staticfs/static/pdfjs/pdf.min.js
	curl -fsSL https://unpkg.com/pdfjs-dist@3.11.174/legacy/build/pdf.worker.min.js -o internal/staticfs/static/pdfjs/pdf.worker.min.js

.PHONY: assets
assets: htmx epubjs pdfjs css ## Fetch + build all static assets

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
build: generate ## Build the server binary
	go build -o ./tmp/embookshelf ./cmd/embookshelf

.PHONY: run
run: generate ## Run the server
	go run ./cmd/embookshelf

.PHONY: dev
dev: ## Live-reload dev loop (air via go tool)
	go tool air

.PHONY: test
test: ## Run Go tests
	go test ./...
