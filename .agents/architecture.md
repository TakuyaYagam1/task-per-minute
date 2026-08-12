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
  -> Redis: ephemeral queue, cache, and revocation TTL
  -> SeaweedFS: task source assets
```

Caddy is the public routing and security boundary. Database, Redis, and internal storage traffic remain on private Docker networks. Do not expose internal services or backend host ports without a reviewed operational requirement.

## Backend Layers

```text
domain
  <- usecases and consumer-owned ports
     <- inbound and outbound adapters
        <- bootstrap and Wire composition
```

- `backend/internal/domain/` contains models, statuses, and pure validation.
- Each package under `backend/internal/usecase/` owns its business workflow, narrow input ports, and workflow-specific models.
- `backend/internal/adapter/inbound/http/` and `backend/internal/adapter/inbound/websocket/` translate external protocols into usecase calls.
- `backend/internal/adapter/outbound/postgres/`, `redis/`, `memory/`, `objectstorage/`, and `clock/` implement usecase ports.
- `backend/internal/bootstrap/` is the only dependency composition root and owns startup, migrations, recovery coordination, serving, and shutdown order.

Dependencies point inward. Domain and usecase code must not depend on HTTP, WebSocket, generated transport DTOs, or concrete adapters. Inbound and outbound adapters must not import each other. `backend/internal/architecture/import_boundary_test.go` enforces these production import rules.

## Frontend Boundaries

- `frontend/app/` contains Next.js route entrypoints.
- `frontend/lib/pages/` coordinates page-level state and composition.
- `frontend/lib/entities/` contains player and game models.
- `frontend/lib/features/` contains user actions and transport hooks.
- `frontend/lib/widgets/` contains composed UI blocks.
- `frontend/lib/shared/` contains API, configuration, runtime parsing, utilities, types, and UI primitives.
- `frontend/e2e/` contains browser contract and gated full-stack tests.

REST consumers use shared API adapters. WebSocket transport lifecycle stays separate from business reducers. Browser storage is a recoverable cache, not an authority. Existing sensitive sessionStorage exceptions are documented in the frontend and security standards and must not be broadened casually.

## Data Authority

| Data                                     | Authority       | Notes                                                     |
| ---------------------------------------- | --------------- | --------------------------------------------------------- |
| Players, tasks, duels, outcomes, history | PostgreSQL      | Durable and transactional                                 |
| Matchmaking queue, cache, revocation TTL | Redis           | Ephemeral; startup may clear or rebuild state             |
| Task source assets                       | SeaweedFS       | Accessed through storage adapters and public proxy policy |
| Browser restore and transport cache      | Session storage | Guarded, recoverable, never authoritative; currently includes sensitive values |
| Duel timer and terminal result           | Backend         | UI clocks are display-only                                |

## Current Reliability Boundaries

- Duel finish uses a database compare-and-set and a transaction. Preserve atomic terminal updates.
- WebSocket hubs, timers, reconnect counts, and delivery state are process-local.
- WebSocket terminal delivery is not a durable replay log.
- Redis updates can occur after PostgreSQL commit; PostgreSQL remains canonical.
- Automatic migrations run during backend startup, so schema changes must be deploy-compatible.

Do not present these current limitations as future product decisions. If a requirement needs stronger guarantees, implement an explicit migration path and tests.

## Hotspots

The canonical hotspot and exclusive-lock list lives in `ownership.md`. Prefer focused modules, reducers, or adapters for new behavior. Do not perform unrelated refactors while touching a hotspot.
