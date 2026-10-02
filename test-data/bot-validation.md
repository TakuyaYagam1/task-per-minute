# Tournament bot validation

## Evidence

Local validation on 2026-10-02 in the project workspace.
The local test stack was subsequently rebuilt and recreated at the owner's
request. No remote deployment was performed.

- The local backend and frontend health endpoints and `/arena` return 200.
  The internal bot health endpoint returns `ready: true`; guest access to its
  public control gateway returns 401.
- The importer and account seeder exited successfully. The preserved local DB
  contains 54 tasks and 16 verified demo accounts. Its existing tournament remains
  in registration. Migration 41 is applied.
- The tester panel is enabled for the owner's existing verified account.
  The ignored `.env.test-bots.local` override has mode 0600. The original
  `.env.local` was consumed by Compose without being read or modified.
- Before restart, a compressed local DB backup was saved to
  `.local-backups/before-bots.dump` with mode 0600, in a directory with mode 0700.
  Neither the backup nor the local tester override is tracked in Git.

- The final lifecycle run passed for both 8 and 16 participants. Actual bot
  HTTP and WebSocket clients joined, checked in, played Swiss, entered Golden,
  drafted the final and completed BO3. The human and organizer test drivers used
  ordinary REST endpoints.
- Both runs created a second tournament with the same accounts, waited for all
  check-ins, cancelled it and verified the previous champion remained recorded.
- Restart restored a paused run. Unit tests covered reconnect, presence during
  pause, idempotent retries, series policy freezing and missing task versions.
- Negative tests rejected guests, other players, missing CSRF and forged control
  headers. The ordinary backend build returned 404 for test routes.
- Migration checks exercised one, two and three candidates, rejected an empty
  list and verified that restoring the old minimum rejects incompatible rows.
  Final publication tests covered atomic reservation cleanup and concurrency.
- Browser tests exercised panel controls and own-answer display without browser
  storage. They used mocked control responses; the tournament lifecycle ran
  separately against disposable PostgreSQL and Redis.

Built local image IDs:

| Image | SHA-256 |
| --- | --- |
| `task-per-minute-local-backend` | `b0b2f92a647f76a24f8f1a3af2800ad063e1c96aa8a10300b423b04df58e6411` |
| `task-per-minute-local-test-bots` | `a4a9c5cea4b2a604a34c1d9094968fccb4f727f6d74d60dae1e81eb0d6245e10` |
| `task-per-minute-local-test-data-importer` | `77d50d8bf1d2ec04d8588f25df064744dff27871535a5db028cc5914931101bc` |

## Changed Files

Paths below are relative to the repository root. Shared files retain the changes
that existed before this task.

