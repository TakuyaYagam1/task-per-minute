#!/usr/bin/env bash
set -euo pipefail

BACKEND_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "$BACKEND_ROOT/.." && pwd)"
FRONTEND_ROOT="$REPO_ROOT/frontend"
TOOLS_ROOT="$BACKEND_ROOT/tools/openapi"
PYTHON="${PYTHON:-python3}"

fail() {
  printf 'openapi-generate.sh: %s\n' "$*" >&2
  exit 1
}

if [ "$#" -ne 0 ]; then
  fail 'this command does not accept arguments or version overrides'
fi

if ! command -v "$PYTHON" >/dev/null 2>&1; then
  fail "required Python interpreter not found: $PYTHON"
fi
for command_name in node go; do
  command -v "$command_name" >/dev/null 2>&1 || fail "required command not found: $command_name"
done
"$PYTHON" -c 'import yaml' >/dev/null 2>&1 || fail 'PyYAML is required for schema merging'

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

if ! node - "$FRONTEND_ROOT/package.json" "$FRONTEND_ROOT/package-lock.json" "$FRONTEND_ROOT/node_modules/@redocly/cli/package.json" "$FRONTEND_ROOT/node_modules/openapi-typescript/package.json" "$FRONTEND_ROOT/node_modules/js-yaml/package.json" <<'NODE'
const fs = require('node:fs');

function fail(message) {
  process.stderr.write(`openapi-generate.sh: ${message}\n`);
  process.exit(1);
}

const packageJson = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
const packageLock = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const installedPackages = [
  JSON.parse(fs.readFileSync(process.argv[4], 'utf8')),
  JSON.parse(fs.readFileSync(process.argv[5], 'utf8')),
  JSON.parse(fs.readFileSync(process.argv[6], 'utf8')),
];
const requiredPackages = [
  {
    name: '@redocly/cli',
    version: '2.51.2',
    resolved: 'https://registry.npmjs.org/@redocly/cli/-/cli-2.51.2.tgz',
    integrity: 'sha512-pviW1gfsjCAuIVutmQcihlhlgoivfNzisBIR61EwSSREHGZ08Sbtjp/h/HPDkVavWwdzR/gs2wgHrdXLd6qU1A==',
    license: 'MIT',
    bin: 'redocly',
    entrypoint: 'bin/cli.js',
  },
  {
    name: 'openapi-typescript',
    version: '7.13.0',
    resolved: 'https://registry.npmjs.org/openapi-typescript/-/openapi-typescript-7.13.0.tgz',
    integrity: 'sha512-EFP392gcqXS7ntPvbhBzbF8TyBA+baIYEm791Hy5YkjDYKTnk/Tn5OQeKm5BIZvJihpp8Zzr4hzx0Irde1LNGQ==',
    license: 'MIT',
    bin: 'openapi-typescript',
    entrypoint: 'bin/cli.js',
  },
  {
    name: 'js-yaml',
    version: '4.3.2',
    resolved: 'https://registry.npmjs.org/js-yaml/-/js-yaml-4.3.2.tgz',
    integrity: 'sha512-SFNOvSJ+Dgf/9An904Yx+CgSlIPCkIpao4qo51lpee25TIRejdH3rhR4EZMGoNx3/TP3O+wzWuiTFl4sqbltzA==',
    license: 'MIT',
    override: true,
  },
];

for (const [index, required] of requiredPackages.entries()) {
  const locked = packageLock.packages?.[`node_modules/${required.name}`];
  const installed = installedPackages[index];

  if (required.override) {
    if (packageJson.overrides?.[required.name] !== required.version) {
      fail(`${required.name} must be exactly overridden to ${required.version}`);
    }
  } else {
    const declared = packageJson.devDependencies?.[required.name];
    const rootLock = packageLock.packages?.['']?.devDependencies?.[required.name];
    if (declared !== required.version || rootLock !== required.version) {
      fail(`${required.name} must be exactly pinned to ${required.version}`);
    }
  }
  if (!locked || locked.version !== required.version || locked.resolved !== required.resolved) {
    fail(`${required.name} lock source does not match the approved package`);
  }
  if (locked.integrity !== required.integrity || locked.license !== required.license) {
    fail(`${required.name} lock integrity or license does not match the approved package`);
  }
  if (locked.hasInstallScript || (required.bin && locked.bin?.[required.bin] !== required.entrypoint)) {
    fail(`${required.name} lock binary identity is not approved`);
  }
  if (installed.name !== required.name || installed.version !== required.version) {
    fail(`${required.name} installed package identity does not match the lock`);
  }
  if (required.bin && installed.bin?.[required.bin] !== required.entrypoint) {
    fail(`${required.name} installed binary identity does not match the lock`);
  }
}
NODE
then
  fail 'local OpenAPI tools do not match the frontend lock'
