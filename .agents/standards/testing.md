# Frontend Testing And Validation Standard

## Principles

- Test accessible observable behavior rather than component internals.
- Keep fixtures deterministic: fixed IDs, explicit timestamps, controlled
  clocks, isolated browser contexts, and bounded retries.
- Validate malformed, stale, duplicate, unauthorized, forbidden, disconnected,
  and rate-limited behavior in addition to the happy path.
- Do not weaken assertions, skip tests, add arbitrary sleeps, or hide failures
  to make a change pass.
- Prefer role, label, placeholder, and user-visible text selectors. Avoid
  selectors coupled to CSS implementation details when an accessible selector
  exists.
- Clean up browser contexts, sockets, routes, temporary tasks, and disposable
  backend state created by a test.

## Existing Test Ownership

| Spec                               | Behavior owned                                                                   |
| ---------------------------------- | -------------------------------------------------------------------------------- |
| `e2e/player-home-contract.spec.ts` | Player join, cookie restore, logout, and leaderboard navigation                 |
| `e2e/admin-contract.spec.ts`       | Admin auth, refresh, tasks, players, source upload, audit history, and admin SSE |
| `e2e/leaderboard-contract.spec.ts` | Polling, stale requests, malformed responses, and cached visible state           |
| `e2e/csp-report.spec.ts`           | CSP headers and report endpoint behavior                                         |
| `e2e/live-backend.spec.ts`         | Explicitly gated admin smoke against a disposable live backend                   |
| `e2e/full-stack-local.spec.ts`     | Disposable compose stack, real cookie REST, task CRUD, and source download       |

Add a new focused spec when a new domain has independent behavior. Extend an
existing spec when the change belongs to the behavior listed above. Keep shared
browser helpers in `e2e/support/` and avoid a second fixture framework.

## Focused Gates

Run the narrowest affected test first from `frontend/`:

```bash
./node_modules/.bin/playwright test e2e/player-home-contract.spec.ts --workers=1
./node_modules/.bin/playwright test e2e/admin-contract.spec.ts --workers=1
./node_modules/.bin/playwright test e2e/leaderboard-contract.spec.ts --workers=1
./node_modules/.bin/playwright test e2e/csp-report.spec.ts --workers=1
```

Use a line or title filter while iterating, then rerun the complete owning spec:

```bash
./node_modules/.bin/playwright test e2e/admin-contract.spec.ts -g "test title" --workers=1
```

Contract-focused tests may mock REST and realtime boundaries. They must still
exercise the rendered page and validate the exact outgoing request or command
when that is part of the contract.

## Standard Frontend Gate

Do not install dependencies or browsers automatically. If `node_modules` or the Chromium runtime is missing, inspect `package.json`, the lockfile, npm configuration, lifecycle scripts, and the requested command. OpenAPI generation also requires Python 3 with PyYAML. If any prerequisite is missing, obtain current network and installation authority before installing it.

Prepare dependencies with lifecycle scripts disabled unless a reviewed package requires a specific setup step:

```bash
cd frontend
npm ci --ignore-scripts
./node_modules/.bin/playwright install chromium
```

The second command downloads a browser and requires explicit network authority. Verify that the local Playwright binary comes from the locked dependency. Run any required lifecycle step separately only after inspecting it.

Run validation from narrow to broad:

```bash
npm run openapi:generate
npm run typecheck
npm run lint
npm run build
npm run test:e2e -- --workers=1
```

With dependencies already installed, the repository shortcut is:

```bash
make NPX='npx --no-install' check
```

For dependency-sensitive or release work, also run:

```bash
make NPX='npx --no-install' verify
```

`make verify` includes the production dependency audit. Review every lockfile
change and do not suppress audit findings without evidence.

## Generated Contract Drift

After a backend REST schema change, regenerate and inspect the frontend type
artifact:

```bash
cd frontend
npm run openapi:generate
git diff -- lib/shared/api/schema.ts
```

