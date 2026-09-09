#!/usr/bin/env bash
set -euo pipefail

BACKEND_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "$BACKEND_ROOT/.." && pwd)"
GENERATOR="$BACKEND_ROOT/scripts/openapi-generate.sh"
MERGER="$BACKEND_ROOT/scripts/merge-schemas.py"
BACKEND_MAKEFILE="$BACKEND_ROOT/Makefile"
FRONTEND_MAKEFILE="$REPO_ROOT/frontend/Makefile"
PYTHON="${PYTHON:-python3}"
export PYTHONDONTWRITEBYTECODE=1

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_policy() {
  if grep -En '(^|[[:space:]])(npx|npm[[:space:]]+exec|go[[:space:]]+run|npm[[:space:]]+(install|ci)|go[[:space:]]+install)([[:space:]]|$)' "$GENERATOR"; then
    fail 'generator contains a transient executor or implicit install'
  fi
  if grep -En 'OAPI_VERSION|REDOCLY_VERSION' "$GENERATOR"; then
    fail 'generator accepts a version override'
  fi
  grep -Fq 'GOPROXY=off' "$GENERATOR" || fail 'Go build is not offline'
  grep -Fq 'GOTOOLCHAIN=local' "$GENERATOR" || fail 'Go toolchain is not local-only'
  grep -Fq 'REDOCLY_TELEMETRY=off' "$GENERATOR" || fail 'Redocly telemetry is not disabled'
  grep -Fq -- "--output \"\$MERGED_SCHEMAS\"" "$GENERATOR" || fail 'merge destination is not explicit'
  grep -Fq "\"\$OPENAPI_TS_BIN\" \"\$BUNDLE\"" "$GENERATOR" || fail 'frontend types do not use the temporary bundle'
  grep -Fq 'gen-openapi openapi: $(QUALITY_PY_DEPS_STAMP)' "$BACKEND_MAKEFILE" || fail 'OpenAPI generation does not bootstrap pinned Python dependencies'
  grep -Fq 'test-openapi: $(QUALITY_PY_DEPS_STAMP)' "$BACKEND_MAKEFILE" || fail 'OpenAPI tests do not bootstrap pinned Python dependencies'
  grep -Fq $'\tPYTHON="$(PYTHON)" bash scripts/openapi-generate.sh' "$BACKEND_MAKEFILE" || fail 'backend Makefile bypasses the locked generator'
  grep -Fq $'\tPYTHON="$(PYTHON)" bash scripts/openapi-generate_test.sh' "$BACKEND_MAKEFILE" || fail 'backend Makefile bypasses the pinned Python interpreter for OpenAPI tests'
  grep -Fq $'\t$(NPM) run openapi:generate' "$FRONTEND_MAKEFILE" || fail 'frontend Makefile bypasses the locked generator'

  node -e '
    const p = require(process.argv[1]);
    if (p.devDependencies?.["@redocly/cli"] !== "2.51.2") process.exit(1);
    if (p.devDependencies?.["openapi-typescript"] !== "7.13.0") process.exit(1);
    if (p.scripts?.["openapi:generate"] !== "bash ../backend/scripts/openapi-generate.sh") process.exit(1);
    for (const name of ["preinstall", "install", "postinstall", "prepare"]) {
      if (p.scripts?.[name]) process.exit(1);
    }
  ' "$REPO_ROOT/frontend/package.json" || fail 'OpenAPI package identity is mutable or lifecycle scripts are present'

  grep -Fxq 'PyYAML==6.0.3' "$BACKEND_ROOT/scripts/requirements-quality.txt" ||
    fail 'PyYAML is not exactly pinned in quality requirements'

  grep -Eq '^require github\.com/oapi-codegen/oapi-codegen/v2 v2\.8\.0$' "$BACKEND_ROOT/tools/openapi/go.mod" ||
    fail 'oapi-codegen module identity is not exact'
  grep -Fq '_ "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen"' "$BACKEND_ROOT/tools/openapi/tools.go" ||
    fail 'tools.go does not pin the oapi-codegen command'

  if ! "$PYTHON" -c 'import yaml' >/dev/null 2>&1; then
    fail "PyYAML is required for merge-schemas.py tests: $PYTHON"
  fi

  local cli_fixture cli_merger deterministic_source deterministic_first deterministic_second duplicate_source empty_source empty_output
    cli_fixture="$(mktemp -d "${TMPDIR:-/tmp}/openapi-merge-cli.XXXXXX")"
    cli_merger="$cli_fixture/scripts/merge-schemas.py"
    mkdir -p "$cli_fixture/scripts" "$cli_fixture/api/components/schemas"
    cp "$MERGER" "$cli_merger"
    printf '%s\n' 'Fixture:' '  type: object' >"$cli_fixture/api/components/schemas/fixture.yml"
    if "$PYTHON" "$cli_merger" >/dev/null 2>&1; then
      rm -rf -- "$cli_fixture"
      fail 'schema merger accepted implicit source and output paths'
    fi
    if "$PYTHON" "$cli_merger" --source-dir "$cli_fixture/api/components/schemas" >/dev/null 2>&1; then
      rm -rf -- "$cli_fixture"
      fail 'schema merger accepted an implicit output path'
    fi
    if "$PYTHON" "$cli_merger" --output "$cli_fixture/schemas.yml" >/dev/null 2>&1; then
      rm -rf -- "$cli_fixture"
      fail 'schema merger accepted an implicit source path'
    fi
    rm -rf -- "$cli_fixture"

    deterministic_source="$(mktemp -d "${TMPDIR:-/tmp}/openapi-deterministic-schemas.XXXXXX")"
    deterministic_first="$deterministic_source/first.yml"
    deterministic_second="$deterministic_source/second.yml"
    mkdir -p "$deterministic_source/schemas"
    printf '%s\n' 'Zeta:' '  type: string' >"$deterministic_source/schemas/zeta.yml"
    printf '%s\n' 'Alpha:' '  type: object' '  properties:' '    id:' '      type: integer' >"$deterministic_source/schemas/alpha.yaml"
    "$PYTHON" "$MERGER" --source-dir "$deterministic_source/schemas" --output "$deterministic_first" >/dev/null
    "$PYTHON" "$MERGER" --source-dir "$deterministic_source/schemas" --output "$deterministic_second" >/dev/null
    cmp -s "$deterministic_first" "$deterministic_second" || {
      rm -rf -- "$deterministic_source"
      fail 'schema merger output is not byte-repeatable'
    }
    rm -rf -- "$deterministic_source"

    duplicate_source="$(mktemp -d "${TMPDIR:-/tmp}/openapi-duplicate-schemas.XXXXXX")"
    printf '%s\n' 'Fixture:' '  type: string' 'Fixture:' '  type: object' >"$duplicate_source/duplicate.yml"
    if "$PYTHON" "$MERGER" --source-dir "$duplicate_source" --output "$duplicate_source/schemas.yml" >"$duplicate_source/duplicate.out" 2>&1; then
      rm -rf -- "$duplicate_source"
      fail 'schema merger accepted duplicate YAML keys'
    fi
    grep -Fq "duplicate key 'Fixture'" "$duplicate_source/duplicate.out" || {
      rm -rf -- "$duplicate_source"
      fail 'schema merger did not clearly report a duplicate YAML key'
    }
    rm -rf -- "$duplicate_source"

    empty_source="$(mktemp -d "${TMPDIR:-/tmp}/openapi-empty-schemas.XXXXXX")"
    empty_output="$empty_source/schemas.yml"
    if "$PYTHON" "$MERGER" --source-dir "$empty_source" --output "$empty_output" >/dev/null 2>&1; then
      rm -rf -- "$empty_source"
      fail 'schema merger accepted an empty source directory'
    fi
    [ ! -e "$empty_output" ] || {
      rm -rf -- "$empty_source"
      fail 'schema merger emitted an empty repository substitute'
    }
    rm -rf -- "$empty_source"

  printf 'openapi generator policy tests passed\n'
}

