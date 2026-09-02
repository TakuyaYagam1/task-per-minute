# Release security toolchain trust

## Decision

The release security preflight is fail-closed and offline. The canonical command
is:

```bash
scripts/release/verify-security-tools.sh
```

Canonical mode accepts no arguments. It does not discover declared tools through
`PATH`, download tools, install packages, update vulnerability databases, pull
images, load images, build images, or trust image tags.

The verifier bootstrap is the exact read-only Nix Python 3.14.6 runtime named in
the script and runs in isolated mode. A missing bootstrap runtime is NO-GO;
`PATH`, `PYTHONPATH`, user-site packages, and repository modules are not fallback
sources.

The current decision is **NO-GO**. This is deliberate. Unknown content identity
is represented as `unresolved` in the lock rather than filled with an inferred
archive hash, commit, image digest, executable digest, or Nix NAR identity.

## Current inventory

The reviewed local version inventory is:

| Component | Observed version | Trust state |
| --- | --- | --- |
| Go | 1.26.5 | Nix root, NAR, executable, and runtime identity locked |
| Node.js | 24.18.1 | Nix root, NAR, executable, and runtime identity locked |
| npm | 11.16.0 | Nix root, NAR, executable, and runtime identity locked |
| Playwright | 1.59.1 | Package lock known, executable provisioning unresolved |
| Chromium | 149.0.7827.55 | Artifact locked, incompatible with the Playwright browser |
| jq | 1.8.2 | Nix root, NAR, executable, and runtime identity locked |
| yq | 4.53.3 | Nix root, NAR, executable, and runtime identity locked |
| Docker CLI | 29.6.2 | Nix root, NAR, executable, and runtime identity locked |
| Docker Compose | 5.3.1 | Nix root, NAR, executable, and runtime identity locked |
| govulncheck | 1.6.0 | Artifact locked, stable exact runtime probe unresolved |
| gitleaks | 8.30.1 | Nix root, NAR, executable, and runtime identity locked |
| Semgrep | 1.161.0 | Nix artifact locked, bounded runtime dependency unresolved |
| Trivy | 0.72.0 | Nix root, NAR, executable, and runtime identity locked |

`frontend/package-lock.json` pins Playwright 1.59.1. Its Chromium binding is
revision 1217 and browser 147.0.7727.15. The only reviewed local Nix Playwright
Chromium was 149.0.7827.55, so the browser binding fails.

The required validation images are currently absent and only mutable Compose
tags are known:

- `caddy:2-alpine`
- `postgres:18.3-alpine3.23`
- `redis:8.6.2-alpine3.23`
- `chrislusf/seaweedfs:4.20`

Tags are inventory hints, not trust anchors. Each image must be reviewed and
locked as an exact `repository@sha256:digest`, platform `linux/amd64`, and config
digest before the gate can pass. The verifier binds those tags to the canonical
repositories `docker.io/library/caddy`, `docker.io/library/postgres`,
`docker.io/library/redis`, and `docker.io/chrislusf/seaweedfs`; a digest from a
different repository is rejected.

The observed Trivy database is bound to the official `aquasecurity/trivy-db`
source, exact metadata and database digests, and its full timestamp. With the
seven day maximum age, the 2026-08-09 database is stale on 2026-09-02.

## Lock contract

`security/tools/release-tools.schema.json` is a strict JSON Schema Draft 2020-12
contract. Unknown fields and missing fields fail validation. The lock must contain
the exact tool and validation image sets once.

Every accepted tool record binds:

- an official HTTPS repository and release source;
- an accepted SPDX license review;
- an exact version probe with a bounded timeout;
- an exact executable SHA-256;
- one immutable provisioning identity;
- an accepted trust decision.

The verifier, not the manifest, owns executable authority. For every canonical
tool name it hardcodes the only permitted read-only runtime probe arguments and
the exact expected output. A manifest argument or output mismatch is rejected
before any declared tool is executed.

Canonical mode also hardcodes the SHA-256 of the complete lock and schema files.
It checks the exact bytes before parsing either file and before any executable
probe. A lock or schema change therefore requires a reviewed update to the
matching verifier constant. TS-08 checks that both constants match the current
repository files. Fixture mode does not use these canonical trust anchors; it
accepts synthetic inputs only through the explicit test-mode interface and
labels every result as a fixture result.

