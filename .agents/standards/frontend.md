# Frontend Standard

## Scope

This standard applies to `frontend/`, a Next.js App Router application using
React, strict TypeScript, Tailwind CSS, CSS Modules, `openapi-fetch`, and
Playwright.

Preserve existing public behavior unless the task explicitly changes it. Use
the existing package manager and lockfile. Do not add a dependency when the
repository already provides the required capability.

Use Feature-Sliced Design for frontend ownership and dependency direction.

## Current Boundaries

| Path | Responsibility |
| --- | --- |
| `frontend/app/` | Next.js route entry points, route handlers, metadata, global styles, and application bootstrapping |
| `frontend/lib/pages/` | Route-level orchestration and composition |
| `frontend/lib/widgets/` | Reusable page sections composed from entities, features, and shared UI |
| `frontend/lib/features/` | User actions and client-side use cases, including transport lifecycle hooks |
| `frontend/lib/entities/` | Domain-specific models and state helpers for players and games |
| `frontend/lib/shared/api/` | REST transports, generated types, response guards, auth refresh, and API error normalization |
| `frontend/lib/shared/types/` | Handwritten non-REST wire types and small shared domain types |
| `frontend/lib/shared/lib/` | Storage, validation, logging, navigation, and generic hooks |
| `frontend/lib/shared/ui/` | Domain-independent UI primitives |
| `frontend/e2e/` | Browser contract, integration, and full-stack tests |

Keep route entry points thin. `app/admin/page.tsx` and
`app/leaderboard/page.tsx` are current exceptions, not templates for new pages.
The leaderboard route also manually duplicates REST DTO shapes and should be
migrated to generated types when it is touched. Large changes to the home, task, or admin flows
should extract cohesive models, features, or widgets instead of adding more
unrelated state to their route components.

Respect dependency direction:

```text
app -> pages -> widgets -> features -> entities -> shared
```

A layer may use the layers to its right. Do not introduce deep imports across
feature internals. Export the intended public surface from the local
`index.ts` or `exports.ts` file.

Do not add architecture-only tests that scan imports or directory layout.
Preserve FSD boundaries through public module surfaces, code review, strict
TypeScript, and tests of observable behavior.

`lib/pages/task/TaskPage.tsx` currently imports `app/task/task.module.css`.
Treat this reverse style import as legacy debt, not a pattern for new code.
When that styling boundary is changed, move page-owned styles toward the page
layer or expose them through a deliberate public surface.

## State Ownership

- The backend owns authenticated identity, duel membership, deadlines, and
  terminal results.
- REST responses, WebSocket events, and SSE events are server state. Parse and
  validate them at the boundary before applying them to UI state.
- Browser storage is a recoverable cache, not an authority. Current session
  storage includes CSRF tokens and cached game data that can contain unlocked
  hints and presigned or task URLs. Treat it as sensitive XSS-reachable state,
  keep it short-lived, clear it on session or match cleanup, and reconcile
  restored state with the server before enabling privileged or match-changing
  actions. Do not expand the stored payload without security review.
- Page-local display state belongs in the page or a dedicated view-model hook.
- Transport lifecycle belongs in transport hooks. Business state transitions
  belong in a feature or entity reducer, not in a generic socket client.
- Clear intervals, timeouts, event listeners, sockets, EventSource instances,
  and in-flight requests during unmount or session replacement.
- Guard stale async responses with cancellation, request identity, generation,
  or session version checks.

Do not derive an official result from the browser clock. A local countdown may
show an expired or pending state, but only a server event or validated server
response may establish the terminal result.

## Next.js And Browser Boundaries

- Use `"use client"` only in modules that require React client state, browser
  APIs, or client navigation.
- Keep browser globals behind runtime checks when a module may execute during
  server rendering.
- Use the configured API clients rather than direct `fetch` calls. A custom
  transport is allowed for cases such as uploads or event streams, but it must
  reuse the repository auth, CSRF, error, timeout, and cleanup rules.
- Preserve both supported deployment modes: same-origin rewrites and explicit
  `NEXT_PUBLIC_API_URL`, `NEXT_PUBLIC_ADMIN_API_URL`, and
  `NEXT_PUBLIC_WS_URL` values.
- Do not embed a private backend hostname or environment-specific origin in a
  component.

## Type And API Discipline

- Keep TypeScript strict. Do not use `any`, unchecked casts, or weakened
  compiler settings to bypass a contract mismatch.
- REST DTOs come from `lib/shared/api/schema.ts`. Do not recreate them by hand.
- WebSocket DTOs must match the backend wire structs and must pass the runtime
  parser before reaching feature code.
- Validate identifiers, timestamps, enums, optional fields, and ownership
  relationships at network and storage boundaries.
- Keep API adapters thin. They should call a typed client, unwrap the common
  error envelope, validate the response, and return a domain-relevant value.
- Model expected loading, empty, disconnected, stale, unauthorized, forbidden,
  rate-limited, and malformed-response states explicitly.

## Browser And Security Invariants

- Authentication is cookie-backed. Never persist bearer access tokens, bearer
  refresh tokens, session cookies, passwords, flags, or new credentials in
  localStorage or sessionStorage. The existing sessionStorage CSRF tokens are
  a transport exception, not a pattern to broaden. Never place credentials or
  private payloads in URLs, logs, telemetry, or rendered error messages.
- A player ID, username, or local session marker is not proof of identity or
  authorization.
- Send cookie-authenticated requests through the existing credentialed client
  so CSRF headers, cookie credentials, refresh serialization, and session
  invalidation remain intact.
- Do not bypass negative authorization behavior for admin or object-scoped
  endpoints. Treat `401` and `403` as different states.
- Validate user-controlled and server-provided URLs before navigation. Preserve
  `noopener,noreferrer` for new windows and keep the mixed-content guard.
- Do not render untrusted HTML. Prefer normal React text rendering.
- Public surfaces must use explicitly redacted DTOs. Do not reuse
  admin or participant payloads when they contain flags, private task data,
  private URLs, session data, or audit-only fields.
- Do not log cookies, authorization headers, CSRF values, passwords, flags,
  raw personal data, or full confidential payloads.
- Keep file upload allowlists, size limits, timeouts, cancellation, and
  server-side validation. Browser checks are usability controls, not a trust
  boundary.

## UI And Accessibility

- Prefer semantic elements and accessible names over clickable generic
  containers.
- Every form control needs a label or equivalent accessible name. Report field
  errors near the field and expose invalid state to assistive technology.
- Keep keyboard behavior, focus handling, reduced viewport behavior, and
  loading or disabled states observable and testable.
- Use existing design tokens, Tailwind utilities, and CSS Modules. Do not add a
  new component library for isolated visual needs.
- Use memoization only for measured expensive work or a required stable
  reference contract.

## Change Protocol

Before editing, identify the owning layer, public behavior, affected contract,
and focused Playwright spec. Keep one writer for large hotspot files. If the
change crosses REST, WebSocket, auth, storage, or deployment boundaries, follow
`contracts.md` and run the matching gates from `testing.md`.
