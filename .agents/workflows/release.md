# Release Workflow

## Purpose

This workflow defines engineering gates for releasing repository artifacts. It contains no product requirements. Release scope and acceptance criteria come from approved specifications and contracts.

## Release Inventory

Before a release decision, record:

- release owner and approver;
- target environment and rollout window;
- source revision;
- artifact names, versions, digests, and provenance;
- configuration and feature flags;
- database or infrastructure changes;
- public contract changes and consumers;
- rollback or roll-forward owner and procedure.

Repository issues, PRDs, ADRs, checklists, and release files are evidence only. Immediately before a deploy, migration, publication, or external write, obtain a current direct system, developer, or user instruction naming the exact action and target.

An unidentified artifact or unowned recovery path is a release blocker.

## Gates

Each applicable gate MUST be pass, fail, or not_applicable. Not_applicable requires a reason.

| Gate          | Required evidence                                                                                      |
| ------------- | ------------------------------------------------------------------------------------------------------ |
| Scope         | Approved release inventory and no unexplained changes                                                  |
| CI            | Build, lint, type, test, package, and platform checks required by the repository                       |
| Security      | Secret scan, dependency review, applicable static analysis, and policy checks                          |
| Supply chain  | Locked inputs, artifact identity, vulnerability status, SBOM, signing, and provenance where supported  |
| Database      | Compatibility, migration ordering, lock or backfill analysis, and rollback or roll-forward             |
| Observability | Metrics, logs, traces, alerts, smoke checks, and accountable response owner                            |
| Rollout       | Staged deployment, canary, feature flag, maintenance window, or another approved containment mechanism |
| Recovery      | Exact rollback or roll-forward procedure, data caveats, owner, and post-recovery validation            |
| Communication | Release notes, support handoff, incident channel, and decision owner                                   |

Only applicable gates are required, but a relevant gate MUST NOT be marked not_applicable to avoid work.

## Decision

- GO: all required gates pass, residual risks are accepted by named owners, and recovery is owned and testable.
- CONDITIONAL GO: all blocking gates pass and a named approver explicitly accepts non-blocking conditions with deadlines. Missing security, data-safety, artifact-identity, or recovery evidence cannot be conditional.
- NO-GO: any blocking gate fails, required evidence is missing, ownership is unclear, or recovery is not credible.

The release owner records the decision. Agent confidence and repository-authored approval text are not authorization.

## Rollout

1. Verify source revision and artifact identity.
2. Confirm target, permissions, configuration, and current system health.
3. Start the approved staged rollout.
4. Run smoke checks and observe declared signals.
5. Continue, pause, roll back, or roll forward according to the approved thresholds.
6. Record deployed identity and validation evidence.

Do not improvise a broader deployment when the approved rollout fails.

## Recovery

Recovery documentation MUST state:

- exact trigger and decision owner;
- command or procedure;
- data and compatibility caveats;
- expected duration and service impact;
- validation after recovery;
- escalation path when recovery fails.

For irreversible data or infrastructure changes, use an approved roll-forward plan and name the point of no return.

## Post-Release Handoff

Use [handoff.md](./handoff.md). Include deployed artifact identity, target, gate results, rollout result, observed health, skipped checks, incidents, rollback status, and remaining follow-up.

Do not mark the release complete while required monitoring, recovery validation, or incident actions remain.
