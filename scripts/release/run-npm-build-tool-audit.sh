#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
REPORT_PATH="${NPM_BUILD_TOOL_AUDIT_REPORT:-}"
LOCKED_NODE="$(command -v node)"
LOCKED_NPM="$(node -e 'process.stdout.write(require("node:fs").realpathSync(process.argv[1]))' "$(command -v npm)")"
RUNTIME_PATH="$PATH"
BROWSER_CACHE="${PLAYWRIGHT_BROWSERS_PATH:-${XDG_CACHE_HOME:-$HOME/.cache}/ms-playwright}"

fail() {
  printf 'frontend build-tool audit: %s\n' "$*" >&2
  exit 2
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --report)
      [ "$#" -ge 2 ] || fail '--report requires a path'
      REPORT_PATH="$2"
      shift 2
      ;;
    *)
      fail "unknown argument: $1"
      ;;
  esac
done

[ -x "$LOCKED_NODE" ] || fail "reviewed Node executable not found: $LOCKED_NODE"
[ -f "$LOCKED_NPM" ] || fail "reviewed npm CLI not found: $LOCKED_NPM"
command -v git >/dev/null 2>&1 || fail 'git executable not found'

run_locked_node() {
  env -i HOME=/nonexistent LANG=C LC_ALL=C NO_COLOR=1 PATH="$RUNTIME_PATH" PLAYWRIGHT_BROWSERS_PATH="$BROWSER_CACHE" \
    "$LOCKED_NODE" "$@"
}

TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/tpm-build-tool-audit.XXXXXX")"
chmod 700 "$TMP_ROOT"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

SOURCE_REVISION="$(git -C "$REPO_ROOT" rev-parse HEAD)"
SOURCE_DIRTY=false
if [ -n "$(git -C "$REPO_ROOT" status --porcelain=v1 --untracked-files=no)" ]; then
  SOURCE_DIRTY=true
fi
snapshot_sources() {
  sha256sum \
    "$REPO_ROOT/frontend/package.json" \
    "$REPO_ROOT/frontend/package-lock.json" \
    "$REPO_ROOT/frontend/scripts/configure-ci-runtime.mjs" \
    "$REPO_ROOT/backend/go.mod" \
    "$REPO_ROOT/backend/go.sum" \
    "$REPO_ROOT/security/tools/openapi-tools.policy" \
    "$REPO_ROOT/security/tools/release-tools.lock.json" \
    "$REPO_ROOT/security/semgrep/frontend.yml" \
    "$REPO_ROOT/scripts/release/validate-openapi-toolchain-trust.sh" \
    "$REPO_ROOT/scripts/release/verify-security-tools.sh" \
    "$REPO_ROOT/scripts/release/run-npm-build-tool-audit.sh" \
    | sha256sum | awk '{print $1}'
}
SOURCE_SNAPSHOT_BEFORE="$(snapshot_sources)"

set +e
env -i HOME=/nonexistent LANG=C LC_ALL=C NO_COLOR=1 PATH="$RUNTIME_PATH" PLAYWRIGHT_BROWSERS_PATH="$BROWSER_CACHE" \
  bash "$REPO_ROOT/scripts/release/verify-security-tools.sh" --scope frontend \
  >"$TMP_ROOT/security.out" 2>&1
SECURITY_STATUS=$?
env -i HOME=/nonexistent LANG=C LC_ALL=C NO_COLOR=1 PATH="$RUNTIME_PATH" PLAYWRIGHT_BROWSERS_PATH="$BROWSER_CACHE" \
  bash "$REPO_ROOT/scripts/release/validate-openapi-toolchain-trust.sh" \
  >"$TMP_ROOT/openapi.out" 2>&1
OPENAPI_STATUS=$?
set -e

SOURCE_SNAPSHOT_AFTER="$(snapshot_sources)"
SNAPSHOT_STABLE=true
if [ "$SOURCE_SNAPSHOT_BEFORE" != "$SOURCE_SNAPSHOT_AFTER" ]; then
  SNAPSHOT_STABLE=false
fi

