#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
scratch="$(mktemp -d)"
trap 'rm -rf -- "$scratch"' EXIT
mkdir "$scratch/bin"
export TEST_RUN_LOG="$scratch/runs"

# Stub only the Go boundary. Exercise the actual discovery and shard runner.
cat >"$scratch/bin/go" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == list ]]; then
  [[ "${TEST_FAIL_LIST:-0}" != 1 ]] || exit 23
  echo example/integration_test
  if [[ "${*: -1}" == ./integration_test/... ]]; then
    printf 'example/integration_test/sub%s\n' {1..7}
  fi
elif [[ "$*" == *' -c '* ]]; then
  while [[ "$1" != -o ]]; do shift; done
  cp "$TEST_ROOT_STUB" "$2"
  chmod +x "$2"
else
  [[ "$*" == *'-race'* ]] || exit 24
  for arg in "$@"; do
    [[ "$arg" != example/* ]] || echo "$arg" >>"$TEST_RUN_LOG"
  done
  [[ "${TEST_FAIL_SUBPACKAGE:-0}" != 1 ]] || exit 25
fi
SH
cat >"$scratch/root" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == *'-test.list='* ]]; then
  printf 'TestCase%02d\n' {1..17}
else
  for arg in "$@"; do
    if [[ "$arg" == -test.run=* ]]; then
      pattern="${arg#-test.run=^(}"
      pattern="${pattern%)$}"
      tr '|' '\n' <<<"$pattern" >>"$TEST_RUN_LOG"
    fi
  done
  [[ "${TEST_FAIL_ROOT:-0}" != 1 ]] || exit 26
fi
SH
chmod +x "$scratch/bin/go"
export TEST_ROOT_STUB="$scratch/root"
export PATH="$scratch/bin:$PATH"
export TPM_TEST_ROOT_SHARDS=5
for shard in {1..5}; do
  TPM_TEST_SHARD_INDEX="$shard" bash "$script_dir/run-integration-suite.sh" race >"$scratch/output" 2>&1
done
{ printf 'TestCase%02d\n' {1..17}; printf 'example/integration_test/sub%s\n' {1..7}; } | sort >"$scratch/expected"
sort "$TEST_RUN_LOG" >"$scratch/actual"
diff -u "$scratch/expected" "$scratch/actual"

for failure in TEST_FAIL_ROOT TEST_FAIL_SUBPACKAGE TEST_FAIL_LIST; do
  if env "$failure=1" TPM_TEST_SHARD_INDEX=1 bash "$script_dir/run-integration-suite.sh" race >"$scratch/output" 2>&1; then
    echo "failure was swallowed: $failure" >&2
    exit 1
  fi
done
for invalid in 0 6 abc; do
  if TPM_TEST_SHARD_INDEX="$invalid" bash "$script_dir/run-integration-suite.sh" race >"$scratch/output" 2>&1; then
    echo "invalid shard accepted: $invalid" >&2
    exit 1
  fi
done
echo "integration shard coverage and failure propagation: PASS"
