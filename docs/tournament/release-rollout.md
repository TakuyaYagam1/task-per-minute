# Frontend release rollout

This is an operator runbook, not deployment authorization. Before each pull, up, reload or recovery, obtain explicit approval naming the environment, exact action and immutable images. Local checks never authorize production operations or access to a production env file.

## Ownership and evidence

- Record release owner, approver, backend compatibility owner, recovery owner, environment, maintenance window, incident channel and prior frontend digest. Agree startup timeout and initial/extended observation windows before starting; do not invent them during an incident.
- Ordinary [pipeline](../../.github/workflows/pipeline.yml) push, PR and manual runs check/build only. The protected-main manual [publication workflow](../../.github/workflows/reusable-publish-tournament-images.yml), with the `frontend-release` environment approval, signs and copies an existing verified OCI index without rebuilding. It does not deploy.
- Retrieve the complete OCI layout, including runtime layers, SPDX and provenance blobs, plus matching `request.json`, `metadata.json`, `identity.json` and the approved revision's lockfile from retained release evidence. Resolve the registry index by digest and retrieve its detached Sigstore bundle through the ORAS referrer of type `application/vnd.dev.sigstore.bundle.v0.3+json`, using policy-pinned tools. A tag, partial image pull or signature JSON alone is insufficient.
- Run the [release verifier](../../scripts/release/verify-frontend-release.mjs) with the exact repository, revision, index reference, bundle and pinned Cosign. This is detached OCI-index `verify-blob` evidence, not the `cosign verify IMAGE` protocol. Require real signature verification, not synthetic test evidence; independently confirm the registry index equals the signed index.
- Require a fresh security scan of that same index and current CI results. Signature verification does not replace scanning, compatibility checks or rollout validation. Complete and record the pinned Caddy version/license/provenance review before GO.

From an approved checkout, with evidence paths and tools already approved:

```sh
node scripts/release/verify-frontend-release.mjs \
  --artifact "$RELEASE_DIR/artifact" --bundle "$RELEASE_DIR/artifact/signature.sigstore.json" \
  --image-ref "$FRONTEND_IMAGE" --repository "$APPROVED_REPOSITORY" --revision "$SOURCE_REVISION" \
  --lockfile "$APPROVED_LOCKFILE" --cosign "$PINNED_TOOLS/cosign" --output "$RELEASE_DIR/signature-gate.json"
```

## Compatibility and routing

- Use the plan contract in [validate-frontend-rollout.mjs](../../scripts/release/validate-frontend-rollout.mjs), not an ad hoc JSON shape. It includes `schema_version: 1`, canonical 40-hex `source_revision`, frontend/backend digest references, public/admin/API origins, `public_build_args`, current schema, frontend schema bounds and the prior frontend image with rollback schema bounds.
- Backend owner verifies the actual running backend digest/revision, current positive schema version and REST/WS API compatibility for both candidate and prior frontend. The declared schema range alone is not proof. No generic migration or backend rollback is part of this procedure.
- Public and admin origins are distinct HTTPS origins. Both API settings are empty for same-origin routing, or both are approved canonical HTTPS root origins, without `/api/v1`, another path, query or fragment. The admin API origin may differ from the public API origin, including use of the admin host. Match `NEXT_PUBLIC_API_URL`, `NEXT_PUBLIC_ADMIN_API_URL` and other public build arguments to the verified artifact and browser behavior; changing runtime environment variables cannot rebuild compiled URLs.
- Preserve [Caddy routing](../../deployment/caddy/Caddyfile): public host blocks `/admin`, `/api/v1/admin` and `/internal` including descendants; admin host retains its admin routes; the separate API host forwards HTTP and WS paths unchanged. Do not strip `/api`, weaken cookie/CSRF checks or add path rewrites to mask incompatibility.
- Require exact approved HTTP/WS Origin allowlists and `WS_REQUIRE_ORIGIN=true`; no wildcard origins or all-address trusted proxy CIDRs such as `0.0.0.0/0` or `::/0`. Proxy trust covers only the approved proxy network.
- Keep exactly one backend process/replica: WS hubs, reconnect state and timers are process-local. Never scale for this rollout. A count mismatch is NO-GO for the backend owner, not permission to stop or replace processes.

