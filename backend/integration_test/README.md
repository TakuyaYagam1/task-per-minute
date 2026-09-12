# Integration test validation

The integration suite runs as one Go test package process. Keep `-p=1` so
there is one package-level `TestMain`, and use the supported `-parallel=5` cap
for tests and subtests that call `t.Parallel()`.

`TestMain` owns the shared PostgreSQL admin/sequential pool and lazily shared
Docker-backed Redis and SeaweedFS services for the package. DB-backed tests
that call `t.Parallel()` use `newParallelTestDB`, which provisions a separate
migrated disposable database for each test. Redis and SeaweedFS remain shared,
so use unique scoped keys. A test may run in parallel only when its database
rows, Redis keys, and object keys are collision-free. Use `uniq(...)` and the
existing scoped fixture helpers for that isolation. Tests that reset shared
tables with `TRUNCATE`, `resetMigrationTables`, or `truncateRoundProofTables`
must remain non-parallel and retain their cleanup.
The verified parallel gate deliberately unsets `TPM_TEST_POSTGRES_DSN`, so
destructive setup can target only the suite-owned disposable Testcontainers
PostgreSQL.

The standard integration race gate is:

```sh
env -u TPM_TEST_POSTGRES_DSN go test -race -count=1 -p=1 -parallel=5 -timeout=20m -tags=integration ./integration_test/...
```

For repeated order and race coverage, run three independent invocations with
fixed shuffle seeds. Each invocation recreates the package-level services, so
one repetition cannot carry state into the next:

```sh
for seed in 1 2 3; do
  env -u TPM_TEST_POSTGRES_DSN go test -race -count=1 -shuffle="$seed" -p=1 -parallel=5 -timeout=20m -tags=integration ./integration_test/...
done
```

These commands describe the required validation. A passing result must be
recorded from a fresh run; this document does not assert a runtime result.
