# Task Per Minute Agent Framework

This directory is the public operating manual for coding agents working in this repository. It explains how to work. It does not define private product behavior.

## Privacy Boundary

- `.agents/`, root `AGENTS.md`, and root `CLAUDE.md` are public artifacts. They must be reviewed and committed before a clean clone can rely on them.
- `PRD/` is ignored and private. It may contain `PRD.md`, task registries, reports, challenge packages, evidence, and temporary planning material.
- Public agent documents may reference a private path, but must not copy private requirements, challenge flags, credentials, commercial details, or unpublished schedules.
- A clean public clone must remain usable when `PRD/` is absent.

## Required Reading

Every task starts with:

1. Root `AGENTS.md`.
2. `project.md` for repository orientation.
3. For implementation work, `workflows/task-lifecycle.md`, `workflows/validation.md`, and `workflows/handoff.md`.
4. `sources-of-truth.md` for the affected behavior.
5. `ownership.md` before parallel edits.
6. The relevant standards and workflow files below.

Do not load every document by default. Read the smallest set that covers the task.

## Agent Entry Points

- Codex discovers root `AGENTS.md`, which routes to canonical guidance in `.agents/`.
- Claude Code discovers root `CLAUDE.md`. That adapter imports `AGENTS.md` so both agents receive the same repository-wide rules.
- `.agents/` remains canonical. Keep the root adapter short and do not duplicate product or engineering rules in it.
- After moving or renaming the adapter or an imported file, resolve every relative import. In Claude Code, use `/memory` to verify the loaded hierarchy.

## Architecture Policy

- Backend work follows Clean Architecture inside a hexagonal monolith. The canonical dependency and ownership rules live in `architecture.md` and `standards/backend.md`.
- Frontend work follows Feature-Sliced Design. The canonical layer direction and public-surface rules live in `architecture.md` and `standards/frontend.md`.
- Preserve these boundaries through design, code review, narrow interfaces, and behavior-focused tests. Do not add `architecture_test.go`, AST import scanners, or reflection-only tests whose sole purpose is policing directory or layer shape.

## Routing Table

| Task area                | Read                                                                                       |
| ------------------------ | ------------------------------------------------------------------------------------------ |
| Repository orientation   | `project.md`, `architecture.md`                                                            |
| Product behavior change  | `sources-of-truth.md`; an exact private path only with current user authorization           |
| Any implementation       | `workflows/task-lifecycle.md`, `workflows/validation.md`, `workflows/handoff.md`            |
| Go backend               | `standards/backend.md`                                                                      |
| Database or migration    | `standards/database.md`, `standards/backend.md`                                             |
| React or Next.js         | `standards/frontend.md`, `standards/testing.md`                                            |
| REST, WebSocket, SSE     | `standards/contracts.md`, both stack standards                                              |
| Docker, Caddy, deploy    | `standards/infrastructure.md`, `standards/security.md`                                      |
| Auth, secrets, isolation | `standards/security.md`                                                                     |
| Agent policy or adapter  | `standards/security.md`, `workflows/validation.md`, `workflows/handoff.md`                   |
| Multi-agent work         | `ownership.md`, `workflows/subagents.md`                                                    |
| Release preparation      | `workflows/release.md`                                                                      |

## Framework Contents

- `project.md` - current public product and repository map.
- `architecture.md` - component and data-flow boundaries.
- `sources-of-truth.md` - canonical, derived, and target artifacts.
- `ownership.md` - parallel work and hotspot locks.
- `standards/` - stack-specific invariants and validation.
- `workflows/` - repeatable task, review, handoff, and release processes.
- `templates/` - small reusable task, ADR, and handoff formats.
- `../AGENTS.md` - compact Codex entrypoint and routing table.
- `../CLAUDE.md` - thin root Claude Code adapter over the same canonical rules.

## Maintenance Rules

- Update these files when repository commands, boundaries, generated paths, or CI gates change.
- Keep instructions factual, concise, and testable.
- Put product rules in the private PRD, architecture decisions in ADRs, wire contracts in their schemas, operations in runbooks, and executable acceptance in tests.
- Avoid copying the same rule into several files. Root `AGENTS.md` routes; detailed files explain.
- Examples must use placeholders and synthetic data, never real secrets or flags.