An unexpected diff is a source-contract problem. Do not patch the generated
file. A contract handoff must include the generated diff or state that codegen
produced no change.

## Full-Stack Gate

The normal Playwright configuration excludes `full-stack-local.spec.ts`.
Cross-stack behavior must therefore run the explicit gate:

```bash
cd frontend
npm run test:e2e:full-stack
```

This command requires Docker, installed frontend dependencies, and the local disposable E2E environment described by `scripts/e2e-local-compose.sh`. The runner reads its env file and executes `docker compose down --volumes`; the bare command is therefore unsafe for agent use.

Before running it, all of these conditions are mandatory:

1. Inspect the runner and Compose file.
2. Create a task-owned synthetic env file outside the repository with mode `0600`; never reuse `.env` or `.env.local`.
3. Set `E2E_COMPOSE_ENV_FILE` to that exact file and supply a synthetic `E2E_ADMIN_PASSWORD`.
4. Set a unique task-owned `E2E_COMPOSE_PROJECT_NAME` and unique host ports.
5. Verify no Docker resource already has that Compose project label. If one exists, choose a new name rather than cleaning it.
6. Set `E2E_COMPOSE_CLEAN=0` so the runner does not perform its initial cleanup.
7. Confirm that final `down --volumes` can affect only resources created by this run.

Do not point the runner at shared, staging, production, or pre-existing local data. If these ownership conditions cannot be proved, skip the gate and report the residual risk.

Run full-stack validation for changes to:

- REST request or response shapes
- cookie, CSRF, refresh, logout, CORS, CSP, or origin behavior
- tournament realtime snapshot shapes
- readiness, draft, submission, pause, replay, result, or leaderboard behavior
- source upload and download behavior
- Next.js API rewrites
- browser and backend behavior that cannot be proven by mocked routes

`e2e/live-backend.spec.ts` is opt-in and must target an explicitly disposable
backend. Respect its environment gates and cleanup requirements.

## Test Requirements By Change Type

| Change                                | Minimum validation                                                                               |
| ------------------------------------- | ------------------------------------------------------------------------------------------------ |
| Pure styles with no behavior change   | Focused browser assertion where practical, typecheck, lint, build                                |
| Page or feature behavior              | Owning Playwright spec, typecheck, lint, build                                                   |
| REST adapter or response guard        | Focused success and malformed-response tests, codegen, typecheck, lint                           |
| Auth, CSRF, storage, or authorization | Positive and negative browser tests plus full-stack gate                                         |
| Realtime parser or state transition   | Valid and malformed snapshot tests, stale or wrong-owner tests, reconnect coverage, full-stack gate |
| Timer or official result              | Controlled browser clock, server-authority regression, reconnect coverage, full-stack gate         |
| Dependency or lockfile                | Standard gate plus `npm audit --omit=dev`                                                        |
| Deployment URL or rewrite             | Build, CSP or URL contract tests, and full-stack same-origin validation                          |

## Reliability And Security Cases

For async and realtime work, cover:

- cleanup on unmount and explicit close
- reconnect backoff and give-up behavior
- late responses from a replaced session
- stale socket generations
- missing initial snapshots and closed connections
- duplicate and out-of-order snapshots when a consumer accepts updates
- local timer versus server result ordering
- malformed storage and network payloads

For authenticated or public boundaries, cover:

- missing and expired sessions
- `401` versus `403`
- wrong-object access
- missing or invalid CSRF on unsafe requests
- redaction of flags, credentials, private URLs, and audit-only fields
- absence of secrets in storage, URLs, logs, and rendered errors

## Handoff

Report sanitized command structures and outcomes. If a required gate is skipped, give the
reason, affected behavior, and residual risk. Do not claim full validation when
only mocked contract tests ran, and do not claim contract compatibility when
codegen or the required full-stack scenario was skipped.