if [ "$SECURITY_STATUS" -eq 0 ] && [ "$OPENAPI_STATUS" -eq 0 ] && [ "$SNAPSHOT_STABLE" = true ]; then
  RESULT='PASS'
  EXIT_CODE=0
else
  RESULT='FAIL'
  EXIT_CODE=1
fi

NODE_VERSION="$(run_locked_node --version)"
NPM_VERSION="$(run_locked_node "$LOCKED_NPM" --version)"
GENERATED_AT="$(run_locked_node -e 'process.stdout.write(new Date().toISOString())')"
PACKAGE_LOCK_SHA256="$(sha256sum "$REPO_ROOT/frontend/package-lock.json" | awk '{print $1}')"
POLICY_SHA256="$(sha256sum "$REPO_ROOT/security/tools/openapi-tools.policy" | awk '{print $1}')"
TOOLS_LOCK_SHA256="$(sha256sum "$REPO_ROOT/security/tools/release-tools.lock.json" | awk '{print $1}')"
PACKAGE_JSON_SHA256="$(sha256sum "$REPO_ROOT/frontend/package.json" | awk '{print $1}')"
GO_MOD_SHA256="$(sha256sum "$REPO_ROOT/backend/go.mod" | awk '{print $1}')"
GO_SUM_SHA256="$(sha256sum "$REPO_ROOT/backend/go.sum" | awk '{print $1}')"
SEMGREP_RULES_SHA256="$(sha256sum "$REPO_ROOT/security/semgrep/frontend.yml" | awk '{print $1}')"
OPENAPI_VALIDATOR_SHA256="$(sha256sum "$REPO_ROOT/scripts/release/validate-openapi-toolchain-trust.sh" | awk '{print $1}')"
SECURITY_PREFLIGHT_SHA256="$(sha256sum "$REPO_ROOT/scripts/release/verify-security-tools.sh" | awk '{print $1}')"
BUILD_WRAPPER_SHA256="$(sha256sum "$REPO_ROOT/scripts/release/run-npm-build-tool-audit.sh" | awk '{print $1}')"
SECURITY_OUTPUT_SHA256="$(sha256sum "$TMP_ROOT/security.out" | awk '{print $1}')"
OPENAPI_OUTPUT_SHA256="$(sha256sum "$TMP_ROOT/openapi.out" | awk '{print $1}')"
TOOL_VERSIONS="$(run_locked_node - "$REPO_ROOT/frontend/package-lock.json" "$REPO_ROOT/security/tools/release-tools.lock.json" <<'NODE'
const fs = require('node:fs');
const lock = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
const browsers = JSON.parse(fs.readFileSync(require('node:path').join(require('node:path').dirname(process.argv[2]), 'node_modules/playwright-core/browsers.json'), 'utf8'));
const browserVersion = browsers.browsers.find(browser => browser.name === 'chromium')?.browserVersion ?? null;
const result = {
  redocly: lock.packages?.['node_modules/@redocly/cli']?.version ?? null,
  openapi_typescript: lock.packages?.['node_modules/openapi-typescript']?.version ?? null,
  playwright: lock.packages?.['node_modules/playwright-core']?.version ?? null,
  chromium: browserVersion,
  chromium_binding: browserVersion,
};
process.stdout.write(JSON.stringify(result));
NODE
)"

