# Backend Standard

## Scope

- Treat the backend as a hexagonal monolith following Clean Architecture. Keep business policy inward and infrastructure at the edges.
- The Go module root is `backend/` and the module requires Go 1.26.2.
- Keep the import direction: process boot -> bootstrap -> adapters -> ports and transport-neutral usecases -> `internal/domain`.
- `internal/domain` owns business types, validation, and stable application error identities without transport or infrastructure dependencies.
- `internal/usecase/` owns application business logic, command/query services, cross-workflow coordination, and narrow consumer-owned outbound contracts. Dependencies between usecase packages must be explicit, acyclic, and transport-neutral.
- Keep usecase packages flat by business capability. A workflow is a file, not a child package. Generated `mocks/` directories are the only routine nesting below a usecase package.
- Each usecase package collects its injected repository, clock, transaction, publisher, and gateway contracts in one `ports.go`. Do not create generic `contract.go`, standalone `repository.go`, workflow-specific `*_ports.go`, or one-interface clock and transaction files. Keep a specialized interface beside its implementation only when the dependency is private to that implementation and naming it in `ports.go` would obscure ownership.
- `internal/port/` owns inbound and shared boundary contracts. Contracts must use domain or usecase types, never generated transport DTOs or concrete persistence models.
- `internal/app/app.go` owns only process boot: request bootstrap assembly, run the server, and report terminal startup or runtime errors.
- HTTP and WebSocket adapters live under `internal/adapter/inbound`. PostgreSQL, Redis, memory, and object-storage adapters live under `internal/adapter/outbound`.
- `internal/bootstrap` is the only composition root and owns providers, Wire generation, runtime assembly, startup dependencies, shutdown ordering, and the wall-clock function injected through consumer-owned ports.
- Add dependencies only when existing repository packages cannot satisfy the requirement. Keep interfaces consumer-owned and narrow.
- Map domain errors to HTTP or WebSocket responses inside the corresponding inbound adapter. Preserve wrapped causes for diagnostics, but return only the safe domain message to clients.

## Contract Sources

- REST source of truth: `backend/api/openapi.yml`, `routes/*.yml`, `components/security.yml`, `components/parameters.yml`, `components/responses.yml`, and `components/schemas/*.yml`.
- WebSocket source of truth: `backend/internal/adapter/inbound/websocket/event.go`, handlers, and protocol tests. WebSocket is intentionally outside OpenAPI.
- Configuration source of truth: `backend/config/config.go`. Use `.env.example` only for public variable names and examples.
- Preserve authentication, cookie, CSRF, Origin, CORS, trusted proxy, and rate-limit boundaries when changing transports.
- Keep transport mapping in inbound adapters. Do not expose persistence models directly as API payloads.

## Generated Files

Never hand-edit generated output. Change its source and regenerate:

- `internal/adapter/inbound/http/api/*.gen.go` <- `api/**/*.yml` plus `codegen/oapi-codegen-*.yml`.
- `internal/adapter/outbound/postgres/sqlc/*` <- `db/migrations/*.sql`, `db/queries/*.sql`, and `codegen/sqlc.yaml`.
- `internal/bootstrap/wire_gen.go` <- bootstrap providers, sets, and `wire.go`.
- `**/mocks/*.go` and configured same-package `*_mock_test.go` files <- handwritten interfaces listed in `codegen/.mockery.yml`.

`make generate` runs all generators. It requires Python 3 with PyYAML, frontend Node dependencies matching the lockfile, the project-local sqlc installer inputs, and the pinned Go tool modules. Review generated diffs before accepting them.

## Runtime Invariants

- Tournament, series, game, and result completion transitions must remain conditional and idempotent.
- Keep official result, score head, audit, projection evidence, and outbox changes in one PostgreSQL transaction.
- Keep Redis side effects outside the PostgreSQL transaction. PostgreSQL remains authoritative if a cache update fails.
- Preserve the meaning of server timestamps. Persist authoritative transition time before transport delivery.
- WebSocket connections, session monitors, and connection counters are process-local. Do not assume durable delivery or multi-instance coordination.
- Startup order is object-storage bucket check -> database migrations -> HTTP serving. Shutdown closes WebSocket work before runtime cancellation and HTTP shutdown.
- Any change to concurrency, result finalization, pause, reconnect, recovery, or WebSocket session monitoring requires focused race tests and integration coverage.

## Validation

The current `backend/Makefile` does not load an env file. Targets inherit only the invoking shell environment, but the commands below are still canonical CI targets rather than automatic permission to run them in an active checkout.

Before any backend `make` command:

- inspect the Make target and scripts it invokes;
- use a clean shell environment containing only variables authorized for that command;
- never source, inspect, move, rename, copy, or delete a user's env file as a workaround.

Mockery, Wire, Goose, the project-local sqlc installer, and the Python quality-tool installer can download version-pinned-by-default dependencies on a cache miss. Their Make variables are overridable, so any override requires separate identity review and installation authority. OpenAPI generation uses lock-matched local Node tools and an offline pinned Go tools module; it must fail closed when those inputs are unavailable.

Run approved commands from `backend/`. On a fresh isolated checkout, generate ignored mocks first.

```bash
make mocks
make lint
make test
make build
```

For persistence, realtime, lifecycle, or migration changes, Docker is required and must be explicitly authorized for a disposable environment:

```bash
make test-int
make test-race
```

For generated-contract changes:

```bash
make generate
make lint
make test
make build
```

Report every skipped gate and its residual risk.

## Change Boundaries

- Inspect `git status --short` before editing and preserve unrelated work.
- Do not inspect, print, overwrite, or commit `.env` files, credentials, session material, or private task data. An explicitly authorized repository command may consume an env file without exposing its contents.
- Do not bypass use cases by calling outbound implementations from inbound adapters.
- Do not publish a REST or WebSocket operation without a complete application path and production composition root.
- Do not define business service interfaces in an inbound adapter when the contract belongs to `internal/port` or its consuming usecase.
- Do not add architecture-only test packages, AST import scanners, or reflection tests to police layer shape. Enforce boundaries through the documented ownership model, code review, narrow ports, and tests of observable behavior.
- Do not stage, commit, push, deploy, or change external state unless the task explicitly authorizes it.
