# Infrastructure Standard

## Current Topology

- Production is a Docker Compose stack with Caddy, PostgreSQL, Redis, SeaweedFS, backend, and frontend on the private `internal` bridge network.
- Caddy is the public edge. It terminates TLS and routes player, admin, API, file, and task domains.
- Backend, frontend, PostgreSQL, Redis, and SeaweedFS use internal exposure in the production compose file. Do not publish their ports to the host without an explicit requirement and threat review.
- The main player domain denies admin UI and admin API paths. Preserve that boundary.
- PostgreSQL, Redis, SeaweedFS, and Caddy state use named volumes. Volume deletion is data deletion.
- The backend image is a multi-stage static Go build. The runtime container uses a non-root user and contains server, migration binary, and migrations.
- Manual production compose builds application images from source. CI publishes and deploys backend and frontend image tags derived from the target Git SHA through `docker-compose.ci.yml`; the current pipeline does not record or verify image digests.

## Configuration And Secrets

- Public variable names and placeholders belong in `.env.example`. Real values belong in the deployment environment and must never be committed, printed, copied into build args, or included in logs.
- Preserve strict backend config validation for database, Redis, object storage, JWT, admin, HTTP, and WebSocket settings.
- Preserve trusted proxy CIDRs and exact Origin allowlists. Never use blanket proxy trust or blanket CORS as a troubleshooting shortcut.
- A literal `$` in a bcrypt value used by Compose must be escaped according to `.env.example`. Never normalize or echo the real value.
- Caddy must continue to forward WebSocket and API traffic to the backend and object downloads through the dedicated file domain.

## Delivery And Lifecycle

- `.github/workflows/pipeline.yml` orchestrates backend checks, frontend verification, image builds, and production deploy.
- Keep third-party actions pinned by commit SHA. Review workflow permissions against the least privilege needed per job; the current top-level pipeline grants `packages: write`, while reusable check and deploy workflows narrow their own permissions.
- Static checks and tests must complete before image publication or deploy.
- Backend startup applies Goose migrations and performs recovery before becoming healthy. Infrastructure changes must account for migration duration and recovery failure.
- Production health requires backend dependencies and a positive schema version. Frontend and Caddy have separate health gates.
- Preserve `restart: unless-stopped`, resource limits, dependency health conditions, and graceful shutdown timeouts unless evidence supports a deliberate change.
- WS hubs, timers, and reconnect state are process-local. Do not add replicas or load balancing without a concrete shared-state and connection-affinity design.
- Treat deploy and rollback as separate state-changing operations. Keep the repository SHA, backend image, frontend image, schema compatibility, and health checks aligned.

## Validation

Repository backend gates run from `backend/`:

These are canonical CI targets. The current backend Makefile exports `../.env` into every target. Follow `backend.md` and do not run them in a checkout where a private root env file exists.

```bash
make mocks
make lint
make test
make test-int
make build
```

The documented production inspection commands run from `deployment/docker/` and require explicit access to the intended environment:

```bash
docker compose --env-file ../../.env ps
docker compose --env-file ../../.env exec -T caddy caddy validate --config /etc/caddy/Caddyfile
docker compose --env-file ../../.env exec -T caddy wget -qO- http://127.0.0.1:2019/config/ >/dev/null
docker compose --env-file ../../.env exec -T backend wget -qO- http://127.0.0.1:8080/health
```

Do not run production inspection commands merely to validate documentation. Report them as skipped when the task has no production authorization.

## Destructive And External Boundaries

- Never run `docker compose down -v`, remove named volumes, prune global Docker state, or replace persistent directories without explicit approval and a verified backup.
- Never run production `pull`, `up`, `restart`, deploy, rollback, migration down, or image publication unless the task explicitly authorizes that external state change.
- `scripts/server-bootstrap.sh` is a root-only host provisioning operation. It installs packages, may download Docker tooling, changes users and groups, prepares `.env`, and changes UFW rules. Do not run it during ordinary development or validation.
- Do not use `git reset --hard` from the runbook in a working repository. Production rollback commands require explicit operator approval and exact target SHA verification.
- Do not inspect or display the real `.env`, deployment keys, registry credentials, webhook URLs, private keys, or runtime secrets. Approved runtime tools may consume configured secrets without exposing them.
- Inspect dirty files before editing infrastructure. Preserve unrelated user changes and stop on ownership collisions.
