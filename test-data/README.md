# Test fixture stack

## Solo tournament testing

The test Compose starts `test-bots` after the importer and account seeder finish.
Set `TEST_BOTS_CONTROLLER_PLAYER_ID` to the UUID of your verified player account
in the test environment, then recreate `backend` and `test-bots`. Without this
setting the controls stay disabled. The ordinary backend image has no test
routes, even if the variable is set. The runner has no published port.

For a local tester override, keep only `TEST_BOTS_CONTROLLER_PLAYER_ID` in the
ignored `.env.test-bots.local` file (mode 0600), without changing `.env.local`.
Pass both files on subsequent Compose commands so recreating the backend does
not disable the panel:

```sh
docker compose --env-file .env.local --env-file .env.test-bots.local \
  -f deployment/docker/docker-compose.local.test.yml up -d --build
```

1. In the admin tab, create an 8- or 16-player tournament with the imported
   catalog, zero reserves, and open registration.
2. In the player tab, join and confirm participation. Open "Тестирование" and
   select "Заполнить ботами". The remaining 7 or 15 accounts join and check in.
3. In the admin tab, run preflight, lock the roster, start Swiss, configure
   pairings, open readiness and start each wave. Bots only perform player actions.
4. For free play, choose the next series outcome and answer delay. The choice
   locks when the series starts and remains the same across BO3 games. To win,
   submit the answer through the normal player form. "Показать тестовый ответ"
   and "Скопировать" are available only for your own active assignment.
5. Advance stages manually. Pause stops bot gameplay but retains connections;
   tournament timers continue. A service restart restores the run paused.
6. Once completed or cancelled, the runner releases the accounts. Start the next
   run in a new tournament. Existing results and database records are retained.

For the reproducible Golden path, set Swiss and semifinals to Crypto with zero
reserves, choose "Пройти Golden" before Swiss and use "Подставить пары сценария"
in the admin pairing editor for every round. Review and save the form yourself.
Lose round 1 and win rounds 2-3 (8 players) or 2-4 (16 players). This creates the
top-four points tie. Open Golden and its readiness window in the admin tab.
Bots start answering Golden after 60 seconds, leaving time for your own answer.
An incompatible pairing, series category override or completed result pauses the
run with a reason; results are never rewritten. The next-series outcome remains
available for playoffs; the Golden scenario fixes Swiss outcomes. The application
supports BO1 and BO3, not BO5.

The importer writes exact task/version/answer mappings for created and reused
tasks into the private `test-bot-catalog` volume. The runner reads it and
`test-account-seed` read-only. `test-bot-state` stores an atomic run record and
command keys, not passwords or answers. Keep all these volumes with the database.
The separate `test-bot-control` volume carries only the internal control key;
the backend does not mount the account or answer manifests.
Do not publish manifests, mount them into the frontend, or copy them into images.
The independent HTTP contract is [test-bots.yaml](../backend/api/test-bots.yaml).

On HTTPS stands, the runner keeps its cookie jar scoped to the public origin and
routes requests directly to the private backend transport, as the reverse proxy
does. Secure cookies, CSRF, session checks and normal rate limits remain enabled.
Initial logins can take longer when the shared login limit returns Retry-After.

### Database compatibility

Migration 41 fixes the historical minimum of three exact-normal task candidates
when the configured reserve count is zero. It changes only that minimum to one;
the plan guards still require the selected primary and configured reserves.
The 16-player fixture can reach this case while preparing the second semifinal,
depending on the participants' task history.

Risk is moderate: replacing and validating the check takes an ACCESS EXCLUSIVE
table lock and scans existing source rows, without rewriting or deleting them.
Apply before starting new tournaments; old application code remains compatible.
Use a maintenance window and a bounded lock timeout on a shared installation.
Rollback validates the old minimum and refuses if one- or two-candidate evidence
exists. In that case keep version 41 and roll forward; never delete tournament
evidence to make a downgrade succeed. Remote/shared application is a separate step.

The lifecycle also corrects two existing BO3 read inconsistencies: current wave
selection follows creation order, and a completed game no longer hides readiness
for the next wave. Final champion publication now releases participation locks
in the same transaction, so the accounts can join another tournament immediately.
Historical assignments, results and player records remain intact.

Verification (read-only):

```sql
SELECT pg_get_constraintdef(oid)
FROM pg_constraint
WHERE conrelid = 'public.exact_normal_assignment_sources'::regclass
  AND conname = 'exact_normal_assignment_sources_check';
SELECT min(jsonb_array_length(candidates)) AS minimum_candidates,
       count(*) FILTER (WHERE jsonb_array_length(candidates) < 1) AS invalid_rows
FROM public.exact_normal_assignment_sources;
```

