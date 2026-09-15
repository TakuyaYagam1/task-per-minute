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

## Backend: Capability-Oriented Modular Monolith

```text
cmd
  -> app process boot
     -> bootstrap and Wire composition
        -> capability modules
           -> inbound and outbound adapters
              -> ports and usecases
                 -> domain
```

- This graph describes dependency direction, not a required directory tree. Organize the backend by business capability inside the Clean Architecture and hexagonal boundaries. A capability may be a package or a namespace with cohesive internal subpackages when that reduces cognitive load and makes ownership, tests, or change boundaries easier to understand.
- `backend/internal/domain/` contains models, statuses, pure validation, and stable error identities shared across application boundaries.
- `backend/internal/usecase/` contains the application business logic used by REST, WebSocket, workers, and administrative flows. Organize it by business capability. Packages may compose narrower usecases through explicit interfaces, and a capability may contain cohesive subpackages, but dependencies must remain acyclic and transport-neutral.
- `backend/internal/port/` contains stable inbound and shared boundary contracts. `backend/internal/port/inbound/` owns transport-neutral command, query, and view models when they are part of an inbound usecase contract or shared by multiple inbound adapters; adapter-only wire models stay with their adapter, usecase-local models stay with their owning usecase, and `internal/app` is not a DTO facade. Usecase-local outbound contracts stay beside the consumer that owns them.
- `backend/internal/app/app.go` is only the process boot boundary. It builds the dependency graph through bootstrap and runs the assembled server. Business workflows, transport contracts, and persistence logic do not belong here.
- `backend/internal/adapter/inbound/http/` and `backend/internal/adapter/inbound/websocket/` translate external protocols into consumer-owned inbound ports.
- `backend/internal/adapter/outbound/postgres/`, `redis/`, `memory/`, and `objectstorage/` implement infrastructure-facing usecase ports.
- `backend/internal/bootstrap/` is the only dependency composition root. It owns providers, Wire generation, runtime assembly, startup dependencies, shutdown ordering, and the wall-clock implementation injected through consumer-owned ports.

Dependencies point inward inside every capability module: domain code has no transport or infrastructure dependency, application and usecase code depends on domain types and consumer-owned ports, and adapters implement those ports. Domain and usecase code must not depend on HTTP, WebSocket, generated transport DTOs, or concrete adapters. Inbound and outbound adapters must not import each other. Nested packages follow the same direction and must not form cycles. Preserve these boundaries through package ownership, consumer-owned ports, code review, and behavior-focused tests.

Cross-workflow coordination belongs in a focused `internal/usecase` capability package, not in `internal/app`, an inbound adapter, or an outbound repository. Pure shared policies and stable value objects belong in `internal/domain`. Do not create a global `common`, `shared`, `util`, `helpers`, or `core` package as a dumping ground. PostgreSQL capability subpackages may use the adapter-local target-state path `backend/internal/adapter/outbound/postgres/internal/db` for pool, transaction, and database primitives; this repo-relative path does not exist yet, and the support package must remain private to the PostgreSQL adapter and responsibility-named. Keep tests and deterministic fixtures beside the package they exercise, and generate interface mocks from `codegen/.mockery.yml`. A published transport operation must have a complete usecase path and production composition root.

The `backend/internal/usecase/tournament/` directory demonstrates this layout:
it contains eleven owner packages for `admin`, `attendance`, `cancellation`,
`catalog`, `connection`, `lifecycle`, `participant`, `pause`, `preflight`,
`progression`, and `roster`. `admin` is the outer tournament orchestrator and
may compose the leaf workflows. A namespace root does not need a facade merely
to flatten paths. Likewise, `backend/internal/adapter/outbound/postgres/` may
be split into capability subpackages such as assignment, execution, projection,
result, and tournament when each owns a coherent persistence surface. During a
staged migration, the root postgres package may temporarily preserve
constructors and type aliases for existing callers. New capability children
should import the repo-relative paths
`backend/internal/adapter/outbound/postgres/internal/db` and
`backend/internal/adapter/outbound/postgres/sqlc` directly rather than the
parent package, which avoids import cycles. The generated `sqlc/` package keeps
its existing source and ownership boundary.

Use three signals when deciding whether to split a package, and do not use a
file-count threshold. For change coupling, sanitized repository metadata means
only file names and co-change counts from the current branch, queried with an
explicit source-code pathspec and without commit messages, authors, diffs, or
other refs. Never inspect `.env`, `PRD/`, private task material, credentials,
or commit contents for cohesion, ownership, or change coupling:

- Cohesion: the responsibilities can be named separately, have a narrow
  interface between them, and can be tested without reaching into each other's
  implementation details.
- Ownership: the resulting packages have distinct maintainers, review paths, or
  source and generated-file owners.
- Independent evolution: in a recent window of up to 20 relevant changes,
  fewer than half touch both responsibilities, or the responsibilities require
  separate validation surfaces.

A split requires cohesion plus distinct ownership or independent evolution.
Keep responsibilities together when they share invariants or transaction
boundaries unless the proposed boundary preserves one clear invariant owner and
the existing transaction semantics. Keep them together when they change in
most relevant changes. Do not create package cycles or generic dumping grounds
to achieve a split.

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

REST consumers use shared API adapters. Public tournament recovery parsing and
state application live in `frontend/lib/shared/api/tournament-recovery.ts`.
No page currently mounts a tournament socket transport. A mounted transport
must keep its lifecycle separate from the recovery reducer. Browser storage is
a recoverable display cache, not an authority or credential store.

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
- WebSocket connections are process-local, role-scoped read boundaries. PostgreSQL owns tournament snapshots, durable outbox events, subscriber identities, delivery receipts, and connection-generation fences used for ordered resume across instances.
- WebSocket delivery is not a substitute for a durable read model or event log.
- Redis updates can occur after PostgreSQL commit; PostgreSQL remains canonical.
- Automatic migrations run during backend startup, so schema changes must be deploy-compatible.

Do not present these current limitations as future product decisions. If a requirement needs stronger guarantees, implement an explicit migration path and tests.

## Hotspots

The canonical hotspot and exclusive-lock list lives in `ownership.md`. Prefer focused modules, reducers, or adapters for new behavior. Do not perform unrelated refactors while touching a hotspot.
