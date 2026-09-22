#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
RUNNER="$ROOT_DIR/scripts/e2e-local-compose.sh"
COMPOSE_FILE="$ROOT_DIR/deployment/docker/docker-compose.local.yml"
TEST_ROOT="$(mktemp -d -p "${TMPDIR:-/tmp}" task-per-minute-e2e-runner-test.XXXXXX)"
trap 'rm -rf -- "$TEST_ROOT"' EXIT
chmod 700 "$TEST_ROOT"

if grep -q -- '--remove-orphans' "$RUNNER"; then
  printf 'runner must not remove orphaned Docker resources\n' >&2
  exit 1
fi

make_env() {
  local path="$1"
  umask 077
  printf 'TPM_E2E_SYNTHETIC=1\n' > "$path"
  chmod 600 "$path"
}

assert_failed_report() {
  local report="$1"
  local expected="$2"
  node - "$report" "$expected" <<'NODE'
const fs = require('node:fs');
const report = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
const expected = process.argv[3];
if (report.status !== 'failed') throw new Error(`unexpected report status: ${report.status}`);
if (report.exit_code === 0) throw new Error('failed report has a zero exit code');
if (!String(report.error || '').includes(expected)) {
  throw new Error(`report error does not contain ${expected}: ${report.error}`);
}
if (fs.statSync(process.argv[2]).mode & 0o077) throw new Error('report is not private');
NODE
}

ports=(42111 42112 42113 42114 42115 42116)
env_file="$TEST_ROOT/synthetic.env"
make_env "$env_file"

early_report="$TEST_ROOT/early.json"
set +e
E2E_COMPOSE_SYNTHETIC=0 \
E2E_COMPOSE_CLEAN=0 \
E2E_COMPOSE_KEEP=0 \
E2E_COMPOSE_ENV_FILE="$env_file" \
E2E_COMPOSE_FILE="$COMPOSE_FILE" \
E2E_COMPOSE_PROJECT_NAME=tpm-test-early \
E2E_COMPOSE_REPORT_FILE="$early_report" \
E2E_FRONTEND_PORT="${ports[0]}" \
E2E_BACKEND_PORT="${ports[1]}" \
E2E_POSTGRES_PORT="${ports[2]}" \
E2E_REDIS_PORT="${ports[3]}" \
E2E_SEAWEEDFS_MASTER_PORT="${ports[4]}" \
E2E_SEAWEEDFS_S3_PORT="${ports[5]}" \
  "$RUNNER" >"$TEST_ROOT/early.out" 2>&1
early_status=$?
set -e
((early_status != 0)) || { cat "$TEST_ROOT/early.out" >&2; exit 1; }
assert_failed_report "$early_report" "E2E_COMPOSE_SYNTHETIC=1 is required"

python3 -m http.server "${ports[0]}" --bind 127.0.0.1 >"$TEST_ROOT/http.log" 2>&1 &
http_pid=$!
trap 'kill "$http_pid" 2>/dev/null || true; rm -rf -- "$TEST_ROOT"' EXIT

occupied_report="$TEST_ROOT/occupied.json"
set +e
E2E_COMPOSE_TEST_MODE=1 \
E2E_COMPOSE_SYNTHETIC=1 \
E2E_COMPOSE_CLEAN=0 \
E2E_COMPOSE_KEEP=0 \
E2E_ADMIN_PASSWORD=test-admin-password \
E2E_COMPOSE_ENV_FILE="$env_file" \
E2E_COMPOSE_FILE="$COMPOSE_FILE" \
E2E_COMPOSE_PROJECT_NAME=tpm-test-occupied \
E2E_COMPOSE_REPORT_FILE="$occupied_report" \
E2E_FRONTEND_PORT="${ports[0]}" \
E2E_BACKEND_PORT="${ports[1]}" \
E2E_POSTGRES_PORT="${ports[2]}" \
E2E_REDIS_PORT="${ports[3]}" \
E2E_SEAWEEDFS_MASTER_PORT="${ports[4]}" \
E2E_SEAWEEDFS_S3_PORT="${ports[5]}" \
  "$RUNNER" >"$TEST_ROOT/occupied.out" 2>&1
occupied_status=$?
set -e
((occupied_status != 0)) || { cat "$TEST_ROOT/occupied.out" >&2; exit 1; }
assert_failed_report "$occupied_report" "already occupied"

fake_bin="$TEST_ROOT/bin"
mkdir -m 700 "$fake_bin"
fake_log="$TEST_ROOT/docker.log"
cat > "$fake_bin/docker" <<'SH'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%s\n' "$*" >> "$FAKE_DOCKER_LOG"
case "${1:-}" in
  ps) printf 'foreign-container\n' ;;
  volume|network) ;;
  *) ;;
esac
SH
chmod 700 "$fake_bin/docker"

conflict_report="$TEST_ROOT/conflict.json"
set +e
FAKE_DOCKER_LOG="$fake_log" PATH="$fake_bin:$PATH" \
E2E_COMPOSE_TEST_MODE=1 \
E2E_COMPOSE_SYNTHETIC=1 \
E2E_COMPOSE_CLEAN=0 \
E2E_COMPOSE_KEEP=0 \
E2E_ADMIN_PASSWORD=test-admin-password \
E2E_COMPOSE_ENV_FILE="$env_file" \
E2E_COMPOSE_FILE="$COMPOSE_FILE" \
E2E_COMPOSE_PROJECT_NAME=tpm-test-conflict \
E2E_COMPOSE_REPORT_FILE="$conflict_report" \
E2E_FRONTEND_PORT="${ports[1]}" \
E2E_BACKEND_PORT="${ports[2]}" \
E2E_POSTGRES_PORT="${ports[3]}" \
E2E_REDIS_PORT="${ports[4]}" \
E2E_SEAWEEDFS_MASTER_PORT="${ports[5]}" \
E2E_SEAWEEDFS_S3_PORT=42117 \
  "$RUNNER" >"$TEST_ROOT/conflict.out" 2>&1
conflict_status=$?
set -e
((conflict_status != 0)) || { cat "$TEST_ROOT/conflict.out" >&2; exit 1; }
assert_failed_report "$conflict_report" "already owns Docker containers"
if grep -q 'down' "$fake_log"; then
  printf 'conflict case attempted cleanup:\n' >&2
  cat "$fake_log" >&2
  exit 1
fi

printf 'e2e-local-compose ownership tests: PASS\n'
