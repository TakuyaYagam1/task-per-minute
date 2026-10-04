#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
SOURCE_FRONTEND_ROOT="$REPO_ROOT/frontend"
REPORT_PATH="${NPM_AUDIT_REPORT:-}"
LOCKED_NODE="$(command -v node)"
LOCKED_NPM="$(node -e 'process.stdout.write(require("node:fs").realpathSync(process.argv[1]))' "$(command -v npm)")"
RUNTIME_PATH="$PATH"
BROWSER_CACHE="${PLAYWRIGHT_BROWSERS_PATH:-${XDG_CACHE_HOME:-$HOME/.cache}/ms-playwright}"

fail() {
  printf 'npm dependency audit: %s\n' "$*" >&2
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

[ -d "$SOURCE_FRONTEND_ROOT" ] || fail "frontend source root not found: $SOURCE_FRONTEND_ROOT"
[ -f "$SOURCE_FRONTEND_ROOT/package.json" ] || fail 'frontend package.json is missing'
[ -f "$SOURCE_FRONTEND_ROOT/package-lock.json" ] || fail 'frontend package-lock.json is missing'
[ -f "$SOURCE_FRONTEND_ROOT/config/npm-audit-exceptions.json" ] || fail 'audit exception policy is missing'
[ -f "$SOURCE_FRONTEND_ROOT/config/npm-empty-userconfig" ] || fail 'credential-free npm userconfig is missing'
[ -x "$LOCKED_NODE" ] || fail "reviewed Node executable not found: $LOCKED_NODE"
[ -f "$LOCKED_NPM" ] || fail "reviewed npm CLI not found: $LOCKED_NPM"
command -v git >/dev/null 2>&1 || fail 'git executable not found'

run_locked_node() {
  env -i HOME=/nonexistent LANG=C LC_ALL=C NO_COLOR=1 PATH="$RUNTIME_PATH" PLAYWRIGHT_BROWSERS_PATH="$BROWSER_CACHE" \
    "$LOCKED_NODE" "$@"
}

TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/tpm-npm-audit.XXXXXX")"
chmod 700 "$TMP_ROOT"
trap 'rm -rf -- "$TMP_ROOT"' EXIT
STAGED_FRONTEND_ROOT="$TMP_ROOT/frontend"
mkdir -p "$STAGED_FRONTEND_ROOT/config"
cp -- "$SOURCE_FRONTEND_ROOT/package.json" "$STAGED_FRONTEND_ROOT/package.json"
cp -- "$SOURCE_FRONTEND_ROOT/package-lock.json" "$STAGED_FRONTEND_ROOT/package-lock.json"
cp -- "$SOURCE_FRONTEND_ROOT/config/npm-audit-exceptions.json" "$STAGED_FRONTEND_ROOT/config/npm-audit-exceptions.json"
cp -- "$SOURCE_FRONTEND_ROOT/config/npm-empty-userconfig" "$STAGED_FRONTEND_ROOT/config/npm-empty-userconfig"

SOURCE_REVISION="$(git -C "$REPO_ROOT" rev-parse HEAD)"
SOURCE_DIRTY=false
if [ -n "$(git -C "$REPO_ROOT" status --porcelain=v1 --untracked-files=no)" ]; then
  SOURCE_DIRTY=true
fi
NODE_VERSION="$(run_locked_node --version)"
NPM_VERSION="$(run_locked_node "$LOCKED_NPM" --version)"
GENERATED_AT="$(run_locked_node -e 'process.stdout.write(new Date().toISOString())')"
LOCK_SHA256="$(sha256sum "$STAGED_FRONTEND_ROOT/package-lock.json" | awk '{print $1}')"
PACKAGE_SHA256="$(sha256sum "$STAGED_FRONTEND_ROOT/package.json" | awk '{print $1}')"
EXCEPTIONS_SHA256="$(sha256sum "$STAGED_FRONTEND_ROOT/config/npm-audit-exceptions.json" | awk '{print $1}')"
VALIDATOR_SCRIPT_SHA256="$(sha256sum "$REPO_ROOT/scripts/release/validate-dependency-advisories.mjs" | awk '{print $1}')"
AUDIT_WRAPPER_SHA256="$(sha256sum "$REPO_ROOT/scripts/release/run-npm-audit.sh" | awk '{print $1}')"

set +e
env -i HOME=/nonexistent LANG=C LC_ALL=C NO_COLOR=1 PATH="$RUNTIME_PATH" PLAYWRIGHT_BROWSERS_PATH="$BROWSER_CACHE" \
  bash "$REPO_ROOT/scripts/release/verify-security-tools.sh" --scope frontend \
  >"$TMP_ROOT/security-preflight.out" 2>&1
SECURITY_STATUS=$?
set -e

VALIDATION_STATUS=1
: >"$TMP_ROOT/validator.out"
: >"$TMP_ROOT/validator.err"
if [ "$SECURITY_STATUS" -eq 0 ]; then
  set +e
  env -i HOME=/nonexistent LANG=C LC_ALL=C NO_COLOR=1 PATH="$RUNTIME_PATH" PLAYWRIGHT_BROWSERS_PATH="$BROWSER_CACHE" \
    DEPENDENCY_AUDIT_FRONTEND_ROOT="$STAGED_FRONTEND_ROOT" \
    DEPENDENCY_AUDIT_EXCEPTIONS="$STAGED_FRONTEND_ROOT/config/npm-audit-exceptions.json" \
    DEPENDENCY_AUDIT_REPORT_DIR="$TMP_ROOT" \
    NPM_NODE="$LOCKED_NODE" \
    NPM_CLI="$LOCKED_NPM" \
    AUDIT_RUNTIME_PATH="$RUNTIME_PATH" \
    "$LOCKED_NODE" "$REPO_ROOT/scripts/release/validate-dependency-advisories.mjs" \
    >"$TMP_ROOT/validator.out" 2>"$TMP_ROOT/validator.err"
  VALIDATION_STATUS=$?
  set -e
fi

if [ "$VALIDATION_STATUS" -eq 0 ]; then
  RESULT='PASS'
else
  RESULT='FAIL'
fi

hash_or_missing() {
  if [ -f "$1" ]; then
    sha256sum "$1" | awk '{print $1}'
  else
    printf 'missing'
  fi
}
PRODUCTION_AUDIT_SHA256="$(hash_or_missing "$TMP_ROOT/production.json")"
COMPLETE_AUDIT_SHA256="$(hash_or_missing "$TMP_ROOT/complete.json")"
VALIDATOR_OUTPUT_SHA256="$(cat "$TMP_ROOT/validator.out" "$TMP_ROOT/validator.err" | sha256sum | awk '{print $1}')"
RAW_PRODUCTION_PATH=""
RAW_COMPLETE_PATH=""
if [ -n "$REPORT_PATH" ]; then
  mkdir -p -- "$(dirname "$REPORT_PATH")"
  REPORT_STEM="$(printf '%s' "$REPORT_PATH" | sed 's/\.json$//')"
  RAW_PRODUCTION_PATH="$REPORT_STEM.production.json"
  RAW_COMPLETE_PATH="$REPORT_STEM.complete.json"
  if [ -f "$TMP_ROOT/production.json" ]; then
    cp -- "$TMP_ROOT/production.json" "$RAW_PRODUCTION_PATH"
    chmod 600 "$RAW_PRODUCTION_PATH"
  fi
  if [ -f "$TMP_ROOT/complete.json" ]; then
    cp -- "$TMP_ROOT/complete.json" "$RAW_COMPLETE_PATH"
    chmod 600 "$RAW_COMPLETE_PATH"
  fi
fi
REPORT_JSON="$(run_locked_node - "$SOURCE_REVISION" "$SOURCE_DIRTY" "$GENERATED_AT" "$NODE_VERSION" "$NPM_VERSION" "$LOCK_SHA256" "$PACKAGE_SHA256" "$EXCEPTIONS_SHA256" "$VALIDATOR_SCRIPT_SHA256" "$AUDIT_WRAPPER_SHA256" "$PRODUCTION_AUDIT_SHA256" "$COMPLETE_AUDIT_SHA256" "$VALIDATOR_OUTPUT_SHA256" "$RAW_PRODUCTION_PATH" "$RAW_COMPLETE_PATH" "$RESULT" "$VALIDATION_STATUS" "$SECURITY_STATUS" <<'NODE'
const [sourceRevision, sourceDirty, generatedAt, nodeVersion, npmVersion, lockSha256, packageSha256, exceptionsSha256, validatorScriptSha256, auditWrapperSha256, productionAuditSha256, completeAuditSha256, validatorOutputSha256, rawProductionPath, rawCompletePath, result, exitCode, securityExitCode] = process.argv.slice(2);
const report = {
  schema_version: 1,
  source_revision: sourceRevision,
  tracked_worktree_dirty: sourceDirty === "true",
  generated_at: generatedAt,
  scope: "frontend",
  result,
  exit_code: Number(exitCode),
  tools: [{ name: "node", version: nodeVersion }, { name: "npm", version: npmVersion }],
  data_sources: [
    { kind: "registry", url: "https://registry.npmjs.org/" },
    { path: "frontend/package.json", sha256: packageSha256 },
    { path: "frontend/package-lock.json", sha256: lockSha256 },
    { path: "frontend/config/npm-audit-exceptions.json", sha256: exceptionsSha256 },
    { path: "scripts/release/validate-dependency-advisories.mjs", sha256: validatorScriptSha256 },
    { path: "scripts/release/run-npm-audit.sh", sha256: auditWrapperSha256 },
  ],
  manifests: { package_json_sha256: packageSha256, package_lock_sha256: lockSha256 },
  audit_outputs_sha256: { production: productionAuditSha256, complete: completeAuditSha256 },
  validator_output_sha256: validatorOutputSha256,
  audit_reports: {
    production: { path: rawProductionPath || null, sha256: productionAuditSha256 },
    complete: { path: rawCompletePath || null, sha256: completeAuditSha256 },
  },
  security_preflight_exit_code: Number(securityExitCode),
  checks: ["production dependencies (--omit=dev)", "complete lockfile", "reviewed development exceptions"],
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

if [ "$VALIDATION_STATUS" -ne 0 ]; then
  printf 'npm dependency audit: FAIL (validator status %s)\n' "$VALIDATION_STATUS" >&2
  if [ "$SECURITY_STATUS" -ne 0 ]; then
    printf 'npm dependency audit: frontend runtime preflight failed (status %s)\n' "$SECURITY_STATUS" >&2
  else
    # Print the validator's bounded diagnostic, not raw registry output.
    run_locked_node - "$TMP_ROOT/validator.err" <<'NODE' >&2
const { readFileSync } = require('node:fs');
const diagnostic = readFileSync(process.argv[2], 'utf8').split('\n')
  .find((line) => line.startsWith('dependency advisory validation: '));
if (diagnostic) console.error(diagnostic.replace(/[\x00-\x1f\x7f]/g, '').slice(0, 500));
NODE
  fi
  exit "$VALIDATION_STATUS"
fi

printf 'npm dependency audit: PASS\n' >&2
