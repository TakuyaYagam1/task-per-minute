#!/usr/bin/env bash
set -euo pipefail

fixture_child() {
  local mode="${1:-}"
  shift || true

  case "$mode" in
    pass)
      printf 'fixture stdout\n'
      printf 'fixture stderr\n' >&2
      ;;
    fail)
      printf 'fixture failed as requested\n' >&2
      exit 23
      ;;
    timeout)
      sleep 5
      ;;
    signal)
      trap 'printf "forwarded SIGINT\n"; exit 130' INT
      trap 'printf "forwarded SIGTERM\n" >&2; exit 143' TERM
      printf 'fixture waiting for signal\n'
      while :; do
        sleep 1
      done
      ;;
    secret)
      [[ "${1:-}" == 'fixture-secret-canary-7261' ]] || exit 64
      printf 'password=%s\n' "$1"
      printf 'authorization bearer %s\n' "$1" >&2
      ;;
    capacity-report)
      child_source="${BASH_SOURCE[0]}"
      child_parent="${child_source%/*}"
      [[ "$child_parent" != "$child_source" ]] || child_parent='.'
      child_root="$(cd -- "$child_parent/../../.." && pwd -P)"
      report_fixture="$child_root/backend/integration_test/testdata/capacity/tournament60-nominal.json"
      python3 - "$report_fixture" <<'PY'
import copy
import json
import pathlib
import sys


fixture = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))


def report(revision: str, digest: str) -> dict[str, object]:
    value = copy.deepcopy(fixture)
    value["identity"]["revision"]["commit"] = revision
    value["digests"]["artifacts"][0]["digest"] = "sha256:" + digest
    return value


def marker(value: dict[str, object]) -> str:
    payload = json.dumps(value, separators=(",", ":"), sort_keys=False)
    return f"TOURNAMENT_CAPACITY_REPORT {payload}"


raw = report("0123456789abcdef0123456789abcdef01234567", "a" * 64)
wrapped = report("fedcba9876543210fedcba9876543210fedcba98", "b" * 64)
split = report("3" * 40, "f" * 64)
extra = report("1" * 40, "c" * 64)
extra["extra"] = {}
print("x" * 70000)
print(marker(extra))

sensitive = report("2" * 40, "d" * 64)
assert isinstance(sensitive["identity"], dict)
sensitive["identity"]["secret_token"] = "fixture-secret-canary-7261"
print(marker(sensitive))

print('TOURNAMENT_CAPACITY_REPORT {"schema":"broken","digest":"' + "e" * 64)
print("password=fixture-secret-canary-7261")
print(marker(raw))
print(json.dumps({
    "Time": "2026-09-02T00:00:00Z",
    "Action": "output",
    "Package": "task-per-minute/integration_test",
    "Output": "    tournament_capacity_nominal_test.go:47: " + marker(wrapped) + "\n",
}, separators=(",", ":")))
split_marker = marker(split)
split_points = [len(split_marker) // 4, len(split_marker) // 2, len(split_marker) * 3 // 4]
split_parts = [
    split_marker[:split_points[0]],
    split_marker[split_points[0]:split_points[1]],
    split_marker[split_points[1]:split_points[2]],
    split_marker[split_points[2]:],
]
for index, part in enumerate(split_parts):
    leader = "    tournament_capacity_nominal_test.go:47: " if index == 0 else ""
    ending = "\n" if index == len(split_parts) - 1 else ""
    print(json.dumps({
        "Time": "2026-09-02T00:00:00Z",
        "Action": "output",
        "Package": "task-per-minute/integration_test",
        "Output": leader + part + ending,
    }, separators=(",", ":")))
PY
      ;;
    capacity-policy)
      profile="${1:?capacity profile is required}"
      scenario="${2:?capacity scenario is required}"
      if [[ "$scenario" == 'underlying-fail' ]]; then
        printf 'postgres://fixture-user:tiny-pass@127.0.0.1/db\n' >&2
        printf 'moss-elk-7\n' >&2
        exit 23
      fi
      child_source="${BASH_SOURCE[0]}"
      child_parent="${child_source%/*}"
      [[ "$child_parent" != "$child_source" ]] || child_parent='.'
      child_root="$(cd -- "$child_parent/../../.." && pwd -P)"
      python3 - "$child_root" "$profile" "$scenario" <<'PY'
from __future__ import annotations

import copy
import datetime as dt
import hashlib
import json
import os
import pathlib
import subprocess
import sys


repo_root = pathlib.Path(sys.argv[1])
expected_profile = sys.argv[2]
scenario = sys.argv[3]
fixture_path = repo_root / "backend/integration_test/testdata/capacity/tournament60-nominal.json"
schema_path = repo_root / "backend/integration_test/testdata/capacity/tournament60-report.schema.json"
value = copy.deepcopy(json.loads(fixture_path.read_text(encoding="utf-8")))

revision = subprocess.check_output(
    ["git", "-C", str(repo_root), "rev-parse", "--verify", "HEAD"],
    text=True,
).strip()
status = subprocess.run(
    ["git", "-C", str(repo_root), "status", "--porcelain=v1", "--untracked-files=all"],
    stdout=subprocess.PIPE,
    stderr=subprocess.DEVNULL,
    check=True,
).stdout
dirty = bool(status.strip())

