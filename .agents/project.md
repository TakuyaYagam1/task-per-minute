# Project Overview

## Purpose

Task Per Minute is a competitive CTF platform for short one-on-one challenge duels. Players join, enter matchmaking, receive a task, submit a flag, and receive a server-authoritative result. The repository also contains leaderboard and administrative flows.

This document describes the current public implementation. Desired future behavior belongs in a private requirement file only when the active user instruction authorizes that exact path.

## Repository Map

| Path                   | Responsibility                                                                                        |
| ---------------------- | ----------------------------------------------------------------------------------------------------- |
| `backend/`             | Go application, domain logic, REST and WebSocket transports, persistence, migrations, code generation |
| `backend/api/`         | Canonical OpenAPI routes, components, and root specification                                            |
| `backend/db/`          | Canonical Goose migrations and sqlc query sources                                                       |
| `backend/codegen/`     | Reproducible configuration for OpenAPI, sqlc, Wire-adjacent mocks, and generators                       |
| `backend/cmd/`         | Application and migration command entrypoints                                                           |
| `backend/config/`      | Backend runtime configuration parsing and validation                                                     |
| `backend/internal/`    | Domain, usecases, inbound and outbound adapters, bootstrap, errors, and context helpers                  |
| `frontend/`            | Next.js application, player UI, task UI, leaderboard, admin UI, browser contract tests                |
| `deployment/`          | Docker Compose and Caddy runtime configuration                                                        |
| `scripts/`             | Server bootstrap, deployment helpers, and disposable full-stack E2E orchestration                     |
| `docs/ru/`, `docs/en/` | Deployment and operational runbooks                                                                   |
| `.github/workflows/`   | CI and deployment automation                                                                          |
| `.agents/`             | Public coding-agent operating guidance                                                                |
| `AGENTS.md`            | Root Codex instruction entrypoint and route map                                                        |
| `CLAUDE.md`            | Root Claude Code adapter importing the shared agent entrypoint                                         |
| `PRD/`                 | Private ignored requirements, task content, reports, and temporary project artifacts; may be absent   |

## Current User Surfaces

- Player join and cookie-backed session.
- Matchmaking queue and duel assignment.
- Task presentation, hints, source links, flag submission, surrender, reconnect, pause, and terminal result.
- Public leaderboard.
- Administrative task and player management.
- REST health endpoints and real-time WebSocket gameplay.

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
