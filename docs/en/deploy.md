# Production Deploy

This guide covers the first server setup for the Docker Compose stack.

## 1. Clone

```bash
sudo mkdir -p /opt/task-per-minute
sudo chown "$USER:$USER" /opt/task-per-minute
git clone <repo-url> /opt/task-per-minute
cd /opt/task-per-minute
```

## 2. Bootstrap Server

Run the bootstrap script as root on Ubuntu 22.04+:

```bash
sudo bash scripts/server-bootstrap.sh
```

The script is idempotent. It installs Docker Engine, ensures the `ctf` runtime
user, installs `git`, creates `/opt/task-per-minute`, prepares UFW rules for
`22/tcp`, `80/tcp`, `443/tcp`, and protects `.env`.

By default `DEPLOY_USER` is taken from `SUDO_USER`: that user gets Docker
access, membership in the `ctf` group, and access to `DEPLOY_PATH` and `.env`.
The `ctf` runtime user stays no-shell and should not be used as the GitHub
Actions SSH user.

Useful overrides:

```bash
sudo DEPLOY_USER="$USER" APP_USER=ctf APP_DIR=/opt/task-per-minute bash scripts/server-bootstrap.sh
sudo CONFIGURE_UFW=0 bash scripts/server-bootstrap.sh
```

After the user is added to the `docker`/`ctf` groups, log in again so the new
groups are visible in your shell session.

## 3. Fill Environment

The runtime `.env` file lives at repository root:

```bash
sudo install -m 0640 -o "$USER" -g ctf .env.example .env
sudo editor .env
sudo chown "$USER:ctf" .env
sudo chmod 0640 .env
```

Secrets must not stay empty:

```bash
openssl rand -base64 48 # JWT_SECRET
openssl rand -base64 32 # POSTGRES_PASSWORD
openssl rand -hex 24    # SEAWEEDFS_ACCESS_KEY
openssl rand -base64 48 # SEAWEEDFS_SECRET_KEY
openssl rand -base64 32 # ADMIN_PASSWORD
```

In Docker Compose, the backend receives `DB_DSN` from the selected env file. For
container runs, the DSN must point to the internal `postgres:5432` host;
`redis:6379` and `seaweedfs:8333` are set by compose as service-to-service
addresses:

```env
# Required only for CI/CD image deploys through docker-compose.ci.yml.
# Manual production compose builds backend/frontend from source.
BACKEND_IMAGE=ghcr.io/<owner>/task-per-minute-backend:<tag>
FRONTEND_IMAGE=ghcr.io/<owner>/task-per-minute-frontend:<tag>
CADDY_IMAGE=caddy:2-alpine
BACKEND_PORT=8080
FRONTEND_PORT=3000
DB_DSN=postgres://admin:<POSTGRES_PASSWORD>@postgres:5432/task_per_minute?sslmode=disable
APP_DOMAIN=example.com
ADMIN_DOMAIN=admin.example.com
API_DOMAIN=api.example.com
FILES_DOMAIN=files.example.com
CADDY_ACME_EMAIL=admin@example.com
CADDY_ACME_CA=https://acme-v02.api.letsencrypt.org/directory
TASK_COOKIE_DOMAIN=myfirstsite.example.com
TASK_ARCHIVE_DOMAIN=archive.example.com
TASK_SAMOVAR_DOMAIN=samovar.example.com
TASK_VKONTAKTE_DOMAIN=vkontakte.example.com
TASK_DEDYS_DOMAIN=dedys.example.com
TASK_COOKIE_UPSTREAM=frontend:3000
TASK_ARCHIVE_UPSTREAM=frontend:3000
TASK_SAMOVAR_UPSTREAM=frontend:3000
TASK_VKONTAKTE_UPSTREAM=frontend:3000
TASK_DEDYS_UPSTREAM=frontend:3000
CADDY_FRONTEND_UPSTREAM=frontend:3000
CADDY_BACKEND_UPSTREAM=backend:8080
CADDY_FILES_UPSTREAM=seaweedfs:8333
SEAWEEDFS_PUBLIC_ENDPOINT=files.example.com
SEAWEEDFS_PUBLIC_SECURE=true
DOCKER_INTERNAL_SUBNET=172.30.0.0/24
HTTP_TRUSTED_PROXY_CIDRS=172.30.0.0/24
```

`POSTGRES_PORT`, `REDIS_PORT`, and `SEAWEEDFS_*_PORT` are host-published ports
for local compose; inside the Docker network, containers keep their default
ports. For running the backend directly from the host, temporarily use a
`DB_DSN` host variant with `localhost:5432`; keep `postgres:5432` for compose.
`SEAWEEDFS_PUBLIC_ENDPOINT` is host-only, without
a scheme, and should match `FILES_DOMAIN`; this host is embedded into presigned
ZIP URLs, while Caddy proxies it to the SeaweedFS S3 endpoint.

