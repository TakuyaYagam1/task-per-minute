#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
ENV_FILE="${E2E_COMPOSE_ENV_FILE:-}"
COMPOSE_FILE="${E2E_COMPOSE_FILE:-$ROOT_DIR/deployment/docker/docker-compose.local.yml}"
COMPOSE_PROJECT_NAME="${E2E_COMPOSE_PROJECT_NAME:-}"
REPORT_FILE="${E2E_COMPOSE_REPORT_FILE:-$ROOT_DIR/.codex/.tmp/e2e-full-stack/report.json}"
TEST_MODE="${E2E_COMPOSE_TEST_MODE:-0}"
SOURCE_REVISION="$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || true)"
SOURCE_MODIFIED="false"
if [[ -n "$(git -C "$ROOT_DIR" status --porcelain=v1 --untracked-files=no 2>/dev/null || true)" ]]; then
  SOURCE_MODIFIED="true"
fi

FRONTEND_PORT="${E2E_FRONTEND_PORT:-${FRONTEND_PORT:-}}"
BACKEND_PORT="${E2E_BACKEND_PORT:-${BACKEND_PORT:-}}"
POSTGRES_PORT="${E2E_POSTGRES_PORT:-${POSTGRES_PORT:-}}"
REDIS_PORT="${E2E_REDIS_PORT:-${REDIS_PORT:-}}"
SEAWEEDFS_MASTER_PORT="${E2E_SEAWEEDFS_MASTER_PORT:-${SEAWEEDFS_MASTER_PORT:-}}"
SEAWEEDFS_S3_PORT="${E2E_SEAWEEDFS_S3_PORT:-${SEAWEEDFS_S3_PORT:-}}"

compose_project_started=0
cleanup_status="not_started"
run_status="not_started"
run_exit_code=1
run_error=""
cleanup_error=""
cleanup_scope="none"
temp_root=""
step_file=""
lock_volume=""
lock_token=""
lock_owned=0

die() {
  run_status="failed"
  run_exit_code=1
  run_error="$1"
  printf 'full-stack e2e: ERROR: %s\n' "$run_error" >&2
  exit 1
}