Nix-provisioned tools require the exact store root and NAR hash. The verifier
checks the NAR hash with the fixed local `nix-store` executable. All currently
accepted canonical local tools use reviewed Nix roots. Archive provisioning is
exercised only by the fixture verifier: it requires an archive SHA-256 and exactly
one declared regular extraction member whose content digest equals the executable
digest. Canonical mode has no archive-root input and therefore rejects an archive
provisioning record. Supporting one later requires a separate reviewed contract
with a fixed canonical archive root.

`test_root` provisioning exists only for the synthetic fixture suite. Canonical
mode rejects it.

The Playwright binding additionally locks:

- the package-lock SHA-256 and `playwright-core` version;
- the `browsers.json` SHA-256;
- Chromium revision and browser version;
- the exact Chromium executable SHA-256.

The Trivy database record binds the metadata file, database file, both SHA-256
digests, exact `UpdatedAt`, and maximum age.

## Offline verifier behavior

For every accepted executable, the verifier:

1. Resolves the declared root and executable without `PATH` fallback.
2. Rejects root escape and any symlink path component.
3. Requires a regular executable that is not group or world writable.
4. Checks the exact executable digest and provisioning identity.
5. Rejects manifest probe arguments or expected output that differ from the
   hardcoded read-only tool policy.
6. Runs the exact version probe without a shell, with stdin closed and a bounded
   timeout.
7. Uses a minimal environment with an unusable `PATH`.

Docker validation is limited to read-only `docker image inspect` of an exact
digest reference. A missing image, tag reference, repository digest mismatch,
platform mismatch, or config digest mismatch fails. The verifier has no pull,
load, or build path.

Trivy probes receive `TRIVY_OFFLINE_SCAN=true`, `TRIVY_SKIP_DB_UPDATE=true`, and
`TRIVY_SKIP_JAVA_DB_UPDATE=true`. Metadata digest, database digest, timestamp,
and freshness are verified at the time of the preflight. A subsequent scan must
also use `--skip-db-update --skip-java-db-update --offline-scan` and a
coordinator-owned immutable snapshot of the verified database. If no snapshot is
available, the coordinator must hash the metadata and database immediately
before and after the scan and require both hashes to match the accepted lock.
TASK-076 does not create that snapshot or perform the scan-time rehash. The
current TS-09 NO-GO cannot authorize a scan or release approval.

## Evidence boundary

Arena gate evidence files use collision-safe atomic publication, mode `0600`, an
embedded content hash, and a checksum sidecar. These controls detect accidental
corruption and prevent filename replacement during publication. They do not
authenticate evidence against another process running under the same uid. A
release decision that needs that stronger guarantee must use a coordinator-owned
append-only store or a signature key that is unavailable to the tested process.

## Closing the NO-GO decision

An operator must perform a separate reviewed provisioning update. That review
must provide exact executable digests and Nix NAR identities for the current
canonical local tool set, install the matching Playwright browser, lock and
preload exact image digests, and provide a fresh reviewed Trivy database. An
archive-backed canonical tool additionally requires a new reviewed fixed-root
contract before it can be accepted. The lock and documentation must be updated
together. The verifier itself must remain offline.

## Fixture coverage

Run the TS-08 suite with:

```bash
scripts/release/tests/verify-security-tools_test.sh
```

The suite creates only synthetic temporary roots. It covers PASS, runtime version
mismatch, manifest probe argument and output tamper before execution, missing tool,
executable tamper, archive tamper, undeclared extraction member, unofficial source,
license failure, Playwright package and browser binding digests, versions, revision,
executable identity and malformed structures, duplicate and non-finite lock JSON,
symlinked lock rejection, schema identity, image tag trust, wrong image digest,
missing image, Trivy metadata and database digest mismatch, staleness, and negative
`PATH` and current-directory Python module poisoning. Fixture results are labeled
`release security fixture`; they never emit canonical preflight PASS or NO-GO text.