For the current production domain split, the backend is exposed as
`https://api.утебяничегонеполучится.рф`, the player frontend as
`https://утебяничегонеполучится.рф`, and the admin frontend as
`https://admin.утебяничегонеполучится.рф`. Use the punycode form in env:
browsers usually serialize IDN domains in the `Origin` header this way, and
the backend compares origins as exact strings.

```env
APP_DOMAIN=xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
ADMIN_DOMAIN=admin.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
API_DOMAIN=api.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
FILES_DOMAIN=files.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
TASK_COOKIE_DOMAIN=myfirstsite.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
TASK_ARCHIVE_DOMAIN=archive.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
TASK_SAMOVAR_DOMAIN=samovar.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
TASK_VKONTAKTE_DOMAIN=vkontakte.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
TASK_DEDYS_DOMAIN=dedys.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
HTTP_ALLOWED_ORIGINS=https://admin.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai,https://xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
WS_ALLOWED_ORIGINS=https://xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
WS_REQUIRE_ORIGIN=true
NEXT_PUBLIC_API_URL=https://api.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
NEXT_PUBLIC_ADMIN_API_URL=https://api.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
NEXT_PUBLIC_WS_URL=wss://api.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai/ws
SEAWEEDFS_PUBLIC_ENDPOINT=files.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
SEAWEEDFS_PUBLIC_SECURE=true
ADMIN_LOGIN_RATE_ATTEMPTS=3
ADMIN_LOGIN_RATE_WINDOW=3m
ADMIN_LOGIN_RATE_BUCKET_TTL=15m
ADMIN_REFRESH_RATE_ATTEMPTS=10
ADMIN_REFRESH_RATE_WINDOW=3m
ADMIN_REFRESH_RATE_BUCKET_TTL=15m
LEADERBOARD_RATE_ATTEMPTS=120
LEADERBOARD_RATE_WINDOW=1m
LEADERBOARD_RATE_BUCKET_TTL=15m
WS_HANDSHAKE_RATE_ATTEMPTS=60
WS_HANDSHAKE_RATE_WINDOW=1m
WS_HANDSHAKE_RATE_BUCKET_TTL=15m
WS_MESSAGE_RATE_ATTEMPTS=120
WS_MESSAGE_RATE_WINDOW=1m
WS_ACTION_RATE_ATTEMPTS=30
WS_ACTION_RATE_WINDOW=1m
```

If `NEXT_PUBLIC_*` stays empty, the frontend reaches the backend through
same-origin rewrites (`/api` and `/ws`) and the internal `BACKEND_URL`. When
these values are set, the browser talks to the public API/WS endpoints directly.
The deploy workflow validates the build mode: either all `NEXT_PUBLIC_*` values
are empty, or all three `NEXT_PUBLIC_API_URL`, `NEXT_PUBLIC_ADMIN_API_URL`, and
`NEXT_PUBLIC_WS_URL` are set. Direct mode requires `https://` for REST,
`wss://.../ws` for WS, a shared backend origin, and explicit GitHub vars
`HTTP_ALLOWED_ORIGINS` and `WS_ALLOWED_ORIGINS` without wildcards; WS origins
must be a subset of REST origins. These values are baked into the frontend image
at build time, so changing the server `.env` after the build does not change
the browser bundle. For browser-only production mode, `WS_REQUIRE_ORIGIN=true`
is recommended; integration CLI clients without `Origin` will receive `403`.

Browser authentication uses HttpOnly cookies. Player/Admin session tokens must
not be stored in `localStorage` or `sessionStorage`; the frontend keeps only an
admin-session marker and readable CSRF tokens. Unsafe REST requests with
cookie-auth must send `X-CSRF-Token`; admin refresh/logout use the refresh CSRF
token from `X-Admin-Refresh-CSRF-Token` in either `X-CSRF-Token` or
`X-Admin-Refresh-CSRF-Token`. WebSocket connects only to `/ws` with
the player session cookie: query token `/ws?token=...`, `X-Session-Token`, and
bearer subprotocol are no longer supported browser contracts.

Caddy/compose defaults are sized for backend uploads up to 100MB: API-capable
routes use `request_body max_size 125MB`, and backend read/write timeout stays
at `5m`.

Production compose now includes a Caddy edge service. Only `CADDY_HTTP_PORT`
and `CADDY_HTTPS_PORT` are published to the host; backend, frontend, Postgres,
Redis, and SeaweedFS stay inside the Docker network. `expose` on internal
services does not publish a host port; it only documents the service port inside
the compose network.