report_profile = "peak" if scenario == "profile-mismatch" else expected_profile
name = "tournament45" if report_profile == "peak" else "tournament60"
duration_seconds = 2700 if report_profile == "peak" else 3600
workers = 8 if report_profile == "peak" else 4
operation_interval = 100 if report_profile == "peak" else 250
minimum_throughput = 60 if report_profile == "peak" else 12
latency_limit = 400 if report_profile == "peak" else 250
lag_limit = 3000 if report_profile == "peak" else 2000

value["identity"]["revision"] = {"commit": revision, "dirty": dirty}
value["workload"]["name"] = name
value["workload"]["profile"] = report_profile
started = dt.datetime.fromisoformat(value["window"]["started_at"].replace("Z", "+00:00"))
value["window"]["ended_at"] = (
    started + dt.timedelta(seconds=duration_seconds)
).isoformat().replace("+00:00", "Z")
value["window"]["duration_seconds"] = duration_seconds
value["load"]["workers"] = workers
value["load"]["operation_interval_ms"] = operation_interval
value["throughput"]["operations_per_second"] = minimum_throughput + 4
value["throughput"]["minimum"] = minimum_throughput
value["latency_ms"]["p99_limit"] = latency_limit
value["lag"]["max_limit_ms"] = lag_limit

thresholds = {item["metric"]: item for item in value["thresholds"]}
thresholds["latency_p99"]["limit"] = latency_limit
thresholds["throughput"]["limit"] = minimum_throughput
thresholds["throughput"]["observed"] = minimum_throughput + 4
thresholds["scheduling_lag_max"]["limit"] = lag_limit

build = value["identity"]["build"]
build_material = "\0".join(
    [revision, str(dirty).lower(), build["go_version"], build["target"]]
).encode("utf-8")
build["digest"] = "sha256:" + hashlib.sha256(build_material).hexdigest()
workload_material = json.dumps(
    {
        "name": name,
        "profile": report_profile,
        "duration_seconds": duration_seconds,
        "participants": value["load"]["participants"],
        "games": value["load"]["games"],
        "workers": workers,
        "operation_interval_ms": operation_interval,
    },
    separators=(",", ":"),
).encode("utf-8")
value["digests"]["artifacts"] = [
    {
        "name": "tournament60-report.schema.json",
        "digest": "sha256:" + hashlib.sha256(schema_path.read_bytes()).hexdigest(),
    },
    {
        "name": name + "-workload",
        "digest": "sha256:" + hashlib.sha256(workload_material).hexdigest(),
    },
]

if scenario == "revision-mismatch":
    value["identity"]["revision"]["commit"] = "0" * 40
    build_material = "\0".join(
        ["0" * 40, str(dirty).lower(), build["go_version"], build["target"]]
    ).encode("utf-8")
    build["digest"] = "sha256:" + hashlib.sha256(build_material).hexdigest()
elif scenario == "invalid":
    value["unexpected"] = True
elif scenario == "threshold-fail":
    value["throughput"]["status"] = "FAIL"
    thresholds["throughput"]["status"] = "FAIL"
elif scenario == "limit-mismatch":
    value["throughput"]["minimum"] = 1

test_name = "TestTournamentPeakSoak" if expected_profile == "peak" else "TestTournamentNominal60Minute"
source_name = (
    "tournament_capacity_peak_test.go"
    if expected_profile == "peak"
    else "tournament_capacity_nominal_test.go"
)
package = "github.com/TakuyaYagam1/task-per-minute/integration_test"


def event(action: str, **fields: object) -> None:
    print(json.dumps({"Action": action, "Package": package, **fields}, separators=(",", ":")))


def emit(parts: list[str]) -> None:
    for index, part in enumerate(parts):
        leader = f"    {source_name}:47: " if index == 0 else ""
        ending = "\n" if index == len(parts) - 1 else ""
        event("output", Test=test_name, Output=leader + part + ending)


event("run", Test=test_name)
event(
    "output",
    Test=test_name,
    Output="postgres://fixture-user:tiny-pass@127.0.0.1/db moss-elk-7\n",
)
print("moss-elk-7 raw diagnostic")
print("postgres://fixture-user:tiny-pass@127.0.0.1/db", file=sys.stderr)
print("moss-elk-7", file=sys.stderr)

if scenario != "missing":
    marker = "TOURNAMENT_CAPACITY_REPORT " + json.dumps(value, separators=(",", ":"))
    if scenario == "duplicate":
        emit([marker])
        emit([marker])
    elif scenario == "positive":
        points = [len(marker) // 4, len(marker) // 2, len(marker) * 3 // 4]
        emit(
            [
                marker[: points[0]],
                marker[points[0] : points[1]],
                marker[points[1] : points[2]],
                marker[points[2] :],
            ]
        )
    else:
        emit([marker])
event("pass", Test=test_name, Elapsed=1.25)
event("pass", Elapsed=1.25)
PY
      ;;
    environment)
      expected_path='/nix/store/dv8vg7k21fdi9v79g5x4b87kwqhl8ykv-go-1.26.8/bin:/nix/store/6f0qqak4qbcrbw4f750phr88c9yhpf5s-git-2.55.0/bin:/nix/store/5y8jchf95jisr09cjx2q7lgz3qwnfi5j-coreutils-full-9.11/bin:/nix/store/bwry105g7v5jspr41bx9x3fcfqsmfkq2-bash-interactive-5.3p15/bin:/nix/store/gxzhl7aaiid7zp3y47jqqiq7zg5mqpwp-python3-3.14.6/bin'
      [[ "$PATH" == "$expected_path" ]]
      [[ -z "${GOROOT+x}" ]]
      [[ -z "${TMPDIR+x}" ]]
      [[ "$CGO_ENABLED" == '0' ]]
      [[ "$GOCACHE" == '/home/takuya/.cache/go-build' ]]
      [[ "$GOMODCACHE" == '/home/takuya/go/pkg/mod' ]]
      [[ "$GOPATH" == '/home/takuya/go' ]]
      [[ "$(command -v go)" == '/nix/store/dv8vg7k21fdi9v79g5x4b87kwqhl8ykv-go-1.26.8/bin/go' ]]
      [[ "$(command -v git)" == '/nix/store/6f0qqak4qbcrbw4f750phr88c9yhpf5s-git-2.55.0/bin/git' ]]
      [[ "$(command -v env)" == '/nix/store/5y8jchf95jisr09cjx2q7lgz3qwnfi5j-coreutils-full-9.11/bin/env' ]]
      [[ "$(command -v bash)" == '/nix/store/bwry105g7v5jspr41bx9x3fcfqsmfkq2-bash-interactive-5.3p15/bin/bash' ]]
      printf 'environment-safe\n'
      ;;
    truncate)
      python3 - <<'PY'
