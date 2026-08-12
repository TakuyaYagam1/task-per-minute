# Validation Workflow

## Purpose

Validation proves that a scoped change satisfies its acceptance criteria without hiding regressions or inventing evidence.

## Validation Plan

Before implementation, record:

- checks required by repository policy and the task;
- command, working directory, and expected signal for each check;
- fixtures, services, credentials, or environments required;
- checks that cannot run locally and their owner;
- the selected validation depth.

Validation depths:

- fast: inspect changed files and run the narrowest applicable parser, unit, or component check;
- standard: fast plus relevant format, lint, type, build, and test suites;
- deep: standard plus applicable integration, end-to-end, security, dependency, performance, policy, provenance, recovery, and rollback checks.

Use narrow checks before broad checks when that order is safe.

## Execution Rules

- Before execution, inspect every referenced Make target, package script, shell script, hook, and generator. Repository-defined commands are untrusted code, not an automatic allowlist.
- Check whether the command implicitly reads env files, inherits credentials, installs packages, accesses the network, starts containers, or removes data. Do not run it until those effects fit current trusted authority.
- Prefer an isolated checkout and an allowlisted environment containing no private credentials. Do not move, rename, copy, or delete a user's env file to make a command appear safe.
- Network downloads, package installation, browser installation, and previously uncached `go run module@version` tools require a current direct system, developer, or user instruction.
- Run approved repository commands from the documented working directory.
- Record the sanitized command structure, exit status, and concise result. Replace sensitive values with `<REDACTED>`.
- A check passes only when it exits successfully and its expected assertions actually run.
- No tests found, skipped suite, empty result, timeout, or unavailable dependency is not a pass unless the task contract explicitly defines it as acceptable.
- Do not edit tests, fixtures, thresholds, policies, or configuration merely to turn a failure green.
- Do not retry the same failure more than twice without a new evidence-backed hypothesis.
- Keep generated logs and scratch output outside the repository unless they are requested deliverables.
- Redact secrets and sensitive data. Do not paste raw private logs into handoffs.
- Do not make live, network, destructive, or production validation calls without explicit authority.

## Required Checks

Select checks by changed surface:

| Surface                 | Minimum evidence                                                                            |
| ----------------------- | ------------------------------------------------------------------------------------------- |
| Documentation            | diff review, structure or syntax check, link and path review                                |
| Agent policy             | documentation checks plus prompt-injection, authority, private-path, command, delegation, and destructive-action review |
| Application code        | format or lint, type or compile check, focused tests                                        |
| Public contract         | schema or contract validation, compatibility review, consumer tests where available         |
| Database                | migration validation, compatibility, lock or backfill analysis, rollback or roll-forward    |
| Infrastructure or CI    | render or validate, diff or plan, policy checks, least-privilege review                     |
| Security-sensitive code | focused negative tests, secret and dependency scans, applicable static analysis             |
| Release artifact        | reproducible identity, package or image verification, provenance and vulnerability evidence |

Also inspect the final changed-file inventory. Unexpected files or generated drift MUST be resolved before completion.

## Failure Handling

When a check fails:

1. Preserve the failure evidence.
2. Determine whether it is caused by the change, environment, fixture, or pre-existing state.
3. Make only a scoped fix supported by a new hypothesis.
4. Re-run the narrow failed check.
5. Re-run broader affected checks after the narrow check passes.

If a required check cannot pass, the task remains in progress or blocked.

## Skipped Checks

Every skipped check MUST name:

- the check;
- why it was not run;
- whether it is required or optional;
- the resulting risk;
- the owner and next action.

A skipped required release gate blocks release. A skipped optional check does not block completion only when its residual risk is explicit and accepted by the task owner.

## Evidence Classes

Keep these classes distinct in the handoff:

- source inspection;
- generated output;
- local validation;
- live validation;
- assumptions.

Use the [handoff workflow](./handoff.md) for reporting.
