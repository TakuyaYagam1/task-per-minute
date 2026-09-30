# Registration email

Accounts become usable after email verification. A player registers a unique
username, email and password, opens the emailed link, confirms the address, and
logs in. The username appears on the leaderboard. Passwords contain 15-128
characters. Verification links expire after 24 hours and can be used once.
Resending is allowed once per minute and replaces the previous link.

## Resend

```env
EMAIL_PROVIDER=resend
EMAIL_FROM=noreply@example.org
APP_PUBLIC_URL=https://example.org
EMAIL_TIMEOUT=10s
RESEND_API_KEY=<secret>
```

The sender domain must be [verified in Resend](https://resend.com/docs/dashboard/domains/introduction).
Choose a sender address on that domain, such as `noreply`. Disable click tracking
for verification messages so redirects do not alter the link. The token travels
in the URL fragment and is removed from the address bar by the verification page.

`APP_PUBLIC_URL` is the frontend HTTPS origin without a path, query or fragment.
HTTP is allowed only for localhost and loopback IP addresses during development.
When `EMAIL_PROVIDER` is empty, `RESEND_ENABLED=true` selects Resend. An explicit
provider takes precedence. Delivery is disabled when no provider is selected.

## SMTP

```env
EMAIL_PROVIDER=smtp
EMAIL_FROM=noreply@example.org
APP_PUBLIC_URL=https://example.org
EMAIL_TIMEOUT=10s
SMTP_HOST=smtp.example.org
SMTP_PORT=587
SMTP_USERNAME=<smtp-user>
SMTP_PASSWORD=<secret>
SMTP_TLS_MODE=starttls
```

`starttls` requires STARTTLS; `tls` starts with TLS immediately. Both verify the
certificate and hostname. Leave both credentials empty for a relay that does
not require authentication. Password whitespace is preserved.

`EMAIL_PROVIDER=disabled` disables sending. Registration and resend return 503.
If delivery fails after creating a pending account, request another message on
`/verify-email` after the one-minute interval.

## Cutover and validation

Stop old backend instances, apply `000035_player_accounts.sql`, then start the
new application. This prevents old code from creating players while username
reservations are being populated. Existing
players and results are retained, and their names remain reserved without case
sensitivity. Registration cannot claim an existing player. Sessions without a
verified account are rejected; nickname-only login returns 410.

The migration refuses rollback while account data exists. Rolling back the
application to an old image reopens nickname-only login and is unsafe for a
public service. Prefer a forward fix or maintenance mode.

Local tests use synthetic addresses, captured messages and loopback SMTP with
TLS. They do not prove real mailbox delivery. Before launch, check registration
on the target domain and delivery to an owner-approved test address. OAuth, MFA
and password recovery are outside this stage.

## Local checks

From `backend`, run the account and migration checks with disposable PostgreSQL:

```sh
go test -race -tags=integration -count=1 ./integration_test/player
go test -race -tags=integration,account_e2e -count=1 ./integration_test/accountflow
```

From `frontend`, run the browser flow:

```sh
E2E_ACCOUNT_FULL_STACK=1 npm run test:e2e -- e2e/account-full-stack.spec.ts --workers=1
```

These checks require a local Docker-compatible runtime, project dependencies,
and the pinned Go and Chromium runtimes. Use an environment without live secrets.
Browser mode starts separate API, frontend and PostgreSQL instances, captures
mail in memory and closes the fixtures after the test. It does not contact a
real mail provider.