import sys

sys.stdout.write("stdout-data-" + "x" * 131072 + "\n")
sys.stderr.write("stderr-data-" + "y" * 131072 + "\n")
PY
      ;;
    argv)
      [[ "$#" -eq 2 ]] || exit 65
      [[ "$1" == 'argument with spaces' ]] || exit 66
      [[ "$2" == 'literal;false' ]] || exit 67
      printf 'argv-safe\n'
      ;;
    *)
      printf 'unknown fixture child mode: %s\n' "$mode" >&2
      exit 64
      ;;
  esac
}

if [[ "${1:-}" == '--fixture-child' ]]; then
  shift
  fixture_child "$@"
  exit 0
fi

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd -- "$script_dir/../../.." && pwd -P)"
runner="$repo_root/scripts/release/run-tournament-go-gate.sh"
schema="$repo_root/scripts/release/schemas/tournament-go-gate.schema.json"
test_script="$script_dir/run-tournament-go-gate_test.sh"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

pass() {
  printf 'PASS: %s\n' "$1"
}

[[ -f "$runner" ]] || fail "runner is missing: $runner"
[[ -f "$schema" ]] || fail "schema is missing: $schema"

umask 077
test_tmp="$(mktemp -d "${TMPDIR:-/tmp}/tournament-go-gate-test.XXXXXX")"
trap 'rm -rf -- "$test_tmp"' EXIT
chmod 0700 "$test_tmp"

new_evidence_dir() {
  local name="$1"
  local path="$test_tmp/$name"
  mkdir -- "$path"
  chmod 0700 "$path"
  printf '%s\n' "$path"
}

record_path() {
  local evidence_dir="$1"
  local expected_count="${2:-1}"
  local gate="${3:-fixture-test}"
  local -a records=()
  mapfile -t records < <(find "$evidence_dir" -maxdepth 1 -type f -name "tournament-$gate-*.json" -print | sort)
  [[ "${#records[@]}" -eq "$expected_count" ]] || fail "expected $expected_count records in $evidence_dir, found ${#records[@]}"
  printf '%s\n' "${records[-1]}"
}

