# Frontend release recovery

Frontend rollback replaces frontend code only. It does not recover backend process-local state, reconnect history, database data, schema or tournament results. Never claim backend recovery from a green frontend healthcheck.

## Stop and assign ownership

- Pause rollout when health, browser, auth/CSRF, HTTP/WS proxy or reconnect checks fail, or when signature, scan or compatibility evidence is missing/stale. Preserve only sanitized incident evidence; do not print env files, rendered Compose JSON, cookies or raw payloads.
- The release owner stops advancement and records environment, observed frontend/backend/proxy identities, schema, symptoms and the agreed observation-window outcome. The recovery owner obtains explicit approval for each exact recovery action and target.
- Abort frontend rollback if backend/schema/API compatibility is uncertain, backend readiness/recovery is failing, state integrity is in doubt, or the prior artifact is unavailable/unverifiable. This is NO-GO; escalate to the backend/data owner for a separately approved roll-forward or incident recovery plan.

## Preconditions for frontend-only rollback

1. Identify the exact previously approved frontend index digest and source revision, complete OCI evidence, detached signature bundle and compiled public API/admin origins. Re-run real FE055 signature verification and a fresh security scan; a previously green report is not current authorization.
2. Backend owner confirms the current running backend digest/revision and live positive schema version, including any changes since rollout. Verify the prior frontend's schema bounds AND REST/WS contract compatibility against that actual backend. Do not infer compatibility from an image tag or schema number alone.
3. Confirm prior compiled origins remain correct for the currently approved public/admin/API hosts, CORS/WS allowlists, cookie/CSRF behavior and proxy paths. Runtime env changes cannot repair a frontend compiled for different URLs. Incompatible prior code requires a newly approved roll-forward artifact.
4. Keep the backend image/process, database volumes and proxy configuration unchanged. Confirm exactly one backend replica. Name the approver, release/recovery/backend owners, bounded restart timeout, expected frontend interruption and both post-recovery observation windows; active page sessions may need reload/reconnect.
5. Create a new rollback plan using the validator's current contract, selecting the prior frontend as the candidate. Re-render privately and re-run the [rollout validation procedure](release-rollout.md#render-and-validate-before-mutation) against the current backend/schema. Do not reuse the candidate rollout's PASS report or overwrite earlier evidence.

## Execute only the approved frontend replacement

From the same approved checkout and Compose file order, re-establish `compose_release` from [rollout](release-rollout.md). Export `FRONTEND_IMAGE` as the verified prior digest and keep `BACKEND_IMAGE` as the unchanged running digest. Consuming the exact approved env file still needs explicit authorization; do not inspect or source it.

```sh
# Templates only; obtain explicit environment/action approval first.
compose_release pull frontend
compose_release up -d --no-deps --no-build --pull never frontend
```

These commands do not restart backend or mutate schema. Do not run `migrate-down`, `down -v`, volume deletion, `reset --hard`, dependency recreation or backend scaling. Never switch a backend image automatically to make a frontend rollback pass. Proxy reload/image replacement is a separate operation requiring its own approval and compatibility checks.

## Validate recovery, then close or escalate

- Confirm the running frontend identity equals the approved prior digest and the backend/proxy identities are unchanged. Check frontend `/health` separately from backend HTTP 200 readiness, healthy dependencies/recovery and the actual schema version.
- Repeat public/admin separation, login/refresh/CSRF, CSP/security headers, HTTP path forwarding and real WS upgrade/reconnect checks. Backend owner confirms authoritative snapshots and tournament state remain coherent; successful static page rendering is insufficient.
- Observe the pre-agreed initial and extended windows. Stop on repeated health failures, auth regressions, WS churn, stale state or missing compatibility evidence. Do not loop through arbitrary older images or broaden scope automatically.
- If backend readiness or state remains wrong after frontend replacement, leave the incident open with the backend/data owner. Use their explicitly approved recovery or roll-forward plan; this runbook provides no database restoration or migration reversal procedure.
- Close only when the named owners accept fresh identity, schema/API, health and browser evidence for the recovered configuration. Record actions, interruption, remaining risks and rollback disposition without private configuration. A local synthetic proxy fixture or historical FE052 run cannot substitute for fresh real full-stack release evidence.
