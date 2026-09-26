#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
POLICY="$REPO_ROOT/security/tools/openapi-tools.policy"
PACKAGE_JSON="$REPO_ROOT/frontend/package.json"
PACKAGE_LOCK="$REPO_ROOT/frontend/package-lock.json"
NPM_USERCONFIG="$REPO_ROOT/frontend/config/npm-empty-userconfig"
GO_MOD="$REPO_ROOT/backend/go.mod"
GO_SUM="$REPO_ROOT/backend/go.sum"

fail() {
  printf 'openapi toolchain trust: %s\n' "$*" >&2
  exit 1
}

usage() {
  printf '%s\n' 'usage: validate-openapi-toolchain-trust.sh [--policy PATH] [--package-json PATH] [--package-lock PATH] [--npm-userconfig PATH] [--go-mod PATH] [--go-sum PATH]'
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --policy) POLICY="${2:-}"; shift 2 ;;
    --package-json) PACKAGE_JSON="${2:-}"; shift 2 ;;
    --package-lock) PACKAGE_LOCK="${2:-}"; shift 2 ;;
    --npm-userconfig) NPM_USERCONFIG="${2:-}"; shift 2 ;;
    --go-mod) GO_MOD="${2:-}"; shift 2 ;;
    --go-sum) GO_SUM="${2:-}"; shift 2 ;;
    --help) usage; exit 0 ;;
    *) usage >&2; fail "unknown argument: $1" ;;
  esac
done

for required_file in "$POLICY" "$PACKAGE_JSON" "$PACKAGE_LOCK" "$NPM_USERCONFIG" "$GO_MOD" "$GO_SUM"; do
  [ -f "$required_file" ] || fail "required file not found: $required_file"
done

if grep -Ev '^[[:space:]]*(#.*)?$' "$NPM_USERCONFIG" | grep -q .; then
  fail 'npm userconfig must be credential-free and contain comments only'
fi

policy_count() {
  local key="$1"
  awk -v prefix="$key=" 'index($0, prefix) == 1 { count++ } END { print count + 0 }' "$POLICY"
}

policy_value() {
  local key="$1"
  [ "$(policy_count "$key")" -eq 1 ] || fail "policy key must appear exactly once: $key"
  awk -v prefix="$key=" 'index($0, prefix) == 1 { print substr($0, length(prefix) + 1) }' "$POLICY"
}

require_policy_value() {
  local key="$1"
  local value
  value="$(policy_value "$key")"
  [ -n "$value" ] || fail "policy value is required: $key"
  printf '%s' "$value"
}

require_policy_equal() {
  local key="$1"
  local expected="$2"
  local actual
  actual="$(require_policy_value "$key")"
  [ "$actual" = "$expected" ] || fail "unexpected $key: $actual"
}

validate_fresh_date() {
  local key="$1"
  local value stamp now age_days
  value="$(require_policy_value "$key")"
  stamp="$(date -u -d "$value" +%s 2>/dev/null)" || fail "invalid date for $key: $value"
  now="$(date -u +%s)"
  [ "$stamp" -le $((now + 86400)) ] || fail "future evidence date for $key: $value"
  age_days=$(((now - stamp) / 86400))
  [ "$age_days" -le 120 ] || fail "stale evidence for $key: $value"
}

require_policy_equal policy.format openapi-toolchain-trust-v1
validate_fresh_date evidence.retrieved

require_policy_equal redocly.package @redocly/cli
require_policy_equal redocly.version 2.51.2
require_policy_equal redocly.repository https://github.com/Redocly/redocly-cli
require_policy_equal redocly.registry https://registry.npmjs.org/
require_policy_equal redocly.license MIT
require_policy_value redocly.publisher >/dev/null
REDOCLY_INTEGRITY="$(require_policy_value redocly.integrity)"
require_policy_value redocly.tarball_sha1 >/dev/null
validate_fresh_date redocly.security_evidence_date

REDOCLY_SCORECARD="$(policy_value redocly.scorecard_url)"
REDOCLY_SCORECARD_JUSTIFICATION="$(policy_value redocly.scorecard_justification)"
if [ -n "$REDOCLY_SCORECARD" ] && [ "$REDOCLY_SCORECARD" != 'https://api.securityscorecards.dev/projects/github.com/Redocly/redocly-cli' ]; then
  fail "unofficial Redocly Scorecard source: $REDOCLY_SCORECARD"
