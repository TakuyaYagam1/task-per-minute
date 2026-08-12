# Evidence-First Handoff

## Purpose

A handoff lets another owner verify the outcome without reconstructing the entire task. Evidence precedes completion claims.

Use the [handoff template](../templates/handoff.md).

## Required Sections

Every implementation handoff MUST contain these headings in this order:

1. Evidence
2. Changed Files
3. Commands Run
4. Validation Result
5. Skipped Checks
6. Residual Risks
7. Next Best Action

Use None when a section is empty. Do not omit the section.

## Evidence Rules

- Separate source inspection, generated output, local validation, live validation, and assumptions.
- Link exact repository files and line numbers when they support a claim.
- State artifact identity when generated packages, images, schemas, or release bundles exist.
- Summarize command output. Include raw output only when needed to reproduce a failure.
- Never include secrets, credentials, private prompts, personal data, or unredacted sensitive logs.
- Do not claim a check was run unless command and result evidence exist.
- Report a sanitized command form. Replace inline credentials, tokens, private paths, and sensitive values with `<REDACTED>` and state that redaction was applied.

## Status Claims

Complete means:

- every requested deliverable exists;
- acceptance criteria are met;
- required validation passed;
- no required action remains.

If any condition is false, report partial or blocked instead of complete. A time limit, model budget, or subagent completion message does not justify complete status.

A blocked handoff MUST identify the blocking condition, evidence, attempted safe actions, required next decision, and affected risk.

## Changed Files

List every changed path and its purpose. State explicitly when no files changed. Do not include unrelated dirty-worktree files as authored changes.

For shared worktrees, distinguish:

- files owned and changed by this task;
- concurrent files observed but not changed;
- ownership collisions that stopped work.

## Commands and Validation

For each command, provide:

- working directory;
- exact command structure with sensitive values redacted;
- exit status;
- concise interpretation.

Validation Result MUST map checks to acceptance criteria. Follow [validation.md](./validation.md) for pass, skip, and failure semantics.

## Receiver Responsibilities

The receiving owner MUST:

1. Confirm changed paths match the ownership assignment.
2. Review evidence and residual risks.
3. Re-run or independently inspect critical checks when required.
4. Accept, request changes, reassign, or mark blocked.
5. Release ownership only after acceptance or explicit cancellation.

Release handoffs additionally follow [release.md](./release.md).