require_absolute_path() {
  local label="$1"
  local value="$2"
  [[ "$value" == /* ]] || die "$label must be an absolute path"
}

require_port() {
  local label="$1"
  local value="$2"
  [[ "$value" =~ ^[0-9]+$ ]] || die "$label must be an integer port"
  ((value >= 1024 && value <= 65535)) || die "$label must be between 1024 and 65535"
}

write_report() {
  [[ -n "$step_file" ]] || return 0
  STEP_FILE="$step_file" \
    REPORT_STATUS="$run_status" \
    REPORT_EXIT_CODE="$run_exit_code" \
    REPORT_ERROR="$run_error" \
    REPORT_CLEANUP_STATUS="$cleanup_status" \
    REPORT_CLEANUP_ERROR="$cleanup_error" \
    REPORT_CLEANUP_SCOPE="$cleanup_scope" \
    REPORT_SOURCE_REVISION="$SOURCE_REVISION" \
    REPORT_SOURCE_MODIFIED="$SOURCE_MODIFIED" \
    node - "$REPORT_FILE" "$step_file" <<'NODE'
const fs = require('node:fs');
const path = require('node:path');

const reportPath = process.argv[2];
const stepPath = process.argv[3];
const rawSteps = fs.readFileSync(stepPath, 'utf8').trim();
const steps = rawSteps === ''
  ? []
  : rawSteps.split('\n').map((line) => {
      const [name, status, exitCode] = line.split('\t');
      return { name, status, exit_code: Number(exitCode) };
    });

fs.mkdirSync(path.dirname(reportPath), { recursive: true, mode: 0o700 });
const report = {
  schema_version: 1,
  source_revision: process.env.REPORT_SOURCE_REVISION || null,
  source_modified: process.env.REPORT_SOURCE_MODIFIED === 'true',
  status: process.env.REPORT_STATUS,
  exit_code: Number(process.env.REPORT_EXIT_CODE),
  steps,
  cleanup: {
    status: process.env.REPORT_CLEANUP_STATUS,
    scope: process.env.REPORT_CLEANUP_SCOPE,
    error: process.env.REPORT_CLEANUP_ERROR || null,
  },
};
if (process.env.REPORT_ERROR) report.error = process.env.REPORT_ERROR;
fs.writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`, { mode: 0o600 });
fs.chmodSync(reportPath, 0o600);
NODE
}

record_step() {
  local name="$1"
  local status="$2"
  local exit_code="$3"
  printf '%s\t%s\t%s\n' "$name" "$status" "$exit_code" >> "$step_file"
  write_report
}

run_step() {
  local name="$1"
  shift
  set +e
  "$@"
  local exit_code=$?
  set -e
  if ((exit_code == 0)); then
    record_step "$name" pass 0
    return 0
  fi
  record_step "$name" fail "$exit_code"
  return "$exit_code"
}

run_capture_step() {
  local name="$1"
  local output_file="$2"
  shift 2
  set +e
  "$@" >"$output_file"
  local exit_code=$?
  set -e
  if ((exit_code == 0)); then
    record_step "$name" pass 0
    return 0
  fi
  record_step "$name" fail "$exit_code"
  return "$exit_code"
}

resource_ids() {
  local kind="$1"
  case "$kind" in
    containers) docker ps -aq --filter "label=com.docker.compose.project=$COMPOSE_PROJECT_NAME" ;;
    volumes) docker volume ls -q --filter "label=com.docker.compose.project=$COMPOSE_PROJECT_NAME" ;;
    networks) docker network ls -q --filter "label=com.docker.compose.project=$COMPOSE_PROJECT_NAME" ;;
    *) return 2 ;;
  esac
}

acquire_project_lock() {
  lock_volume="${COMPOSE_PROJECT_NAME}-runner-lock"
  lock_token="${SOURCE_REVISION:-unknown}-$$-$(date +%s)"
  if docker volume inspect "$lock_volume" >/dev/null 2>&1; then
    die "runner lock volume '$lock_volume' already exists; refusing project reuse"
  fi
  docker volume create \
    --label "com.task-per-minute.e2e.runner=1" \
    --label "com.task-per-minute.e2e.project=$COMPOSE_PROJECT_NAME" \
    --label "com.task-per-minute.e2e.run-token=$lock_token" \
    "$lock_volume" >/dev/null || die "could not reserve runner lock volume '$lock_volume'"
  local actual_token
  actual_token="$(docker volume inspect -f '{{ index .Labels "com.task-per-minute.e2e.run-token" }}' "$lock_volume")"
  [[ "$actual_token" == "$lock_token" ]] || die "runner lock volume '$lock_volume' is not owned by this run"
  lock_owned=1
}

remove_project_lock() {
  ((lock_owned == 1)) || return 0
  if [[ "$TEST_MODE" == "1" ]]; then
    return 0
  fi
  local actual_token
  actual_token="$(docker volume inspect -f '{{ index .Labels "com.task-per-minute.e2e.run-token" }}' "$lock_volume" 2>/dev/null || true)"
  if [[ "$actual_token" != "$lock_token" ]]; then
    cleanup_error="runner lock volume '$lock_volume' changed outside this run; cleanup skipped"
    return 1
  fi
  docker volume rm "$lock_volume" >/dev/null
}

assert_no_project_resources() {
  local kind
  local ids
  for kind in containers volumes networks; do
    ids="$(resource_ids "$kind")"
    [[ -z "$ids" ]] || die "compose project '$COMPOSE_PROJECT_NAME' already owns Docker $kind; refusing cleanup"
  done
}

assert_owned_resources() {
  local kind
  local current
  local expected
  for kind in containers volumes networks; do
    current="$(resource_ids "$kind")"
    expected="$(printf '%s\n' "${owned_resources[$kind]:-}" | sed '/^$/d' | sort)"
    if [[ "$(printf '%s\n' "$current" | sed '/^$/d' | sort)" != "$expected" ]]; then
      cleanup_error="Docker $kind changed outside this run; cleanup skipped"
      return 1
    fi
  done
  return 0
}

check_port_free() {
  local label="$1"
  local port="$2"
  command -v ss >/dev/null 2>&1 || die "ss is required to check ownership of host ports"
  if ss -H -ltn "sport = :$port" | grep -q .; then
    die "$label port $port is already occupied; refusing cleanup or reuse"
  fi
}

cleanup() {
  local status=$?
  set +e
  if ((compose_project_started == 1)); then
    if assert_owned_resources; then
      cleanup_scope="compose project '$COMPOSE_PROJECT_NAME', its containers, networks, named volumes and runner lock"
      if [[ "$TEST_MODE" == "1" ]]; then
        cleanup_status="simulated"
      else
        docker compose -p "$COMPOSE_PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" down --volumes
        local cleanup_exit=$?
        if ((cleanup_exit == 0)); then
          cleanup_status="passed"
        else
          cleanup_status="failed"
          cleanup_error="docker compose cleanup exited with $cleanup_exit"
          status=1
        fi
      fi
    else
      cleanup_status="skipped_conflict"
      status=1
    fi
  else
    cleanup_status="not_started"
  fi
  if [[ "$cleanup_status" != "skipped_conflict" ]] && ! remove_project_lock; then
    cleanup_status="failed"
    status=1
  fi
  run_exit_code="$status"
  if ((status == 0)); then
    run_status="passed"
  elif [[ "$run_status" != "failed" ]]; then
    run_status="failed"
  fi
  write_report
  rm -rf -- "$temp_root" 2>/dev/null || true
  exit "$status"
}
trap cleanup EXIT

require_absolute_path E2E_COMPOSE_REPORT_FILE "$REPORT_FILE"
mkdir -p "$(dirname "$REPORT_FILE")"
temp_root="$(mktemp -d -p "${TMPDIR:-/tmp}" task-per-minute-full-stack.XXXXXX)"
chmod 700 "$temp_root"
step_file="$temp_root/steps.tsv"
touch "$step_file"
chmod 600 "$step_file"
trap cleanup EXIT
write_report
run_status="running"
write_report

[[ "$TEST_MODE" == "1" || "${E2E_COMPOSE_SYNTHETIC:-0}" == "1" ]] || die "E2E_COMPOSE_SYNTHETIC=1 is required"
[[ "${E2E_COMPOSE_CLEAN:-0}" == "0" ]] || die "E2E_COMPOSE_CLEAN must be 0; pre-run cleanup is forbidden"
[[ "${E2E_COMPOSE_KEEP:-0}" == "0" ]] || die "E2E_COMPOSE_KEEP must be 0; scoped cleanup is mandatory"
[[ -n "$ENV_FILE" ]] || die "E2E_COMPOSE_ENV_FILE must point to a task-owned synthetic env file"
require_absolute_path E2E_COMPOSE_ENV_FILE "$ENV_FILE"
[[ -f "$ENV_FILE" ]] || die "synthetic env file not found: $ENV_FILE"
[[ "$(stat -c '%a' "$ENV_FILE" 2>/dev/null || true)" == "600" ]] || die "synthetic env file must have mode 0600"
[[ "$(basename "$ENV_FILE")" != ".env" && "$(basename "$ENV_FILE")" != ".env.local" ]] || die "real env filenames are not accepted"
require_absolute_path E2E_COMPOSE_FILE "$COMPOSE_FILE"
[[ -f "$COMPOSE_FILE" ]] || die "compose file not found: $COMPOSE_FILE"
[[ -n "$COMPOSE_PROJECT_NAME" ]] || die "E2E_COMPOSE_PROJECT_NAME is required"
[[ "$COMPOSE_PROJECT_NAME" =~ ^[a-z0-9][a-z0-9_-]{2,62}$ ]] || die "compose project name is invalid"
[[ "$COMPOSE_PROJECT_NAME" != task-per-minute-e2e ]] || die "default compose project name is not isolated"

for port_spec in \
  "E2E_FRONTEND_PORT:$FRONTEND_PORT" \
  "E2E_BACKEND_PORT:$BACKEND_PORT" \
  "E2E_POSTGRES_PORT:$POSTGRES_PORT" \
  "E2E_REDIS_PORT:$REDIS_PORT" \
  "E2E_SEAWEEDFS_MASTER_PORT:$SEAWEEDFS_MASTER_PORT" \
  "E2E_SEAWEEDFS_S3_PORT:$SEAWEEDFS_S3_PORT"; do
  require_port "${port_spec%%:*}" "${port_spec#*:}"
done

declare -A owned_resources

if [[ "$TEST_MODE" != "1" ]]; then
  run_step "frontend runtime preflight" bash "$ROOT_DIR/scripts/release/verify-security-tools.sh" --scope frontend || die "frontend runtime preflight failed"
  run_step "docker runtime" docker info >/dev/null || die "Docker runtime is unavailable"
  run_step "compose runtime" docker compose version >/dev/null || die "Docker Compose runtime is unavailable"
else
  record_step "runtime preflight" pass 0
fi

run_step "suite source boundary" bash -c "! grep -Eq '(^|[^[:alnum:]_])(page|context)\\.route|route\\.fulfill' '$ROOT_DIR/frontend/e2e/full-stack-local.spec.ts'" \
  || die "full-stack suite contains REST or WebSocket mocks"

for port_spec in \
  "frontend:$FRONTEND_PORT" \
  "backend:$BACKEND_PORT" \
  "postgres:$POSTGRES_PORT" \
  "redis:$REDIS_PORT" \
  "seaweedfs-master:$SEAWEEDFS_MASTER_PORT" \
  "seaweedfs-s3:$SEAWEEDFS_S3_PORT"; do
  check_port_free "${port_spec%%:*}" "${port_spec#*:}"
done

if [[ "$TEST_MODE" == "1" ]]; then
  record_step "runner project lock" pass 0
else
  run_step "runner project lock" acquire_project_lock || die "could not reserve isolated runner project"
fi
assert_no_project_resources
[[ -n "${E2E_ADMIN_PASSWORD:-}" ]] || die "E2E_ADMIN_PASSWORD is required for full-stack browser e2e"

compose=(docker compose -p "$COMPOSE_PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE")
export FRONTEND_PORT BACKEND_PORT POSTGRES_PORT REDIS_PORT SEAWEEDFS_MASTER_PORT SEAWEEDFS_S3_PORT
export E2E_FULL_STACK=1
export E2E_FULL_STACK_ISOLATED=1
export E2E_SKIP_WEB_SERVER=1
export E2E_FRONTEND_URL="http://127.0.0.1:${FRONTEND_PORT}"
export E2E_BACKEND_URL="http://127.0.0.1:${BACKEND_PORT}"
full_stack_grep="${E2E_FULL_STACK_GREP:-}"
playwright_suite=(
  "$ROOT_DIR/frontend/node_modules/.bin/playwright"
  test
  e2e/full-stack-local.spec.ts
)
if [[ -n "$full_stack_grep" ]]; then
  playwright_suite+=(--grep "$full_stack_grep")
fi

compose_project_started=1
if ! run_step "compose startup" "${compose[@]}" up --build -d; then
  "${compose[@]}" ps >&2 || true
  "${compose[@]}" logs --tail=200 backend frontend >&2 || true
  for kind in containers volumes networks; do
    owned_resources[$kind]="$(resource_ids "$kind")"
  done
  die "compose startup failed"
fi
for kind in containers volumes networks; do
  owned_resources[$kind]="$(resource_ids "$kind")"
done

wait_for_url() {
  local name="$1"
  local url="$2"
  local attempts=90
  for ((attempt = 1; attempt <= attempts; attempt += 1)); do
    if curl -fsS --max-time 5 "$url" >/dev/null; then
      printf '%s is ready: %s\n' "$name" "$url"
      return 0
    fi
    sleep 2
  done
  printf '%s did not become ready: %s\n' "$name" "$url" >&2
  "${compose[@]}" ps >&2 || true
  "${compose[@]}" logs --tail=200 backend frontend >&2 || true
  return 1
}

run_step "backend readiness" wait_for_url backend "http://127.0.0.1:${BACKEND_PORT}/health" || die "backend did not become ready"
run_step "frontend readiness" wait_for_url frontend "http://127.0.0.1:${FRONTEND_PORT}/" || die "frontend did not become ready"

suite_output="$temp_root/suite.json"
if ! (
  cd "$ROOT_DIR/frontend"
  run_capture_step "full-stack suite discovery" "$suite_output" "${playwright_suite[@]}" --list --reporter=json
); then
  die "full-stack suite discovery failed"
fi
suite_count="$(node - "$suite_output" <<'NODE'
const fs = require('node:fs');
const report = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
function count(value) {
  if (Array.isArray(value)) return value.reduce((total, item) => total + count(item), 0);
  if (!value || typeof value !== 'object') return 0;
  let total = Array.isArray(value.tests) ? value.tests.length : 0;
  for (const [key, child] of Object.entries(value)) if (key !== 'tests') total += count(child);
  return total;
}
const total = count(report);
if (total < 1) process.exit(1);
process.stdout.write(String(total));
NODE
)" || die "full-stack suite is empty or malformed"
record_step "full-stack suite composition" pass 0
printf 'full-stack suite discovered: %s test(s)\n' "$suite_count"

if ! (
  cd "$ROOT_DIR/frontend"
  run_step "full-stack browser suite" node scripts/run-e2e.mjs "${playwright_suite[@]:2}"
); then
  "${compose[@]}" logs --tail=200 backend frontend >&2 || true
  die "full-stack browser suite failed"
fi

run_status="passed"
run_exit_code=0
run_error=""
