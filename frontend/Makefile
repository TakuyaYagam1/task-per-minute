SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

NPM ?= npm
NPX ?= npx

ROOT_DIR := $(abspath ..)
BACKEND_DIR := $(ROOT_DIR)/backend
OPENAPI_FILE := $(BACKEND_DIR)/internal/openapi/openapi.yml
OPENAPI_TMP := $(BACKEND_DIR)/internal/openapi/components/schemas.yml
GENERATED_TYPES := lib/shared/api/schema.ts
ESLINT_EXT := .js,.jsx,.ts,.tsx
ESLINT_IGNORE := --ignore-pattern .next --ignore-pattern node_modules --ignore-pattern lib/shared/api/schema.ts

##@ Build & run

.PHONY: install
install: ## Install pinned dependencies from package-lock.json
	$(NPM) ci

.PHONY: dev
dev: ## Run Next.js dev server
	$(NPM) run dev

.PHONY: build
build: ## Build the production frontend
	$(NPM) run build

.PHONY: start
start: ## Start the production frontend server
	$(NPM) run start

.PHONY: clean
clean: ## Remove frontend build and test artefacts
	rm -rf .next/ node_modules/ playwright-report/ test-results/ coverage/ tsconfig.tsbuildinfo next-env.d.ts

##@ Code generation

.PHONY: gen
gen: gen-types ## Run all frontend code generators

.PHONY: generate
generate: gen ## Alias for gen

.PHONY: gen-types
gen-types: ## Generate REST API types from backend OpenAPI
	@set -euo pipefail; \
	trap 'rm -f "$(OPENAPI_TMP)"' EXIT; \
	cd "$(BACKEND_DIR)" && python3 scripts/merge-schemas.py; \
	cd "$(CURDIR)" && $(NPX) openapi-typescript "$(OPENAPI_FILE)" -o "$(GENERATED_TYPES)"

.PHONY: openapi
openapi: gen-types ## Alias for gen-types

.PHONY: types
types: gen-types ## Alias for gen-types

##@ Lint, typecheck, test

.PHONY: lint
lint: ## ESLint with zero warnings
	$(NPX) eslint . --ext $(ESLINT_EXT) --max-warnings=0 $(ESLINT_IGNORE)

.PHONY: lint-fix
lint-fix: ## ESLint auto-fix
	$(NPX) eslint . --ext $(ESLINT_EXT) --max-warnings=0 $(ESLINT_IGNORE) --fix

.PHONY: typecheck
typecheck: ## TypeScript typecheck without emit
	$(NPX) tsc --noEmit --pretty false

.PHONY: test
test: ## Playwright mock contract tests
	$(NPX) playwright test

.PHONY: test-live
test-live: ## Gated live backend smoke tests; requires E2E_ADMIN_PASSWORD
	E2E_LIVE=1 $(NPX) playwright test e2e/live-backend.spec.ts

.PHONY: test-full-stack
test-full-stack: ## Start local compose with .env.local and run full-stack e2e
	../scripts/e2e-local-compose.sh

.PHONY: audit
audit: ## npm production dependency audit
	$(NPM) audit --omit=dev

.PHONY: check
check: gen-types typecheck lint build test ## Full frontend verification

.PHONY: verify
verify: check audit ## Full frontend verification plus production audit

##@ Help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN{FS=":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z0-9_-]+:.*?##/ {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2} \
		/^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0, 5)}' $(MAKEFILE_LIST)
