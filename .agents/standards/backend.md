# Backend Standard

## Scope

- Treat the backend as a capability-oriented modular monolith following Clean Architecture and hexagonal boundaries. Keep business policy inward and infrastructure at the edges.
- The Go module root is `backend/` and the module requires Go 1.26.8.
- Keep the import direction: process boot -> bootstrap -> adapters -> ports and transport-neutral usecases -> `internal/domain`.
- `internal/domain` owns business types, validation, and stable application error identities without transport or infrastructure dependencies.
- `internal/usecase/` owns application business logic, command/query services, cross-workflow coordination, and narrow consumer-owned outbound contracts. Organize it by business capability; a capability may be a package or a namespace containing cohesive subpackages. Dependencies between usecase packages must be explicit, acyclic, and transport-neutral.
- Use cohesion, ownership, and independent evolution to decide whether a capability needs a subpackage. For change coupling, use only file names and co-change counts from the current branch, queried with an explicit source-code pathspec and without commit messages, authors, diffs, or other refs. Never inspect `.env`, `PRD/`, private task material, credentials, or commit contents for these signals. A split requires a cohesive responsibility boundary plus distinct ownership or independent evolution. Up to 20 recent relevant changes may establish independent evolution when fewer than half touch both responsibilities; separate validation surfaces are also evidence. Shared invariants or transaction boundaries require one clear invariant owner and unchanged transaction semantics across the proposed boundary. File count alone never requires a split or justifies a new package. Record the owner and dependency direction in review.
- Keep consumer-owned ports close to the capability that consumes them. They may be collected in `ports.go` or in cohesive capability subpackages. Do not create generic `contract.go`, standalone `repository.go`, workflow-specific `*_ports.go`, or one-interface clock and transaction files merely to move declarations. Keep a specialized interface beside its implementation only when the dependency is private to that implementation and naming it with the consumer-owned ports would obscure ownership.
- Do not introduce a global backend support package named `common`, `shared`, `util`, `helpers`, or `core`. Shared adapter mechanics belong in a responsibility-named internal package owned by that adapter. For PostgreSQL capability subpackages, the target-state repo-relative path `backend/internal/adapter/outbound/postgres/internal/db` is allowed for pool, transaction, and database primitives; this path does not exist yet and remains private to the PostgreSQL adapter when introduced.
- `internal/port/` owns inbound and shared boundary contracts. Contracts must use domain or usecase types, never generated transport DTOs or concrete persistence models.
- `internal/app/app.go` owns only process boot: request bootstrap assembly, run the server, and report terminal startup or runtime errors.
- HTTP and WebSocket adapters live under `internal/adapter/inbound`. PostgreSQL, Redis, memory, and object-storage adapters live under `internal/adapter/outbound`.
- Organize outbound adapters by technology and capability. `internal/adapter/outbound/postgres/` may contain capability subpackages such as assignment, execution, projection, result, and tournament when each owns a coherent persistence surface. Subpackages implement consumer-owned ports and may share only narrow domain types or adapter-local responsibility primitives; inbound and outbound adapters never import each other. PostgreSQL capability children should import the repo-relative paths `backend/internal/adapter/outbound/postgres/internal/db` and generated `backend/internal/adapter/outbound/postgres/sqlc` directly rather than the parent postgres facade, which avoids import cycles. During a staged migration, the root facade may temporarily preserve constructors and type aliases for existing callers. Keep generated `backend/internal/adapter/outbound/postgres/sqlc/` output in its source-owned package.
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
