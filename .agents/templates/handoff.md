# Evidence-First Handoff

- Task ID: <task-id>
- Owner: <implementation owner>
- Status: complete, partial, or blocked
- Ownership scope: <exact writable paths>

## Evidence

Source inspection:

- <file and line, or None>

Generated output:

- <artifact identity and location, or None>

Local validation:

- <evidence, or None>

Live validation:

- <evidence, or None>

Assumptions:

- <assumption and impact, or None>

## Changed Files

- <exact path>: <purpose>

Concurrent files observed but not changed:

- <path or None>

## Commands Run

| Working directory | Sanitized command | Exit     | Result           |
| ----------------- | ----------------- | -------- | ---------------- |
| <path>            | <command with sensitive values replaced by REDACTED> | <status> | <concise result> |

## Validation Result

- <acceptance criterion or check>: pass, fail, or blocked - <evidence>

## Skipped Checks

- <check>: <reason>; <required or optional>; risk: <risk>; next owner: <owner>

Use None only when no checks were skipped.

## Residual Risks

- <risk, impact, and owner, or None>

## Next Best Action

<Single concrete next action, or None when the task is complete and accepted.>

Complete status is valid only when all requested deliverables exist, acceptance criteria are met, required checks passed, and no required action remains.