verify_record() {
  local record="$1"
  local expected_outcome="$2"
  local expected_exit="$3"
  local expected_forwarded="${4:-null}"

  python3 - "$schema" "$record" "$repo_root" "$expected_outcome" "$expected_exit" "$expected_forwarded" <<'PY'
from __future__ import annotations

import copy
import hashlib
import json
import os
import pathlib
import re
import sys
from typing import Any


schema_path = pathlib.Path(sys.argv[1])
record_path = pathlib.Path(sys.argv[2])
repo_root = pathlib.Path(sys.argv[3])
expected_outcome = sys.argv[4]
expected_exit = None if sys.argv[5] == "null" else int(sys.argv[5])
expected_forwarded = None if sys.argv[6] == "null" else sys.argv[6]
schema = json.loads(schema_path.read_text(encoding="utf-8"))
record = json.loads(record_path.read_text(encoding="utf-8"))


def resolve_ref(root: dict[str, Any], reference: str) -> dict[str, Any]:
    if not reference.startswith("#/"):
        raise AssertionError(f"unsupported reference: {reference}")
    node: Any = root
    for part in reference[2:].split("/"):
        node = node[part.replace("~1", "/").replace("~0", "~")]
    assert isinstance(node, dict)
    return node


def matches_type(value: Any, expected: str) -> bool:
    return {
        "object": isinstance(value, dict),
        "array": isinstance(value, list),
        "string": isinstance(value, str),
        "integer": isinstance(value, int) and not isinstance(value, bool),
        "boolean": isinstance(value, bool),
        "null": value is None,
    }.get(expected, False)


def validate(value: Any, rule: dict[str, Any], root: dict[str, Any], path: str = "record") -> None:
    if "$ref" in rule:
        validate(value, resolve_ref(root, rule["$ref"]), root, path)
        return

    for candidate in rule.get("allOf", []):
        validate(value, candidate, root, path)
    if "anyOf" in rule:
        for candidate in rule["anyOf"]:
            try:
                validate(value, candidate, root, path)
            except AssertionError:
                continue
            break
        else:
            raise AssertionError(f"{path}: no anyOf branch matched")
    if "if" in rule:
        try:
            validate(value, rule["if"], root, path)
        except AssertionError:
            branch = rule.get("else")
        else:
            branch = rule.get("then")
        if branch is not None:
            validate(value, branch, root, path)

    declared_type = rule.get("type")
    if declared_type is not None:
        allowed = declared_type if isinstance(declared_type, list) else [declared_type]
        assert any(matches_type(value, item) for item in allowed), f"{path}: wrong type"
    if "const" in rule:
        assert value == rule["const"], f"{path}: const mismatch"
    if "enum" in rule:
        assert value in rule["enum"], f"{path}: enum mismatch"
    if isinstance(value, dict):
        properties = rule.get("properties", {})
        for required in rule.get("required", []):
            assert required in value, f"{path}.{required}: missing"
        if rule.get("additionalProperties") is False:
            extras = set(value) - set(properties)
            assert not extras, f"{path}: unexpected fields: {sorted(extras)}"
        for name, child in value.items():
            if name in properties:
                validate(child, properties[name], root, f"{path}.{name}")
    if isinstance(value, list) and "items" in rule:
        for index, child in enumerate(value):
            validate(child, rule["items"], root, f"{path}[{index}]")
    if isinstance(value, str):
        if "pattern" in rule:
            assert re.search(rule["pattern"], value), f"{path}: pattern mismatch"
        if "minLength" in rule:
            assert len(value) >= rule["minLength"], f"{path}: too short"
        if "maxLength" in rule:
            assert len(value) <= rule["maxLength"], f"{path}: too long"
    if isinstance(value, int) and not isinstance(value, bool):
        if "minimum" in rule:
            assert value >= rule["minimum"], f"{path}: below minimum"
        if "maximum" in rule:
            assert value <= rule["maximum"], f"{path}: above maximum"


assert schema.get("$schema") == "https://json-schema.org/draft/2020-12/schema"
assert schema.get("additionalProperties") is False
validate(record, schema, schema)
assert record["outcome"] == expected_outcome
assert record["exit_status"] == expected_exit
assert record["forwarded_signal"] == expected_forwarded
assert record["result"] == ("PASS" if expected_outcome == "pass" else "FAIL")
if expected_exit is None:
    assert record["exit_code"] in {137, 143}
else:
    assert record["exit_code"] == expected_exit
assert re.fullmatch(r"[0-9a-f]{40}", record["source_revision"])
assert record["owner"] == f"uid:{os.getuid()}"
assert record["evidence_uri"] == f"tournament-evidence://{record_path.name}"
assert record["environment"]["working_directory"] == "repository-fixture"
assert record["environment"]["cgo_enabled"] is False
assert len(record["artifact_digests"]) == 3
for artifact in record["artifact_digests"]:
    artifact_path = repo_root / artifact["name"]
    assert artifact_path.is_file()
    assert hashlib.sha256(artifact_path.read_bytes()).hexdigest() == artifact["sha256"]
for stream_name in ("stdout", "stderr"):
    stream = record[stream_name]
    digest = hashlib.sha256(stream["text"].encode("utf-8")).hexdigest()
    assert stream["sha256"] == digest

normalized = copy.deepcopy(record)
normalized["evidence_sha256"] = "0" * 64
normalized_bytes = json.dumps(
    normalized,
    sort_keys=True,
    separators=(",", ":"),
    ensure_ascii=True,
).encode("utf-8")
assert record["evidence_sha256"] == hashlib.sha256(normalized_bytes).hexdigest()

mutated = copy.deepcopy(record)
mutated["unexpected"] = True
try:
    validate(mutated, schema, schema)
except AssertionError:
    pass
else:
    raise AssertionError("schema accepted an unknown root property")


def assert_rejected(candidate: dict[str, Any], label: str) -> None:
    try:
        validate(candidate, schema, schema)
    except AssertionError:
        return
    raise AssertionError(f"schema accepted contradictory record: {label}")


consistent = copy.deepcopy(record)
consistent.update(
    {
        "result": "PASS",
        "exit_code": 0,
        "outcome": "pass",
        "exit_status": 0,
        "termination_signal": None,
        "forwarded_signal": None,
        "timed_out": False,
    }
)
validate(consistent, schema, schema)
for field, value in (
    ("result", "FAIL"),
    ("exit_code", 1),
    ("exit_status", 1),
    ("timed_out", True),
    ("termination_signal", "SIGTERM"),
    ("forwarded_signal", "SIGINT"),
):
    contradiction = copy.deepcopy(consistent)
    contradiction[field] = value
    assert_rejected(contradiction, f"pass/{field}")

evidence_contract = copy.deepcopy(consistent)
evidence_contract.update({"result": "FAIL", "outcome": "evidence-contract"})
validate(evidence_contract, schema, schema)
contradiction = copy.deepcopy(evidence_contract)
contradiction["result"] = "PASS"
assert_rejected(contradiction, "evidence-contract/result")

failed = copy.deepcopy(consistent)
failed.update({"result": "FAIL", "exit_code": 23, "exit_status": 23, "outcome": "fail"})
validate(failed, schema, schema)
contradiction = copy.deepcopy(failed)
contradiction["exit_status"] = 0
assert_rejected(contradiction, "fail/exit_status")

timed_out_record = copy.deepcopy(failed)
timed_out_record.update({"outcome": "timeout", "timed_out": True, "forwarded_signal": None})
validate(timed_out_record, schema, schema)
contradiction = copy.deepcopy(timed_out_record)
contradiction["timed_out"] = False
assert_rejected(contradiction, "timeout/timed_out")

signal_record = copy.deepcopy(failed)
signal_record.update({"outcome": "signal", "forwarded_signal": "SIGINT"})
validate(signal_record, schema, schema)
contradiction = copy.deepcopy(signal_record)
contradiction["forwarded_signal"] = None
contradiction["termination_signal"] = None
assert_rejected(contradiction, "signal/missing signal")

nominal = copy.deepcopy(consistent)
nominal["gate"] = "capacity-nominal"
nominal["timeout_seconds"] = 4800
nominal["environment"]["working_directory"] = "backend"
nominal["command"] = {
    "allowlist_id": "capacity-nominal-v1",
    "kind": "go-test",
    "executable_basename": "env",
    "executable_sha256": "c88776f602efc831ecffdda48d347648e9584b5e76c9ff4d6d4676a5daa5b4a7",
    "argument_count": 12,
    "go_executable_basename": "go",
    "go_executable_sha256": "abe5a36e95186f29453f3e1d1840d436445e208417191e10c83998b6a7d8e652",
    "go_test_identity": "TestTournamentNominal60Minute",
    "fixture_script_basename": None,
    "fixture_script_sha256": None,
}
validate(nominal, schema, schema)
for path, value in (
    (("timeout_seconds",), 4500),
    (("environment", "working_directory"), "repository-fixture"),
    (("command", "allowlist_id"), "capacity-peak-v1"),
    (("command", "kind"), "fixture"),
    (("command", "go_test_identity"), "TestTournamentPeakSoak"),
):
    contradiction = copy.deepcopy(nominal)
    target: Any = contradiction
    for part in path[:-1]:
        target = target[part]
    target[path[-1]] = value
    assert_rejected(contradiction, "nominal/" + "/".join(path))
PY

  local sidecar="$record.sha256"
  [[ -f "$sidecar" ]] || fail "record checksum is missing: $sidecar"
  [[ "$(stat -c '%a' "$record")" == '600' ]] || fail "record mode is not 0600: $record"
  [[ "$(stat -c '%a' "$sidecar")" == '600' ]] || fail "checksum mode is not 0600: $sidecar"
  (
    cd -- "$(dirname -- "$record")"
    sha256sum -c -- "$(basename -- "$sidecar")" >/dev/null
  ) || fail "record checksum mismatch: $record"
}