If the backend should use the real client IP for rate limits and logs,
`HTTP_TRUSTED_PROXY_CIDRS` must match the internal Docker network where Caddy
talks to the backend:

```env
DOCKER_INTERNAL_SUBNET=172.30.0.0/24
HTTP_TRUSTED_PROXY_CIDRS=172.30.0.0/24
```

If every user behind Caddy receives `429` on login/refresh/join or `/ws`, check
that `HTTP_TRUSTED_PROXY_CIDRS` matches the Docker subnet and that Caddy sends
`X-Forwarded-For`. The backend reads forwarded headers only from trusted
proxies; with an empty or wrong CIDR, limits collapse to the proxy address.

## 4. First Start

Before starting HTTPS, create DNS `A/AAAA` records for `APP_DOMAIN`,
`ADMIN_DOMAIN`, `API_DOMAIN`, `FILES_DOMAIN`, and every `TASK_*_DOMAIN`, all
pointing to the server. On first boot, Caddy serves HTTP, completes ACME
HTTP-01 challenges automatically, stores certificates in `caddy_data`, then
serves HTTPS for the service and task subdomains.

```bash
cd /opt/task-per-minute/deployment/docker
docker compose --env-file ../../.env up -d --build --remove-orphans
docker compose --env-file ../../.env logs -f caddy
```

This is the manual source-build mode: backend/frontend are built on the server
from the current checkout. CI/CD deploy uses the same stack with the
`docker-compose.ci.yml` override, where backend/frontend run from prebuilt
SHA-tagged images.

For DNS/firewall testing without Let's Encrypt production rate-limit risk, set
`CADDY_ACME_CA=https://acme-staging-v02.api.letsencrypt.org/directory` first.
After staging verification, set it back to
`https://acme-v02.api.letsencrypt.org/directory` and restart Caddy:

```bash
docker compose --env-file ../../.env restart caddy
```

Caddy manages initial issuance and renewal itself. If issuance fails, fix
DNS/firewall and run `docker compose --env-file ../../.env restart caddy`.

Docker will restart the containers after host reboot once the stack has been
created because the compose services use `restart: unless-stopped`.

## 5. Check Deploy

```bash
cd /opt/task-per-minute/deployment/docker
docker compose --env-file ../../.env ps
docker compose --env-file ../../.env exec -T caddy caddy validate --config /etc/caddy/Caddyfile
docker compose --env-file ../../.env exec -T caddy wget -qO- http://127.0.0.1:2019/config/ >/dev/null
docker compose --env-file ../../.env logs --tail=100 caddy
docker compose --env-file ../../.env logs -f backend
docker compose --env-file ../../.env exec -T backend wget -qO- http://127.0.0.1:8080/health
docker compose --env-file ../../.env exec -T frontend sh -c 'wget -qO- "http://127.0.0.1:${PORT:-3000}/" >/dev/null'
```

The production compose file does not publish backend/frontend ports on the host.
Public traffic enters through Caddy:

```bash
curl -fsSI "https://$APP_DOMAIN/"
curl -fsSI "https://$ADMIN_DOMAIN/admin"
curl -fsS "https://$API_DOMAIN/health"
curl -fsSI "https://$FILES_DOMAIN/"
curl -fsSI "https://$TASK_COOKIE_DOMAIN/"
```

Renewal runs inside Caddy and uses the persistent `caddy_data` volume. The old
`certbot_*` and `nginx_cache` volumes are not used by the Caddy stack; removing
services from compose or containers with `docker rm -f` does not remove those
volumes unless you explicitly pass a volume-removal flag.

## 6. GitHub Actions Deploy

The single workflow is `.github/workflows/pipeline.yml`. It only orchestrates
the nearby reusable workflow files: backend checks, frontend verify, image
builds, and production deploy. One push to `main` creates one GitHub Actions run
with all jobs. Configure the GitHub Environment named `production` with required
reviewers in repository settings.

The workflow uses GitHub-hosted runners and deploys to the server over SSH. A
self-hosted runner on the production server is not required.

Configure these in `Settings -> Environments -> production` so they are scoped
to the production environment, not the whole repo.

Required environment **secrets** (sensitive values):

```text
DEPLOY_HOST           # SSH host (IP or DNS)
DEPLOY_USER           # SSH user with docker + git access
DEPLOY_SSH_KEY        # PEM private key for the SSH user
DEPLOY_PATH           # absolute path to the deploy checkout on the server
```

Optional environment **secrets** (also sensitive, only one webhook is required):

```text
TG_DEPLOY_WEBHOOK     # Telegram bot webhook URL for deploy notifications
SLACK_DEPLOY_WEBHOOK  # Slack webhook URL (used if TG webhook is not set)
```

