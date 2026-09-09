# Architecture

## Runtime Topology

```text
browser
  -> Caddy
     -> Next.js frontend
     -> Go REST and WebSocket backend
     -> SeaweedFS public asset path

Go backend
  -> PostgreSQL: durable business state
  -> Redis: revocation TTL and explicitly disposable caches
  -> SeaweedFS: task source assets
```

Caddy is the public routing and security boundary. Database, Redis, and internal storage traffic remain on private Docker networks. Do not expose internal services or backend host ports without a reviewed operational requirement.

## Backend: Clean Architecture In A Hexagonal Monolith

```text
cmd
  -> app process boot
     -> bootstrap and Wire composition
        -> inbound and outbound adapters
           -> ports and usecases
              -> domain
```

- `backend/internal/domain/` contains models, statuses, pure validation, and stable error identities shared across application boundaries.
- `backend/internal/usecase/` contains the application business logic used by REST, WebSocket, workers, and administrative flows. Packages may compose narrower usecases through explicit interfaces, but dependencies must remain acyclic and transport-neutral.
- `backend/internal/port/` contains stable inbound and shared boundary contracts. Usecase-local outbound contracts stay beside the consumer that owns them.
- `backend/internal/app/app.go` is only the process boot boundary. It builds the dependency graph through bootstrap and runs the assembled server. Business workflows, transport contracts, and persistence logic do not belong here.
- `backend/internal/adapter/inbound/http/` and `backend/internal/adapter/inbound/websocket/` translate external protocols into consumer-owned inbound ports.
- `backend/internal/adapter/outbound/postgres/`, `redis/`, `memory/`, and `objectstorage/` implement infrastructure-facing usecase ports.
- `backend/internal/bootstrap/` is the only dependency composition root. It owns providers, Wire generation, runtime assembly, startup dependencies, shutdown ordering, and the wall-clock implementation injected through consumer-owned ports.

Dependencies point inward. Domain and usecase code must not depend on HTTP, WebSocket, generated transport DTOs, or concrete adapters. Inbound and outbound adapters must not import each other. Preserve these boundaries through package ownership, consumer-owned ports, code review, and behavior-focused tests.

Cross-workflow coordination belongs in a focused `internal/usecase` package, not in `internal/app`, an inbound adapter, or an outbound repository. Pure shared policies and stable value objects belong in `internal/domain`; do not create a generic contract or DTO dumping ground. Keep tests and deterministic fixtures beside the package they exercise, and generate interface mocks from `codegen/.mockery.yml`. A published transport operation must have a complete usecase path and production composition root.

The `backend/internal/usecase/tournament/` directory is a namespace, not a Go
package. Production code lives in ten owner packages: `admin`, `attendance`,
`cancellation`, `catalog`, `lifecycle`, `participant`, `pause`, `preflight`,
`progression`, and `roster`. `admin` is the outer tournament orchestrator
exception and may compose the leaf workflows. The namespace root provides no
facade.

Do not add `architecture_test.go`, AST import scanners, or reflection-only tests whose sole purpose is enforcing directory structure, import direction, or interface shape. Tests should prove business behavior, public contracts, persistence behavior, or runtime invariants.

Inbound adapters map domain errors to protocol-specific statuses and payloads. Internal causes remain in the error chain for diagnostics and must not be exposed to clients.

## Frontend: Feature-Sliced Design

```text
app -> pages -> widgets -> features -> entities -> shared
```

- `frontend/app/` contains Next.js route entrypoints.
- `frontend/lib/pages/` coordinates page-level state and composition.
- `frontend/lib/entities/` contains shared player models.
- `frontend/lib/features/` contains reusable user actions.
- `frontend/lib/widgets/` contains composed UI blocks.
- `frontend/lib/shared/` contains API, configuration, runtime parsing, utilities, types, and UI primitives.
- `frontend/e2e/` contains browser contract and gated full-stack tests.

REST consumers use shared API adapters. A future tournament realtime client must
keep transport lifecycle separate from business reducers. Browser storage is a
recoverable display cache, not an authority or credential store.

## Data Authority

| Data                                     | Authority       | Notes                                                     |
| ---------------------------------------- | --------------- | --------------------------------------------------------- |
| Players, tasks, tournaments, results, audit | PostgreSQL      | Durable and transactional                                 |
| JWT revocation TTL                         | Redis           | Security state; never clear an entry before its TTL       |
| Disposable rate-limit and cache entries    | Redis           | Ephemeral; startup may clear or rebuild these entries     |
| Task source assets                       | SeaweedFS       | Accessed through storage adapters and public proxy policy |
| Player display cache                     | Session storage | Guarded, recoverable, never authoritative                                  |
| Tournament clocks and official results     | Backend         | UI clocks are display-only                                |

## Current Reliability Boundaries

- Tournament, roster, wave, game, result, pause, and correction writes use database compare-and-set transactions.
- WebSocket connections are role-scoped read boundaries. Durable tournament state remains in PostgreSQL.
- WebSocket delivery is not a substitute for a durable read model or event log.
- Redis updates can occur after PostgreSQL commit; PostgreSQL remains canonical.
- Automatic migrations run during backend startup, so schema changes must be deploy-compatible.

Do not present these current limitations as future product decisions. If a requirement needs stronger guarantees, implement an explicit migration path and tests.

## Hotspots

The canonical hotspot and exclusive-lock list lives in `ownership.md`. Prefer focused modules, reducers, or adapters for new behavior. Do not perform unrelated refactors while touching a hotspot.