The bot lifecycle integration test uses disposable PostgreSQL and Redis and
real player HTTP/WebSocket handlers:

```sh
cd backend
go test -tags=integration,testtools ./integration_test -run TestBotsTournamentLifecycle -count=1 -timeout=15m
go test -tags=integration ./integration_test -run TestNormalCandidateCountMigration -count=1
go test -race ./internal/testbots
go test ./internal/adapter/inbound/http/v1 -run TestProductionBuildHasNoBotRoutes
go test -tags=testtools ./internal/adapter/inbound/http/v1 -run TestBotGateway
```

## Catalog and startup

The test importer creates or reuses 54 catalog tasks: 44 normal tasks, including
35 Crypto tasks, and 10 Golden tasks (8 Crypto, 1 Web, and 1 Reverse). The
existing five web runtimes and their domains are reused; no additional web
services or domains are needed. With the default stage setup and zero reserves,
a 16-player tournament requires 51 tasks, so the fixture supports the default
8- and 16-player cases.

The fixture also creates 16 verified demo accounts, `demo01` through `demo16`.
The seeder writes their generated passwords to `/seed/accounts.json` in the
`test-account-seed` volume. It reuses the accounts and credentials after
restarts and does not print credentials in its logs. Keep that volume with its
database so account records, passwords, and sessions stay together. The
`test-task-seed` volume holds the private seed used to derive generated task
answers; the database stores the tasks and answers. Removing the account volume
loses the password manifest. Removing `test-task-seed` can change generated
answers after reimport. Neither test stack clears or resets data automatically.

Before starting, place an `admin/flag.txt` file for each of the twelve packaged
tasks. Compose mounts these files read-only. A clean clone needs them supplied
separately. Keep challenge flags, hints, writeups, solver material, runtime
databases, caches, and dependencies local; repository ignore rules exclude them.

Start the server test stack with the server test environment:

```sh
docker compose --env-file .env -f deployment/docker/docker-compose.test.yml up --build
```

Start the local test stack with the local environment:

```sh
docker compose --env-file .env.local -f deployment/docker/docker-compose.local.test.yml up --build
```

The server importer uses `TASK_*_URL` overrides when set. Otherwise, it builds
HTTPS URLs from the existing `TASK_*_DOMAIN` values under `APP_DOMAIN`; configure
those domains and Caddy certificates as for the server test stack. Local test
overrides the five task URLs to loopback HTTP on ports 3001 through 3005. Both
importers use `ADMIN_PASSWORD` from the matching environment file.

Both test Compose files start the task importer and account seeder automatically.
The server test project is named `task-per-minute-test`; its PostgreSQL and other
named volumes are separate from the regular server stack. The local test project
uses `task-per-minute-local`, the same project and database volumes as the normal
local stack. The local importer adds or reuses fixture tasks in that local
database, so keep `DB_DSN` pointed at the local `postgres` service and do not use
a production database.

The two test stacks use the same host-facing application and Caddy ports. Start
them only when those ports are available. The test task services remain on the
isolated task network, and the importer uses the existing five web task domains.
Generated tasks and accounts are test data, not a production roster or board.

The one-shot importer creates or reuses catalog tasks without overwriting
changed tasks, uploads public source archives, and verifies a published
tournament content revision. The one-shot account seeder waits for a healthy
backend and checks that the connected database name matches `POSTGRES_DB`. A
database mismatch or account collision fails closed. Both one-shot services may
run again after a stack restart; task and account data persist in their named
volumes. If either service exits with an error, inspect its status and logs
before retrying:

```sh
docker compose --env-file .env -f deployment/docker/docker-compose.test.yml ps -a test-accounts test-data-importer
docker compose --env-file .env -f deployment/docker/docker-compose.test.yml logs --no-color test-accounts test-data-importer
```

For local test, use `.env.local` and `deployment/docker/docker-compose.local.test.yml`
in the same commands. Do not delete the seed volumes to recover from a failed
run. Keep test accounts, task answers, and their corresponding test database
together across restarts.

To read the generated demo credentials manually, use the read-only volume
command for the stack in use. It prints passwords in clear text to the terminal;
run it only in a trusted terminal and do not copy the output into logs or share
it. The `alpine:3.23` image is already used by the backend build.

```sh
docker run --rm --network none --read-only --mount type=volume,source=task-per-minute-test_test-account-seed,target=/seed,readonly --entrypoint /bin/cat alpine:3.23 /seed/accounts.json
```

For local test, the volume name is
`task-per-minute-local_test-account-seed`. Do not run destructive volume cleanup
commands as part of normal test startup or restart.