if [ "${1:-}" = "--policy" ]; then
  assert_policy
  exit 0
fi
if [ "$#" -ne 0 ]; then
  fail 'usage: openapi-generate_test.sh [--policy]'
fi

assert_policy >/dev/null

TEST_TMP="$(mktemp -d "${TMPDIR:-/tmp}/openapi-generate-test.XXXXXX")"
trap 'rm -rf -- "$TEST_TMP"' EXIT
FIXTURE="$TEST_TMP/work"
FIXTURE_BACKEND="$FIXTURE/backend"
FIXTURE_FRONTEND="$FIXTURE/frontend"
FAKE_BIN="$TEST_TMP/bin"
TRACE="$TEST_TMP/trace.log"
mkdir -p \
  "$FIXTURE_BACKEND/scripts" \
  "$FIXTURE_BACKEND/codegen" \
  "$FIXTURE_BACKEND/api/components/schemas" \
  "$FIXTURE_BACKEND/internal/adapter/inbound/http/api" \
  "$FIXTURE_BACKEND/tools/openapi" \
  "$FIXTURE_FRONTEND/lib/shared/api" \
  "$FIXTURE_FRONTEND/node_modules/.bin" \
  "$FIXTURE_FRONTEND/node_modules/@redocly/cli/bin" \
  "$FIXTURE_FRONTEND/node_modules/openapi-typescript/bin" \
  "$FAKE_BIN" \
  "$TEST_TMP/runtime"

