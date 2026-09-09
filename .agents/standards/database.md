# Database Standard

## Ownership And Sources

- PostgreSQL is the system of record for players, tasks, tournaments, assignments, results, projections, recovery, audit records, and canonical leaderboard statistics.
- Redis owns revocation entries with TTL and explicitly disposable caches. Do not treat Redis as a durable tournament or scoring ledger.
- Goose migrations under `backend/db/migrations/` are the schema source of truth.
- SQL under `backend/db/queries/` is the source of truth for sqlc-managed queries. A small amount of handwritten operational SQL currently lives beside its PostgreSQL adapter, including schema-version reads and LISTEN or UNLISTEN commands. Generated sqlc code is derivative and must not be edited manually.
- Usecase packages own their narrow repository ports. PostgreSQL implementations live in `backend/internal/adapter/outbound/postgres`.

## Schema And Migration Rules

- Use ordered Goose migrations and never modify a migration that may already have been applied outside disposable local environments.
- Prefer expand -> deploy compatible code -> backfill -> enforce -> contract.
- Default to additive nullable columns or new tables. Classify renames, drops, type rewrites, mass updates, new uniqueness, and `NOT NULL` enforcement as high risk.
- For large or unknown tables, analyze PostgreSQL locks, rewrite cost, transaction duration, replication impact, and rollback before editing.
- Make backfills bounded, ordered, idempotent, resumable, observable, and independently verifiable.
- Keep old code compatible with the expanded schema until contract cleanup. Do not combine a risky schema change, application cutover, and large backfill into one unexplained step.
- Add constraints only after existing data is verified. Use staged validation or concurrent index creation where PostgreSQL and Goose transaction rules require it.
- Every migration proposal must state risk level, deploy order, roll-forward plan, rollback or restore plan, and verification queries.

## Data Integrity Invariants

- Series participants must differ. A winner, when present, must be one of the Series participants.
- Tournament, Series, game, wave, roster, and pause state must remain consistent with their timestamps and revisions.
- An assignment task snapshot is immutable, private, and linked to its delivery receipt and runtime instance.
- Final-state writes use conditional revision and state checks. Preserve compare-and-set behavior for concurrent submissions, recovery, replay, and correction paths.
- Keep official result, score head, audit, projection evidence, and outbox changes in one transaction. Nested repository work must reuse the transaction from context.
- Capture authoritative timestamps once per operation and pass them through the transaction. Do not mix client timestamps into settlement state.
- Leaderboard cache updates occur only after PostgreSQL commit. Canonical reads must remain derivable from PostgreSQL.
- Admin leaderboard overrides intentionally take precedence over computed statistics and must remain auditable.
- Task deletion must fail while immutable tournament evidence references the task. Treat historical data destruction as an explicit operation.
- The recovery usecase reconstructs authority from persisted tournament state and production startup composes it. Schema changes must preserve recovery evidence and deadline rearming.

## Validation

Follow the env-inheritance and tool-install preflight in `backend.md` before any backend `make` command. Applying migrations requires an explicitly authorized disposable database and a validated target supplied without inspecting the user's private env file.

- Run `make lint-sql` after changing SQL migrations or sqlc queries.
- Never auto-fix an applied migration. `make lint-sql-fix` intentionally edits only `db/queries/` and then checks both queries and migrations.

```bash
make lint-sql
make gen-sqlc
make mocks
make test
make build
make test-int
```

Migration inspection and local application commands are:

```bash
make migrate-status
make migrate-up
```

Use `make migrate-create NAME=<snake_case_name>` to create a new ordered migration. Verify both an empty database and representative existing data before approving a risky migration.

## Destructive Boundaries

- Never run `make migrate-down` against shared, production, or unknown data without explicit approval, a compatible application image, and a verified backup or restore plan.
- Never execute ad hoc `DROP`, `TRUNCATE`, unbounded `DELETE`, unbounded `UPDATE`, or a destructive backfill without exact target validation and explicit authorization.
- Do not claim rollback safety for lossy data transformations. Prefer roll-forward recovery when schema rollback would lose data.
- Never delete named database volumes or reset a database as a substitute for diagnosing migration or integrity failures.
- Do not expose connection strings, credentials, session tokens, private task data, or stored sensitive values in logs, tests, fixtures, or handoff text.
