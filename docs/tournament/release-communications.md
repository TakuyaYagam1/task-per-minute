# Frontend release communications

Use this procedure to prepare an operational handoff, not to send an automatic announcement. This file names no actual contacts, released version, release date or approved deployment. Local validation and completed tasks do not imply publication or a current real GO. External messages and production actions require separate explicit authorization.

## Preflight handoff

1. The release owner completes the [candidate inventory](release-notes.md#candidate-inventory), names the backend owner, recovery owner and approver, and confirms each person's acceptance. Keep real contact details and approved communication channels in the private operational record, not this template.
2. The backend owner supplies current external readiness, schema/API compatibility and single-process realtime evidence. Do not infer it from capacity results, task status, old full-stack tests or frontend health.
3. Each evidence owner supplies the normalized gate envelope and retains the inspected native reports, run identifiers, commands and raw-evidence hashes. Match source/index, backend revision/image and environment across all seven gates. Record actual completion timestamps and test counts without fabricating missing evidence.
4. The release owner runs the [readiness CLI](../../scripts/release/check-frontend-readiness.mjs) using the [core contract](../../scripts/release/frontend-readiness.mjs) and reviews its sanitized result. Preserve `decision`, `checked_at`, gate statuses and `{gate, code}` issues together with the manifest hash. Preserve producer completion times separately from the time the aggregate was checked.
5. A consistency GO still has `scope=evidence_consistency_only` and `release_authorized=false`. The approver separately records the exact permitted environment/action, artifact identities and window. Agree initial/extended observation windows, interruption expectations, pause/abort conditions and the next update time before any operation.

## Decision and incident updates

| Event | Decision owner and required update |
| --- | --- |
| Preflight / NO-GO | Release owner lists missing, failed, skipped, stale, synthetic or mismatched gates and assigns evidence follow-up. Do not announce readiness from a task marked done. |
| Consistency GO | Release owner reports the aggregate's limited scope and real evidence reviewed. Approver status remains separate; do not describe this as deployment permission. |
| Pause | Release owner stops advancement for failed health, browser, auth/CSRF, HTTP/WS or stale evidence. Record time, affected identity, observed impact, assigned owner and next update time. |
| Abort / escalate | Release owner escalates uncertain schema/API compatibility, backend recovery failure or state integrity risk to the backend/data owner. Missing authority or evidence is NO-GO, not a waiver opportunity. |
| Frontend rollback | Recovery owner presents the exact prior frontend digest, current-backend/schema compatibility and a fresh approval request. Follow [release recovery](release-recovery.md); do not imply data, schema or backend recovery. |
| Close or continue incident | Named owners review fresh identity, health, real browser/WS and schema/API evidence for the resulting configuration over the agreed windows. If backend state remains unresolved, keep the incident open with its owner. |

A proxy test with a synthetic backend is not real full-stack evidence. Current release acceptance needs candidate-bound same-origin checks against the real backend, including HTTP and WS behavior and public/admin separation. Report a selected scenario set as selected coverage, not the entire suite. Never turn an all-pass fixture into a release announcement.

## Update template

Fill only inspected facts; mark unavailable evidence explicitly. The bracketed text is a placeholder, not an assertion or approval.

```text
Event and recorded_at (UTC): [actual event and time]
Candidate/environment: [source revision, frontend index, backend revision/image, environment]
Named owners: [release, backend, recovery, approver]
Evidence: [manifest hash, checked_at, gate completion times and sanitized issue codes]
Aggregate decision: [GO / NO-GO / unavailable]
Aggregate scope: evidence_consistency_only; release_authorized=false
Native evidence review: [reviewers and retained hash-bound audit references, or missing]
Operation authorization: [not granted, or exact separately approved action/target/window]
Observed impact and coverage: [verified facts; distinguish real runs from fixtures]
Recovery disposition: [prior frontend compatibility verified, or backend-owner escalation]
Next action/owner/update time: [explicit assignment and actual agreed time]
```

Do not include raw reports, env contents, rendered Compose JSON, credentials, private URLs, cookies or participant payloads in a communication. Share only an approved sanitized summary; keep raw evidence in its controlled location. Obtain approval for the intended recipients/channel and content before sending anything.

If identities change, inputs change, evidence expires or an operation fails, stop using the earlier aggregate as current evidence. Preserve it, collect new producer results, create a new manifest/report without overwriting history, and repeat owner review. Frontend rollback changes code only; backend/data recovery and any roll-forward remain separately owned and approved. [Rollout](release-rollout.md) and [recovery](release-recovery.md) define those operational boundaries.