cp "$GENERATOR" "$FIXTURE_BACKEND/scripts/openapi-generate.sh"
cp "$BACKEND_ROOT/scripts/merge-schemas.py" "$FIXTURE_BACKEND/scripts/merge-schemas.py"
cp "$BACKEND_ROOT/scripts/requirements-quality.txt" "$FIXTURE_BACKEND/scripts/requirements-quality.txt"
cp "$BACKEND_MAKEFILE" "$FIXTURE_BACKEND/Makefile"
cp "$BACKEND_ROOT/tools/openapi/go.mod" "$FIXTURE_BACKEND/tools/openapi/go.mod"
cp "$BACKEND_ROOT/tools/openapi/go.sum" "$FIXTURE_BACKEND/tools/openapi/go.sum"
cp "$BACKEND_ROOT/tools/openapi/tools.go" "$FIXTURE_BACKEND/tools/openapi/tools.go"
cp "$BACKEND_ROOT"/codegen/oapi-codegen-*.yml "$FIXTURE_BACKEND/codegen/"

cat >"$FIXTURE_BACKEND/scripts/openapi-generate_test.sh" <<'TEST_OPENAPI'
#!/usr/bin/env bash
set -euo pipefail
: "${OPENAPI_TEST_TRACE:?}"
printf 'test-openapi PYTHON=%s\n' "${PYTHON:-}" >>"$OPENAPI_TEST_TRACE"
"$PYTHON" -c 'import yaml'
TEST_OPENAPI

printf '%s\n' 'openapi: 3.0.3' 'info: {title: Fixture, version: 1.0.0}' 'paths: {}' 'components:' '  schemas:' "    \$ref: ./components/schemas.yml" >"$FIXTURE_BACKEND/api/openapi.yml"
printf '%s\n' 'Fixture:' '  type: object' >"$FIXTURE_BACKEND/api/components/schemas/fixture.yml"
printf '%s\n' '{"name":"fixture","private":true,"devDependencies":{"@redocly/cli":"2.51.2","openapi-typescript":"7.13.0"}}' >"$FIXTURE_FRONTEND/package.json"
printf '%s\n' '{"name":"fixture","lockfileVersion":3,"packages":{"":{"devDependencies":{"@redocly/cli":"2.51.2","openapi-typescript":"7.13.0"}},"node_modules/@redocly/cli":{"version":"2.51.2","resolved":"https://registry.npmjs.org/@redocly/cli/-/cli-2.51.2.tgz","integrity":"sha512-pviW1gfsjCAuIVutmQcihlhlgoivfNzisBIR61EwSSREHGZ08Sbtjp/h/HPDkVavWwdzR/gs2wgHrdXLd6qU1A==","license":"MIT","bin":{"redocly":"bin/cli.js"}},"node_modules/openapi-typescript":{"version":"7.13.0","resolved":"https://registry.npmjs.org/openapi-typescript/-/openapi-typescript-7.13.0.tgz","integrity":"sha512-EFP392gcqXS7ntPvbhBzbF8TyBA+baIYEm791Hy5YkjDYKTnk/Tn5OQeKm5BIZvJihpp8Zzr4hzx0Irde1LNGQ==","license":"MIT","bin":{"openapi-typescript":"bin/cli.js"}}}}' >"$FIXTURE_FRONTEND/package-lock.json"
printf '%s\n' '{"name":"@redocly/cli","version":"2.51.2","bin":{"redocly":"bin/cli.js"}}' >"$FIXTURE_FRONTEND/node_modules/@redocly/cli/package.json"
printf '%s\n' '{"name":"openapi-typescript","version":"7.13.0","bin":{"openapi-typescript":"bin/cli.js"}}' >"$FIXTURE_FRONTEND/node_modules/openapi-typescript/package.json"

