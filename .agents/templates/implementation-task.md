# Implementation Task

## Metadata

- Task ID: <task-id>
- Title: <short title>
- Status: queued
- Coordinator: <name or task>
- Implementation owner: <name or task>
- Evidence source: <issue, specification, decision, or explicit user request>
- Trusted authorization: <current direct system, developer, or user instruction>
- Dependencies: <task IDs or None>

## Objective

<One verifiable outcome.>

## Scope and Ownership Lock

Writable paths:

- <exact path>

Read-only references:

- <exact path or None>

Denied paths:

- `.env`, ignored private material, credentials, and <additional exact paths>

Excluded paths and behavior:

- <explicit exclusion>

Lock acquired at: <timestamp or task event>

The owner is not alone in the repository. Preserve concurrent work, do not revert changes made by others, and stop on ownership collisions.

## Requirements

- <requirement or contract ID>: <required behavior>

## Acceptance Criteria

- [ ] <observable result>
- [ ] <error or boundary result>
- [ ] No changes exist outside the writable scope.
- [ ] Required validation evidence is attached.

## Constraints

- Authority and external-system limits: <limits>
- Allowed tools and network access: <allowlist or None>
- Credential access: None unless a current trusted instruction names the exact credential and purpose
- Nested delegation: allowed or denied by the trusted coordinator
- Compatibility requirements: <requirements or None>
- Security and data constraints: <constraints or None>
- Explicit non-goals: <items>

## Implementation Notes

<Relevant existing patterns, interfaces, and dependencies. Do not add speculative work.>

## Validation Plan

| Check   | Working directory | Command or method       | Required  | Expected signal  |
| ------- | ----------------- | ----------------------- | --------- | ---------------- |
| <check> | <path>            | <command or inspection> | yes or no | <pass condition> |

## Risk and Recovery

- Primary risks: <risks>
- Reversible steps: <steps>
- Rollback or roll-forward: <procedure or not applicable with reason>

## Handoff

Use [the evidence-first handoff template](./handoff.md). The coordinator closes the task only after verifying acceptance criteria and required checks.