run_fixture() {
  local evidence_dir="$1"
  local mode="$2"
  shift 2
  local output="$evidence_dir/wrapper.out"
  if ! bash "$runner" fixture-test "$evidence_dir" -- \
    bash "$test_script" --fixture-child "$mode" "$@" >"$output" 2>&1; then
    sed -n '1,80p' "$output" >&2
    fail "retained fixture outcome returned nonzero: $mode"
  fi
}

expect_reject() {
  local label="$1"
  local evidence_dir="$2"
  shift 2
  local output="$test_tmp/reject-${label// /-}.out"
  if bash "$runner" fixture-test "$evidence_dir" -- \
    bash "$test_script" --fixture-child pass >"$output" 2>&1; then
    fail "$label unexpectedly passed"
  fi
  pass "$label"
}

pass_dir="$(new_evidence_dir pass)"
run_fixture "$pass_dir" pass
pass_record="$(record_path "$pass_dir")"
verify_record "$pass_record" pass 0
python3 - "$pass_record" <<'PY'
import json
import pathlib
import sys

record = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
assert "fixture stdout" in record["stdout"]["text"]
assert "fixture stderr" in record["stderr"]["text"]
PY
pass 'retains PASS with separate stdout and stderr'

fail_dir="$(new_evidence_dir fail)"
run_fixture "$fail_dir" fail
fail_record="$(record_path "$fail_dir")"
verify_record "$fail_record" fail 23
pass 'retains underlying nonzero FAIL and returns success'

timeout_dir="$(new_evidence_dir timeout)"
run_fixture "$timeout_dir" timeout
timeout_record="$(record_path "$timeout_dir")"
verify_record "$timeout_record" timeout null
python3 - "$timeout_record" <<'PY'
import json
import pathlib
import sys

record = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
assert record["timed_out"] is True
assert record["termination_signal"] in {"SIGTERM", "SIGKILL"}
PY
pass 'retains timeout and returns success'

run_signal_case() {
  local signal_name="$1"
  local expected_exit
  if [[ "$signal_name" == 'INT' ]]; then
    expected_exit=130
  else
    expected_exit=143
  fi
  local evidence_dir
  evidence_dir="$(new_evidence_dir "signal-${signal_name,,}")"
  local output="$evidence_dir/wrapper.out"

  bash "$runner" fixture-test "$evidence_dir" -- \
    bash "$test_script" --fixture-child signal >"$output" 2>&1 &
  local runner_pid=$!
  sleep 0.25
  kill "-$signal_name" "$runner_pid" || fail "could not send $signal_name to runner"

  local status=0
  wait "$runner_pid" || status=$?
  [[ "$status" -eq 0 ]] || fail "retained $signal_name returned $status"

  local record
  record="$(record_path "$evidence_dir")"
  verify_record "$record" signal "$expected_exit" "SIG$signal_name"
  python3 - "$record" "SIG$signal_name" <<'PY'
import json
import pathlib
import sys

record = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
assert sys.argv[2] in record["stdout"]["text"] + record["stderr"]["text"]
PY
  pass "forwards and retains $signal_name"
}

run_signal_case INT
run_signal_case TERM