cat >"$FIXTURE_FRONTEND/node_modules/@redocly/cli/bin/cli.js" <<'REDOCLY'
#!/usr/bin/env bash
set -euo pipefail
: "${OPENAPI_TEST_TRACE:?}"
[ "${REDOCLY_TELEMETRY:-}" = off ] || exit 61
[ "${NO_UPDATE_NOTIFIER:-}" = 1 ] || exit 62
printf 'redocly %s\n' "$*" >>"$OPENAPI_TEST_TRACE"
[ "${OPENAPI_TEST_FAIL_REDOCLY:-0}" != 1 ] || exit 63
command_name="${1:-}"
shift || true
if [ "$command_name" = lint ]; then
  exit 0
fi
[ "$command_name" = bundle ] || exit 64
output=''
while [ "$#" -gt 0 ]; do
  if [ "$1" = '-o' ]; then
    shift
    output="$1"
  fi
  shift
done
[ -n "$output" ] || exit 64
printf '%s\n' 'openapi: 3.0.3' 'paths: {}' >"$output"
REDOCLY
chmod +x "$FIXTURE_FRONTEND/node_modules/@redocly/cli/bin/cli.js"
ln -s ../@redocly/cli/bin/cli.js "$FIXTURE_FRONTEND/node_modules/.bin/redocly"

cat >"$FIXTURE_FRONTEND/node_modules/openapi-typescript/bin/cli.js" <<'OPENAPI_TYPESCRIPT'
#!/usr/bin/env bash
set -euo pipefail
: "${OPENAPI_TEST_TRACE:?}"
printf 'openapi-typescript %s\n' "$*" >>"$OPENAPI_TEST_TRACE"
[ "${OPENAPI_TEST_FAIL_OPENAPI_TYPESCRIPT:-0}" != 1 ] || exit 66
output=''
while [ "$#" -gt 0 ]; do
  if [ "$1" = '-o' ]; then
    shift
    output="$1"
  fi
  shift
done
[ -n "$output" ] || exit 65
mkdir -p -- "$(dirname "$output")"
printf '%s\n' '// fixture frontend API types' >"$output"
OPENAPI_TYPESCRIPT
chmod +x "$FIXTURE_FRONTEND/node_modules/openapi-typescript/bin/cli.js"
ln -s ../openapi-typescript/bin/cli.js "$FIXTURE_FRONTEND/node_modules/.bin/openapi-typescript"

cat >"$FAKE_BIN/go" <<'GO'
#!/usr/bin/env bash
set -euo pipefail
: "${OPENAPI_TEST_TRACE:?}"
[ "${GOTOOLCHAIN:-}" = local ] || exit 71
[ "${GOPROXY:-}" = off ] || exit 72
[ "${GOSUMDB:-}" = off ] || exit 73
printf 'go %s\n' "$*" >>"$OPENAPI_TEST_TRACE"
if [ "${1:-}" = mod ] && [ "${2:-}" = verify ]; then
  exit 0
fi
output=''
while [ "$#" -gt 0 ]; do
  if [ "$1" = '-o' ]; then
    shift
    output="$1"
  fi
  shift
done
[ -n "$output" ] || exit 74
cat >"$output" <<'OAPI'
#!/usr/bin/env bash
set -euo pipefail
: "${OPENAPI_TEST_TRACE:?}"
printf 'oapi-codegen %s\n' "$*" >>"$OPENAPI_TEST_TRACE"
if [ "${1:-}" != '-config' ] || [ -z "${2:-}" ]; then
  exit 75
