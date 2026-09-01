#!/usr/bin/env bash
set -euo pipefail

BACKEND_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "$BACKEND_ROOT/.." && pwd)"
GENERATOR="$BACKEND_ROOT/scripts/openapi-generate.sh"
MERGER="$BACKEND_ROOT/scripts/merge-schemas.py"
BACKEND_MAKEFILE="$BACKEND_ROOT/Makefile"
FRONTEND_MAKEFILE="$REPO_ROOT/frontend/Makefile"

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
  grep -Fq $'\tbash scripts/openapi-generate.sh' "$BACKEND_MAKEFILE" || fail 'backend Makefile bypasses the locked generator'
  grep -Fq $'\t$(NPM) run openapi:generate' "$FRONTEND_MAKEFILE" || fail 'frontend Makefile bypasses the locked generator'

  node -e '
    const p = require(process.argv[1]);
    if (p.devDependencies?.["@redocly/cli"] !== "1.34.0") process.exit(1);
    if (p.scripts?.["openapi:generate"] !== "bash ../backend/scripts/openapi-generate.sh") process.exit(1);
    for (const name of ["preinstall", "install", "postinstall", "prepare"]) {
      if (p.scripts?.[name]) process.exit(1);
    }
  ' "$REPO_ROOT/frontend/package.json" || fail 'Redocly package identity is mutable or lifecycle scripts are present'

  grep -Eq '^require github\.com/oapi-codegen/oapi-codegen/v2 v2\.5\.1$' "$BACKEND_ROOT/tools/openapi/go.mod" ||
    fail 'oapi-codegen module identity is not exact'
  grep -Fq '_ "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen"' "$BACKEND_ROOT/tools/openapi/tools.go" ||
    fail 'tools.go does not pin the oapi-codegen command'

  if python3 -c 'import yaml' >/dev/null 2>&1; then
    local empty_source empty_output
    empty_source="$(mktemp -d "${TMPDIR:-/tmp}/openapi-empty-schemas.XXXXXX")"
    empty_output="$empty_source/schemas.yml"
    if python3 "$MERGER" --source-dir "$empty_source" --output "$empty_output" >/dev/null 2>&1; then
      rm -rf -- "$empty_source"
      fail 'schema merger accepted an empty source directory'
    fi
    [ ! -e "$empty_output" ] || {
      rm -rf -- "$empty_source"
      fail 'schema merger emitted an empty repository substitute'
    }
    rm -rf -- "$empty_source"
  fi

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
  "$FIXTURE_BACKEND/tools/openapi" \
  "$FIXTURE_FRONTEND/node_modules/.bin" \
  "$FIXTURE_FRONTEND/node_modules/@redocly/cli/bin" \
  "$FIXTURE_FRONTEND/node_modules/openapi-typescript/bin" \
  "$FAKE_BIN" \
  "$TEST_TMP/runtime"

cp "$GENERATOR" "$FIXTURE_BACKEND/scripts/openapi-generate.sh"
cp "$BACKEND_ROOT/scripts/merge-schemas.py" "$FIXTURE_BACKEND/scripts/merge-schemas.py"
cp "$BACKEND_ROOT/tools/openapi/go.mod" "$FIXTURE_BACKEND/tools/openapi/go.mod"
cp "$BACKEND_ROOT/tools/openapi/go.sum" "$FIXTURE_BACKEND/tools/openapi/go.sum"
cp "$BACKEND_ROOT/tools/openapi/tools.go" "$FIXTURE_BACKEND/tools/openapi/tools.go"
cp "$BACKEND_ROOT"/codegen/oapi-codegen-*.yml "$FIXTURE_BACKEND/codegen/"

printf '%s\n' 'openapi: 3.0.3' 'info: {title: Fixture, version: 1.0.0}' 'paths: {}' 'components:' '  schemas:' "    \$ref: ./components/schemas.yml" >"$FIXTURE_BACKEND/api/openapi.yml"
printf '%s\n' 'Fixture:' '  type: object' >"$FIXTURE_BACKEND/api/components/schemas/fixture.yml"
printf '%s\n' '{"name":"fixture","private":true,"devDependencies":{"@redocly/cli":"1.34.0","openapi-typescript":"^7.13.0"}}' >"$FIXTURE_FRONTEND/package.json"
printf '%s\n' '{"name":"fixture","lockfileVersion":3,"packages":{"":{"devDependencies":{"@redocly/cli":"1.34.0","openapi-typescript":"^7.13.0"}},"node_modules/@redocly/cli":{"version":"1.34.0"},"node_modules/openapi-typescript":{"version":"7.13.0"}}}' >"$FIXTURE_FRONTEND/package-lock.json"
printf '%s\n' '{"name":"@redocly/cli","version":"1.34.0","bin":{"redocly":"bin/cli.js"}}' >"$FIXTURE_FRONTEND/node_modules/@redocly/cli/package.json"
printf '%s\n' '{"name":"openapi-typescript","version":"7.13.0","bin":{"openapi-typescript":"bin/cli.js"}}' >"$FIXTURE_FRONTEND/node_modules/openapi-typescript/package.json"

