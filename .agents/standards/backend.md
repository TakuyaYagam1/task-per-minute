# Backend Standard

## Scope

- The Go module root is `backend/` and the module requires Go 1.26.2.
- Keep the existing import direction: inbound and outbound adapters -> consumer-owned usecase ports -> `internal/domain`.
- `internal/domain` owns business types and validation without transport or infrastructure concerns.
- Each package under `internal/usecase/{admin,duel,leaderboard,player,recovery}` owns its narrow ports in `ports.go` and its workflow models in `models.go` where needed.
- HTTP and WebSocket adapters live under `internal/adapter/inbound`. PostgreSQL, Redis, memory, object-storage, and wall-clock adapters live under `internal/adapter/outbound`.
- `internal/bootstrap` is the only composition root and owns providers, Wire generation, startup, recovery coordination, server lifetime, and shutdown.
- Add dependencies only when existing repository packages cannot satisfy the requirement. Keep interfaces consumer-owned and narrow.

## Contract Sources

- REST source of truth: `backend/api/openapi.yml`, `routes/*.yml`, `components/security.yml`, and `components/schemas/*.yml`.
- WebSocket source of truth: `backend/internal/adapter/inbound/websocket/event.go`, handlers, and protocol tests. WebSocket is intentionally outside OpenAPI.
- Configuration source of truth: `backend/config/config.go`. Use `.env.example` only for public variable names and examples.
- Preserve authentication, cookie, CSRF, Origin, CORS, trusted proxy, and rate-limit boundaries when changing transports.
- Keep transport mapping in inbound adapters. Do not expose persistence models directly as API payloads.

## Generated Files

Never hand-edit generated output. Change its source and regenerate:

- `internal/adapter/inbound/http/api/*.gen.go` <- `api/**/*.yml` plus `codegen/oapi-codegen-*.yml`.
- `internal/adapter/outbound/postgres/sqlc/*` <- `db/migrations/*.sql`, `db/queries/*.sql`, and `codegen/sqlc.yaml`.
- `internal/bootstrap/wire_gen.go` <- bootstrap providers, sets, and `wire.go`.
- `**/mocks/*` <- handwritten interfaces listed in `codegen/.mockery.yml`.

`make generate` runs all generators. It requires Python 3 with PyYAML, Node with `npx`, an installed `sqlc`, and Go tool dependencies. Review generated diffs before accepting them.

## Runtime Invariants

- A terminal duel transition must remain conditional and idempotent. The database update only succeeds while the duel is active.
- Keep duel finish, solved state, history, and both player status updates in one PostgreSQL transaction.
- Keep Redis side effects outside the PostgreSQL transaction. PostgreSQL remains authoritative if a cache update fails.
- Preserve the meaning of server timestamps. `started_at` is created with the duel row, while WebSocket delivery happens later.
- WebSocket hubs, client buffers, timers, hint schedules, and reconnect counters are process-local. Do not assume durable delivery or multi-instance coordination.
- Startup order is object-storage bucket check -> database migrations -> recovery -> HTTP serving. Shutdown closes WebSocket work before runtime cancellation and HTTP shutdown.
- Any change to concurrency, duel finalization, timers, reconnect, or terminal events requires focused race tests and integration coverage.

## Validation

The current `backend/Makefile` includes and exports `../.env` for every target when that file exists. Therefore, the commands below are canonical CI targets, not automatic permission to run them in an active checkout.

Before any backend `make` command:

- inspect the Make target and scripts it invokes;
- check only whether `../.env` exists, without opening it;
- if it exists, do not run `make` in that checkout;
- use a clean isolated worktree with no private env file, or run an inspected underlying command in a scrubbed allowlisted environment;
- never move, rename, copy, source, or delete the user's env file as a workaround.

Several generator targets invoke pinned Go tools and `npx -y @redocly/cli@1.34.0`; a cache miss performs a network download. Verify tool identity and obtain current network or installation authority before running them. Verify PATH-selected `sqlc` matches `SQLC_VERSION` in `backend/Makefile`.

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
- Do not stage, commit, push, deploy, or change external state unless the task explicitly authorizes it.
