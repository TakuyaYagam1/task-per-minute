SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

# Pinned tool versions - keep in sync with codegen configs.
GOOSE_VERSION   ?= v3.28.0
SQLC_VERSION    ?= v1.31.1
MOCKERY_VERSION ?= v3.5.5
WIRE_VERSION    ?= v0.7.0

TOOLS_DIR  ?= $(CURDIR)/.cache/tools
SQLC       := $(TOOLS_DIR)/sqlc
SQLC_STAMP := $(TOOLS_DIR)/sqlc-$(SQLC_VERSION).stamp

GOOSE   := go run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)
MOCKERY := go run github.com/vektra/mockery/v3@$(MOCKERY_VERSION)
WIRE    := go run github.com/google/wire/cmd/wire@$(WIRE_VERSION)

DB_DSN ?= postgres://tpm:tpm@localhost:5432/task_per_minute?sslmode=disable
MIGRATIONS_DIR := db/migrations
QUALITY_PY_REQUIREMENTS ?= scripts/requirements-quality.txt
QUALITY_VENV ?= $(CURDIR)/.venv-quality
QUALITY_PYTHON ?= $(QUALITY_VENV)/bin/python
QUALITY_PY_DEPS_STAMP ?= $(QUALITY_VENV)/.deps.stamp
BOOTSTRAP_PYTHON ?= python3
PYTHON ?= $(QUALITY_PYTHON)
GO_LINT_CACHE := $(CURDIR)/.cache/golangci-lint

##@ Build & run

.PHONY: build
build: ## Build the server binary into ./bin/server
	@mkdir -p bin
	go build -trimpath -o bin/server ./cmd/app

.PHONY: run
run: ## Run the server with the current shell environment
	go run ./cmd/app

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

.PHONY: clean
clean: ## Remove build artefacts and generated mocks
	rm -rf bin/ tmp/ coverage.out coverage.html
	find . -type d -name mocks -prune -exec rm -rf {} +

##@ Code generation

.PHONY: gen generate
gen generate: gen-openapi gen-sqlc gen-wire mocks ## Run all code generators

.PHONY: install-tools
install-tools: $(SQLC_STAMP) ## Install pinned project-local generator binaries

$(SQLC_STAMP):
	@mkdir -p "$(TOOLS_DIR)"
	GOBIN="$(TOOLS_DIR)" go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
	@actual="$$($(SQLC) version)"; test "$$actual" = "$(SQLC_VERSION)" || { \
		echo "sqlc: expected $(SQLC_VERSION), got $$actual"; exit 1; \
	}
	@touch "$@"

.PHONY: gen-openapi openapi
gen-openapi openapi: $(QUALITY_PY_DEPS_STAMP) ## Merge OpenAPI components, run oapi-codegen, cleanup intermediate files
	PYTHON="$(PYTHON)" bash scripts/openapi-generate.sh

.PHONY: test-openapi
test-openapi: $(QUALITY_PY_DEPS_STAMP) ## Run OpenAPI generator and contract toolchain tests
	PYTHON="$(PYTHON)" bash scripts/openapi-generate_test.sh

.PHONY: gen-sqlc check-sqlc
gen-sqlc: check-sqlc ## Generate type-safe SQL code from db/queries/
	$(SQLC) generate -f codegen/sqlc.yaml

check-sqlc: $(SQLC_STAMP) ## Verify the pinned project-local sqlc binary
	@actual="$$($(SQLC) version)"; test "$$actual" = "$(SQLC_VERSION)" || { \
		echo "sqlc: expected $(SQLC_VERSION), got $$actual"; exit 1; \
	}

.PHONY: gen-wire
gen-wire: ## Generate compile-time DI graph
	$(WIRE) ./internal/bootstrap/...

.PHONY: mocks
mocks: ## Generate testify mocks for consumer-owned usecase ports
	@if grep -qE '^[[:space:]]+github\.com/' codegen/.mockery.yml; then \
		$(MOCKERY) --config codegen/.mockery.yml; \
	else \
		echo "mockery: no active targets in codegen/.mockery.yml - skipping"; \
	fi

##@ Lint, format, test

.PHONY: install-quality-python-deps
install-quality-python-deps: $(QUALITY_PY_DEPS_STAMP) ## Install pinned Python quality tools