secret_dir="$(new_evidence_dir secret)"
canary='fixture-secret-canary-7261'
run_fixture "$secret_dir" secret "$canary"
secret_record="$(record_path "$secret_dir")"
verify_record "$secret_record" pass 0
if grep -R -F -- "$canary" "$secret_dir" >/dev/null; then
  fail 'secret canary leaked into retained evidence'
fi
pass 'redacts secret canary from retained evidence'

capacity_report_dir="$(new_evidence_dir capacity-report)"
run_fixture "$capacity_report_dir" capacity-report
capacity_report_record="$(record_path "$capacity_report_dir")"
verify_record "$capacity_report_record" pass 0
python3 - "$capacity_report_record" \
  "$repo_root/backend/integration_test/testdata/capacity/tournament60-nominal.json" <<'PY'
import copy
import json
import pathlib
import sys


fixture = json.loads(pathlib.Path(sys.argv[2]).read_text(encoding="utf-8"))


def report(revision: str, digest: str) -> dict[str, object]:
    value = copy.deepcopy(fixture)
    value["identity"]["revision"]["commit"] = revision
    value["digests"]["artifacts"][0]["digest"] = "sha256:" + digest
    return value


record = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
text = record["stdout"]["text"]
raw_revision = "0123456789abcdef0123456789abcdef01234567"
wrapped_revision = "fedcba9876543210fedcba9876543210fedcba98"
split_revision = "3" * 40
raw_marker = "TOURNAMENT_CAPACITY_REPORT " + json.dumps(
    report(raw_revision, "a" * 64), sort_keys=True, separators=(",", ":")
)
wrapped_marker = "TOURNAMENT_CAPACITY_REPORT " + json.dumps(
    report(wrapped_revision, "b" * 64), sort_keys=True, separators=(",", ":")
)
split_marker = "TOURNAMENT_CAPACITY_REPORT " + json.dumps(
    report(split_revision, "f" * 64), sort_keys=True, separators=(",", ":")
)
assert raw_marker in text
assert wrapped_marker in text
assert split_marker in text
assert raw_revision in text
assert wrapped_revision in text
assert split_revision in text
assert "a" * 64 in text
assert "b" * 64 in text
assert "f" * 64 in text
for rejected in ("1" * 40, "2" * 40, "c" * 64, "d" * 64, "e" * 64):
    assert rejected not in text
assert "fixture-secret-canary-7261" not in text
assert text.count("[REDACTED]") >= 3
assert record["stdout"]["truncated"] is True
PY
if grep -R -F -- "$canary" "$capacity_report_dir" >/dev/null; then
  fail 'capacity report fixture leaked the secret canary'
fi
pass 'preserves validated raw and Go JSON capacity markers while redacting rejects'

truncate_dir="$(new_evidence_dir truncate)"
run_fixture "$truncate_dir" truncate
truncate_record="$(record_path "$truncate_dir")"
verify_record "$truncate_record" pass 0
python3 - "$truncate_record" <<'PY'
import json
import pathlib
import sys

record = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
for name in ("stdout", "stderr"):
    stream = record[name]
    assert stream["truncated"] is True
    assert stream["stored_bytes"] <= 65536
    assert stream["total_bytes"] > stream["stored_bytes"]
PY
pass 'bounds and marks truncated stdout and stderr'

argv_dir="$(new_evidence_dir argv)"
run_fixture "$argv_dir" argv 'argument with spaces' 'literal;false'
argv_record="$(record_path "$argv_dir")"
verify_record "$argv_record" pass 0
python3 - "$argv_record" <<'PY'
import json
import pathlib
import sys

record = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
assert "argv-safe" in record["stdout"]["text"]
assert record["command"]["argument_count"] == 5
PY
pass 'preserves spaces and metacharacters without shell evaluation'

expect_reject 'rejects relative evidence directory' relative-evidence
[[ ! -e "$repo_root/relative-evidence" ]] || fail 'runner created a relative evidence directory'

missing_dir="$test_tmp/missing"
expect_reject 'rejects missing evidence directory' "$missing_dir"
[[ ! -e "$missing_dir" ]] || fail 'runner created a missing evidence directory'

expect_reject 'rejects repository evidence directory' "$repo_root/scripts/release"

symlink_target="$(new_evidence_dir symlink-target)"
symlink_path="$test_tmp/symlink-evidence"
ln -s -- "$symlink_target" "$symlink_path"
expect_reject 'rejects symlink evidence directory' "$symlink_path"

permission_dir="$(new_evidence_dir permissions)"
chmod 0750 "$permission_dir"
expect_reject 'rejects non-0700 evidence directory' "$permission_dir"

allowlist_dir="$(new_evidence_dir allowlist)"
allowlist_output="$allowlist_dir/wrapper.out"
if bash "$runner" fixture-test "$allowlist_dir" -- printf 'not allowlisted\n' >"$allowlist_output" 2>&1; then
  fail 'fixture gate accepted an arbitrary command'
fi
[[ -z "$(find "$allowlist_dir" -maxdepth 1 -type f -name '*.json' -print -quit)" ]] || fail 'allowlist rejection retained a record'
pass 'rejects arbitrary fixture command before execution'

