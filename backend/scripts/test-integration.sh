#!/usr/bin/env bash
set -euo pipefail

readonly backend_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd -- "$backend_dir"

run_tests() {
	case "${1:-}" in
	--suite-fast)
		shift
		bash scripts/run-integration-suite.sh fast "$@"
		;;
	--suite-race)
		shift
		bash scripts/run-integration-suite.sh race "$@"
		;;
	*)
		go test "$@"
		;;
	esac
}

if [[ -n "${TPM_TEST_POSTGRES_DSN:-}" ]]; then
	run_tests "$@"
	exit
fi

readonly docker_bin="${DOCKER_BIN:-docker}"
readonly container_name="tpm-integration-postgres-$$"

cleanup() {
	"$docker_bin" rm --force "$container_name" >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

"$docker_bin" run --rm --detach \
	--name "$container_name" \
	--publish 127.0.0.1::5432 \
	--env POSTGRES_DB=postgres \
	--env POSTGRES_USER=tpm \
	--env POSTGRES_PASSWORD=tpm \
	postgres:18-alpine \
	postgres -c max_connections=300 >/dev/null

ready=0
for _ in $(seq 1 120); do
	if "$docker_bin" exec "$container_name" pg_isready --username=tpm --dbname=postgres >/dev/null 2>&1; then
		ready=1
		break
	fi
	sleep 0.25
done
if [[ "$ready" -ne 1 ]]; then
	"$docker_bin" logs "$container_name" >&2
	echo "integration postgres did not become ready" >&2
	exit 1
fi

port_binding="$("$docker_bin" port "$container_name" 5432/tcp | head -n 1)"
postgres_port="${port_binding##*:}"
if [[ -z "$postgres_port" || "$postgres_port" == "$port_binding" ]]; then
	echo "integration postgres port mapping is unavailable" >&2
	exit 1
fi

export TPM_TEST_POSTGRES_DSN="postgres://tpm:tpm@127.0.0.1:${postgres_port}/postgres?sslmode=disable"
run_tests "$@"
