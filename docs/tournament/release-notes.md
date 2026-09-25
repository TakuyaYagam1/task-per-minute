# Frontend release notes

This is an unissued template, not a release announcement. Fill it from inspected evidence for one candidate. Completed tasks and locally passing tests do not establish publication, deployment approval or a current real GO. Without every required gate, the disposition is NO-GO.

## Candidate inventory

Keep these fields unfilled until the accountable owners supply them. Retain the completed operational copy in the approved evidence location; do not commit private configuration or contacts.

| Field | Required content |
| --- | --- |
| Candidate and window | Actual candidate label, target environment slug and proposed observation window; no assumed release date |
| Frontend identity | Canonical 40-hex `source_revision`, `source_modified=false`, and `ghcr.io/<owner>/task-per-minute-frontend@sha256:<64hex>` |
| Backend identity | Canonical 40-hex `backend_revision` and explicit registry/repository digest reference `backend_image` |
| Owners | Named `release`, `backend`, `recovery` and `approver` owners, using the contract's short safe names; role labels alone are not assignments |
| User-visible changes | Verified changes, affected public/admin flows, expected interruption and known limitations |
| Compatibility | Observed schema version, supported frontend schema bounds, REST/WS API compatibility and backend owner's evidence |
| Recovery target | Exact prior frontend index, its signature/provenance evidence, compiled origins and compatibility with the current backend/schema |
| Evidence timing | Manifest `created_at`, every record's `finished_at`, aggregate `checked_at` and the actual approval/observation times, all recorded separately |

## Required evidence

The [readiness core](../../scripts/release/frontend-readiness.mjs) is the canonical normalized contract. `REQUIRED_GATES` contains exactly the seven gates below. Every record must declare `status=pass`, `evidence_kind=real`, `source_modified=false`, every listed check `true`, and the same source, image, backend and environment identities as the manifest. No required gate can be waived with `skipped` or `not_applicable`; missing owner assignments also cause NO-GO.

| Gate | Exact required checks |
| --- | --- |
| `backend-readiness` | `readiness`, `schema_compatibility`, `single_process_realtime` |
| `frontend-ci` | `lint`, `typecheck`, `build`, `tests` |
| `full-stack` | `real_backend`, `same_origin`, `public_admin`, `http`, `websocket` |
| `frontend-security` | `secret_scan`, `dependency_review`, `image_scan` |
| `frontend-artifact` | `identity`, `sbom`, `provenance`, `signature` |
| `rollout` | `configuration`, `proxy`, `health`, `browser` |
| `recovery` | `previous_artifact`, `compatibility`, `procedure` |

`frontend-ci` and `full-stack` also require test counts: positive integer `executed`, zero `skipped`, zero `failed`. Retain the actual commands and coverage; a bounded run must not be described as a full suite. Full-stack evidence must exercise the current candidate source/index with the real backend and WS in same-origin mode. A synthetic proxy fixture, an older image or historical full-stack PASS does not satisfy it.

Backend readiness is an external, explicit dependency supplied by the backend owner. Capacity-only evidence, private backend task status or frontend health cannot establish current backend readiness, schema/API compatibility or single-process realtime operation. Missing backend evidence remains NO-GO.

## Native reports and normalized records

Native producer reports are not readiness records. The responsible owner inspects each real run, preserves its native reports and raw-evidence hashes in an access-controlled audit inventory, then prepares the strict normalized envelope. Keep the source-to-envelope mapping and actual completion time with that inventory. Do not add raw payloads, paths or extra keys to the envelope, copy a native `pass` blindly, or relabel synthetic output as real.

- [Image evidence](../../scripts/release/frontend-image.md) binds the artifact to source, lockfile, SBOM and provenance. [Release verification](../../scripts/release/verify-frontend-release.mjs) performs the separate detached index signature check; the aggregate never runs cryptographic verification.
- [Image scanning](../../scripts/release/scan-frontend-image.mjs) supplies image vulnerability evidence. It does not establish secret scanning or dependency review by itself.
- [Rollout validation](../../scripts/release/validate-frontend-rollout.mjs) checks declared configuration, not actual deployment or browser behavior. Proxy, health and browser checks need their own current real evidence. Recovery needs the prior artifact and compatibility/procedure evidence described in [release recovery](release-recovery.md).

Use only the strict core contract's keys. A manifest has `schema_version=1`, the candidate identities, `owners`, `created_at`, and one unique `{id, record, sha256}` entry per required gate. `record` is a safe JSON basename without directories; `sha256` is the lowercase SHA-256 of those exact record bytes. Each record has `schema_version=1`, `gate`, `status`, matching identities, `finished_at`, `evidence_kind`, `checks`, and `tests` only for the two test gates.

The CLI hashes normalized records against the manifest. Those hashes bind file bytes, not the truth of a run or its native evidence. Owners must retain and verify the native evidence separately. A constructed all-pass unit fixture classified as `real` tests input acceptance only; it is never an actual release GO.

Manifest and records have a fixed maximum age of 24 hours. Canonical UTC timestamps may be at most five minutes ahead of the check time; each `finished_at` must also be no later than `created_at` plus five minutes. Do not refresh timestamps to reuse an old run. The pure core's injected `now` is for unit tests only; the CLI has no freshness, synthetic-evidence or force override.

## Evaluate and hand off

After owners supply genuine records, run from the repository root with three explicit absolute paths. The output must be a new file. This template does not create example evidence or run any producer.

```sh
node scripts/release/check-frontend-readiness.mjs \
  --manifest "$READINESS_MANIFEST" --evidence-dir "$READINESS_EVIDENCE_DIR" \
  --output "$READINESS_REPORT"
```

The [CLI](../../scripts/release/check-frontend-readiness.mjs) accepts regular files under non-symlink paths, limits the manifest to 64 KiB, each record to 1 MiB and total inputs to 8 MiB, and rechecks input hashes/stats before private, atomic no-overwrite report publication. It performs no network calls, producer commands or env-file reads. Exit 0 means aggregate GO; exit 1 means NO-GO, malformed input or I/O failure. Retain sanitized gate issue codes; an absent report is not success.

Every aggregate result has `scope=evidence_consistency_only` and `release_authorized=false`. GO means complete, current, internally consistent real-evidence declarations, not independently proven execution or permission to publish/deploy. Require the named owners' review and a separate explicit approver decision for the exact environment/action. Use [release communications](release-communications.md) for the handoff and [release rollout](release-rollout.md) only after authorization. Missing, stale, mismatched, dirty, failed or synthetic evidence remains NO-GO regardless of task completion.