| Files | Purpose |
| --- | --- |
| `backend/cmd/testbots/main.go` | Internal test service entrypoint and shutdown |
| `backend/internal/testbots/model.go` | Private manifests, atomic run storage, deterministic pairs |
| `backend/internal/testbots/client.go` | Player sessions, CSRF, HTTP, WebSocket reconnect |
| `backend/internal/testbots/engine.go` | Run lifecycle, command ledger, scenario validation |
| `backend/internal/testbots/player.go` | Player actions and own-assignment answers |
| `backend/internal/testbots/http.go` | Internal control API |
| `backend/internal/testbots/engine_test.go` | State, scenario, retry and access tests |
| `backend/internal/testbots/client_test.go` | Connection, policy, pause and failure tests |
| `backend/internal/testbots/contract_test.go` | OpenAPI validation |
| `backend/api/test-bots.yaml` | Separate test API contract |
| `backend/internal/adapter/inbound/http/v1/router.go` | Test route registration hook |
| `backend/internal/adapter/inbound/http/v1/testbots_enabled.go` | Test-only session gateway and internal read capability |
| `backend/internal/adapter/inbound/http/v1/testbots_disabled.go` | Ordinary-build no-op |
| `backend/internal/adapter/inbound/http/v1/testbots_enabled_test.go` | Gateway authorization and CSRF tests |
| `backend/internal/adapter/inbound/http/v1/testbots_disabled_test.go` | Ordinary-build 404 test |
| `backend/db/migrations/000041_normal_assignment_candidate_count.sql` | Approved candidate-count constraint correction |
| `backend/db/queries/participant_read.sql` | Consistent current-wave selection for BO3 |
| `backend/internal/adapter/outbound/postgres/sqlc/participant_read.sql.go` | Regenerated sqlc output |
| `backend/internal/usecase/tournament/participant/participant_query.go` | Next-wave readiness after a completed BO3 game |
| `backend/internal/usecase/tournament/participant/participant_lobby_test.go` | Readiness regression test |
| `backend/internal/adapter/outbound/postgres/projection/projection_final.go` | Release participation locks with final publication |
| `backend/integration_test/projection_final_repository_test.go` | Reservation cleanup and rollback assertions |
| `backend/integration_test/normal_candidate_migration_test.go` | Constraint migration and representative data checks |
| `backend/integration_test/test_bots_test.go` | Complete 8/16-player acceptance scenarios |
| `backend/integration_test/restapi_fixture_test.go` | Policy-compliant synthetic fixture password |
| `backend/integration_test/tournament_create_to_champion_test.go` | Real admission service in the existing REST fixture |
| `frontend/lib/shared/api/test-bots.ts` | Typed control client and response validation |
| `frontend/lib/widgets/test-bots/TestBotsPanel.tsx` | Collapsible tester panel |
| `frontend/lib/widgets/test-bots/TestBotsPanel.module.css` | Responsive panel styling |
| `frontend/lib/widgets/test-bots/index.ts` | Widget export |
| `frontend/lib/pages/arena/ArenaRolePage.tsx` | Player-page panel mount |
| `frontend/lib/pages/arena/ArenaPublicTournamentPage.tsx` | Registration-page panel mount |
| `frontend/lib/widgets/tournament-admin/SwissPairingEditor.tsx` | Manual scenario pair prefill |
| `frontend/e2e/test-bots.spec.ts` | Browser control and access tests |
| `test-data/importer/bot_manifest.py` | Private task/version/answer catalog and separate control key |
| `test-data/importer/test_bot_manifest.py` | Atomic replacement, permissions and volume separation tests |
| `test-data/importer/import_tasks.py` | Capture created and reused task versions after import |
| `test-data/importer/Dockerfile` | Manifest writer and private volume directories |
| `backend/Dockerfile` | Separate bot and test-backend targets; ordinary runtime stays default |
| `deployment/docker/docker-compose.test.backend.yml` | Test-only backend overlay |
| `deployment/docker/docker-compose.local.test.yml` | Local runner wiring |
| `deployment/docker/docker-compose.test.yml` | Server test runner and private volumes |
| `scripts/tests/test-seed-compose_test.py` | Additional bot isolation assertions in existing seed checks |
| `.env.example` | Optional tester ID setting |
| `.gitignore` | Exclude the local tester override and local database backups |
| `test-data/README.md` | Operation, lifecycle, migration and recovery instructions |
| `test-data/bot-validation.md` | This validation record |

Pre-existing license, root README, leaderboard, task-catalog and account-seeder
changes were preserved. No ownership collision occurred.

## Commands Run

Commands below used the cached Go 1.26.8 toolchain, an allowlisted environment,
`GOTOOLCHAIN=local`, `GOPROXY=off`, and `GOSUMDB=off`. Container tests used the
local Podman socket and disposed of their own test databases and containers.
No credentials or manifest contents were printed.