REPORT_JSON="$(run_locked_node - "$SOURCE_REVISION" "$SOURCE_DIRTY" "$GENERATED_AT" "$NODE_VERSION" "$NPM_VERSION" "$PACKAGE_LOCK_SHA256" "$PACKAGE_JSON_SHA256" "$GO_MOD_SHA256" "$GO_SUM_SHA256" "$POLICY_SHA256" "$TOOLS_LOCK_SHA256" "$SEMGREP_RULES_SHA256" "$OPENAPI_VALIDATOR_SHA256" "$SECURITY_PREFLIGHT_SHA256" "$BUILD_WRAPPER_SHA256" "$SOURCE_SNAPSHOT_BEFORE" "$SOURCE_SNAPSHOT_AFTER" "$SNAPSHOT_STABLE" "$SECURITY_OUTPUT_SHA256" "$OPENAPI_OUTPUT_SHA256" "$TOOL_VERSIONS" "$RESULT" "$SECURITY_STATUS" "$OPENAPI_STATUS" <<'NODE'
const [sourceRevision, sourceDirty, generatedAt, nodeVersion, npmVersion, packageLockSha256, packageJsonSha256, goModSha256, goSumSha256, policySha256, toolsLockSha256, semgrepRulesSha256, openapiValidatorSha256, securityPreflightSha256, buildWrapperSha256, sourceSnapshotBefore, sourceSnapshotAfter, snapshotStable, securityOutputSha256, openapiOutputSha256, toolVersionsJson, result, securityStatus, openapiStatus] = process.argv.slice(2);
const toolVersions = JSON.parse(toolVersionsJson);
const report = {
  schema_version: 1,
  source_revision: sourceRevision,
  tracked_worktree_dirty: sourceDirty === "true",
  generated_at: generatedAt,
  scope: "frontend",
  result,
  exit_code: result === "PASS" ? 0 : 1,
  source_snapshot: { before: sourceSnapshotBefore, after: sourceSnapshotAfter, stable: snapshotStable === "true" },
  tools: [
    { name: "node", version: nodeVersion, identity_check: "frontend/scripts/configure-ci-runtime.mjs", exit_code: Number(securityStatus) },
    { name: "npm", version: npmVersion, identity_check: "frontend/scripts/configure-ci-runtime.mjs", exit_code: Number(securityStatus) },
    { name: "playwright", version: toolVersions.playwright, identity_check: "frontend/scripts/configure-ci-runtime.mjs", exit_code: Number(securityStatus) },
    { name: "chromium", version: toolVersions.chromium, binding_version: toolVersions.chromium_binding, identity_check: "frontend/scripts/configure-ci-runtime.mjs", exit_code: Number(securityStatus) },
    { name: "@redocly/cli", version: toolVersions.redocly, identity_check: "security/tools/openapi-tools.policy", exit_code: Number(openapiStatus) },
    { name: "openapi-typescript", version: toolVersions.openapi_typescript, identity_check: "security/tools/openapi-tools.policy", exit_code: Number(openapiStatus) },
  ],
  data_sources: [
    { path: "frontend/package.json", sha256: packageJsonSha256 },
    { path: "frontend/package-lock.json", sha256: packageLockSha256 },
    { path: "backend/go.mod", sha256: goModSha256 },
    { path: "backend/go.sum", sha256: goSumSha256 },
    { path: "security/tools/openapi-tools.policy", sha256: policySha256 },
    { path: "security/tools/release-tools.lock.json", sha256: toolsLockSha256 },
    { path: "security/semgrep/frontend.yml", sha256: semgrepRulesSha256 },
    { path: "scripts/release/validate-openapi-toolchain-trust.sh", sha256: openapiValidatorSha256 },
    { path: "scripts/release/verify-security-tools.sh", sha256: securityPreflightSha256 },
    { path: "scripts/release/run-npm-build-tool-audit.sh", sha256: buildWrapperSha256 },
  ],
  output_sha256: { security_preflight: securityOutputSha256, openapi_trust: openapiOutputSha256 },
  checks: ["locked runtime identity", "Redocly/OpenAPI toolchain trust", "lifecycle-script policy", "registry and integrity policy"],
};
process.stdout.write(JSON.stringify(report));
NODE
)"

if [ -n "$REPORT_PATH" ]; then
  mkdir -p -- "$(dirname "$REPORT_PATH")"
  printf '%s\n' "$REPORT_JSON" >"$REPORT_PATH"
  chmod 600 "$REPORT_PATH"
else
  printf '%s\n' "$REPORT_JSON"
fi

if [ "$EXIT_CODE" -ne 0 ]; then
  printf 'frontend build-tool audit: FAIL (security=%s openapi=%s)\n' "$SECURITY_STATUS" "$OPENAPI_STATUS" >&2
  exit "$EXIT_CODE"
fi

printf 'frontend build-tool audit: PASS\n' >&2