fi
if [ "${OPENAPI_TEST_FAIL_OAPI_CONFIG:-}" = "$(basename "$2")" ]; then
  exit 77
fi
generated_output="$(sed -n 's/^output:[[:space:]]*//p' "$2")"
[ -n "$generated_output" ] || exit 76
mkdir -p -- "$(dirname "$generated_output")"
printf '%s\n' "// fixture output ${OPENAPI_TEST_OUTPUT_TOKEN:-stable} from $(basename "$2")" 'package api' >"$generated_output"
OAPI
chmod +x "$output"
GO
chmod +x "$FAKE_BIN/go"

cat >"$FAKE_BIN/python3" <<'PYTHON'
#!/usr/bin/env bash
set -euo pipefail
: "${OPENAPI_TEST_TRACE:?}"
printf 'python %s\n' "$*" >>"$OPENAPI_TEST_TRACE"
if [ "${1:-}" = '-c' ]; then
  exit 0
fi
if [ "${1:-}" = '-m' ] && [ "${2:-}" = 'pip' ]; then
  exit 0
fi
output=''
while [ "$#" -gt 0 ]; do
  if [ "$1" = '--output' ]; then
    shift
    output="$1"
  fi
  shift
done
[ -n "$output" ] || exit 81
printf '%s\n' '# fixture schema merge' '{}' >"$output"
PYTHON
chmod +x "$FAKE_BIN/python3"
cp "$FAKE_BIN/python3" "$TEST_TMP/selected-python"

cat >"$TEST_TMP/bootstrap-python" <<'PYTHON'
#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" != '-m' ] || [ "${2:-}" != 'venv' ] || [ -z "${3:-}" ]; then
  exit 88
fi
mkdir -p "$3/bin"
cp "$OPENAPI_TEST_SELECTED_PYTHON" "$3/bin/python"
chmod +x "$3/bin/python"
PYTHON
chmod +x "$TEST_TMP/bootstrap-python"

cat >"$FAKE_BIN/python3" <<'PYTHON'
#!/usr/bin/env bash
exit 87
PYTHON
chmod +x "$FAKE_BIN/python3"

cp "$FIXTURE_FRONTEND/package-lock.json" "$TEST_TMP/package-lock.clean.json"
cp "$FIXTURE_BACKEND/tools/openapi/go.mod" "$TEST_TMP/go.mod.clean"
cp "$FIXTURE_BACKEND/tools/openapi/go.sum" "$TEST_TMP/go.sum.clean"
cp "$FIXTURE_BACKEND/tools/openapi/tools.go" "$TEST_TMP/tools.go.clean"

run_fixture_generator() {
  PYTHON="$TEST_TMP/selected-python" \
    PATH="$FAKE_BIN:$PATH" \
    TMPDIR="$TEST_TMP/runtime" \
    OPENAPI_TEST_TRACE="$TRACE" \
    OPENAPI_TEST_OUTPUT_TOKEN="${OPENAPI_TEST_OUTPUT_TOKEN:-stable}" \
    OPENAPI_TEST_FAIL_OAPI_CONFIG="${OPENAPI_TEST_FAIL_OAPI_CONFIG:-}" \
    OPENAPI_TEST_FAIL_OPENAPI_TYPESCRIPT="${OPENAPI_TEST_FAIL_OPENAPI_TYPESCRIPT:-0}" \
    bash "$FIXTURE_BACKEND/scripts/openapi-generate.sh"
}

run_fixture_make() {
  env -u PYTHON \
    OPENAPI_TEST_SELECTED_PYTHON="$TEST_TMP/selected-python" \
    PATH="$FAKE_BIN:$PATH" \
    TMPDIR="$TEST_TMP/runtime" \
    OPENAPI_TEST_TRACE="$TRACE" \
    make -C "$FIXTURE_BACKEND" "$@"
}

expect_fixture_rejection() {
  local name="$1"
  if run_fixture_generator >"$TEST_TMP/$name.out" 2>&1; then
    fail "generator accepted $name"
  fi
}

