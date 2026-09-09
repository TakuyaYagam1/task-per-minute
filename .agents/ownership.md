# Parallel Ownership

## Principle

Parallel work is safe only when each writer has an exclusive file set and shared contracts are coordinated through one owner. Agents share a working tree and must assume other valid edits can appear at any time.

## Before Dispatch

The coordinator must:

1. Inspect `git status --short`.
2. Record pre-existing modified and untracked files.
3. Divide work by bounded responsibility, not by arbitrary file count.
4. Assign exact files or directories to each writer.
5. Name the owner of shared contracts, generated artifacts, migrations, and lockfiles.
6. Tell every worker that it is not alone and must not revert or reformat unrelated changes.

## Recommended Lanes

| Lane              | Exclusive responsibility                                    | Coordination boundary             |
| ----------------- | ----------------------------------------------------------- | --------------------------------- |
| Backend domain    | Domain policies, usecases, ports, and process boot                | REST, realtime, migrations     |
| Database          | Migration sequence, SQL queries, sqlc inputs                | Backend owner and deploy owner    |
| REST contract     | OpenAPI sources, handlers, generated REST artifacts         | Backend and frontend consumers    |
| Realtime contract | WebSocket or SSE event definitions, parsers, protocol tests | Backend, frontend, feed consumers |
| Player UI         | Player routes, entities, features, widgets                  | REST and realtime owners          |
| Operator UI       | Admin or operator routes and features                       | Contract owner                    |
| Public UI         | Leaderboard and other unauthenticated surfaces              | Public API and contract owners    |
| Infrastructure    | Compose, Caddy, workflows, runtime configuration            | Backend config and release owner  |
| Test integration  | Cross-stack fixtures, broad E2E, CI drift checks            | All behavior owners               |
| Integrator        | Generated drift, lockfiles, merge resolution, final gate    | Every lane                        |

## Exclusive Hotspot Locks

Only one writer at a time may edit:

- `frontend/lib/pages/home/HomePage.tsx`
- `frontend/app/admin/page.tsx`
- `frontend/package-lock.json`
- `frontend/lib/shared/api/schema.ts`
- `backend/api/openapi.yml`
- `backend/api/components/security.yml`
- `backend/api/components/schemas/*.yml`
- `backend/internal/adapter/inbound/websocket/event.go`
- the next database migration number
- `backend/internal/bootstrap/wire_gen.go`
- `deployment/docker/docker-compose.yml`
- `deployment/docker/docker-compose.ci.yml`
- `deployment/docker/docker-compose.local.yml`

Generated output ownership belongs to the contract or integration owner, never to several feature workers simultaneously.

## Worker Contract

Each worker receives:

- Objective and non-goals.
- Exact owned files.
- Required source-of-truth documents.
- Inputs from earlier tasks and outputs expected by later tasks.
- Validation commands.
- Report path or response format.

Workers must not stage, commit, regenerate broad outputs, add dependencies, or modify another lane unless explicitly assigned.

## Collision Handling

If an owned file becomes modified by another writer:

1. Stop editing that file.
2. Preserve both sets of work.
3. Notify the coordinator with the exact path and observed diff scope.
4. Reassign ownership or sequence the work.
5. Never resolve a collision by reset, checkout, overwrite, or bulk formatting.

## Integration

The integrator reviews contracts before implementation diffs, regenerates derived files once, runs cross-stack validation, and reports any skipped gate. Parallel task completion is not release completion.
