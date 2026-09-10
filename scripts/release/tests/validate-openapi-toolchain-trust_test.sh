#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
VALIDATOR="$REPO_ROOT/scripts/release/validate-openapi-toolchain-trust.sh"
POLICY="$REPO_ROOT/docs/engineering/openapi-toolchain-trust.md"
PACKAGE_JSON="$REPO_ROOT/frontend/package.json"
PACKAGE_LOCK="$REPO_ROOT/frontend/package-lock.json"
NPM_USERCONFIG="$REPO_ROOT/frontend/config/npm-empty-userconfig"
GO_MOD="$REPO_ROOT/backend/tools/openapi/go.mod"
GO_SUM="$REPO_ROOT/backend/tools/openapi/go.sum"
TOOLS_GO="$REPO_ROOT/backend/tools/openapi/tools.go"

TEST_TMP="$(mktemp -d "${TMPDIR:-/tmp}/openapi-trust-test.XXXXXX")"
trap 'rm -rf -- "$TEST_TMP"' EXIT

run_validator() {
  bash "$VALIDATOR" \
    --policy "$1/policy.md" \
    --package-json "$1/package.json" \
    --package-lock "$1/package-lock.json" \
    --npm-userconfig "$1/npm-empty-userconfig" \
    --go-mod "$1/go.mod" \
    --go-sum "$1/go.sum" \
    --tools-go "$1/tools.go"
}

make_fixture() {
  local name="$1"
  local fixture="$TEST_TMP/$name"
  mkdir -p "$fixture"
  cp "$POLICY" "$fixture/policy.md"
  cp "$PACKAGE_JSON" "$fixture/package.json"
  cp "$PACKAGE_LOCK" "$fixture/package-lock.json"
  cp "$NPM_USERCONFIG" "$fixture/npm-empty-userconfig"
  cp "$GO_MOD" "$fixture/go.mod"
  cp "$GO_SUM" "$fixture/go.sum"
  cp "$TOOLS_GO" "$fixture/tools.go"
  printf '%s\n' "$fixture"
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

if [ ! -x "$VALIDATOR" ]; then
  printf 'FAIL: validator is missing or not executable: %s\n' "$VALIDATOR" >&2
  exit 1
fi

baseline="$(make_fixture baseline)"
run_validator "$baseline" >/dev/null
printf 'PASS: accepted locked toolchain\n'

fixture="$(make_fixture unofficial-source)"
sed -i 's#redocly.repository=https://github.com/Redocly/redocly-cli#redocly.repository=https://example.invalid/redocly-cli#' "$fixture/policy.md"
expect_reject unofficial-source "$fixture"

fixture="$(make_fixture missing-license)"
sed -i 's/^oapi.license=.*/oapi.license=/' "$fixture/policy.md"
expect_reject missing-license "$fixture"

fixture="$(make_fixture stale-security-evidence)"
sed -i 's/^redocly.security_evidence_date=.*/redocly.security_evidence_date=2020-01-01/' "$fixture/policy.md"
expect_reject stale-security-evidence "$fixture"

fixture="$(make_fixture missing-scorecard)"
sed -i 's#^redocly.scorecard_url=.*#redocly.scorecard_url=#' "$fixture/policy.md"
sed -i 's/^redocly.scorecard_justification=.*/redocly.scorecard_justification=/' "$fixture/policy.md"
expect_reject missing-scorecard "$fixture"

fixture="$(make_fixture wrong-lock-identity)"
sed -i '0,/"@redocly\/cli": "2.51.2"/s//"@redocly\/cli": "2.51.3"/' "$fixture/package-lock.json"
expect_reject wrong-lock-identity "$fixture"

fixture="$(make_fixture checksum-mismatch)"
sed -i '0,/"integrity": "sha512-[^"]*"/s//"integrity": "sha512-INVALID"/' "$fixture/package-lock.json"
expect_reject checksum-mismatch "$fixture"

fixture="$(make_fixture missing-yaml-compatibility)"
sed -i 's/"yaml": "2.9.0"/"yaml": "2.8.4"/' "$fixture/package.json"
expect_reject missing-yaml-compatibility "$fixture"

fixture="$(make_fixture missing-js-yaml-patch)"
sed -i 's/"js-yaml": "4.3.2"/"js-yaml": "4.3.1"/' "$fixture/package.json"
expect_reject missing-js-yaml-patch "$fixture"

fixture="$(make_fixture js-yaml-checksum-mismatch)"
node - "$fixture/package-lock.json" <<'NODE'
const fs = require('node:fs');
const path = process.argv[2];
const lock = JSON.parse(fs.readFileSync(path, 'utf8'));
lock.packages['node_modules/js-yaml'].integrity = 'sha512-INVALID';
fs.writeFileSync(path, `${JSON.stringify(lock, null, 2)}\n`);
NODE
expect_reject js-yaml-checksum-mismatch "$fixture"

fixture="$(make_fixture wrong-openapi-typescript-tarball)"
sed -i 's#openapi-typescript/-/openapi-typescript-7.13.0.tgz#openapi-typescript/-/openapi-typescript-7.12.0.tgz#' "$fixture/package-lock.json"
expect_reject wrong-openapi-typescript-tarball "$fixture"

fixture="$(make_fixture advisory-gate-bypass)"
sed -i 's# && bash ../scripts/release/validate-openapi-toolchain-trust.sh##' "$fixture/package.json"
expect_reject advisory-gate-bypass "$fixture"

fixture="$(make_fixture unnamed-trust-decision)"
sed -i 's/^oapi.trust_decision=.*/oapi.trust_decision=/' "$fixture/policy.md"
expect_reject unnamed-trust-decision "$fixture"

printf 'openapi toolchain trust fixture tests passed\n'
