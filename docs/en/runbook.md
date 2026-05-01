# Deploy Runbook

Operational notes for production deploys and manual rollback.

## Normal Deploy

The GitHub Actions deploy workflow builds a SHA-tagged backend image, pushes it
to GHCR, SSHes to the server, resets the repository to the target SHA, then
runs:

```bash
cd /opt/task-per-minute/deployment/docker
export BACKEND_IMAGE=<sha-image>
docker compose --env-file ../../.env pull backend migrate
docker compose --env-file ../../.env run --rm migrate
docker compose --env-file ../../.env up -d --remove-orphans backend
```

The migration step happens before the backend container is replaced. If a
migration fails, the workflow stops and the old backend keeps running.

## Health Gate

Deploy is considered healthy only when `/health` returns all dependencies as
`ok` and a positive schema version:

```bash
curl -fsS http://127.0.0.1:8080/health | jq .
```

Expected fields:

```json
{
  "status": "ok",
  "db": "ok",
  "redis": "ok",
  "seaweedfs": "ok",
  "schema_version": 2
}
```

## Roll Back Backend Image

Use this when the new container starts but health verification fails. The
workflow performs this automatically from `.last-deployed-sha`; these are the
manual commands.

```bash
cd /opt/task-per-minute

IMAGE_REPO=ghcr.io/<owner>/task-per-minute-backend
PREVIOUS_SHA="$(tr -d '[:space:]' < .last-deployed-sha)"
PREVIOUS_IMAGE="$IMAGE_REPO:$PREVIOUS_SHA"

git fetch --prune origin
git reset --hard "$PREVIOUS_SHA"

cd deployment/docker
export BACKEND_IMAGE="$PREVIOUS_IMAGE"
docker compose --env-file ../../.env pull backend
docker compose --env-file ../../.env up -d --remove-orphans backend
curl -fsS http://127.0.0.1:8080/health | jq .
```

## Restart Stack

For a normal restart without deleting data:

```bash
cd /opt/task-per-minute/deployment/docker
docker compose --env-file ../../.env restart backend
```

To recreate the backend container on the current image:

```bash
docker compose --env-file ../../.env up -d --remove-orphans backend
```

## Full Stack Removal

Stop the stack without deleting data:

```bash
cd /opt/task-per-minute/deployment/docker
docker compose --env-file ../../.env down --remove-orphans
```

Remove the stack together with database, Redis, and SeaweedFS volumes:

```bash
docker compose --env-file ../../.env down -v --remove-orphans
```

The `-v` command deletes data. Use it only for a full environment reset or after
creating a backup.

## Manual Migration Rollback

Production deploy does not run `goose down` automatically. Use this only after
checking that the migration has a valid `-- +goose Down` block, the rollback is
compatible with the backend image you are restoring, and the database has a
fresh backup.

Check status:

```bash
cd /opt/task-per-minute/deployment/docker
export BACKEND_IMAGE="$PREVIOUS_IMAGE"
docker compose --env-file ../../.env run --rm migrate status
```

Roll back one migration:

```bash
export BACKEND_IMAGE="$PREVIOUS_IMAGE"
docker compose --env-file ../../.env run --rm migrate down
```

Repeat `migrate down` only when each step has been reviewed. After schema
rollback, redeploy the compatible backend image and verify `/health`.