## Render and validate before mutation

Use [base Compose](../../deployment/docker/docker-compose.yml), then [CI overlay](../../deployment/docker/docker-compose.ci.yml), then [release overlay](../../deployment/docker/docker-compose.release.yml). CI removes `build` and requires `FRONTEND_IMAGE`/`BACKEND_IMAGE`; release fixes one backend replica and the Caddy index pin. Require digest-only frontend/backend refs and no build in the rendered configuration.

After separate approval to consume the exact env file, export the approved image refs and use a new private directory (`0700`) for local evidence. Never source or print the env file, rendered JSON, adapted proxy config or raw secret-bearing logs. The rendering below creates a new `0600` file; only the validator's sanitized report may be shared.

```sh
compose_release() {
  docker compose --project-name "$APPROVED_PROJECT" --env-file "$APPROVED_ENV_FILE" \
    -f deployment/docker/docker-compose.yml -f deployment/docker/docker-compose.ci.yml \
    -f deployment/docker/docker-compose.release.yml "$@"
}
(umask 077; set -C; compose_release config --format json > "$RELEASE_DIR/compose.json")
node scripts/release/validate-frontend-rollout.mjs --plan "$RELEASE_DIR/plan.json" \
  --compose "$RELEASE_DIR/compose.json" --caddyfile "$APPROVED_CADDYFILE" --output "$RELEASE_DIR/rollout-gate.json"
```

The reviewed Caddyfile must be the mounted file. Confirm the running proxy matches the approved pin; a frontend-only update does not replace Caddy. A different running proxy image needs its own approved rollout, not merely a reload.

## Apply, observe, decide

Before mutation, require backend HTTP 200 readiness with healthy dependencies/recovery and the observed schema, frontend `/health` HTTP 200 with `status=ok`, and approved rollback evidence. Keep `BACKEND_IMAGE` fixed to the already running backend.

```sh
# Only after explicit approval of these exact frontend-only actions:
compose_release pull frontend
compose_release up -d --no-deps --no-build --pull never frontend
```

Do not restart backend, databases or other dependencies. Reload Caddy only for a separately approved configuration change, after validating the exact mounted Caddyfile; do not combine it silently with frontend rollout.

During both agreed observation windows, record image identity, frontend/backend readiness, schema and sanitized error/reconnect rates. Exercise public/admin navigation, auth/CSRF, CSP/security headers, HTTP proxy paths and real WS upgrade/reconnect. Pause on failed health/browser/auth/WS checks or stale evidence; abort on unsafe compatibility or state uncertainty. Use [release recovery](release-recovery.md), not an improvised backend change.

## Local validation is not release proof

Use the fixture's approved synthetic setup and owned loopback runtime. `APPROVED_CADDY_BIN` must be the absolute path to the reviewed, hash-checked Caddy executable with its version verified. The dedicated proxy suite requires `E2E_RELEASE_PROXY=1`; ordinary default mode intentionally excludes it.

```sh
node --test scripts/release/tests/validate-frontend-rollout.test.mjs
cd frontend
E2E_RELEASE_PROXY=1 E2E_SKIP_WEB_SERVER=1 E2E_PRODUCTION=1 \
  E2E_FRONTEND_URL=http://127.0.0.1:4381 \
  E2E_RELEASE_FRONTEND_URL=http://127.0.0.1:3188 \
  CADDY_BIN="$APPROVED_CADDY_BIN" \
  node scripts/run-e2e.mjs e2e/release-proxy.spec.ts --workers=1
```

The proxy fixture uses real Caddy and a production frontend runtime but a synthetic backend; it does not prove tournament correctness. Record whether the frontend is a local production build or an OCI runtime; only the latter can supply image-runtime evidence. GO for the current release separately requires fresh same-origin full-stack validation against the real backend, bound to the candidate source/index, backend revision/schema and proxy identity. Historical full-stack results or synthetic proxy PASS cannot replace that gate. The release owner approves the representative real scenarios and records their coverage; a bounded run must not be reported as the full suite. Missing applicable evidence remains NO-GO, not deferred acceptance.