Optional environment **variables** (`Settings -> Environments -> production ->
Variables`, not secrets - these are non-sensitive operational config):

If the VS Code/GitHub Actions extension reports `Value 'production' is not
valid`, it usually means the repository does not have a GitHub Environment named
`production` yet, or the extension cannot see repository settings. `Context
access might be invalid` for `vars.*`/`secrets.*` means the corresponding
variable/secret is not configured in the scope where the job reads it.

```text
DEPLOY_PORT             # SSH port, default 22
DEPLOY_HOST_FINGERPRINT # public host key fingerprint for strict host checking
DEPLOY_HEALTH_URL       # required public backend health URL, for example https://api.example.com/health
DEPLOY_FRONTEND_HEALTH_URL # required public frontend URL, for example https://example.com/
```

Frontend build-time values are read by the image build job before the production
environment is entered, so configure these as repository or organization
**variables**:

```text
BACKEND_PORT            # backend container HTTP port for default frontend rewrites, default 8080
FRONTEND_BACKEND_URL    # build-time BACKEND_URL for Next rewrites, default http://backend:$BACKEND_PORT
FRONTEND_PORT           # build-time frontend image port, default 3000
NEXT_PUBLIC_API_URL     # public API URL for direct browser-to-backend mode
NEXT_PUBLIC_ADMIN_API_URL # public admin API URL
NEXT_PUBLIC_WS_URL      # public WS URL
HTTP_ALLOWED_ORIGINS    # REST browser origins; required for direct mode
WS_ALLOWED_ORIGINS      # WS browser origins; subset of HTTP_ALLOWED_ORIGINS
WS_REQUIRE_ORIGIN       # require browser Origin on /ws, recommended true in prod
ADMIN_REFRESH_RATE_ATTEMPTS  # POST /api/v1/admin/refresh limit, default 10
ADMIN_REFRESH_RATE_WINDOW    # refresh rate-limit window, default 3m
ADMIN_REFRESH_RATE_BUCKET_TTL # refresh limiter idle bucket TTL, default 15m
LEADERBOARD_RATE_ATTEMPTS    # GET /api/v1/leaderboard per-IP limit, default 120
LEADERBOARD_RATE_WINDOW      # leaderboard rate-limit window, default 1m
LEADERBOARD_RATE_BUCKET_TTL  # leaderboard limiter idle bucket TTL, default 15m
WS_HANDSHAKE_RATE_ATTEMPTS   # /ws handshakes per-IP limit, default 60
WS_HANDSHAKE_RATE_WINDOW     # WS handshake limiter window, default 1m
WS_HANDSHAKE_RATE_BUCKET_TTL # WS limiter idle bucket TTL, default 15m
WS_MESSAGE_RATE_ATTEMPTS     # parsed WS messages per connection, default 120
WS_MESSAGE_RATE_WINDOW       # WS message limiter window, default 1m
WS_ACTION_RATE_ATTEMPTS      # join/leave/flag/surrender actions per connection, default 30
WS_ACTION_RATE_WINDOW        # WS action limiter window, default 1m
```

If you keep these values as `secrets.*` instead of `vars.*`, GitHub Actions will
either not expose them to the relevant job or the VS Code GitHub Actions
extension will flag context-access warnings.

`DEPLOY_USER` should be an SSH user with shell, git access, docker access, and
read/write access to `DEPLOY_PATH`/`.env`. Bootstrap configures this for the
user from `SUDO_USER` or the explicit `DEPLOY_USER`. Do not use the no-shell
`ctf` runtime user unless you intentionally change its shell.

The workflow builds and pushes immutable SHA-tagged backend and frontend images,
SSHes to the server, resets the repository to the deployed SHA, and runs:

```bash
export BACKEND_IMAGE=<sha-image>
export FRONTEND_IMAGE=<sha-image>
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml pull backend frontend
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml up -d --remove-orphans backend frontend
```

The backend runs Goose migrations during startup. If migration fails, the new
backend container exits or stays unhealthy, the health gate fails, and the
workflow rollback path restores the previous backend/frontend images. Before
Docker build, the frontend job regenerates OpenAPI types from the backend spec
in the same target SHA.

After deployment it verifies both the frontend page and the same-origin
`/api/v1/leaderboard` rewrite, so a broken baked `BACKEND_URL` fails the
frontend rollout instead of passing on a static page response. After a
successful health gate, the workflow pins `BACKEND_IMAGE` and `FRONTEND_IMAGE`
in the server `.env` to the deployed SHA images.

Manual rollback commands are in [runbook.md](runbook.md).
