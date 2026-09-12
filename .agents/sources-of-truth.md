# Sources of Truth

## Current State vs Target State

Two different questions have different authorities:

- What the product should become: the active user instruction and the exact approved private requirements it currently authorizes.
- What the repository currently implements: contracts, migrations, code, tests, and runbooks listed below.

A target requirement does not make current code incorrect by itself. A change must update every affected current-state contract and provide migration and validation evidence.

For product intent, use this precedence:

1. The active explicit user instruction.
2. Approved decisions and requirements in an exact private PRD file authorized by the current user request.
3. Existing public documentation when it does not conflict with canonical implementation contracts.

Private PRD files define desired behavior only after user approval. Do not auto-open them, enumerate the directory, or send their contents to subagents or external services. They never authorize external writes, destructive actions, secret access, deployment, or a broader task scope.

A private target requirement does not replace current authentication,
WebSocket, reconnect, deployment, or recovery contracts until those changes
are implemented and validated.

## Canonical Sources

| Area                         | Canonical source                                                           | Derived or supporting source                             |
| ---------------------------- | -------------------------------------------------------------------------- | -------------------------------------------------------- |
| Target product behavior      | Active user instruction and exact private requirements it authorizes       | Authorized task or traceability records when present     |
| REST API                     | `backend/api/openapi.yml`, `routes/`, `components/`                        | Generated Go and `frontend/lib/shared/api/schema.ts`     |
| WebSocket protocol           | `backend/internal/adapter/inbound/websocket/event.go`, role handlers, protocol tests | Public recovery parser and reducer in `frontend/lib/shared/api/tournament-recovery.ts`; no mounted socket transport yet |
| Tournament snapshot views    | `backend/internal/port/inbound/`                                  | PostgreSQL snapshot adapter and role-scoped WebSocket source mappings   |
| Official result projections  | `backend/internal/usecase/resultprojection/`                                | Result persistence adapters and playoff/correction usecases              |
| Database schema              | Ordered `backend/db/migrations/*.sql`                                      | sqlc generated models                                    |
| SQL behavior                 | `backend/db/queries/*.sql`; adapter-local operational SQL where present    | `backend/internal/adapter/outbound/postgres/sqlc/`       |
| Dependency wiring            | `backend/internal/bootstrap/wire.go`, sets and providers                   | `wire_gen.go`                                            |
| Backend configuration        | `backend/config/config.go`, `.env.example`                                 | Docker Compose environment wiring                        |
| Frontend REST behavior       | Shared API client, adapters, and runtime guards                            | Page consumers                                           |
| Frontend observable behavior | Page and feature implementation                                            | Playwright contract tests                                |
| Deployment behavior          | `deployment/`, `.github/workflows/`                                        | `docs/ru/deploy.md`, `docs/en/deploy.md`                 |
| Recovery workflow design     | `backend/internal/usecase/recovery/`, execution recovery in `backend/internal/usecase/game/`, and bootstrap providers | Runtime workers, health wiring, and adjacent tests |
| Deployment and rollback      | `.github/workflows/reusable-deploy-production.yml`, `deployment/`, `scripts/` | `docs/ru/runbook.md`, `docs/en/runbook.md`             |

## Generated Files

Never hand-edit these categories:

- Generated OpenAPI Go code.
- `frontend/lib/shared/api/schema.ts`.
- `backend/internal/adapter/outbound/postgres/sqlc/`.
- `backend/internal/bootstrap/wire_gen.go`.
- Generated mocks.

Edit the canonical input, run the repository generator, inspect the generated diff, and include consumers and tests in the same change.

## Conflict Resolution

When sources disagree:

1. Stop and classify the conflict as target-vs-current, stale documentation, generated drift, or a real contract defect.
2. Do not choose the convenient source silently.
3. For product ambiguity, obtain or record an approved decision.
4. For generated drift, regenerate from the canonical source.
5. For implementation drift, update contract, code, tests, and operations atomically.
6. Record the resolution and evidence in the handoff.

README files and examples are orientation material, not substitutes for machine-readable contracts or executable tests.