$(QUALITY_PY_DEPS_STAMP): $(QUALITY_PY_REQUIREMENTS)
	@echo "Installing Python quality tools..."
	@command -v "$(BOOTSTRAP_PYTHON)" >/dev/null 2>&1 || { \
		echo "quality Python bootstrap interpreter not found: $(BOOTSTRAP_PYTHON)"; exit 1; \
	}
	$(BOOTSTRAP_PYTHON) -m venv $(QUALITY_VENV)
	$(QUALITY_PYTHON) -m pip install --disable-pip-version-check -r $(QUALITY_PY_REQUIREMENTS)
	@touch $(QUALITY_PY_DEPS_STAMP)

.PHONY: lint lint-go
lint: ## Run Go and SQL linters, reporting failures from both
	@go_status=0; sql_status=0; \
	$(MAKE) --no-print-directory lint-go || go_status=$$?; \
	$(MAKE) --no-print-directory lint-sql || sql_status=$$?; \
	if [ "$$go_status" -ne 0 ] || [ "$$sql_status" -ne 0 ]; then exit 1; fi

lint-go: ## Run golangci-lint
	GOLANGCI_LINT_CACHE="$(GO_LINT_CACHE)" golangci-lint run ./...

.PHONY: lint-fix lint-go-fix
lint-fix: ## Auto-fix Go and SQL queries, reporting failures from both
	@go_status=0; sql_status=0; \
	$(MAKE) --no-print-directory lint-go-fix || go_status=$$?; \
	$(MAKE) --no-print-directory lint-sql-fix || sql_status=$$?; \
	if [ "$$go_status" -ne 0 ] || [ "$$sql_status" -ne 0 ]; then exit 1; fi

lint-go-fix: ## Auto-fix Go lint findings
	GOLANGCI_LINT_CACHE="$(GO_LINT_CACHE)" golangci-lint run --fix ./...

.PHONY: lint-sql
lint-sql: install-quality-python-deps ## Lint migrations and sqlc queries
	$(QUALITY_PYTHON) -m sqlfluff lint --config .sqlfluff db/migrations db/queries

# Applied migrations are immutable. Keep the fixer hard-scoped to queries.
.PHONY: lint-sql-fix
lint-sql-fix: install-quality-python-deps ## Auto-fix queries, then lint queries and immutable migrations
	$(QUALITY_PYTHON) -m sqlfluff fix --config .sqlfluff db/queries
	$(QUALITY_PYTHON) -m sqlfluff lint --config .sqlfluff db/migrations db/queries

.PHONY: fmt
fmt: ## golangci-lint fmt
	golangci-lint fmt ./...

.PHONY: test
test: test-migration-create ## Unit tests with race detector (skips integration_test/)
	go test -race -count=1 -parallel=5 $$(go list ./... | grep -v /integration_test)

.PHONY: test-int
test-int: ## Integration tests (testcontainers - one package, up to 5 parallel tests)
	go test -race -count=1 -p=1 -parallel=5 -timeout=20m -tags=integration ./integration_test/...

.PHONY: test-race
test-race: ## Unit + integration tests with race detector (full suite, requires Docker)
	$(MAKE) test
	$(MAKE) test-int

.PHONY: cover
cover: ## Unit-test coverage report (HTML)
	go test -race -count=1 -parallel=5 -coverprofile=coverage.out $$(go list ./... | grep -v /integration_test)
	go tool cover -html=coverage.out -o coverage.html

##@ Database migrations

.PHONY: migrate-up
migrate-up: ## goose up - apply all pending migrations
	$(GOOSE) -dir $(MIGRATIONS_DIR) postgres "$(DB_DSN)" up

.PHONY: migrate-down
migrate-down: ## goose down - roll back the last migration
	$(GOOSE) -dir $(MIGRATIONS_DIR) postgres "$(DB_DSN)" down

.PHONY: migrate-status
migrate-status: ## goose status
	$(GOOSE) -dir $(MIGRATIONS_DIR) postgres "$(DB_DSN)" status

.PHONY: migrate-create
migrate-create: ## Create a new migration: make migrate-create NAME=add_widgets
	@if [ -z "$(NAME)" ]; then echo "Usage: make migrate-create NAME=<snake_case_name>"; exit 1; fi
	bash scripts/create-migration.sh "$(MIGRATIONS_DIR)" "$(NAME)"

.PHONY: test-migration-create
test-migration-create: ## Test six-digit migration creation and validation
	bash scripts/create-migration_test.sh

##@ Help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN{FS=":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z0-9_-]+:.*?##/ {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2} \
		/^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0, 5)}' $(MAKEFILE_LIST)
