#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
VALIDATOR="$REPO_ROOT/scripts/release/validate-dependency-advisories.mjs"
TEST_TMP="$(mktemp -d "${TMPDIR:-/tmp}/dependency-advisory-test.XXXXXX")"
trap 'rm -rf -- "$TEST_TMP"' EXIT

# Keep advisory fixtures independent of the application's dependency count.
mkdir -p "$TEST_TMP/frontend/config"
printf '%s\n' '{"lockfileVersion":3,"packages":{"":{},"node_modules/fixture-runtime":{"version":"1.0.0"},"node_modules/fixture-package":{"version":"1.0.0","dev":true}}}' >"$TEST_TMP/frontend/package-lock.json"
: >"$TEST_TMP/frontend/config/npm-empty-userconfig"

FAKE_NPM="$TEST_TMP/npm"
cat >"$FAKE_NPM" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

fixture="$(basename "$0")"
fixture="${fixture#npm-}"
is_production=false
for argument in "$@"; do
  if [ "$argument" = "--omit=dev" ]; then
    is_production=true
  fi
done

if [ "$fixture" = "wrong-format" ]; then
  printf '%s\n' '{"auditReportVersion":1,"vulnerabilities":{},"metadata":{"dependencies":{"prod":1,"dev":1,"optional":0,"peer":0,"peerOptional":0,"total":2},"vulnerabilities":{"info":0,"low":0,"moderate":0,"high":0,"critical":0,"total":0}}}'
  exit 0
fi
if [ "$fixture" = "inconsistent" ]; then
  printf '%s\n' '{"auditReportVersion":2,"vulnerabilities":{"fixture-package":{"via":[{"source":12345}],"severity":"high"}},"metadata":{"dependencies":{"prod":1,"dev":1,"optional":0,"peer":0,"peerOptional":0,"total":2},"vulnerabilities":{"info":0,"low":0,"moderate":0,"high":0,"critical":0,"total":0}}}'
  exit 0
fi

if [ "$fixture" = "production" ] || { [ "$fixture" = "development" ] && [ "$is_production" = false ]; }; then
  printf '%s\n' '{"auditReportVersion":2,"vulnerabilities":{"fixture-package":{"via":[{"source":12345}],"severity":"high"}},"metadata":{"dependencies":{"prod":1,"dev":1,"optional":0,"peer":0,"peerOptional":0,"total":2},"vulnerabilities":{"info":0,"low":0,"moderate":0,"high":1,"critical":0,"total":1}}}'
  exit 1
fi

printf '%s\n' '{"auditReportVersion":2,"vulnerabilities":{},"metadata":{"dependencies":{"prod":1,"dev":1,"optional":0,"peer":0,"peerOptional":0,"total":2},"vulnerabilities":{"info":0,"low":0,"moderate":0,"high":0,"critical":0,"total":0}}}'
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
  local fixture="$1"
  ln -sf -- "$FAKE_NPM" "$TEST_TMP/npm-$fixture"
  DEPENDENCY_AUDIT_FRONTEND_ROOT="$TEST_TMP/frontend" \
    DEPENDENCY_AUDIT_EXCEPTIONS="$TEST_TMP/exceptions.json" \
    NPM_BIN="$TEST_TMP/npm-$fixture" \
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
expect_reject wrong-audit-format wrong-format
expect_reject inconsistent-audit inconsistent

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
