# Project Overview

## Purpose

Task Per Minute is a competitive CTF tournament platform. Operators manage a roster and tournament lifecycle, participants receive private task assignments, and the server owns result, recovery, audit, and public projection authority. The repository also contains leaderboard and administrative flows.

This document describes the current public implementation. Desired future behavior belongs in a private requirement file only when the active user instruction authorizes that exact path.

## Repository Map

| Path                   | Responsibility                                                                                        |
| ---------------------- | ----------------------------------------------------------------------------------------------------- |
| `backend/`             | Go application, domain logic, REST and WebSocket transports, persistence, migrations, code generation |
| `backend/api/`         | Canonical OpenAPI routes, components, and root specification                                            |
| `backend/db/`          | Canonical Goose migrations and sqlc query sources                                                       |
| `backend/codegen/`     | Reproducible OpenAPI, sqlc, Wire, and Mockery generator configuration                                   |
| `backend/cmd/`         | Application and migration command entrypoints                                                           |
| `backend/config/`      | Backend runtime configuration parsing and validation                                                     |
| `backend/internal/`    | Process boot, domain policies, usecases, ports, adapters, bootstrap, and context helpers                 |
| `frontend/`            | Next.js application, player join, leaderboard, admin UI, and browser contract tests                   |
| `deployment/`          | Docker Compose and Caddy runtime configuration                                                        |
| `scripts/`             | Server bootstrap, deployment helpers, and disposable full-stack E2E orchestration                     |
| `docs/ru/`, `docs/en/` | Deployment and operational runbooks                                                                   |
| `.github/workflows/`   | CI and deployment automation                                                                          |
| `.agents/`             | Public coding-agent operating guidance                                                                |
| `AGENTS.md`            | Root Codex instruction entrypoint                                                                      |
| `AGENT_ROUTING.md`     | Project route, risk, companion, and validation selector                                                |
| `CLAUDE.md`            | Root Claude Code adapter importing the shared agent entrypoint                                         |
| `PRD/`                 | Private ignored requirements, task content, reports, and temporary project artifacts; may be absent   |

## Current User Surfaces

- Player join and cookie-backed session.
- Public leaderboard.
- Administrative task and player management.
- Operator tournament catalog listing and tournament creation.
- REST health endpoints and role-scoped, read-only tournament WebSocket snapshots.

All protocol-neutral business workflows live under `internal/usecase`,
including assignment, authority, draft, game execution, Golden, Swiss,
tournament administration, progression, recovery, realtime delivery, result
projection and correction, leaderboard caching, and task source files.
`internal/app/app.go` only starts the dependency graph assembled by
`internal/bootstrap`. A usecase is not a public REST surface until it has an
inbound port, durable adapters where required, composition-root wiring, and
contract tests for the complete operation.

Do not assume an unimplemented feature exists merely because a private PRD mentions it. Verify current behavior against the canonical implementation contracts.

## Technology Baseline

- Backend: Go 1.26.2, Chi, pgx, sqlc, oapi-codegen, Wire, Redis, Goose, WebSocket.
- Frontend: Next.js 15, React 19, TypeScript, generated OpenAPI types, Playwright.
- Data: PostgreSQL is durable authority, Redis is ephemeral coordination and cache, SeaweedFS stores task source assets.
- Runtime: Docker Compose, Caddy, GitHub Actions.

Version changes require an explicit task, compatibility review, regenerated artifacts where applicable, and validation of deployment images.

## Product Change Boundary

For a feature request:

1. Determine whether it changes observable product behavior.
2. If yes, require an approved private requirement or an explicit user decision.
3. Map the requirement to current REST, realtime, data, UI, test, and operations contracts.
4. Do not silently reinterpret current behavior to fit an aspirational document.
5. Keep current-state documentation and target-state requirements distinct until the implementation and migration are complete.
