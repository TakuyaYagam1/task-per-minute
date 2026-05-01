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
`22/tcp`, `80/tcp`, `443/tcp`, and protects `.env` as `0600 ctf`.

Useful overrides:

```bash
sudo APP_USER=ctf APP_DIR=/opt/task-per-minute bash scripts/server-bootstrap.sh
sudo CONFIGURE_UFW=0 bash scripts/server-bootstrap.sh
```

## 3. Fill Environment

The runtime `.env` file lives at repository root:

```bash
sudo install -m 0600 -o ctf -g ctf .env.example .env
sudo editor .env
sudo chown ctf:ctf .env
sudo chmod 0600 .env
```

Secrets must not stay empty:

```bash
openssl rand -base64 48 # JWT_SECRET
openssl rand -base64 32 # POSTGRES_PASSWORD
openssl rand -hex 24    # SEAWEEDFS_ACCESS_KEY
openssl rand -base64 48 # SEAWEEDFS_SECRET_KEY
openssl rand -base64 32 # ADMIN_PASSWORD
```

Inside Docker Compose, use service names in runtime addresses:

```env
DB_DSN=postgres://<user>:<password>@postgres:5432/<db>?sslmode=disable
REDIS_ADDR=redis:6379
SEAWEEDFS_ENDPOINT=seaweedfs:8333
```

## 4. First Start

```bash
cd /opt/task-per-minute/deployment/docker
sudo -u ctf docker compose --env-file ../../.env run --rm migrate
sudo -u ctf docker compose --env-file ../../.env up -d --remove-orphans
```

Docker will restart the containers after host reboot once the stack has been
created because the compose services use `restart: unless-stopped`.

## 5. Check Deploy

```bash
cd /opt/task-per-minute/deployment/docker
sudo -u ctf docker compose --env-file ../../.env ps
sudo -u ctf docker compose --env-file ../../.env logs -f backend
curl -fsS http://127.0.0.1:8080/health
```

If a reverse proxy exposes the service, route public HTTP/HTTPS traffic to the
backend or to the shared `proxy_tpm` Docker network expected by the compose
file.

## 6. GitHub Actions Deploy

The deploy workflow is `.github/workflows/backend-deploy.yml`. Configure the
GitHub Environment named `production` with required reviewers in repository
settings.

The workflow uses GitHub-hosted runners and deploys to the server over SSH. A
self-hosted runner on the production server is not required.

Required repository secrets:

```text
DEPLOY_HOST
DEPLOY_USER
DEPLOY_SSH_KEY
DEPLOY_PATH
```

Optional repository secrets:

```text
DEPLOY_PORT
DEPLOY_HOST_FINGERPRINT
DEPLOY_HEALTH_URL
TG_DEPLOY_WEBHOOK
SLACK_DEPLOY_WEBHOOK
```

`DEPLOY_USER` should be an SSH user with shell, git access, docker access, and
read/write access to `DEPLOY_PATH`. Do not use the no-shell `ctf` runtime user
unless you intentionally change its shell.

The workflow builds and pushes a SHA-tagged backend image, SSHes to the server,
resets the repository to the deployed SHA, and runs:

```bash
export BACKEND_IMAGE=<sha-image>
docker compose --env-file ../../.env pull backend migrate
docker compose --env-file ../../.env run --rm migrate
docker compose --env-file ../../.env up -d --remove-orphans backend
```

The migrate step runs before the backend container is replaced. If a migration
fails, the workflow stops and the currently running backend stays up.

Manual rollback commands are in [runbook.md](runbook.md).
