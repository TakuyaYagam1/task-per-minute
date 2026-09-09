#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
VALIDATOR="$REPO_ROOT/scripts/release/validate-dependency-advisories.mjs"
TEST_TMP="$(mktemp -d "${TMPDIR:-/tmp}/dependency-advisory-test.XXXXXX")"
trap 'rm -rf -- "$TEST_TMP"' EXIT

FAKE_NPM="$TEST_TMP/npm"
cat >"$FAKE_NPM" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

is_production=false
for argument in "$@"; do
  if [ "$argument" = "--omit=dev" ]; then
    is_production=true
  fi
done

if [ "${AUDIT_FIXTURE:-clean}" = "production" ] || { [ "${AUDIT_FIXTURE:-clean}" = "development" ] && [ "$is_production" = false ]; }; then
  printf '%s\n' '{"vulnerabilities":{"fixture-package":{"via":[{"source":12345}],"severity":"high"}},"metadata":{"vulnerabilities":{"total":1}}}'
  exit 1
fi

printf '%s\n' '{"vulnerabilities":{},"metadata":{"vulnerabilities":{"total":0}}}'
EOF
chmod +x "$FAKE_NPM"

write_exceptions() {
  local reviewed_on="$1"
  local expires_on="$2"
  cat >"$TEST_TMP/exceptions.json" <<EOF
{"schema_version":1,"exceptions":[{"package":"fixture-package","advisory_id":12345,"scope":"development","owner":"backend-team","reviewed_by":"security-team","reason":"bounded development-only fixture","reviewed_on":"$reviewed_on","expires_on":"$expires_on"}]}
EOF
}

run_validator() {
  DEPENDENCY_AUDIT_EXCEPTIONS="$TEST_TMP/exceptions.json" \
    NPM_BIN="$FAKE_NPM" \
    AUDIT_FIXTURE="$1" \
    node "$VALIDATOR"
}

expect_reject() {
  local name="$1"
  local fixture="$2"
  if run_validator "$fixture" >"$TEST_TMP/$name.out" 2>&1; then
    printf 'FAIL: validator accepted %s\n' "$name" >&2
    return 1
  fi
  printf 'PASS: rejected %s\n' "$name"
}

printf '%s\n' '{"schema_version":1,"exceptions":[]}' >"$TEST_TMP/exceptions.json"
run_validator clean >/dev/null
printf 'PASS: accepted clean dependency graph\n'

expect_reject production-finding production
expect_reject unreviewed-development-finding development

reviewed_on="$(date -u +%Y-%m-%d)"
expires_on="$(date -u -d '+30 days' +%Y-%m-%d)"
write_exceptions "$reviewed_on" "$expires_on"
run_validator development >/dev/null
printf 'PASS: accepted current development exception\n'

expired_on="$(date -u -d '-1 day' +%Y-%m-%d)"
write_exceptions "$(date -u -d '-30 days' +%Y-%m-%d)" "$expired_on"
expect_reject expired-development-exception clean

write_exceptions "$reviewed_on" "$expires_on"
expect_reject stale-development-exception clean

printf 'dependency advisory fixture tests passed\n'