fi
[ -n "$REDOCLY_SCORECARD" ] || [ -n "$REDOCLY_SCORECARD_JUSTIFICATION" ] || fail 'Redocly Scorecard evidence or justification is required'
require_policy_equal redocly.trust_decision accepted-bounded
require_policy_value redocly.residual_risk >/dev/null

require_policy_equal yaml.package yaml
require_policy_equal yaml.version 2.9.0
require_policy_equal yaml.repository https://github.com/eemeli/yaml
require_policy_equal yaml.registry https://registry.npmjs.org/
require_policy_equal yaml.license ISC
require_policy_value yaml.publisher >/dev/null
YAML_INTEGRITY="$(require_policy_value yaml.integrity)"
require_policy_value yaml.tarball_sha1 >/dev/null
validate_fresh_date yaml.security_evidence_date
require_policy_equal yaml.trust_decision accepted-compatibility
require_policy_value yaml.residual_risk >/dev/null

require_policy_equal js_yaml.package js-yaml
require_policy_equal js_yaml.version 4.3.2
require_policy_equal js_yaml.repository https://github.com/nodeca/js-yaml
require_policy_equal js_yaml.registry https://registry.npmjs.org/
require_policy_equal js_yaml.license MIT
require_policy_value js_yaml.publisher >/dev/null
JS_YAML_INTEGRITY="$(require_policy_value js_yaml.integrity)"
require_policy_value js_yaml.tarball_sha1 >/dev/null
validate_fresh_date js_yaml.security_evidence_date
require_policy_equal js_yaml.advisory https://github.com/advisories/GHSA-2883-xcg3-v3hh
require_policy_equal js_yaml.trust_decision accepted-patched
require_policy_value js_yaml.residual_risk >/dev/null

require_policy_equal openapi_typescript.package openapi-typescript
require_policy_equal openapi_typescript.version 7.13.0
require_policy_equal openapi_typescript.repository https://github.com/openapi-ts/openapi-typescript
require_policy_equal openapi_typescript.registry https://registry.npmjs.org/
require_policy_equal openapi_typescript.license MIT
require_policy_value openapi_typescript.publisher >/dev/null
OPENAPI_TYPESCRIPT_INTEGRITY="$(require_policy_value openapi_typescript.integrity)"
require_policy_value openapi_typescript.tarball_sha1 >/dev/null
validate_fresh_date openapi_typescript.security_evidence_date
require_policy_equal openapi_typescript.trust_decision accepted-bounded
require_policy_value openapi_typescript.residual_risk >/dev/null

require_policy_equal oapi.module github.com/oapi-codegen/oapi-codegen/v2
require_policy_equal oapi.command github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen
require_policy_equal oapi.version v2.8.0
require_policy_equal oapi.repository https://github.com/oapi-codegen/oapi-codegen
require_policy_equal oapi.proxy https://proxy.golang.org
require_policy_equal oapi.sumdb https://sum.golang.org
require_policy_equal oapi.license Apache-2.0
require_policy_value oapi.publisher >/dev/null
OAPI_MODULE_SUM="$(require_policy_value oapi.module_sum)"
OAPI_MOD_SUM="$(require_policy_value oapi.mod_sum)"
validate_fresh_date oapi.security_evidence_date

OAPI_SCORECARD="$(policy_value oapi.scorecard_url)"
OAPI_SCORECARD_JUSTIFICATION="$(policy_value oapi.scorecard_justification)"
if [ -n "$OAPI_SCORECARD" ] && [ "$OAPI_SCORECARD" != 'https://api.securityscorecards.dev/projects/github.com/oapi-codegen/oapi-codegen' ]; then
  fail "unofficial oapi-codegen Scorecard source: $OAPI_SCORECARD"
fi
[ -n "$OAPI_SCORECARD" ] || [ -n "$OAPI_SCORECARD_JUSTIFICATION" ] || fail 'oapi-codegen Scorecard evidence or justification is required'
if [ -n "$OAPI_SCORECARD" ]; then
  validate_fresh_date oapi.scorecard_observed_at
  require_policy_value oapi.scorecard_score >/dev/null
fi
require_policy_equal oapi.trust_decision accepted-bounded
require_policy_value oapi.residual_risk >/dev/null

node - "$PACKAGE_JSON" "$PACKAGE_LOCK" "$REDOCLY_INTEGRITY" "$YAML_INTEGRITY" "$OPENAPI_TYPESCRIPT_INTEGRITY" "$JS_YAML_INTEGRITY" <<'NODE'
const fs = require('node:fs');

