# Contract Standard

## Sources Of Truth

### REST

The canonical REST contract is the backend OpenAPI source:

- `backend/api/openapi.yml`
- `backend/api/routes/*.yml`
- `backend/api/components/security.yml`
- `backend/api/components/parameters.yml`
- `backend/api/components/responses.yml`
- `backend/api/components/schemas/*.yml`

`frontend/lib/shared/api/schema.ts` is generated from that source. It is a
derived artifact and must never be edited manually.

Frontend REST responsibilities are split as follows:

- `client.ts` owns credentialed transport, CSRF, admin refresh serialization,
  common errors, and response unwrapping.
- Domain adapters such as `player.ts`, `admin.ts`, and `leaderboard.ts` own
  endpoint calls.
- `guards.ts` owns runtime validation of successful response bodies.
- Features and pages consume adapter results rather than raw OpenAPI clients.

### WebSocket

The canonical WebSocket wire behavior is defined by the backend WebSocket
adapter, primarily:

- `backend/internal/adapter/inbound/websocket/event.go`
- `backend/internal/adapter/inbound/websocket/server.go`
- `backend/internal/adapter/inbound/websocket/tournament/protocol.go`
- the associated role-scoped snapshot and handshake tests

OpenAPI intentionally does not describe the WebSocket protocol. The current
frontend has no tournament realtime consumer. A future consumer must define
runtime validation beside its transport adapter before it handles snapshots.

### Browser Storage

`frontend/lib/shared/lib/storage.ts` owns the recoverable player ID and username
display cache. `frontend/lib/shared/api/client.ts` owns short-lived CSRF token
storage. No browser session marker or tournament state is authoritative;
HttpOnly backend cookies and PostgreSQL state remain the source of truth. Treat
CSRF tokens as XSS-reachable transport material: never log or export them, and
clear them on logout or invalid session. Do not store credentials, flags,
private task payloads, presigned URLs, or tournament authority in the browser.

## Atomic REST Changes

A REST contract change is incomplete until the same change set contains all
applicable steps:

1. Update the backend OpenAPI route or component schema.
2. Regenerate backend OpenAPI code with `make gen-openapi` from `backend/`.
3. Update backend handlers, mappings, authorization, and validation.
4. Regenerate `frontend/lib/shared/api/schema.ts`.
5. Update the frontend API adapter.
6. Add or update a runtime response guard.
7. Update entity, feature, and page consumers.
8. Add focused success, malformed-response, error, and authorization tests.
9. Run codegen drift, typecheck, lint, build, focused browser tests, and any
   affected full-stack scenario.

Generate with:

```bash
cd frontend
npm run openapi:generate
```

Review the generated diff. Do not hide an unexpected generated change by
editing the output. Fix the source OpenAPI schema or generator input instead.

Use the existing RFC 7807 `ProblemDetails` envelope. Preserve HTTP status,
`Retry-After`, request identity, and safe user-facing detail. Do not convert
authorization, rate limiting, contract errors, and network failures into one
indistinguishable error.

## Atomic WebSocket Changes

A WebSocket change is incomplete until the same change set contains all
applicable steps:

1. Add or update the backend event constant and payload struct.
2. Update server emission or incoming-event handling and authorization.
3. Update backend protocol tests.
4. Update the frontend message or command union and payload mapping.
5. Update runtime parsing and field validation.
6. Update the owning state reducer or event handler.
7. Add focused browser contract tests for valid, malformed, stale, duplicate,
   wrong-owner, and wrong-aggregate events.
8. Run a full-stack test when the wire shape or state transition changes.

Preserve these invariants:

- Ignore unknown or malformed server messages without mutating valid state.
- Check aggregate identifiers before applying an event.
- Check tournament, participant, series, game, and assignment ownership where
  the event depends on them.
- Do not let a stale socket generation mutate the current session.
- Server snapshots are read-only; clients cannot advance durable state through
  the realtime channel.
- A local timeout does not finalize a game or series.
- Reconnect must revalidate the authenticated session and tournament role.
- Do not retry non-recoverable authentication or authorization closures.
- Client commands must include only fields accepted by the server. Never trust
  a client-supplied player identity when it can be derived from the session.

Tournament realtime is snapshot-only. It uses the public, participant, and
operator `/api/v1/.../realtime` paths. Every connection starts at sequence 1;
there is no durable resume cursor or replay stream. A reconnect obtains a fresh
consistent snapshot and starts a new connection-local sequence.

## SSE And New Event Feeds

The current admin player SSE is an invalidation channel, not a general event
bus. Add a separate adapter for a new event feed rather than expanding an
admin-specific client into a cross-domain transport.

A durable or public event feed must define before implementation:

- versioned event names and payload schemas
- event ID, ordering, timestamp, and aggregate identity
- initial snapshot semantics
- resume cursor and retention behavior
- duplicate and out-of-order handling
- provisional and final state rules
- authentication, authorization, and public redaction
- heartbeat, stale, disconnect, and retry behavior
- schema compatibility and deprecation policy

Consumers must be able to rebuild the same visible state from a snapshot plus
ordered events. Public feeds must use explicit public DTOs and must not expose
participant, admin, task-secret, or audit-only fields.

## Authentication And CSRF Contract

- Player and browser admin authentication is based on HttpOnly cookies.
- Unsafe cookie-authenticated requests must use the CSRF behavior in
  `credentialedFetch` or `adminCredentialedFetch`.
- Admin refresh is single-flight. Concurrent `401` responses must not start
  independent refresh rotations.
- Logout and session replacement invalidate stale in-flight work.
- Browser clients must not restore bearer tokens from storage.
- WebSocket and EventSource connections must use the documented cookie and
  origin model. Do not move credentials into query strings.
- Add negative tests for missing, expired, wrong-owner, and forbidden sessions
  when an endpoint or event stream is protected.

## URL And Deployment Contract

The deployed configuration contract supports two modes:

1. Same-origin mode with empty `NEXT_PUBLIC_*` URLs and Next.js rewrites to
   `BACKEND_URL`.
2. Direct browser mode where public player and admin API origins are explicitly
   configured.

The current frontend does not open tournament WebSocket connections. A future
public or participant client must derive its WebSocket origin from the player
API origin, while an operator client must derive it from the admin API origin.
Do not add a third WebSocket URL setting. CI rejects partial public API URL
combinations for deployable builds. Do not depend on partial mode without an
explicit contract decision. Changes to URL resolution, rewrites, cookies, CORS,
CSP, or WebSocket origins require deployment-mode tests and review of browser
credential behavior.

## Contract Ownership

Assign one contract owner for a cross-stack change. Other agents may implement
independent consumers against an agreed fixture, but they must not concurrently
change the same schema, generated file, event union, or payload parser.

The handoff must name:

- canonical source changed
- generated artifacts changed
- compatibility impact
- auth or redaction impact
- focused and full-stack tests run
- skipped checks and residual contract risk