cat >"$FIXTURE_FRONTEND/node_modules/@redocly/cli/bin/cli.js" <<'REDOCLY'
#!/usr/bin/env bash
set -euo pipefail
: "${OPENAPI_TEST_TRACE:?}"
[ "${REDOCLY_TELEMETRY:-}" = off ] || exit 61
[ "${NO_UPDATE_NOTIFIER:-}" = 1 ] || exit 62
printf 'redocly %s\n' "$*" >>"$OPENAPI_TEST_TRACE"
[ "${OPENAPI_TEST_FAIL_REDOCLY:-0}" != 1 ] || exit 63
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
OAPI
chmod +x "$output"
GO
chmod +x "$FAKE_BIN/go"

cat >"$FAKE_BIN/python3" <<'PYTHON'
#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" = '-c' ]; then
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
cp "$FAKE_BIN/python3" "$TEST_TMP/python3-ok"

printf '%s\n' keep >"$TEST_TMP/runtime/sentinel"
PATH="$FAKE_BIN:$PATH" \
TMPDIR="$TEST_TMP/runtime" \
OPENAPI_TEST_TRACE="$TRACE" \
OAPI_VERSION=v9.9.9 \
REDOCLY_VERSION=9.9.9 \
bash "$FIXTURE_BACKEND/scripts/openapi-generate.sh" >/dev/null

[ -f "$TEST_TMP/runtime/sentinel" ] || fail 'generator removed an unowned temp file'
if find "$TEST_TMP/runtime" -mindepth 1 ! -name sentinel -print -quit | grep -q .; then
  fail 'generator left its temp directory behind'
fi
[ ! -e "$FIXTURE_BACKEND/api/components/schemas.yml" ] || fail 'generator created a repository intermediate'
grep -Fq 'go mod verify' "$TRACE" || fail 'generator did not verify cached Go modules'
grep -Fq 'go build -mod=readonly' "$TRACE" || fail 'generator did not build the pinned command read-only'
grep -Fq 'redocly bundle' "$TRACE" || fail 'generator did not use local Redocly'
grep -Fq 'oapi-codegen -config' "$TRACE" || fail 'generator did not invoke the pinned binary'
grep -Fq 'openapi-typescript' "$TRACE" || fail 'generator did not invoke locked frontend type generation'
[ -s "$FIXTURE_FRONTEND/lib/shared/api/schema.ts" ] || fail 'generator did not create frontend API types'

mv "$FIXTURE_FRONTEND/node_modules/.bin/redocly" "$TEST_TMP/redocly-link"
if PATH="$FAKE_BIN:$PATH" TMPDIR="$TEST_TMP/runtime" OPENAPI_TEST_TRACE="$TRACE" bash "$FIXTURE_BACKEND/scripts/openapi-generate.sh" >"$TEST_TMP/missing-redocly.out" 2>&1; then
  fail 'generator accepted a missing local Redocly binary'
fi
mv "$TEST_TMP/redocly-link" "$FIXTURE_FRONTEND/node_modules/.bin/redocly"

mv "$FIXTURE_FRONTEND/node_modules/.bin/openapi-typescript" "$TEST_TMP/openapi-typescript-link"
if PATH="$FAKE_BIN:$PATH" TMPDIR="$TEST_TMP/runtime" OPENAPI_TEST_TRACE="$TRACE" bash "$FIXTURE_BACKEND/scripts/openapi-generate.sh" >"$TEST_TMP/missing-openapi-typescript.out" 2>&1; then
  fail 'generator accepted a missing local openapi-typescript binary'
fi
mv "$TEST_TMP/openapi-typescript-link" "$FIXTURE_FRONTEND/node_modules/.bin/openapi-typescript"

NO_PYTHON_BIN="$TEST_TMP/no-python"
mkdir -p "$NO_PYTHON_BIN"
ln -s "$(command -v dirname)" "$NO_PYTHON_BIN/dirname"
if PATH="$NO_PYTHON_BIN" bash "$FIXTURE_BACKEND/scripts/openapi-generate.sh" >"$TEST_TMP/missing-python.out" 2>&1; then
  fail 'generator accepted a missing Python interpreter'
fi

cat >"$FAKE_BIN/python3" <<'PYTHON'
#!/usr/bin/env bash
exit 1
PYTHON
chmod +x "$FAKE_BIN/python3"
if PATH="$FAKE_BIN:$PATH" bash "$FIXTURE_BACKEND/scripts/openapi-generate.sh" >"$TEST_TMP/missing-pyyaml.out" 2>&1; then
  fail 'generator accepted a missing PyYAML module'
fi
cp "$TEST_TMP/python3-ok" "$FAKE_BIN/python3"

if PATH="$FAKE_BIN:$PATH" TMPDIR="$TEST_TMP/runtime" OPENAPI_TEST_TRACE="$TRACE" OPENAPI_TEST_FAIL_REDOCLY=1 bash "$FIXTURE_BACKEND/scripts/openapi-generate.sh" >"$TEST_TMP/failing-redocly.out" 2>&1; then
  fail 'generator hid a Redocly failure'
fi
[ -f "$TEST_TMP/runtime/sentinel" ] || fail 'failure cleanup removed an unowned temp file'
if find "$TEST_TMP/runtime" -mindepth 1 ! -name sentinel -print -quit | grep -q .; then
  fail 'failure cleanup left its temp directory behind'
fi

printf 'openapi generator fixture tests passed\n'