function fail(message) {
  process.stderr.write(`openapi toolchain trust: ${message}\n`);
  process.exit(1);
}

const packageJson = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
const packageLock = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const expectedIntegrity = process.argv[4];
const expectedYamlIntegrity = process.argv[5];
const expectedOpenapiTypescriptIntegrity = process.argv[6];
const expectedJsYamlIntegrity = process.argv[7];
const lifecycle = ['preinstall', 'install', 'postinstall', 'prepare'];
const auditScript = 'bash ../scripts/release/run-npm-audit.sh && bash ../scripts/release/run-npm-build-tool-audit.sh';

if (packageJson.devDependencies?.['@redocly/cli'] !== '2.51.2') {
  fail('package.json must pin @redocly/cli to 2.51.2');
}
for (const name of lifecycle) {
  if (packageJson.scripts?.[name]) fail(`package.json lifecycle script is forbidden: ${name}`);
}
if (packageJson.scripts?.['audit:dependencies'] !== auditScript) {
  fail('package.json dependency gate must include advisory and toolchain trust validation');
}
if (packageLock.lockfileVersion !== 3) fail('package-lock.json must use lockfileVersion 3');
if (packageLock.packages?.['']?.devDependencies?.['@redocly/cli'] !== '2.51.2') {
  fail('package-lock root has the wrong @redocly/cli identity');
}
const cli = packageLock.packages?.['node_modules/@redocly/cli'];
if (!cli || cli.version !== '2.51.2') fail('package-lock entry for @redocly/cli 2.51.2 is missing');
if (cli.resolved !== 'https://registry.npmjs.org/@redocly/cli/-/cli-2.51.2.tgz') {
  fail('package-lock has an unofficial @redocly/cli source');
}
if (cli.integrity !== expectedIntegrity) fail('package-lock @redocly/cli checksum does not match policy');
if (cli.license !== 'MIT') fail('package-lock @redocly/cli license is missing or wrong');
if (cli.hasInstallScript) fail('@redocly/cli must not declare a lifecycle install script');
if (cli.bin?.redocly !== 'bin/cli.js') fail('@redocly/cli binary identity is wrong');

if (packageJson.devDependencies?.yaml !== '2.9.0') fail('package.json must pin yaml to 2.9.0');
if (packageLock.packages?.['']?.devDependencies?.yaml !== '2.9.0') {
  fail('package-lock root has the wrong yaml compatibility identity');
}
const yaml = packageLock.packages?.['node_modules/yaml'];
if (!yaml || yaml.version !== '2.9.0') fail('package-lock entry for yaml 2.9.0 is missing');
if (yaml.resolved !== 'https://registry.npmjs.org/yaml/-/yaml-2.9.0.tgz') {
  fail('package-lock has an unofficial yaml source');
}
if (yaml.integrity !== expectedYamlIntegrity) fail('package-lock yaml checksum does not match policy');
if (yaml.license !== 'ISC') fail('package-lock yaml license is missing or wrong');
if (yaml.hasInstallScript) fail('yaml must not declare a lifecycle install script');
if (yaml.bin?.yaml !== 'bin.mjs') fail('yaml package binary identity is wrong');

if (packageJson.overrides?.['js-yaml'] !== '4.3.2') {
  fail('package.json must override js-yaml to patched version 4.3.2');
}
const jsYaml = packageLock.packages?.['node_modules/js-yaml'];
if (!jsYaml || jsYaml.version !== '4.3.2') fail('package-lock entry for js-yaml 4.3.2 is missing');
if (jsYaml.resolved !== 'https://registry.npmjs.org/js-yaml/-/js-yaml-4.3.2.tgz') {
  fail('package-lock has an unofficial js-yaml source');
}
if (jsYaml.integrity !== expectedJsYamlIntegrity) fail('package-lock js-yaml checksum does not match policy');
if (jsYaml.license !== 'MIT') fail('package-lock js-yaml license is missing or wrong');
if (jsYaml.hasInstallScript) fail('js-yaml must not declare a lifecycle install script');