if run_fixture_make test-openapi BOOTSTRAP_PYTHON="$TEST_TMP/missing-bootstrap-python" >"$TEST_TMP/missing-bootstrap.out" 2>&1; then
  fail 'Make accepted a missing bootstrap Python interpreter'
fi
grep -Fq "quality Python bootstrap interpreter not found: $TEST_TMP/missing-bootstrap-python" "$TEST_TMP/missing-bootstrap.out" ||
  fail 'Make did not clearly report a missing bootstrap Python interpreter'

run_fixture_make gen-openapi BOOTSTRAP_PYTHON="$TEST_TMP/bootstrap-python" >/dev/null
[ -f "$FIXTURE_BACKEND/.venv-quality/.deps.stamp" ] || fail 'Make did not record installed Python dependencies'
grep -Fq 'python -m pip install' "$TRACE" || fail 'Make did not bootstrap pinned Python dependencies'

run_fixture_make test-openapi >/dev/null
grep -Fq "test-openapi PYTHON=$FIXTURE_BACKEND/.venv-quality/bin/python" "$TRACE" ||
  fail 'Make did not pass the quality Python interpreter to OpenAPI tests'

printf '%s\n' keep >"$TEST_TMP/runtime/sentinel"
OAPI_VERSION=v9.9.9 \
REDOCLY_VERSION=9.9.9 \
run_fixture_generator >/dev/null

[ -f "$TEST_TMP/runtime/sentinel" ] || fail 'generator removed an unowned temp file'
if find "$TEST_TMP/runtime" -mindepth 1 ! -name sentinel -print -quit | grep -q .; then
  fail 'generator left its temp directory behind'
fi
[ ! -e "$FIXTURE_BACKEND/api/components/schemas.yml" ] || fail 'generator created a repository intermediate'
grep -Fq 'go mod verify' "$TRACE" || fail 'generator did not verify cached Go modules'
grep -Fq 'go build -mod=readonly' "$TRACE" || fail 'generator did not build the pinned command read-only'
grep -Fq 'python -c import yaml' "$TRACE" || fail 'generator did not use the selected Python interpreter'
grep -Fq 'redocly bundle' "$TRACE" || fail 'generator did not use local Redocly'
grep -Fq 'redocly lint' "$TRACE" || fail 'generator did not lint the bundled contract'
grep -Fq 'oapi-codegen -config' "$TRACE" || fail 'generator did not invoke the pinned binary'
grep -Fq 'openapi-typescript' "$TRACE" || fail 'generator did not invoke locked frontend type generation'
[ -s "$FIXTURE_FRONTEND/lib/shared/api/schema.ts" ] || fail 'generator did not create frontend API types'

find "$FIXTURE_BACKEND/internal/adapter/inbound/http/api" "$FIXTURE_FRONTEND/lib/shared/api/schema.ts" -type f -print0 |
  sort -z |
  xargs -0 sha256sum >"$TEST_TMP/generated-first.sha256"
run_fixture_generator >/dev/null
find "$FIXTURE_BACKEND/internal/adapter/inbound/http/api" "$FIXTURE_FRONTEND/lib/shared/api/schema.ts" -type f -print0 |
  sort -z |
  xargs -0 sha256sum >"$TEST_TMP/generated-second.sha256"
cmp -s "$TEST_TMP/generated-first.sha256" "$TEST_TMP/generated-second.sha256" ||
  fail 'generator output is not byte-repeatable'

OPENAPI_TEST_OUTPUT_TOKEN=failed-second \
OPENAPI_TEST_FAIL_OAPI_CONFIG=oapi-codegen-spec.yml \
expect_fixture_rejection 'a failing second Go generator'
find "$FIXTURE_BACKEND/internal/adapter/inbound/http/api" "$FIXTURE_FRONTEND/lib/shared/api/schema.ts" -type f -print0 |
  sort -z |
  xargs -0 sha256sum >"$TEST_TMP/generated-after-second-failure.sha256"
cmp -s "$TEST_TMP/generated-first.sha256" "$TEST_TMP/generated-after-second-failure.sha256" ||
  fail 'a failed Go generator partially replaced generated outputs'

