#!/usr/bin/env bash
set -euo pipefail

readonly mode="${1:-}"
case "$mode" in
fast | race) ;;
*)
	echo "usage: $0 <fast|race>" >&2
	exit 2
	;;
esac

# Root processes share one PostgreSQL cluster. Keep cross-process physical DDL
# serialized by default while preserving in-process t.Parallel capacity.
readonly shard_count="${TPM_TEST_ROOT_SHARDS:-2}"
if [[ ! "$shard_count" =~ ^[1-9][0-9]*$ ]]; then
	echo "TPM_TEST_ROOT_SHARDS must be a positive integer" >&2
	exit 2
fi
readonly root_parallel="${TPM_TEST_ROOT_PARALLEL:-5}"
if [[ ! "$root_parallel" =~ ^[1-9][0-9]*$ ]]; then
	echo "TPM_TEST_ROOT_PARALLEL must be a positive integer" >&2
	exit 2
fi
readonly subpackage_parallel="${TPM_TEST_SUBPACKAGE_PARALLEL:-1}"
if [[ ! "$subpackage_parallel" =~ ^[1-9][0-9]*$ ]]; then
	echo "TPM_TEST_SUBPACKAGE_PARALLEL must be a positive integer" >&2
	exit 2
fi

readonly scratch_dir="$(mktemp -d)"
root_pid=""
cleanup() {
	if [[ -n "$root_pid" ]]; then
		kill "$root_pid" >/dev/null 2>&1 || true
		wait "$root_pid" >/dev/null 2>&1 || true
	fi
	find "$scratch_dir" -type f -delete 2>/dev/null || true
	rmdir "$scratch_dir" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

root_build_args=(-tags=integration)
subpackage_args=(-count=1 "-p=${subpackage_parallel}" "-parallel=${subpackage_parallel}" -timeout=20m -tags=integration)
if [[ "$mode" == "race" ]]; then
	root_build_args=(-race "${root_build_args[@]}")
	subpackage_args=(-race "${subpackage_args[@]}")
fi

readonly root_binary="$scratch_dir/integration-root.test"
go test "${root_build_args[@]}" -c -o "$root_binary" ./integration_test

readonly list_log="$scratch_dir/root-list.log"
if ! "$root_binary" -test.list='^Test' -test.timeout=2m >"$list_log" 2>&1; then
	cat "$list_log" >&2
	exit 1
fi
grep -E '^Test[[:alnum:]_]+$' "$list_log" >"$scratch_dir/root-tests.txt"
if [[ ! -s "$scratch_dir/root-tests.txt" ]]; then
	echo "root integration package did not expose any tests" >&2
	exit 1
fi

for ((index = 0; index < shard_count; index++)); do
	: >"$scratch_dir/shard-${index}.txt"
done
index=0
while IFS= read -r test_name; do
	shard=$((index % shard_count))
	printf '%s\n' "$test_name" >>"$scratch_dir/shard-${shard}.txt"
	index=$((index + 1))
done <"$scratch_dir/root-tests.txt"

echo "root integration tests: $index tests across $shard_count isolated shards"
root_status=0
for ((shard = 0; shard < shard_count; shard++)); do
	test_pattern="^($(paste -sd '|' "$scratch_dir/shard-${shard}.txt"))$"
	"$root_binary" \
		-test.count=1 \
		"-test.parallel=${root_parallel}" \
		-test.timeout=20m \
		-test.run="$test_pattern" \
		>"$scratch_dir/shard-${shard}.log" 2>&1 &
	root_pid="$!"
	if wait "$root_pid"; then
		echo "root integration shard $((shard + 1))/$shard_count: PASS"
	else
		echo "root integration shard $((shard + 1))/$shard_count: FAIL" >&2
		cat "$scratch_dir/shard-${shard}.log" >&2
		root_status=1
	fi
	root_pid=""
done

root_package="$(go list -tags=integration ./integration_test)"
mapfile -t subpackages < <(
	go list -tags=integration ./integration_test/... | while IFS= read -r package; do
		if [[ "$package" != "$root_package" ]]; then
			printf '%s\n' "$package"
		fi
	done
)

subpackage_status=0
if ! go test "${subpackage_args[@]}" "${subpackages[@]}"; then
	subpackage_status=1
fi

if [[ "$root_status" -ne 0 || "$subpackage_status" -ne 0 ]]; then
	exit 1
fi
