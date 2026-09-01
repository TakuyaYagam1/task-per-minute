#!/usr/bin/env bash
set -euo pipefail

BACKEND_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "$BACKEND_ROOT/.." && pwd)"
FRONTEND_ROOT="$REPO_ROOT/frontend"
TOOLS_ROOT="$BACKEND_ROOT/tools/openapi"

fail() {
  printf 'openapi-generate.sh: %s\n' "$*" >&2
  exit 1
}

if [ "$#" -ne 0 ]; then
  fail 'this command does not accept arguments or version overrides'
fi

for command_name in python3 node go; do
  command -v "$command_name" >/dev/null 2>&1 || fail "required command not found: $command_name"
done
python3 -c 'import yaml' >/dev/null 2>&1 || fail 'PyYAML is required for schema merging'

REDOCLY_BIN="$FRONTEND_ROOT/node_modules/.bin/redocly"
REDOCLY_ENTRY="$FRONTEND_ROOT/node_modules/@redocly/cli/bin/cli.js"
OPENAPI_TS_BIN="$FRONTEND_ROOT/node_modules/.bin/openapi-typescript"
OPENAPI_TS_ENTRY="$FRONTEND_ROOT/node_modules/openapi-typescript/bin/cli.js"
[ -x "$REDOCLY_BIN" ] || fail "local Redocly binary not found: $REDOCLY_BIN"
[ -f "$REDOCLY_ENTRY" ] || fail "local Redocly entrypoint not found: $REDOCLY_ENTRY"
[ "$(readlink -f "$REDOCLY_BIN")" = "$(readlink -f "$REDOCLY_ENTRY")" ] || fail 'local Redocly binary points outside the locked package'
[ -x "$OPENAPI_TS_BIN" ] || fail "local openapi-typescript binary not found: $OPENAPI_TS_BIN"
[ -f "$OPENAPI_TS_ENTRY" ] || fail "local openapi-typescript entrypoint not found: $OPENAPI_TS_ENTRY"
[ "$(readlink -f "$OPENAPI_TS_BIN")" = "$(readlink -f "$OPENAPI_TS_ENTRY")" ] || fail 'local openapi-typescript binary points outside the locked package'

if ! node - "$FRONTEND_ROOT/package.json" "$FRONTEND_ROOT/package-lock.json" "$FRONTEND_ROOT/node_modules/@redocly/cli/package.json" "$FRONTEND_ROOT/node_modules/openapi-typescript/package.json" <<'NODE'
const fs = require('node:fs');
const packageJson = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
const packageLock = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const redocly = JSON.parse(fs.readFileSync(process.argv[4], 'utf8'));
const openapiTypescript = JSON.parse(fs.readFileSync(process.argv[5], 'utf8'));
if (packageJson.devDependencies?.['@redocly/cli'] !== '1.34.0') process.exit(1);
if (packageLock.packages?.['']?.devDependencies?.['@redocly/cli'] !== '1.34.0') process.exit(1);
if (packageLock.packages?.['node_modules/@redocly/cli']?.version !== '1.34.0') process.exit(1);
if (redocly.name !== '@redocly/cli' || redocly.version !== '1.34.0') process.exit(1);
if (redocly.bin?.redocly !== 'bin/cli.js') process.exit(1);
const declaredOpenapiTypescript = packageJson.devDependencies?.['openapi-typescript'];
const lockedOpenapiTypescript = packageLock.packages?.['node_modules/openapi-typescript'];
if (!declaredOpenapiTypescript || packageLock.packages?.['']?.devDependencies?.['openapi-typescript'] !== declaredOpenapiTypescript) process.exit(1);
if (!lockedOpenapiTypescript || openapiTypescript.name !== 'openapi-typescript' || openapiTypescript.version !== lockedOpenapiTypescript.version) process.exit(1);
if (openapiTypescript.bin?.['openapi-typescript'] !== 'bin/cli.js') process.exit(1);
NODE
then
  fail 'local OpenAPI tools do not match the frontend lock'
fi

for tool_file in go.mod go.sum tools.go; do
  [ -f "$TOOLS_ROOT/$tool_file" ] || fail "pinned tools module file not found: $TOOLS_ROOT/$tool_file"
done
grep -Fxq 'require github.com/oapi-codegen/oapi-codegen/v2 v2.5.1' "$TOOLS_ROOT/go.mod" || fail 'oapi-codegen v2.5.1 is not pinned in the tools module'

TMP_BASE="${TMPDIR:-/tmp}"
[ -d "$TMP_BASE" ] && [ -w "$TMP_BASE" ] || fail "temporary directory is not writable: $TMP_BASE"
TMP_BASE="$(cd "$TMP_BASE" && pwd -P)"
WORK_DIR="$(mktemp -d "$TMP_BASE/openapi-generate.XXXXXX")"

cleanup() {
  case "$WORK_DIR" in
    "$TMP_BASE"/openapi-generate.*) rm -rf -- "$WORK_DIR" ;;
    *) printf 'openapi-generate.sh: refusing unsafe cleanup path: %s\n' "$WORK_DIR" >&2 ;;
  esac
}
trap cleanup EXIT

cp -R -- "$BACKEND_ROOT/api" "$WORK_DIR/api"
MERGED_SCHEMAS="$WORK_DIR/api/components/schemas.yml"
BUNDLE="$WORK_DIR/openapi.bundle.yml"
OAPI_BIN="$WORK_DIR/oapi-codegen"

python3 "$BACKEND_ROOT/scripts/merge-schemas.py" \
  --source-dir "$BACKEND_ROOT/api/components/schemas" \
  --output "$MERGED_SCHEMAS"

if grep -R -En "\\\$ref:[[:space:]].*https?://" "$WORK_DIR/api" >/dev/null; then
  fail 'remote OpenAPI references are forbidden in offline generation'
fi

(
  cd "$TOOLS_ROOT"
  GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOWORK=off go mod verify
  GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOWORK=off \
    go build -mod=readonly -trimpath -o "$OAPI_BIN" \
    github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen
)

printf 'openapi-generate.sh: bundling local OpenAPI sources\n'
REDOCLY_TELEMETRY=off NO_UPDATE_NOTIFIER=1 \
  "$REDOCLY_BIN" bundle "$WORK_DIR/api/openapi.yml" -o "$BUNDLE" --ext yml >/dev/null

shopt -s nullglob
configs=("$BACKEND_ROOT"/codegen/oapi-codegen-*.yml "$BACKEND_ROOT"/codegen/oapi-codegen-*.yaml)
shopt -u nullglob
[ "${#configs[@]}" -gt 0 ] || fail 'no codegen/oapi-codegen-*.yml configs found'

cd "$BACKEND_ROOT"
for cfg in "${configs[@]}"; do
  printf 'openapi-generate.sh: generating with %s\n' "${cfg#"$BACKEND_ROOT/"}"
  "$OAPI_BIN" -config "$cfg" "$BUNDLE"
done

printf 'openapi-generate.sh: generating frontend OpenAPI types\n'
"$OPENAPI_TS_BIN" "$BUNDLE" -o "$FRONTEND_ROOT/lib/shared/api/schema.ts"
