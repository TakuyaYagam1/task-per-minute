# Frontend image checks

The frontend security gate scans the local OCI artifact produced by
`scripts/release/build-frontend-image.mjs`. It does not pull or publish an image,
read application environment files, or scan the backend. The requested digest
is the OCI index digest, not a mutable image tag.

```sh
node scripts/release/scan-frontend-image.mjs \
  --artifact /absolute/path/to/frontend-image \
  --image-digest sha256:<verified-index-digest> \
  --cache-dir /absolute/path/to/trivy-cache \
  --scanner /absolute/path/to/verified-trivy \
  --output /absolute/path/to/new-security-evidence
```

Prepare the scanner and database separately. The gate must not repair missing
tools or refresh stale evidence during a scan. It rechecks the OCI identity,
scanner bytes and database before accepting results. Missing or altered input,
an outdated database, scanner failure, malformed results, or high/critical
findings prevent a successful report. Lower-severity findings remain visible;
they are not silently ignored. Policy is in `frontend-policy.json`.

## Scanner identity

The existing Trivy 0.72.0 baseline is retained. The following Linux/amd64
distributions were independently reviewed on 2026-09-25:

| Distribution | SHA-256 | Verification |
| --- | --- | --- |
| Official release archive | `bbb64b9695866ce4a7a8f5c9592002c5961cab378577fa3f8a040df362b9b2ea` | Immutable GitHub release, checksums and Sigstore bundle |
| Binary inside that archive | `0e69edd134a3c338baa1a6806920773615d682b18cbc6a0cba2a3b658ef9b63e` | Extracted only after archive verification |
| Local Nix binary | `6854ff0a4373688d1a2d158e22378395f663b769f490e84e9666732e3adad54f` | Nix content verification and `cache.nixos.org-1` signature |

Source: [Trivy 0.72.0 release](https://github.com/aquasecurity/trivy/releases/tag/v0.72.0),
[Apache-2.0 license](https://github.com/aquasecurity/trivy/blob/v0.72.0/LICENSE).
The official archive signature was verified with issuer
`https://token.actions.githubusercontent.com` and certificate identity
`https://github.com/aquasecurity/trivy/.github/workflows/reusable-release.yaml@refs/tags/v0.72.0`.
CI checks the reviewed archive and executable hashes before execution. The gate
uses its frontend-specific policy; it does not claim that an obsolete global
tooling lock or an unrelated backend check has passed.

The database comes from `ghcr.io/aquasecurity/trivy-db:2`. Database freshness is
checked at scan time and its content identity is recorded in the report. Do
not reuse a previously successful report as proof about a different image or
a later vulnerability database.

## Runtime and browser checks

Start the verified local runtime by its config/image ID, bind it only to a
loopback address, and use a read-only filesystem, a bounded temporary `/tmp`,
dropped capabilities, no-new-privileges and resource limits. Keep the container
ID and image ID with local evidence. No production service is required.

```sh
node scripts/release/check-frontend-runtime.mjs \
  --url http://127.0.0.1:3186 \
  --output /absolute/path/to/new-runtime-report.json
```

The runtime check requires a healthy production frontend and its security
headers, including an enforced CSP. It is not a backend health check. A network
probe alone cannot prove which container answered; pair it with the exact
runtime image inspection. For a same-origin image, run the browser checks from
`frontend/` with the existing verified browser runtime:

```sh
E2E_SKIP_WEB_SERVER=1 E2E_PRODUCTION=1 E2E_WORKERS=1 \
E2E_FRONTEND_URL=http://127.0.0.1:3186 \
node scripts/run-e2e.mjs e2e/player-home-contract.spec.ts e2e/csp-report.spec.ts
```

For direct API mode, explicitly supply the expected public origins to the
runtime check. Do not loosen the CSP to accommodate a mismatched build.
Stop and remove only the disposable container created for the check.

## Trust boundary

Threats addressed here are stale or substituted artifacts, missing scanner
coverage, suppressed failures, expired databases and overly broad public
runtime configuration. Checks use local files and scrubbed child environments;
they do not need application secrets, a host container socket or credentials.
OCI provenance remains unsigned build evidence. Scan success is neither a
signature nor production deployment authorization. The shared workflow blocks
frontend output on scan failure; a registry export may already exist, but it
must not be treated as deployable without the gate. Backend gates stay separate.