fi

for tool_file in go.mod go.sum tools.go; do
  [ -f "$TOOLS_ROOT/$tool_file" ] || fail "pinned tools module file not found: $TOOLS_ROOT/$tool_file"
done
grep -Fxq 'module task-per-minute/tools/openapi' "$TOOLS_ROOT/go.mod" || fail 'unexpected OpenAPI tools module identity'
grep -Fxq 'go 1.25.0' "$TOOLS_ROOT/go.mod" || fail 'unexpected OpenAPI tools module Go version'
grep -Fxq 'require github.com/oapi-codegen/oapi-codegen/v2 v2.8.0' "$TOOLS_ROOT/go.mod" || fail 'oapi-codegen v2.8.0 is not pinned in the tools module'
if grep -Eq '^(replace|exclude|retract|toolchain)[[:space:]]' "$TOOLS_ROOT/go.mod"; then
  fail 'OpenAPI tools module contains a source or version override'
fi
grep -Fxq 'github.com/oapi-codegen/oapi-codegen/v2 v2.8.0 h1:s4hxMxuqtR8jPzXkBTtFwY/SBuj3gEAYikmbBSdtLMM=' "$TOOLS_ROOT/go.sum" ||
  fail 'oapi-codegen v2.8.0 module checksum does not match the approved tool'
grep -Fxq 'github.com/oapi-codegen/oapi-codegen/v2 v2.8.0/go.mod h1:yae2TI9IYB5vxQ35gFrpXh9L5H1eJv4MAUK1jumGMTo=' "$TOOLS_ROOT/go.sum" ||
  fail 'oapi-codegen v2.8.0 go.mod checksum does not match the approved tool'
grep -Fxq 'import _ "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen"' "$TOOLS_ROOT/tools.go" ||
  fail 'oapi-codegen command identity is not pinned in tools.go'

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

"$PYTHON" "$BACKEND_ROOT/scripts/merge-schemas.py" \
  --source-dir "$WORK_DIR/api/components/schemas" \
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

printf 'openapi-generate.sh: linting bundled OpenAPI contract\n'
REDOCLY_TELEMETRY=off NO_UPDATE_NOTIFIER=1 \
  "$REDOCLY_BIN" lint "$BUNDLE"

shopt -s nullglob
configs=("$BACKEND_ROOT"/codegen/oapi-codegen-*.yml "$BACKEND_ROOT"/codegen/oapi-codegen-*.yaml)
shopt -u nullglob
[ "${#configs[@]}" -gt 0 ] || fail 'no codegen/oapi-codegen-*.yml configs found'

STAGED_CONFIG_ROOT="$WORK_DIR/codegen"
STAGED_BACKEND_ROOT="$WORK_DIR/backend"
STAGED_FRONTEND_ROOT="$WORK_DIR/frontend"
declare -a staged_go_outputs=()
declare -a target_go_outputs=()

