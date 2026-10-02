# Task Per Minute

[Русский](README.md)

Task Per Minute is a competitive CTF tournament platform. Participants advance
through Swiss rounds, the Golden stage, and playoffs while the backend keeps
rosters, assignments, results, recovery evidence, and projections authoritative.

Created for **RedShift**.

- RedShift Telegram channel: [@redshift_ctf](https://t.me/redshift_ctf)

![Task Per Minute](frontend/public/task.png)

## Quick Start

- Prepare the environment:

```bash
cp .env.example .env.local
cp .env.example .env
```

- Fill the secrets in `.env.local` for local compose and in `.env` for
  production/server compose.

In Docker Compose, the backend receives `DB_DSN` from the selected env file, so
the container DSN must point to the internal `postgres:5432` host. `REDIS_ADDR`
and `SEAWEEDFS_ENDPOINT` are set by compose to `redis:6379` and
`seaweedfs:8333`; host variants are only for running the backend directly from
the host:

```env
DB_DSN=postgres://admin:password@postgres:5432/task_per_minute?sslmode=disable
REDIS_ADDR=localhost:6379
SEAWEEDFS_ENDPOINT=localhost:8333
SEAWEEDFS_PUBLIC_ENDPOINT=localhost:8333
SEAWEEDFS_PUBLIC_SECURE=false
```

For a non-container backend, temporarily use the host variant
`postgres://admin:password@localhost:5432/task_per_minute?sslmode=disable`.

`POSTGRES_PORT`, `REDIS_PORT`, and `SEAWEEDFS_*_PORT` are published on the host
for local debugging; inside the Docker network, containers keep their default
ports.
`SEAWEEDFS_PUBLIC_ENDPOINT` is embedded into browser-facing presigned URLs.

- Start the local compose stack:

```bash
cd deployment/docker
docker compose --env-file ../../.env.local -f docker-compose.local.yml up -d --build
```

The local compose stack starts the backend and the production frontend build.
By default the backend listens on `BACKEND_PORT=8080` and the frontend listens
on `FRONTEND_PORT=3000`.

Health check:

```bash
curl -fsS http://127.0.0.1:8080/health
curl -fsS http://127.0.0.1:3000/
```

Frontend development without Docker:

```bash
cd frontend
npm install
npm run dev
```

Backend development:

```bash
cd backend
go test ./...
go run ./cmd/app
```

Players register a username, email and password at `/register`, verify their
email through the supplied link, and sign in at `/login`. The username appears
on the leaderboard. Nickname-only `POST /api/v1/players/join` returns `410`.
See the [email guide](docs/en/email.md) for Resend, SMTP and migration from
legacy player sessions.

## Server

[scripts/server-bootstrap.sh](scripts/server-bootstrap.sh) is the first-time
Ubuntu/Debian server preparation script. It installs Docker, Docker Compose, and
git, creates the runtime user, app directory, `.env`, and basic firewall rules.

It is not the deploy pipeline. Automated deploys are handled by GitHub Actions
over SSH; the bootstrap script is needed once before the first server start.

Minimal first start after filling `.env`:

```bash
sudo bash scripts/server-bootstrap.sh
cd /opt/task-per-minute/deployment/docker
docker compose --env-file ../../.env up -d --build --remove-orphans
```

The main production compose builds backend/frontend from source on the server.
CI/CD deploy uses the same stack with the
`deployment/docker/docker-compose.ci.yml` override, where backend/frontend run
from prebuilt image tags.

## Documentation

- [Server deployment](docs/en/deploy.md)
- [Deploy and rollback runbook](docs/en/runbook.md)

## Contracts

- [OpenAPI](backend/api/openapi.yml) is the current REST contract.
- [Deployment](docs/en/deploy.md) documents production configuration,
  cookie-auth, CSRF, and WebSocket origin policy.
- [Runbook](docs/en/runbook.md) documents operational checks, rollback, and
  tournament runtime mechanics.

## Development Team

- [CaXaRo4iK](https://github.com/CaXaRo4iK) - DevOps, deployment,
  infrastructure, and tasks
- [FANATBEBRbl](https://github.com/FANATBEBRbl) - Frontend
- [skr1ms](https://github.com/skr1ms) - Backend

## Social Links

- RedShift Telegram: [@redshift_ctf](https://t.me/redshift_ctf)
- RedShift chat: [@redshift_ctf_chat](https://t.me/redshift_ctf_chat)

## License

Copyright (c) 2025 Task Per Minute contributors.

This project is licensed under the [GNU General Public License, version 3](LICENSE)
(`GPL-3.0-only`). You may redistribute and modify this program under the terms
of that license. This program is provided without any warranty, including the
implied warranties of merchantability or fitness for a particular purpose.

Third-party components retain their own licenses and copyright notices.
