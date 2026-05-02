SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

# Pinned tool versions - keep in sync with codegen configs.
GOOSE_VERSION   ?= v3.27.0
SQLC_VERSION    ?= v1.31.0
OAPI_VERSION    ?= v2.5.1
MOCKERY_VERSION ?= v3.5.5
WIRE_VERSION    ?= v0.7.0

GOOSE   := go run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)
# sqlc cannot be `go run` (its go.mod uses replace directives); install once with
# `go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)` or via your package manager.
SQLC    := sqlc
OAPI    := go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_VERSION)
MOCKERY := go run github.com/vektra/mockery/v3@$(MOCKERY_VERSION)
WIRE    := go run github.com/google/wire/cmd/wire@$(WIRE_VERSION)

# Project-wide .env lives at the repo root; make targets inherit DB_DSN etc.
ifneq (,$(wildcard ../.env))
include ../.env
export
endif

DB_DSN ?= postgres://tpm:tpm@localhost:5432/task_per_minute?sslmode=disable
MIGRATIONS_DIR := migrations

##@ Build & run

.PHONY: build
build: ## Build the server binary into ./bin/server
	@mkdir -p bin
	go build -trimpath -o bin/server ./cmd/app

.PHONY: run
run: ## Run the server (uses .env if present)
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

.PHONY: merge-schemas
merge-schemas: ## Only merge components/schemas/*.yml -> schemas.yml (no codegen)
	python3 scripts/merge-schemas.py

.PHONY: gen-openapi openapi
gen-openapi openapi: ## Merge OpenAPI components, run oapi-codegen, cleanup intermediate files
	OAPI_VERSION=$(OAPI_VERSION) bash scripts/openapi-generate.sh
	@rm -f internal/openapi/components/schemas.yml

.PHONY: gen-sqlc
gen-sqlc: ## Generate type-safe SQL code from queries/
	$(SQLC) generate -f codegen/sqlc.yaml

.PHONY: gen-wire
gen-wire: ## Generate compile-time DI graph
	$(WIRE) ./internal/wire/...

.PHONY: mocks
mocks: ## Generate testify mocks for hand-written usecase ports (skipped while no targets are defined)
	@if grep -qE '^[[:space:]]+github\.com/' codegen/.mockery.yml; then \
		$(MOCKERY) --config codegen/.mockery.yml; \
	else \
		echo "mockery: no active targets in codegen/.mockery.yml - skipping"; \
	fi

##@ Lint, format, test

.PHONY: lint
lint: ## golangci-lint run
	golangci-lint run ./...

.PHONY: lint-fix
lint-fix: ## golangci-lint run --fix
	golangci-lint run --fix ./...

.PHONY: fmt
fmt: ## golangci-lint fmt
	golangci-lint fmt ./...

.PHONY: test
test: ## Unit tests with race detector (skips integration_test/)
	go test -race -count=1 -parallel=5 $$(go list ./... | grep -v /integration_test)

.PHONY: test-int
test-int: ## Integration tests (testcontainers - requires Docker)
	go test -race -count=1 -parallel=5 -tags=integration ./integration_test/...

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
	$(GOOSE) -dir $(MIGRATIONS_DIR) -s create $(NAME) sql

##@ Help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN{FS=":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z0-9_-]+:.*?##/ {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2} \
		/^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0, 5)}' $(MAKEFILE_LIST)
