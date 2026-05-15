# Deploy Runbook

Operational notes for production deploys and manual rollback.

## Normal Deploy

The GitHub Actions deploy workflow builds immutable SHA-tagged backend/frontend
images, pushes them to GHCR, SSHes to the server, resets the repository to the
target SHA, then runs:

```bash
cd /opt/task-per-minute/deployment/docker
export BACKEND_IMAGE=<sha-image>
export FRONTEND_IMAGE=<sha-image>
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml pull backend frontend
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml up -d --remove-orphans backend frontend
```

The backend runs Goose migrations during startup. If a migration fails, the new
backend container exits or stays unhealthy, the health gate fails, and the
workflow rollback path restores the previous backend/frontend images and
verifies health. Successful deploy and rollback steps also update
`BACKEND_IMAGE` and `FRONTEND_IMAGE` in the server `.env`, so later manual
compose operations keep using the pinned images.

## Health Gate

Deploy is considered healthy only when `/health` returns all dependencies as
`ok` and a positive schema version:

```bash
cd /opt/task-per-minute/deployment/docker
docker compose --env-file ../../.env exec -T backend wget -qO- http://127.0.0.1:8080/health | jq .
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

The frontend health gate checks that the frontend URL returns a successful HTTP
status and that the same-origin API rewrite returns the leaderboard response
shape:

```bash
cd /opt/task-per-minute/deployment/docker
docker compose --env-file ../../.env exec -T frontend sh -c 'wget -qO- "http://127.0.0.1:${PORT:-3000}/" >/dev/null'
docker compose --env-file ../../.env exec -T frontend sh -c 'wget -qO- "http://127.0.0.1:${PORT:-3000}/api/v1/leaderboard"' | jq -e '.entries | type == "array"'
docker compose --env-file ../../.env exec -T caddy caddy validate --config /etc/caddy/Caddyfile
docker compose --env-file ../../.env exec -T caddy wget -qO- http://127.0.0.1:2019/config/ >/dev/null
docker compose --env-file ../../.env logs --tail=100 caddy
```

## Fast Auth/WS Troubleshooting

If WebSocket does not connect:

- Check browser DevTools: the URL must be `/ws` or `wss://<api>/ws` without
  `?token=...`.
- Check `WS_ALLOWED_ORIGINS`: the player frontend origin must match exactly,
  including punycode for IDN domains.
- If `WS_REQUIRE_ORIGIN=true` is enabled, make sure the client sends a browser
  `Origin`; CLI/script clients without Origin will receive `403`.
- Check that player join/me responses issue the `tpm_player_session` cookie and
  that the browser sends it to `/ws`.
- The backend should return `401/403/429` as `application/problem+json` before
  upgrade when the session is missing, the origin is denied, or the handshake
  rate-limit is exhausted.

If unsafe REST requests receive `403 csrf token invalid`:

- For player cookie-auth, check `tpm_player_csrf` and `X-CSRF-Token`.
- For admin mutations, check the access CSRF token in `X-CSRF-Token`.
- For admin refresh/logout, check the refresh CSRF token from
  `X-Admin-Refresh-CSRF-Token`; send it in either `X-CSRF-Token` or
  `X-Admin-Refresh-CSRF-Token`. The access CSRF token is not valid for these
  endpoints.
- After logout, the frontend should clear the session marker and CSRF tokens;
  the next login should receive fresh CSRF headers.

If every user receives `429` behind Caddy:

- Check `HTTP_TRUSTED_PROXY_CIDRS` against the real compose network:
  `docker network inspect task-per-minute_internal`.
- Compare backend `client_ip` in security logs with Caddy logs.
- Ensure Caddy sends `X-Forwarded-For`, and the backend trusts only the proxy
  CIDR, not the whole internet.

## Roll Back Backend Image

Use this when the new container starts but health verification fails. The
workflow performs this automatically from `.last-deployed-sha`; these are the
manual commands. If `.last-deployed-sha` is missing, automatic rollback is not
available and the deploy requires manual recovery.