python_poison_bin="$test_tmp/python-poison-bin"
mkdir -- "$python_poison_bin"
cat >"$python_poison_bin/python3" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
: >"${TOURNAMENT_POISON_MARKER:?}"
exit 97
EOF
chmod 0700 "$python_poison_bin/python3"
python_poison_marker="$test_tmp/python-path-poisoned"
python_poison_dir="$(new_evidence_dir python-path-poison)"
(
  PATH="$python_poison_bin:$PATH" TOURNAMENT_POISON_MARKER="$python_poison_marker" \
    bash "$runner" fixture-test "$python_poison_dir" -- \
      bash "$test_script" --fixture-child pass
) >"$python_poison_dir/wrapper.out" 2>&1 || fail 'PATH python3 poisoning blocked the pinned runtime'
[[ ! -e "$python_poison_marker" ]] || fail 'runner executed PATH-provided python3'
python_poison_record="$(record_path "$python_poison_dir")"
verify_record "$python_poison_record" pass 0
pass 'ignores PATH-provided python3'

python_shadow_dir="$test_tmp/python-shadow"
mkdir -- "$python_shadow_dir"
cat >"$python_shadow_dir/json.py" <<'PY'
raise RuntimeError("cwd json module was imported")
PY
python_shadow_evidence="$(new_evidence_dir python-shadow-evidence)"
(
  cd -- "$python_shadow_dir"
  bash "$runner" fixture-test "$python_shadow_evidence" -- \
    bash "$test_script" --fixture-child pass
) >"$python_shadow_evidence/wrapper.out" 2>&1 || fail 'cwd module shadowing blocked isolated Python'
python_shadow_record="$(record_path "$python_shadow_evidence")"
verify_record "$python_shadow_record" pass 0
pass 'isolates Python from cwd module shadowing'

runtime_poison_bin="$test_tmp/runtime-poison-bin"
mkdir -- "$runtime_poison_bin"
trusted_test_bash="${BASH:?}"
for runtime_name in bash git go env; do
  poison_marker="$test_tmp/runtime-poison-$runtime_name"
  cat >"$runtime_poison_bin/$runtime_name" <<EOF
#!$trusted_test_bash
set -euo pipefail
: >$(printf '%q' "$poison_marker")
exit 97
EOF
  chmod 0700 "$runtime_poison_bin/$runtime_name"
done
runtime_poison_dir="$(new_evidence_dir runtime-poison)"
(
  PATH="$runtime_poison_bin:$PATH" \
    GOROOT='/caller/goroot' TMPDIR='/caller/tmp' CGO_ENABLED='1' \
    "$trusted_test_bash" "$runner" fixture-test "$runtime_poison_dir" -- \
      bash "$test_script" --fixture-child environment
) >"$runtime_poison_dir/wrapper.out" 2>&1 || fail 'caller runtime environment changed pinned execution'
for runtime_name in bash git go env; do
  [[ ! -e "$test_tmp/runtime-poison-$runtime_name" ]] || \
    fail "runner executed PATH-provided $runtime_name"
done
runtime_poison_record="$(record_path "$runtime_poison_dir")"
verify_record "$runtime_poison_record" pass 0
python3 - "$runtime_poison_record" <<'PY'
import json
import pathlib
import sys

record = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
assert "environment-safe" in record["stdout"]["text"]
assert record["command"]["executable_basename"] == "bash"
assert record["command"]["executable_sha256"] == "4eadb049773ad49e107adec9ad130ee83c0ec44f404740289620b234eeacc69d"
PY
pass 'ignores caller runtime paths and inherited Go environment'

run_capacity_policy_case() {
  local profile="$1"
  local scenario="$2"
  local expected_outcome="$3"
  local expected_name="$4"
  local expected_exit="${5:-0}"
  local evidence_dir
  local record
  evidence_dir="$(new_evidence_dir "policy-$profile-$scenario")"
  run_fixture "$evidence_dir" capacity-policy "$profile" "$scenario"
  record="$(record_path "$evidence_dir")"
  verify_record "$record" "$expected_outcome" "$expected_exit"
  python3 - "$record" "$expected_outcome" "$profile" "$expected_name" <<'PY'
import json
import pathlib
import sys

record = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
expected_outcome = sys.argv[2]
expected_profile = sys.argv[3]
expected_name = sys.argv[4]
assert record["gate"] == "fixture-test"
assert record["command"]["kind"] == "fixture"
combined = record["stdout"]["text"] + record["stderr"]["text"]
assert "tiny-pass" not in combined
assert "fixture-user" not in combined
assert "moss-elk-7" not in combined
assert record["stderr"]["text"] in {"", "[capacity stderr omitted]\n"}
for line in record["stdout"]["text"].splitlines():
    if line.startswith("TOURNAMENT_CAPACITY_REPORT "):
        continue
    event = json.loads(line)
    assert set(event) <= {"Action", "Elapsed", "Test"}
    assert "Output" not in event
if expected_outcome == "pass":
    text = record["stdout"]["text"]
    assert text.count("TOURNAMENT_CAPACITY_REPORT ") == 1
    marker = next(line for line in text.splitlines() if line.startswith("TOURNAMENT_CAPACITY_REPORT "))
    report = json.loads(marker.removeprefix("TOURNAMENT_CAPACITY_REPORT "))
    assert report["workload"] == {
        "name": expected_name,
        "operation": "game_repository_get",
        "profile": expected_profile,
    }
    assert report["identity"]["revision"]["commit"] == record["source_revision"]
    assert report["identity"]["revision"]["dirty"] == record["source_modified"]
PY
  pass "fixture capacity policy $profile/$scenario retains $expected_outcome"
}