if (packageJson.devDependencies?.['openapi-typescript'] !== '7.13.0') {
  fail('package.json must pin openapi-typescript to 7.13.0');
}
if (packageLock.packages?.['']?.devDependencies?.['openapi-typescript'] !== '7.13.0') {
  fail('package-lock root has the wrong openapi-typescript identity');
}
const openapiTypescript = packageLock.packages?.['node_modules/openapi-typescript'];
if (!openapiTypescript || openapiTypescript.version !== '7.13.0') {
  fail('package-lock entry for openapi-typescript 7.13.0 is missing');
}
if (openapiTypescript.resolved !== 'https://registry.npmjs.org/openapi-typescript/-/openapi-typescript-7.13.0.tgz') {
  fail('package-lock has an unofficial openapi-typescript source');
}
if (openapiTypescript.integrity !== expectedOpenapiTypescriptIntegrity) {
  fail('package-lock openapi-typescript checksum does not match policy');
}
if (openapiTypescript.license !== 'MIT') fail('package-lock openapi-typescript license is missing or wrong');
if (openapiTypescript.hasInstallScript) fail('openapi-typescript must not declare a lifecycle install script');
if (openapiTypescript.bin?.['openapi-typescript'] !== 'bin/cli.js') {
  fail('openapi-typescript binary identity is wrong');
}

for (const [name, entry] of Object.entries(packageLock.packages || {})) {
  if (!entry.resolved) continue;
  let source;
  try {
    source = new URL(entry.resolved);
  } catch {
    fail(`invalid resolved URL for ${name}`);
  }
  if (source.protocol !== 'https:' || source.hostname !== 'registry.npmjs.org') {
    fail(`unofficial resolved source for ${name}`);
  }
  if (typeof entry.integrity !== 'string' || !entry.integrity.startsWith('sha512-')) {
    fail(`missing SHA-512 integrity for ${name}`);
  }
  const encoded = entry.integrity.slice('sha512-'.length);
  const decoded = Buffer.from(encoded, 'base64');
  if (decoded.length !== 64 || decoded.toString('base64') !== encoded) {
    fail(`invalid SHA-512 integrity for ${name}`);
  }
}
NODE

grep -Fxq 'module github.com/TakuyaYagam1/task-per-minute' "$GO_MOD" || fail 'unexpected backend module identity'
grep -Fxq 'go 1.26.8' "$GO_MOD" || fail 'unexpected backend module Go version'
awk '
  $1 == "github.com/oapi-codegen/oapi-codegen/v2" && $2 == "v2.8.0" { count++ }
  END { exit count == 1 ? 0 : 1 }
' "$GO_MOD" || fail 'oapi-codegen tool is missing or mutable'
if grep -Eq '^(replace|exclude|retract|toolchain)[[:space:]]' "$GO_MOD"; then
  fail 'backend module contains a version or source override'
fi

grep -Fxq "github.com/oapi-codegen/oapi-codegen/v2 v2.8.0 $OAPI_MODULE_SUM" "$GO_SUM" || fail 'oapi-codegen module checksum mismatch'
grep -Fxq "github.com/oapi-codegen/oapi-codegen/v2 v2.8.0/go.mod $OAPI_MOD_SUM" "$GO_SUM" || fail 'oapi-codegen go.mod checksum mismatch'

while read -r module version; do
  [ -n "$module" ] || continue
  grep -Eq "^${module//./\\.} ${version//+/\\+} h1:[A-Za-z0-9+/]+={0,2}$" "$GO_SUM" || fail "missing module checksum: $module $version"
  grep -Eq "^${module//./\\.} ${version//+/\\+}/go.mod h1:[A-Za-z0-9+/]+={0,2}$" "$GO_SUM" || fail "missing go.mod checksum: $module $version"
done < <(
  awk '
    /^require \(/ { block = 1; next }
    block && /^\)/ { block = 0; next }
    block && NF >= 2 { print $1, $2 }
    /^require [^(]/ { print $2, $3 }
  ' "$GO_MOD"
)

awk '
  /^tool \(/ { in_tool = 1; next }
  in_tool && /^\)/ { in_tool = 0 }
  in_tool {
    line = $0
    sub(/[[:space:]]*\/\/.*$/, "", line)
    if (line ~ /^[[:space:]]*github\.com\/oapi-codegen\/oapi-codegen\/v2\/cmd\/oapi-codegen[[:space:]]*$/) oapi++
    if (line ~ /^[[:space:]]*github\.com\/google\/wire\/cmd\/wire[[:space:]]*$/) wire++
  }
  END { exit (oapi == 1 && wire == 1) ? 0 : 1 }
' "$GO_MOD" || fail 'go.mod tool block is missing an exact active command pin'

printf 'openapi toolchain trust validation passed\n'
