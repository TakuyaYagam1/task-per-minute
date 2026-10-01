# Accounts and email

Accounts become usable after email verification. A player registers a unique
username, email and password, opens the emailed link, confirms the address, and
logs in. The username appears on the leaderboard. Passwords contain 10-128
characters, including at least one lowercase letter, uppercase letter,
decimal digit, and punctuation mark or symbol. A space does not meet the
punctuation or symbol requirement.

Email must be a bare mailbox address. Its local part must be an ASCII dot-atom
with 1-64 characters. The domain must contain at least two dot-separated ASCII
DNS labels. The final label must consist only of at least two ASCII letters or
use the `xn--...` punycode form. `localhost` and domain literals are rejected.

An activation message appears for an unverified account only after the password
is checked and found correct. An incorrect password receives the generic sign-in
error. A session is created only after email verification. Verification links
expire after 24 hours and can be used once. Resending is allowed once per minute
and replaces the previous link.

On the verification page, confirm the address with the button. After success,
choose Sign in or wait for the automatic redirect after five seconds.

After a correct password for an unverified account, the sign-in form offers a
button to send another message to the address stored on the account. The button
is available once per minute. A request with an incorrect password sends no
message and receives the generic sign-in error.

After registration, the resend button sends another confirmation email without
leaving the page. A visible 60-second cooldown applies between requests. The
address is kept only in page memory; the password is cleared after registration.

Email bodies live in `backend/internal/adapter/outbound/mail/templates/`, with
HTML and plain-text versions for each message. `go:embed` includes them in the
binary, so template changes require rebuilding the backend. HTML values are
escaped by the standard `html/template` package.

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
If delivery fails after creating a pending account, use the resend button on
the registration page. Resending is also available on the sign-in page after
the correct account credentials have been checked.

## Account settings

Settings belong to the owner of a live player session. Changing a username or
password, and starting an email change, require the current password. An
incorrect current password does not end the session. Username changes update
the name reservation without replacing the player ID or results. Password
changes rotate the current session and CSRF token; the previous session stops
authenticating.

Email changes use the same Resend or SMTP adapter to send a six-digit code to
the new address. Codes expire after 10 minutes, with a 60-second resend delay.
Each account has an hourly budget of five sends and five failed confirmation
attempts. Cancellation, resend and page reload do not reset these budgets.
Only the code hash is stored. The previous address remains active until
confirmation, when uniqueness of the new address is checked again.

Confirmation rotates the session and sends a notice to the previous address.
The API reports notice delivery failure separately: the committed new email
remains active. Failure to send the confirmation code returns 503 rather than
claiming delivery succeeded.

Avatars use the `avatars/` prefix in the existing private S3 bucket. They need
no additional bucket or credentials. The server accepts JPG, PNG, GIF and MP4 up to
5 MiB, validates content and dimensions, strips metadata and preserves GIF
animation. Images are limited to 4096 pixels per dimension; GIF frame count and
total frame pixels are also bounded. Only the owner can retrieve the avatar through
the API, with `Cache-Control: private, no-store`.

MP4 avatars accept H.264, HEVC and MPEG-4 video. They are limited to 10 seconds,
60 fps, 1920 pixels per dimension and 2,073,600 pixels per frame.
FFmpeg validates and converts them to silent H.264 video at up to 512 x 512
pixels and 30 fps, removing source metadata. Processing
has time limits and one concurrent video slot per backend instance. The browser
loops the video without sound; reduced-motion mode starts it paused.
The backend requires `ffmpeg` and `ffprobe` on PATH. The Docker image builds the
pinned FFmpeg source after checking its checksum and release signature. The CI
media test runs the real codec tests with these same binaries in a container
without network access.

Selecting an avatar uploads it immediately. A failed replacement preserves the
previous image. Deletion removes it from the profile immediately and queues
the object for background deletion with retries on S3 failure. The same queue
handles replaced images, unfinished uploads and deleted accounts' avatars.

Apply `000037_player_account_settings.sql`, `000038_player_avatars.sql` and
`000039_player_avatar_video.sql` before starting the new version.
Rollback is restricted to avoid losing email
confirmation state or object cleanup work. Use a forward fix while such data
exists. Migration 39 cannot roll back while any MP4 object remains, including
objects awaiting cleanup. 2FA setup and forgotten-password recovery remain
outside this stage.

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

## Account deletion

Administrator deletion removes the linked account's email, password hash,
verification data and original username reservation. The email and username
can be registered again after a new email verification. A new registration
does not inherit the old player ID or results. The historical player record
marked as deleted, tournament results and action audit remain available.

The active session is revoked in the same transaction. To notify the browser,
the server temporarily retains only the revoked token's hash and expiry,
without an email, password or account reference. HTTP `401` with code
`player.account_deleted` distinguishes deletion from ordinary session expiry.
The active tab checks its session every 10 seconds and when focus returns, then
displays the administrator deletion notice. Acknowledging it opens the login page.
Expired hashes no longer affect authentication and are removed in bounded
batches during later player deletions; they can remain stored if no further
deletions occur.

Migration `000036_player_account_deletion.sql` removes credentials and username
reservations belonging to already deleted players. Stop old backend instances
before applying it, then start the updated application. Cleanup is irreversible,
so the migration refuses rollback; use a forward fix if needed. Verify that no
account remains linked to a deleted player and exercise registration with the
released identity on a test environment.

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