OPENAPI_TEST_OUTPUT_TOKEN=failed-typescript \
OPENAPI_TEST_FAIL_OPENAPI_TYPESCRIPT=1 \
expect_fixture_rejection 'a failing TypeScript generator'
find "$FIXTURE_BACKEND/internal/adapter/inbound/http/api" "$FIXTURE_FRONTEND/lib/shared/api/schema.ts" -type f -print0 |
  sort -z |
  xargs -0 sha256sum >"$TEST_TMP/generated-after-typescript-failure.sha256"
cmp -s "$TEST_TMP/generated-first.sha256" "$TEST_TMP/generated-after-typescript-failure.sha256" ||
  fail 'a failed TypeScript generator partially replaced generated outputs'

FAIL_COPY_BIN="$TEST_TMP/fail-copy-bin"
mkdir -p "$FAIL_COPY_BIN"
cat >"$FAIL_COPY_BIN/cp" <<FAIL_COPY
#!/usr/bin/env bash
set -euo pipefail
destination="\${!#}"
if [ "\$destination" = "$FIXTURE_FRONTEND/lib/shared/api/schema.ts" ] && [ ! -e "$TEST_TMP/fail-copy.once" ]; then
  : >"$TEST_TMP/fail-copy.once"
  exit 97
fi
exec "$(command -v cp)" "\$@"
FAIL_COPY
chmod +x "$FAIL_COPY_BIN/cp"

PATH="$FAIL_COPY_BIN:$FAKE_BIN:$PATH" \
OPENAPI_TEST_OUTPUT_TOKEN=failed-publication \
expect_fixture_rejection 'a late generated artifact publication failure'
find "$FIXTURE_BACKEND/internal/adapter/inbound/http/api" "$FIXTURE_FRONTEND/lib/shared/api/schema.ts" -type f -print0 |
  sort -z |
  xargs -0 sha256sum >"$TEST_TMP/generated-after-publication-failure.sha256"
cmp -s "$TEST_TMP/generated-first.sha256" "$TEST_TMP/generated-after-publication-failure.sha256" ||
  fail 'a failed generated artifact publication partially replaced generated outputs'

rm -f -- "$FIXTURE_FRONTEND/lib/shared/api/schema.ts" "$TEST_TMP/fail-copy.once"
find "$FIXTURE_BACKEND/internal/adapter/inbound/http/api" -type f -print0 |
  sort -z |
  xargs -0 sha256sum >"$TEST_TMP/generated-without-typescript.sha256"
PATH="$FAIL_COPY_BIN:$FAKE_BIN:$PATH" \
OPENAPI_TEST_OUTPUT_TOKEN=failed-publication-without-typescript \
expect_fixture_rejection 'a late publication failure with no prior TypeScript artifact'
[ ! -e "$FIXTURE_FRONTEND/lib/shared/api/schema.ts" ] ||
  fail 'a failed generated artifact publication created a missing pre-run TypeScript artifact'
find "$FIXTURE_BACKEND/internal/adapter/inbound/http/api" -type f -print0 |
  sort -z |
  xargs -0 sha256sum >"$TEST_TMP/generated-after-missing-typescript-failure.sha256"
cmp -s "$TEST_TMP/generated-without-typescript.sha256" "$TEST_TMP/generated-after-missing-typescript-failure.sha256" ||
  fail 'a failed generated artifact publication changed pre-run Go artifacts'

node - "$FIXTURE_FRONTEND/package-lock.json" <<'NODE'
const fs = require('node:fs');
const path = process.argv[2];
const lock = JSON.parse(fs.readFileSync(path, 'utf8'));
lock.packages['node_modules/@redocly/cli'].resolved = 'https://example.invalid/cli-2.51.2.tgz';
fs.writeFileSync(path, `${JSON.stringify(lock)}\n`);
NODE
expect_fixture_rejection 'a tampered Redocly source'
cp "$TEST_TMP/package-lock.clean.json" "$FIXTURE_FRONTEND/package-lock.json"

