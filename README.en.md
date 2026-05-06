# Task Per Minute

[Русский](README.md)

Task Per Minute is a competitive CTF platform for short one-on-one duels.
Players join a match, receive a web challenge, solve it against the clock, and
win by submitting the correct flag first.

Created for **RedShift**.

- RedShift Telegram channel: [@redshift_ctf](https://t.me/redshift_ctf)

![Task Per Minute](frontend/public/task.png)

## Quick Start

- Prepare the environment:

```bash
cp .env.example .env
```

- Fill the secrets in `.env`.

In Docker Compose, the backend receives `DB_DSN` from `.env` directly, so the
container DSN must point to the internal `postgres:5432` host. `REDIS_ADDR` and
`SEAWEEDFS_ENDPOINT` are set by compose to `redis:6379` and `seaweedfs:8333`;
host variants are only for running the backend directly from the host:

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
npm install.
```

Backend development:

```bash
cd backend
go test ./...
go run ./cmd/app
```

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
docker compose --env-file ../../.env up -d --remove-orphans
```

## Documentation

- [Server deployment](docs/en/deploy.md)
- [Deploy and rollback runbook](docs/en/runbook.md)

## Development Team

- [CaXaRo4iK](https://github.com/CaXaRo4iK) - DevOps, deployment,
  infrastructure, and tasks
- [FANATBEBRbl](https://github.com/FANATBEBRbl) - Frontend
- [skr1ms](https://github.com/skr1ms) - Backend

## Social Links

- RedShift Telegram: [@redshift_ctf](https://t.me/redshift_ctf)
- RedShift chat: [@redshift_ctf_chat](https://t.me/redshift_ctf_chat)

## License

This project is licensed under the [MIT License](LICENSE).