| Working directory | Command | Exit / result |
| --- | --- | --- |
| `backend` | `go test -race -count=1 -parallel=5 ./...` | 0, all ordinary-build unit packages passed |
| `backend` | `go test -race -tags=testtools ./internal/testbots ./internal/adapter/inbound/http/v1` | 0, runner and test gateway passed |
| `backend` | `go test -tags=integration,testtools ./integration_test -run '^TestBotsTournamentLifecycle$' -count=1 -timeout=15m -v` | 0, final run 314.754 seconds |
| `backend` | `go test -tags=integration ./integration_test -run '^(TestNormalCandidateCountMigration\|TestFinalProjectionRepositoryCommitsAuthoritativeResult\|TestFinalProjectionConcurrentPublication)$' -count=1 -timeout=5m -v` | 0, all three checks passed |
| `backend` | `go tool golangci-lint run --build-tags=testtools ./cmd/testbots/... ./internal/testbots/... ./internal/adapter/inbound/http/v1/... ./internal/usecase/tournament/participant/... ./internal/adapter/outbound/postgres/projection/...` | 0, zero issues |
| `backend` | `make gen-sqlc` | 0, query bindings regenerated |
| `backend` | `make lint-sql` | 0, migrations and queries passed |
| `frontend` | `npm run typecheck` | 0 |
| `frontend` | `npm run lint` | 0, including FSD checks |
| `frontend` | `npm run build` | 0, production build |
| `frontend` | `E2E_FRONTEND_PORT=3117 E2E_BACKEND_URL=http://127.0.0.1:1 node node_modules/@playwright/test/cli.js test e2e/test-bots.spec.ts --workers=1 --reporter=line` | 0, 2 tests passed |
| `test-data/importer` | `python3 -m unittest test_bot_manifest test_easy_tasks` | 0, 8 tests passed |
| Repository root | `python3 scripts/tests/test-seed-compose_test.py` | 0, 4 tests passed |
| Repository root | `docker compose --env-file .env.local -f deployment/docker/docker-compose.local.test.yml build backend test-bots test-data-importer` | 0, three images built; no containers recreated |
| Repository root | Local and server test Compose `config --quiet` | 0, both configurations rendered |
| Repository root | `git diff --check` | 0 |

Local activation also completed successfully with exit 0:

```sh
docker compose --env-file .env.local -f deployment/docker/docker-compose.local.test.yml build frontend backend test-bots test-data-importer test-accounts
docker compose --env-file .env.local --env-file .env.test-bots.local -f deployment/docker/docker-compose.local.test.yml config --quiet
docker compose --env-file .env.local --env-file .env.test-bots.local -f deployment/docker/docker-compose.local.test.yml up -d --force-recreate --no-build --pull never --wait --wait-timeout 180
```

Read-only SQL checked the migration version, task count, verified demo-account
count and tournament state. PostgreSQL volumes were retained. The backup used
`pg_dump -Fc` inside the existing PostgreSQL service, consuming its configured
credentials without printing them.

## Validation Result

The solo flow works with 7 or 15 bots and manual organizer actions. Golden ties
and progression were exercised for both roster sizes. The final BO3 exercised
draft turns, consecutive games and series policy retention. The first Swiss
round exercised human defeat; subsequent rounds exercised human wins.

Credentials and answers are supplied through separate private volumes. Only the
bot service receives their manifests. The UI receives an answer only after the
backend validates the tester session and current assigned task version.

## Skipped Checks

- BO5: not present in the application's tournament model. BO1 and BO3 were
  validated; adding another series format remains a separate product change.
- Remote deployment and live shared-database migration: outside this request.
- Full repository integration matrix: focused lifecycle, migration, publication
  and existing champion regression checks were used. Release owners should run
  their normal full release gate before deployment.
- Manual authenticated browser walkthrough on the owner's current stack: left
  to the owner. Service readiness, seeded data and guest isolation were checked;
  automated authenticated flow validation used the disposable stack above.

## Residual Risks

- Migration 41 validates a CHECK under an exclusive table lock, with bounded
  lock and statement timeouts. Use the documented maintenance/roll-forward
  procedure on a shared installation.
- Normal login limits remain in effect. Initial bot login can wait for
  `Retry-After`; the test runner does not bypass those limits.
- Integration fixtures use a test limiter; production throttling is covered at
  the client retry boundary, not by a full timed production-limit rehearsal.
- Human-win mode withholds the opponent's correct answer. It does not suspend
  real tournament deadlines or prevent a human from submitting too late.

## Next Best Action

Open `http://localhost:3000/arena` as the configured tester. Start a new 8-player
tournament for the first manual UI walkthrough, following the
[startup guide](README.md). Keep the admin tab responsible for pair saving, waves
and stages. Pass both local env files when recreating this stack again.