node - "$FIXTURE_FRONTEND/package-lock.json" <<'NODE'
const fs = require('node:fs');
const path = process.argv[2];
const lock = JSON.parse(fs.readFileSync(path, 'utf8'));
lock.packages['node_modules/openapi-typescript'].integrity = 'sha512-tampered';
fs.writeFileSync(path, `${JSON.stringify(lock)}\n`);
NODE
expect_fixture_rejection 'a tampered openapi-typescript integrity'
cp "$TEST_TMP/package-lock.clean.json" "$FIXTURE_FRONTEND/package-lock.json"

node - "$FIXTURE_BACKEND/tools/openapi/go.sum" <<'NODE'
const fs = require('node:fs');
const path = process.argv[2];
const source = fs.readFileSync(path, 'utf8');
const target = 'github.com/oapi-codegen/oapi-codegen/v2 v2.8.0 h1:s4hxMxuqtR8jPzXkBTtFwY/SBuj3gEAYikmbBSdtLMM=';
if (!source.includes(target)) process.exit(1);
fs.writeFileSync(path, source.replace(target, `${target}tampered`));
NODE
expect_fixture_rejection 'a tampered oapi-codegen checksum'
cp "$TEST_TMP/go.sum.clean" "$FIXTURE_BACKEND/tools/openapi/go.sum"

node - "$FIXTURE_BACKEND/tools/openapi/tools.go" <<'NODE'
const fs = require('node:fs');
const path = process.argv[2];
const source = fs.readFileSync(path, 'utf8');
const target = 'github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen';
if (!source.includes(target)) process.exit(1);
fs.writeFileSync(path, source.replace(target, 'example.invalid/oapi-codegen'));
NODE
expect_fixture_rejection 'an unexpected oapi-codegen command'
cp "$TEST_TMP/tools.go.clean" "$FIXTURE_BACKEND/tools/openapi/tools.go"

mv "$FIXTURE_FRONTEND/node_modules/.bin/redocly" "$TEST_TMP/redocly-link"
expect_fixture_rejection 'a missing local Redocly binary'
mv "$TEST_TMP/redocly-link" "$FIXTURE_FRONTEND/node_modules/.bin/redocly"

mv "$FIXTURE_FRONTEND/node_modules/.bin/openapi-typescript" "$TEST_TMP/openapi-typescript-link"
expect_fixture_rejection 'a missing local openapi-typescript binary'
mv "$TEST_TMP/openapi-typescript-link" "$FIXTURE_FRONTEND/node_modules/.bin/openapi-typescript"

NO_PYTHON_BIN="$TEST_TMP/no-python"
mkdir -p "$NO_PYTHON_BIN"
ln -s "$(command -v dirname)" "$NO_PYTHON_BIN/dirname"
if PYTHON= PATH="$NO_PYTHON_BIN" bash "$FIXTURE_BACKEND/scripts/openapi-generate.sh" >"$TEST_TMP/missing-python.out" 2>&1; then
  fail 'generator accepted a missing Python interpreter'
fi

cat >"$FAKE_BIN/python3" <<'PYTHON'
#!/usr/bin/env bash
exit 1
PYTHON
chmod +x "$FAKE_BIN/python3"
if PYTHON="$FAKE_BIN/python3" PATH="$FAKE_BIN:$PATH" bash "$FIXTURE_BACKEND/scripts/openapi-generate.sh" >"$TEST_TMP/missing-pyyaml.out" 2>&1; then
  fail 'generator accepted a missing PyYAML module'
fi
cp "$TEST_TMP/selected-python" "$FAKE_BIN/python3"

if PATH="$FAKE_BIN:$PATH" TMPDIR="$TEST_TMP/runtime" OPENAPI_TEST_TRACE="$TRACE" OPENAPI_TEST_FAIL_REDOCLY=1 bash "$FIXTURE_BACKEND/scripts/openapi-generate.sh" >"$TEST_TMP/failing-redocly.out" 2>&1; then
  fail 'generator hid a Redocly failure'
fi
[ -f "$TEST_TMP/runtime/sentinel" ] || fail 'failure cleanup removed an unowned temp file'
if find "$TEST_TMP/runtime" -mindepth 1 ! -name sentinel -print -quit | grep -q .; then
  fail 'failure cleanup left its temp directory behind'
fi

printf 'openapi generator fixture tests passed\n'
