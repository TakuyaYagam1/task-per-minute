#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${E2E_COMPOSE_ENV_FILE:-.env.local}"
COMPOSE_FILE="${E2E_COMPOSE_FILE:-deployment/docker/docker-compose.local.yml}"
COMPOSE_PROJECT_NAME="${E2E_COMPOSE_PROJECT_NAME:-task-per-minute-e2e}"

if [[ "$ENV_FILE" != /* ]]; then
  ENV_FILE="$ROOT_DIR/$ENV_FILE"
fi
if [[ "$COMPOSE_FILE" != /* ]]; then
  COMPOSE_FILE="$ROOT_DIR/$COMPOSE_FILE"
fi

if [[ ! -f "$ENV_FILE" ]]; then
  echo "Env file not found: $ENV_FILE" >&2
  exit 1
fi
if [[ ! -f "$COMPOSE_FILE" ]]; then
  echo "Compose file not found: $COMPOSE_FILE" >&2
  exit 1
fi

export FRONTEND_PORT="${FRONTEND_PORT:-${E2E_FRONTEND_PORT:-3100}}"
export BACKEND_PORT="${BACKEND_PORT:-${E2E_BACKEND_PORT:-18080}}"
export POSTGRES_PORT="${POSTGRES_PORT:-${E2E_POSTGRES_PORT:-15432}}"
export REDIS_PORT="${REDIS_PORT:-${E2E_REDIS_PORT:-16379}}"
export SEAWEEDFS_MASTER_PORT="${SEAWEEDFS_MASTER_PORT:-${E2E_SEAWEEDFS_MASTER_PORT:-19333}}"
export SEAWEEDFS_S3_PORT="${SEAWEEDFS_S3_PORT:-${E2E_SEAWEEDFS_S3_PORT:-18333}}"
export SEAWEEDFS_PUBLIC_ENDPOINT="${SEAWEEDFS_PUBLIC_ENDPOINT:-${E2E_SEAWEEDFS_PUBLIC_ENDPOINT:-127.0.0.1:${SEAWEEDFS_S3_PORT}}}"

export HTTP_ALLOWED_ORIGINS="${HTTP_ALLOWED_ORIGINS:-http://127.0.0.1:${FRONTEND_PORT},http://localhost:${FRONTEND_PORT}}"
export WS_ALLOWED_ORIGINS="${WS_ALLOWED_ORIGINS:-http://127.0.0.1:${FRONTEND_PORT},http://localhost:${FRONTEND_PORT}}"

if [[ "${E2E_DIRECT_BROWSER_API:-0}" != "1" ]]; then
  export NEXT_PUBLIC_API_URL=""
  export NEXT_PUBLIC_ADMIN_API_URL=""
  export NEXT_PUBLIC_WS_URL=""
else
  export NEXT_PUBLIC_API_URL="${NEXT_PUBLIC_API_URL:-http://127.0.0.1:${BACKEND_PORT}}"
  export NEXT_PUBLIC_ADMIN_API_URL="${NEXT_PUBLIC_ADMIN_API_URL:-http://127.0.0.1:${BACKEND_PORT}}"
  export NEXT_PUBLIC_WS_URL="${NEXT_PUBLIC_WS_URL:-ws://127.0.0.1:${BACKEND_PORT}/ws}"
fi

read_env_value() {
  local key="$1"
  awk -v key="$key" '
    /^[[:space:]]*#/ { next }
    index($0, key "=") == 1 {
      sub("^[^=]*=", "")
      print
      exit
    }
  ' "$ENV_FILE" | tr -d '\r'
}

if [[ -z "${E2E_ADMIN_PASSWORD:-}" ]]; then
  admin_password_from_env="$(read_env_value ADMIN_PASSWORD)"
  admin_password_from_env="${admin_password_from_env%\"}"
  admin_password_from_env="${admin_password_from_env#\"}"
  admin_password_from_env="${admin_password_from_env%\'}"
  admin_password_from_env="${admin_password_from_env#\'}"

  if [[ -z "$admin_password_from_env" || "$admin_password_from_env" == \$2* || "$admin_password_from_env" == \$\$2* ]]; then
    echo "E2E_ADMIN_PASSWORD is required when ADMIN_PASSWORD in $ENV_FILE is empty or bcrypt-hashed." >&2
    exit 1
  fi
  export E2E_ADMIN_PASSWORD="$admin_password_from_env"
fi

PLAYWRIGHT_BIN="$ROOT_DIR/frontend/node_modules/.bin/playwright"
if [[ ! -x "$PLAYWRIGHT_BIN" ]]; then
  echo "Frontend dependencies are required for Playwright. Run: cd frontend && npm ci" >&2
  exit 1
fi

compose=(docker compose -p "$COMPOSE_PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE")

wait_for_url() {
  local name="$1"
  local url="$2"
  local attempts="${3:-90}"

  for ((i = 1; i <= attempts; i++)); do
    if curl -fsS "$url" >/dev/null; then
      echo "$name is ready: $url"
      return 0
    fi
    sleep 2
  done

  echo "$name did not become ready: $url" >&2
  "${compose[@]}" ps >&2 || true
  "${compose[@]}" logs --tail=200 backend frontend >&2 || true
  return 1
}

cleanup() {
  local status=$?
  if [[ "${E2E_COMPOSE_KEEP:-0}" != "1" ]]; then
    "${compose[@]}" down --volumes --remove-orphans
  else
    echo "Keeping compose project '$COMPOSE_PROJECT_NAME' for inspection."
  fi
  exit "$status"
}
trap cleanup EXIT

if [[ "${E2E_COMPOSE_CLEAN:-1}" == "1" ]]; then
  "${compose[@]}" down --volumes --remove-orphans
fi

"${compose[@]}" up --build -d

wait_for_url "backend" "http://127.0.0.1:${BACKEND_PORT}/health"
wait_for_url "frontend" "http://127.0.0.1:${FRONTEND_PORT}/"

(
  cd "$ROOT_DIR/frontend"
  E2E_SKIP_WEB_SERVER=1 \
    E2E_FULL_STACK=1 \
    E2E_FULL_STACK_ISOLATED=1 \
    E2E_FRONTEND_URL="http://127.0.0.1:${FRONTEND_PORT}" \
    E2E_BACKEND_URL="http://127.0.0.1:${BACKEND_PORT}" \
    "$PLAYWRIGHT_BIN" test e2e/full-stack-local.spec.ts --reporter=line
)