for cfg in "${configs[@]}"; do
  target_output="$(sed -n 's/^output:[[:space:]]*//p' "$cfg")"
  [ -n "$target_output" ] || fail "OpenAPI config has no output path: ${cfg#"$BACKEND_ROOT/"}"
  case "$target_output" in
    internal/adapter/inbound/http/api/*.gen.go) ;;
    *) fail "OpenAPI config has an unapproved output path: $target_output" ;;
  esac

  staged_output="$STAGED_BACKEND_ROOT/$target_output"
  staged_config="$STAGED_CONFIG_ROOT/${cfg##*/}"
  mkdir -p -- "$(dirname "$staged_output")" "$STAGED_CONFIG_ROOT"
  sed "s|^output:[[:space:]]*.*$|output: $staged_output|" "$cfg" >"$staged_config"

  printf 'openapi-generate.sh: generating with %s\n' "${cfg#"$BACKEND_ROOT/"}"
  "$OAPI_BIN" -config "$staged_config" "$BUNDLE"
  [ -s "$staged_output" ] || fail "oapi-codegen produced no output for ${cfg#"$BACKEND_ROOT/"}"
  staged_go_outputs+=("$staged_output")
  target_go_outputs+=("$target_output")
done

STAGED_TYPESCRIPT_OUTPUT="$STAGED_FRONTEND_ROOT/lib/shared/api/schema.ts"
mkdir -p -- "$(dirname "$STAGED_TYPESCRIPT_OUTPUT")"
printf 'openapi-generate.sh: generating frontend OpenAPI types\n'
"$OPENAPI_TS_BIN" "$BUNDLE" --default-non-nullable false -o "$STAGED_TYPESCRIPT_OUTPUT"
[ -s "$STAGED_TYPESCRIPT_OUTPUT" ] || fail 'openapi-typescript produced no output'

command -v gofmt >/dev/null 2>&1 || fail 'required command not found: gofmt'
for staged_output in "${staged_go_outputs[@]}"; do
  gofmt -d "$staged_output" >/dev/null || fail "generated Go output is invalid: $staged_output"
done

declare -a staged_outputs=("${staged_go_outputs[@]}" "$STAGED_TYPESCRIPT_OUTPUT")
declare -a target_outputs=()
for target_output in "${target_go_outputs[@]}"; do
  target_outputs+=("$BACKEND_ROOT/$target_output")
done
target_outputs+=("$FRONTEND_ROOT/lib/shared/api/schema.ts")
[ "${#staged_outputs[@]}" -eq "${#target_outputs[@]}" ] || fail 'generated artifact staging is incomplete'

PUBLICATION_BACKUP_ROOT="$WORK_DIR/publication-backups"
declare -a publication_backups=()
declare -a publication_had_original=()
mkdir -p -- "$PUBLICATION_BACKUP_ROOT"

for index in "${!target_outputs[@]}"; do
  target_output="${target_outputs[$index]}"
  [ ! -L "$target_output" ] || fail "generated artifact target must not be a symlink: $target_output"
  publication_backups+=("$PUBLICATION_BACKUP_ROOT/$index")
  if [ -e "$target_output" ]; then
    [ -f "$target_output" ] || fail "generated artifact target is not a regular file: $target_output"
    cp -- "$target_output" "${publication_backups[$index]}" ||
      fail "could not back up generated artifact before publication: $target_output"
    publication_had_original+=(1)
  else
    publication_had_original+=(0)
  fi
done

restore_published_artifacts() {
  local index target_output restore_failed=0
  for index in "${!target_outputs[@]}"; do
    target_output="${target_outputs[$index]}"
    if [ "${publication_had_original[$index]}" = 1 ]; then
      if ! cp -- "${publication_backups[$index]}" "$target_output"; then
        printf 'openapi-generate.sh: could not restore generated artifact: %s\n' "$target_output" >&2
        restore_failed=1
      fi
    elif ! rm -f -- "$target_output"; then
      printf 'openapi-generate.sh: could not remove newly created generated artifact: %s\n' "$target_output" >&2
      restore_failed=1
    fi
  done
  return "$restore_failed"
}

printf 'openapi-generate.sh: replacing generated API artifacts\n'
publication_failed=0
for index in "${!staged_outputs[@]}"; do
  if ! cp -- "${staged_outputs[$index]}" "${target_outputs[$index]}"; then
    publication_failed=1
    break
  fi
done

if [ "$publication_failed" -ne 0 ]; then
  if ! restore_published_artifacts; then
    fail 'generated artifact publication failed and rollback was incomplete'
  fi
  fail 'generated artifact publication failed; pre-run artifacts were restored'
fi