run_capacity_policy_case nominal missing evidence-contract tournament60
run_capacity_policy_case nominal underlying-fail fail tournament60 23
run_capacity_policy_case nominal positive pass tournament60
run_capacity_policy_case peak positive pass tournament45
run_capacity_policy_case nominal duplicate evidence-contract tournament60
run_capacity_policy_case nominal invalid evidence-contract tournament60
run_capacity_policy_case nominal profile-mismatch evidence-contract tournament60
run_capacity_policy_case nominal revision-mismatch evidence-contract tournament60
run_capacity_policy_case nominal threshold-fail evidence-contract tournament60
run_capacity_policy_case nominal limit-mismatch evidence-contract tournament60

split_tag_dir="$(new_evidence_dir split-tag)"
if (
  cd -- "$repo_root/backend"
  bash "$runner" capacity-nominal "$split_tag_dir" -- \
    env GOTOOLCHAIN=local GOPROXY=off go test -json -count=1 \
    -tags=integration capacity ./integration_test -run '^TestTournamentNominal60Minute$' -timeout 80m
) >/dev/null 2>&1; then
  fail 'capacity gate accepted a split build-tag argument'
fi
[[ -z "$(find "$split_tag_dir" -maxdepth 1 -type f -name '*.json' -print -quit)" ]] || \
  fail 'split build-tag rejection retained a record'
pass 'rejects a split production build-tag argument'

exact_command_dir="$(new_evidence_dir exact-command)"
if (
  cd -- "$repo_root"
  bash "$runner" capacity-nominal "$exact_command_dir" -- \
    env GOTOOLCHAIN=local GOPROXY=off go test -json -count=1 \
    -tags='integration capacity' ./integration_test -run '^TestTournamentNominal60Minute$' -timeout 80m
) >"$exact_command_dir/wrapper.out" 2>&1; then
  fail 'exact capacity command unexpectedly ran outside backend'
fi
grep -Fq 'capacity gates must run from the repository backend directory' \
  "$exact_command_dir/wrapper.out" || fail 'exact capacity command did not reach cwd policy'
[[ -z "$(find "$exact_command_dir" -maxdepth 1 -type f -name '*.json' -print -quit)" ]] || \
  fail 'exact command policy rejection retained a record'
pass 'accepts only the exact capacity command shape before cwd enforcement'

deleted_migration_repo="$test_tmp/deleted-migration-repo"
git clone --quiet --no-hardlinks -- "$repo_root" "$deleted_migration_repo"
cp -- "$runner" "$deleted_migration_repo/scripts/release/run-tournament-go-gate.sh"
for refactored_artifact in \
  backend/internal/adapter/outbound/postgres/game.go \
  backend/internal/adapter/outbound/postgres/tournament.go \
  backend/integration_test/tournament_migration_test.go \
  backend/integration_test/tournament_roster_migration_test.go \
  backend/integration_test/swiss_migration_test.go \
  backend/integration_test/game_migration_test.go \
  backend/integration_test/draft_migration_action_test.go \
  backend/integration_test/draft_migration_fixture_test.go \
  backend/integration_test/draft_migration_flow_test.go \
  backend/integration_test/reconnect_migration_assertion_fixture_test.go \
  backend/integration_test/reconnect_migration_presence_test.go \
  backend/integration_test/reconnect_migration_setup_test.go; do
  cp -- "$repo_root/$refactored_artifact" "$deleted_migration_repo/$refactored_artifact"
done

deleted_migration="$({
  git -C "$deleted_migration_repo" ls-files -- 'backend/db/migrations/*.sql'
} | sed -n '1p')"
[[ "$deleted_migration" == backend/db/migrations/*.sql ]] || \
  fail 'migration fixture has no safe tracked migration'
[[ -f "$deleted_migration_repo/$deleted_migration" ]] || \
  fail 'tracked migration fixture is missing'
rm -- "$deleted_migration_repo/$deleted_migration"

replacement_migration="$deleted_migration_repo/backend/db/migrations/999999_inventory_fixture.sql"
[[ ! -e "$replacement_migration" ]] || \
  fail 'migration inventory fixture collides with repository state'
printf '%s\n' \
  '-- +goose Up' \
  'SELECT 1;' \
  '-- +goose Down' \
  'SELECT 1;' >"$replacement_migration"

deleted_migration_dir="$(new_evidence_dir deleted-migration)"
deleted_migration_output="$test_tmp/deleted-migration.out"
if (
  cd -- "$deleted_migration_repo/backend"
  bash "$deleted_migration_repo/scripts/release/run-tournament-go-gate.sh" \
    capacity-nominal "$deleted_migration_dir" -- printf 'not allowlisted\n'
) >"$deleted_migration_output" 2>&1; then
  fail 'deleted migration fixture unexpectedly passed command validation'
fi
grep -Fq 'command does not match the capacity-nominal allowlist' \
  "$deleted_migration_output" || {
    sed -n '1,80p' "$deleted_migration_output" >&2
    fail 'deleted tracked migration remained in artifact inventory'
  }
pass 'excludes deleted tracked migrations from artifact inventory'

collision_dir="$(new_evidence_dir collision)"
run_fixture "$collision_dir" pass
run_fixture "$collision_dir" pass
record_path "$collision_dir" 2 >/dev/null
pass 'uses collision-safe record names'

if find "$test_tmp" -type f \( -name '*.tmp' -o -name '.*.tmp' -o -name '*.partial' \) -print -quit | grep -q .; then
  fail 'temporary or partial evidence file remains'
fi
pass 'leaves no temporary or partial record'

printf 'run-tournament-go-gate fixture tests passed\n'