```bash
cd /opt/task-per-minute

IMAGE_REPO=ghcr.io/<owner>/task-per-minute-backend
PREVIOUS_SHA="$(tr -d '[:space:]' < .last-deployed-sha)"
PREVIOUS_IMAGE="$IMAGE_REPO:$PREVIOUS_SHA"

git fetch --prune origin
git reset --hard "$PREVIOUS_SHA"

cd deployment/docker
export BACKEND_IMAGE="$PREVIOUS_IMAGE"
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml pull backend
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml up -d --remove-orphans backend
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml exec -T backend wget -qO- http://127.0.0.1:8080/health | jq .
```

## Roll Back Frontend Image

Use this when the new frontend image deploys but frontend health verification
fails. The workflow performs this automatically from
`.last-deployed-frontend-sha` and verifies the frontend page plus same-origin
API rewrite after rollback. If `.last-deployed-frontend-sha` is missing,
automatic rollback is not available and the deploy requires manual recovery.
Successful deploy and rollback steps also update `FRONTEND_IMAGE` in the server
`.env`, so later manual compose operations keep using the pinned image.

```bash
cd /opt/task-per-minute

IMAGE_REPO=ghcr.io/<owner>/task-per-minute-frontend
PREVIOUS_SHA="$(tr -d '[:space:]' < .last-deployed-frontend-sha)"
PREVIOUS_IMAGE="$IMAGE_REPO:$PREVIOUS_SHA"

git fetch --prune origin
git reset --hard "$PREVIOUS_SHA"

cd deployment/docker
export FRONTEND_IMAGE="$PREVIOUS_IMAGE"
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml pull frontend
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml up -d --remove-orphans frontend
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml exec -T frontend sh -c 'wget -qO- "http://127.0.0.1:${PORT:-3000}/" >/dev/null'
```

## Restart Stack

For a normal restart without deleting data:

```bash
cd /opt/task-per-minute/deployment/docker
docker compose --env-file ../../.env restart backend frontend
```

To recreate backend/frontend from the current checkout:

```bash
docker compose --env-file ../../.env up -d --build --remove-orphans backend frontend
```

If the stack was deployed through CI/CD images, use the image override:

```bash
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml up -d --remove-orphans backend frontend
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
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml run --rm --entrypoint /app/migrate backend status
```

Roll back one migration:

```bash
export BACKEND_IMAGE="$PREVIOUS_IMAGE"
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml run --rm --entrypoint /app/migrate backend down
```

Repeat `/app/migrate down` only when each step has been reviewed. After schema
rollback, redeploy the compatible backend image and verify `/health`.

## Hint Mechanics

Every duel task carries exactly three hints. The backend pushes them to both
players over the WebSocket `hint_unlocked` event at 25 %, 50 %, and 75 % of
the task's `time_limit` (see `domain.BuildHintSchedule`).

- Hints **do not affect scoring** and cannot be unlocked manually; they are
  released on a timer aligned to `started_at`.
- When the duel timer pauses (`opponent_disconnected`), the hint schedule
  freezes together with the duel deadline and resumes after `duel_resume`.
- On player reconnect, the backend replays already-unlocked hints inside
  `duel_resume`, so no hint is ever dropped.
- End-to-end coverage: `TestE2EHintFlow_AutoUnlocksAt25_50_75` in
  `backend/integration_test/e2e_test.go` boots the real backend and asserts
  ordering and text of all three events.

## Reconnect Mechanics

- A WebSocket disconnect during an active duel puts the duel into reconnect
  pause: the duel deadline and hint schedule freeze, and the opponent receives
  `opponent_disconnected`.
- If the player returns within the reconnect window, the server sends
  `duel_resume` to that player and `opponent_reconnected` to the opponent; the
  deadline and hints then continue with the paused duration accounted for.
- If the reconnect window expires, the duel finishes as a draw. Exceeding the
  disconnect/reconnect limit for a player also immediately finishes the duel as
  a draw.
- If both players disconnect and both reconnect windows expire, the result is
  still a draw.
- Draws do not increment the leaderboard; `winner_id` remains empty.
