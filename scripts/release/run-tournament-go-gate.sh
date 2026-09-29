#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: run-tournament-go-gate.sh <gate-name> <absolute-evidence-dir> -- <command> [args...]

Run one allowlisted Tournament Go gate and retain its bounded evidence record.
EOF
}

if (($# < 4)); then
  usage >&2
  exit 2
fi

gate_name="$1"
evidence_dir="$2"
shift 2

if [[ "${1:-}" != '--' ]]; then
  usage >&2
  exit 2
fi
shift

if (($# == 0)); then
  usage >&2
  exit 2
fi

script_source="${BASH_SOURCE[0]}"
script_parent="${script_source%/*}"
if [[ "$script_parent" == "$script_source" ]]; then
  script_parent='.'
fi
script_dir="$(cd -- "$script_parent" && pwd -P)"
repo_root="$(cd -- "$script_dir/../.." && pwd -P)"
schema_path="$repo_root/scripts/release/schemas/tournament-go-gate.schema.json"
python_bin='/nix/store/gxzhl7aaiid7zp3y47jqqiq7zg5mqpwp-python3-3.14.6/bin/python3.14'

if [[ ! -f "$python_bin" || ! -x "$python_bin" || -L "$python_bin" ]]; then
  printf 'tournament go gate: pinned Python runtime is unavailable or unsafe\n' >&2
  exit 1
fi

exec "$python_bin" -I - "$repo_root" "$schema_path" "$gate_name" "$evidence_dir" "$@" <<'PY'
from __future__ import annotations

import datetime as dt
import hashlib
import json
import os
import pathlib
import platform
import re
import selectors
import signal
import stat
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass, field
from typing import Any


CAPTURE_LIMIT = 65536
CAPTURE_HEAD_LIMIT = 49152
CAPTURE_TAIL_LIMIT = 16384
CAPTURE_SEPARATOR = b"\n[...TRUNCATED...]\n"
TERMINATION_GRACE_SECONDS = 2.0
SCHEMA_ID = "scripts/release/schemas/tournament-go-gate.schema.json"
RECORD_SCHEMA_JSON_ID = "https://task-per-minute.local/schemas/tournament-go-gate.schema.json"
RECORD_ROOT_KEYS = frozenset(
    {
        "schema",
        "schema_version",
        "gate",
        "source_revision",
        "source_modified",
        "artifact_digests",
        "result",
        "exit_code",
        "evidence_uri",
        "evidence_sha256",
        "summary",
        "owner",
        "environment",
        "timeout_seconds",
        "started_at",
        "finished_at",
        "duration_ms",
        "outcome",
        "exit_status",
        "termination_signal",
        "forwarded_signal",
        "timed_out",
        "command",
        "stdout",
        "stderr",
    }
)
FIXTURE_CANARY = "fixture-secret-canary-7261"
PINNED_PYTHON = pathlib.Path(
    "/nix/store/gxzhl7aaiid7zp3y47jqqiq7zg5mqpwp-python3-3.14.6/bin/python3.14"
)
PINNED_PYTHON_SHA256 = "465d82f95e8e1069347b0ebf288d14d37802a1ae6cb831c15953cba0859ff766"
PINNED_GO = pathlib.Path(
    "/nix/store/62rzn370ba6jc0sfvmb9a93s4619f6kv-go-1.26.8/bin/go"
)
PINNED_GO_SHA256 = "d9a2fa19c7ef8b57f420012c21f49f235c46f08a68c12077d9c753dbb6ccdc34"
PINNED_GIT = pathlib.Path(
    "/nix/store/6f0qqak4qbcrbw4f750phr88c9yhpf5s-git-2.55.0/bin/git"
)
PINNED_GIT_SHA256 = "d776b30d3f856aca98c8681a249cf8606fd14d4a9dc9debd358014522fa7d067"
PINNED_ENV = pathlib.Path(
    "/nix/store/5y8jchf95jisr09cjx2q7lgz3qwnfi5j-coreutils-full-9.11/bin/env"
)
PINNED_ENV_SHA256 = "c88776f602efc831ecffdda48d347648e9584b5e76c9ff4d6d4676a5daa5b4a7"
PINNED_BASH = pathlib.Path(
    "/nix/store/bwry105g7v5jspr41bx9x3fcfqsmfkq2-bash-interactive-5.3p15/bin/bash"
)
PINNED_BASH_SHA256 = "4eadb049773ad49e107adec9ad130ee83c0ec44f404740289620b234eeacc69d"
CAPACITY_REPORT_PREFIX = "TOURNAMENT_CAPACITY_REPORT "
CAPACITY_REPORT_PREFIX_BYTES = CAPACITY_REPORT_PREFIX.encode("ascii")
CAPACITY_PACKAGE = "github.com/TakuyaYagam1/task-per-minute/integration_test"
CAPACITY_GATE_WORKLOAD = {
    "capacity-nominal": ("nominal", "tournament60"),
    "capacity-peak": ("peak", "tournament45"),
}
CAPACITY_THRESHOLD_METRICS = frozenset(
    {
        "failed_operations",
        "correctness_failures",
        "latency_p99",
        "throughput",
        "scheduling_lag_max",
        "reconnect_failures",
        "goroutines_max",
        "heap_alloc_bytes_max",
        "database_connections_max",
    }
)
CAPACITY_REPORT_ROOT_KEYS = frozenset(
    {
        "schema",
        "version",
        "identity",
        "workload",
        "window",
        "load",
        "operations",
        "latency_ms",
        "throughput",
        "lag",
        "reconnect",
        "resources",
        "database",
        "thresholds",
        "digests",
        "environment_equivalence",
    }
)
CAPACITY_REPORT_OBJECT_KEYS = CAPACITY_REPORT_ROOT_KEYS - {
    "schema",
    "version",
    "thresholds",
}
GO_TEST_LOG_PREFIX = re.compile(
    r"^[ \t]+tournament_capacity_(nominal|peak|report)_test\.go:[1-9][0-9]*: $"
)


class RunnerError(Exception):
    pass


def fail(message: str) -> None:
    raise RunnerError(message)


def sha256_file(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def timestamp(value: dt.datetime) -> str:
    return value.astimezone(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%f")[:23] + "Z"


def validate_pinned_python() -> None:
    try:
        runtime = PINNED_PYTHON.resolve(strict=True)
        active_runtime = pathlib.Path(sys.executable).resolve(strict=True)
    except OSError as exc:
        fail(f"pinned Python runtime cannot be resolved: {exc}")
    if runtime != PINNED_PYTHON or active_runtime != PINNED_PYTHON:
        fail("active Python runtime does not match the pinned path")
    metadata = runtime.stat()
    if not runtime.is_file() or not os.access(runtime, os.X_OK):
        fail("pinned Python runtime is not an executable regular file")
    if stat.S_IMODE(metadata.st_mode) != 0o555:
        fail("pinned Python runtime mode does not match the immutable declaration")
    if sha256_file(runtime) != PINNED_PYTHON_SHA256:
        fail("pinned Python runtime digest does not match the immutable declaration")


def validate_pinned_executable(
    name: str,
    executable: pathlib.Path,
    expected_sha256: str,
) -> None:
    try:
        metadata = executable.stat()
    except OSError as exc:
        fail(f"pinned {name} executable cannot be resolved: {exc}")
    if not executable.is_file() or not os.access(executable, os.X_OK):
        fail(f"pinned {name} path is not an executable regular file")
    if stat.S_IMODE(metadata.st_mode) != 0o555:
        fail(f"pinned {name} executable mode does not match the immutable declaration")
    if sha256_file(executable) != expected_sha256:
        fail(f"pinned {name} executable digest does not match the immutable declaration")


def validate_evidence_dir(path_text: str, repo_root: pathlib.Path) -> pathlib.Path:
    if not os.path.isabs(path_text):
        fail("evidence directory must be an absolute path")

    lexical = pathlib.Path(path_text)
    if any(part in {".", ".."} for part in lexical.parts):
        fail("evidence directory must not contain dot path components")
    if not lexical.exists():
        fail("evidence directory must already exist")

    current = pathlib.Path(lexical.anchor)
    for part in lexical.parts[1:]:
        current /= part
        if current.is_symlink():
            fail("evidence directory path must not contain symlinks")

    try:
        resolved = lexical.resolve(strict=True)
    except OSError as exc:
        fail(f"evidence directory cannot be resolved: {exc}")
    if not resolved.is_dir():
        fail("evidence directory is not a directory")

    resolved_repo = repo_root.resolve(strict=True)
    try:
        common = pathlib.Path(os.path.commonpath((str(resolved), str(resolved_repo))))
    except ValueError:
        common = pathlib.Path("")
    if common == resolved_repo:
        fail("evidence directory must be outside the repository")

    metadata = resolved.stat()
    if metadata.st_uid != os.getuid():
        fail("evidence directory must be owned by the current uid")
    if stat.S_IMODE(metadata.st_mode) != 0o700:
        fail("evidence directory mode must be exactly 0700")
    return resolved


def child_environment() -> dict[str, str]:
    result = {
        "PATH": ":".join(
            str(path.parent)
            for path in (PINNED_GO, PINNED_GIT, PINNED_ENV, PINNED_BASH, PINNED_PYTHON)
        ),
        "GOCACHE": "/home/takuya/.cache/go-build",
        "GOMODCACHE": "/home/takuya/go/pkg/mod",
        "GOPATH": "/home/takuya/go",
        "CGO_ENABLED": "0",
        "GOENV": "off",
        "HOME": "/nonexistent",
        "LANG": "C",
        "LC_ALL": "C",
    }
    docker_host = os.environ.get("DOCKER_HOST", "")
    runtime_text = os.environ.get("XDG_RUNTIME_DIR", "")
    if docker_host or runtime_text:
        expected_runtime = pathlib.Path(f"/run/user/{os.getuid()}")
        expected_host = f"unix://{expected_runtime}/podman/podman.sock"
        if runtime_text != str(expected_runtime) or docker_host != expected_host:
            fail("container runtime environment does not match the reviewed rootless socket")
        try:
            runtime = pathlib.Path(runtime_text).resolve(strict=True)
            runtime_metadata = runtime.stat()
            socket_path = pathlib.Path(docker_host.removeprefix("unix://"))
            socket_metadata = socket_path.lstat()
        except OSError as exc:
            fail(f"reviewed rootless container socket is unavailable: {exc}")
        if runtime != expected_runtime or not runtime.is_dir():
            fail("rootless container runtime directory is unsafe")
        if runtime_metadata.st_uid != os.getuid() or stat.S_IMODE(runtime_metadata.st_mode) != 0o700:
            fail("rootless container runtime directory ownership or mode is unsafe")
        if socket_path.is_symlink() or not stat.S_ISSOCK(socket_metadata.st_mode):
            fail("rootless container endpoint is not a direct socket")
        if socket_metadata.st_uid != os.getuid():
            fail("rootless container socket is not owned by the current uid")
        result["DOCKER_HOST"] = docker_host
        result["XDG_RUNTIME_DIR"] = runtime_text
    return result


def source_identity(repo_root: pathlib.Path) -> tuple[str, bool]:
    environment = {
        "PATH": str(PINNED_GIT.parent),
        "LANG": "C",
        "LC_ALL": "C",
        "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_OPTIONAL_LOCKS": "0",
        "GIT_TERMINAL_PROMPT": "0",
    }

    try:
        revision_probe = subprocess.run(
            [str(PINNED_GIT), "-C", str(repo_root), "rev-parse", "--verify", "HEAD"],
            stdin=subprocess.DEVNULL,
            capture_output=True,
            check=False,
            timeout=5,
            env=environment,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        fail(f"source revision probe failed: {exc}")
    revision = revision_probe.stdout.decode("ascii", errors="ignore").strip()
    if revision_probe.returncode != 0 or re.fullmatch(r"[0-9a-f]{40}", revision) is None:
        fail("source revision probe returned an invalid revision")

    try:
        status_probe = subprocess.run(
            [
                str(PINNED_GIT),
                "-C",
                str(repo_root),
                "status",
                "--porcelain=v1",
                "--untracked-files=all",
            ],
            stdin=subprocess.DEVNULL,
            capture_output=True,
            check=False,
            timeout=5,
            env=environment,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        fail(f"source status probe failed: {exc}")
    if status_probe.returncode != 0:
        fail("source status probe failed")
    return revision, bool(status_probe.stdout.strip())


def artifact_digest_records(
    gate: str,
    repo_root: pathlib.Path,
    schema_path: pathlib.Path,
) -> list[dict[str, str]]:
    relative_paths = [
        "scripts/release/run-tournament-go-gate.sh",
        "scripts/release/schemas/tournament-go-gate.schema.json",
    ]
    if gate == "fixture-test":
        relative_paths.append("scripts/release/tests/run-tournament-go-gate_test.sh")
    else:
        relative_paths.extend(
            [
                "backend/go.mod",
                "backend/go.sum",
                "backend/internal/adapter/outbound/postgres/execution/game/game.go",
                "backend/internal/adapter/outbound/postgres/tournament/roster/tournament_roster.go",
                "backend/internal/adapter/outbound/postgres/tx_manager.go",
                "backend/integration_test/main_test.go",
                "backend/integration_test/tournament/main_test.go",
                "backend/integration_test/tournament/tournament_migration_test.go",
                "backend/integration_test/tournament/tournament_migration_flow.go",
                "backend/integration_test/tournament/tournament_roster_migration_test.go",
                "backend/integration_test/tournament/tournament_roster_migration_flow.go",
                "backend/integration_test/swiss/main_test.go",
                "backend/integration_test/swiss/runner.go",
                "backend/integration_test/swiss/swiss_migration_test.go",
                "backend/integration_test/game/main_test.go",
                "backend/integration_test/game/runner.go",
                "backend/integration_test/game/game_migration_test.go",
                "backend/integration_test/draft/main_test.go",
                "backend/integration_test/draft/runner.go",
                "backend/integration_test/draft/draft_migration_test.go",
                "backend/integration_test/draft/draft_migration_action.go",
                "backend/integration_test/draft/draft_migration_fixture.go",
                "backend/integration_test/draft/draft_migration_flow.go",
                "backend/integration_test/reconnect/main_test.go",
                "backend/integration_test/reconnect/runner.go",
                "backend/integration_test/reconnect/reconnect_migration_test.go",
                "backend/integration_test/reconnect/migration_helpers.go",
                "backend/integration_test/reconnect/migration_lock_helpers.go",
                "backend/integration_test/reconnect/migration_scenarios.go",
                "backend/integration_test/reconnect_migration_presence_test.go",
                "backend/integration_test/reconnect_migration_setup_test.go",
                "backend/integration_test/tournament_capacity_nominal_test.go",
                "backend/integration_test/tournament_capacity_peak_test.go",
                "backend/integration_test/tournament_capacity_reconnect_test.go",
                "backend/integration_test/tournament_capacity_report_assertion_test.go",
                "backend/integration_test/tournament_capacity_report_build_test.go",
                "backend/integration_test/tournament_capacity_report_postgres_test.go",
                "backend/integration_test/tournament_capacity_report_types_test.go",
                "backend/integration_test/tournament_capacity_report_validation_test.go",
                "backend/integration_test/testdata/capacity/tournament60-report.schema.json",
                "backend/integration_test/testdata/capacity/tournament60-nominal.json",
            ]
        )
        migrations_dir = repo_root / "backend/db/migrations"
        if migrations_dir.is_symlink() or not migrations_dir.is_dir():
            fail("migration artifact directory is missing or unsafe")
        filesystem_migrations = {
            path.relative_to(repo_root).as_posix()
            for path in migrations_dir.glob("*.sql")
        }
        migration_env = {
            "PATH": str(PINNED_GIT.parent),
            "LANG": "C",
            "LC_ALL": "C",
            "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_OPTIONAL_LOCKS": "0",
            "GIT_TERMINAL_PROMPT": "0",
        }
        try:
            migration_probe = subprocess.run(
                [
                    str(PINNED_GIT),
                    "-C",
                    str(repo_root),
                    "ls-files",
                    "-z",
                    "--cached",
                    "--others",
                    "--exclude-standard",
                    "--",
                    "backend/db/migrations/*.sql",
                ],
                stdin=subprocess.DEVNULL,
                capture_output=True,
                check=False,
                timeout=5,
                env=migration_env,
            )
            deleted_probe = subprocess.run(
                [
                    str(PINNED_GIT),
                    "-C",
                    str(repo_root),
                    "ls-files",
                    "-z",
                    "--deleted",
                    "--",
                    "backend/db/migrations/*.sql",
                ],
                stdin=subprocess.DEVNULL,
                capture_output=True,
                check=False,
                timeout=5,
                env=migration_env,
            )
        except (OSError, subprocess.TimeoutExpired) as exc:
            fail(f"migration artifact inventory failed: {exc}")
        if migration_probe.returncode != 0 or deleted_probe.returncode != 0:
            fail("migration artifact inventory failed")
        try:
            tracked_migrations = {
                item.decode("utf-8")
                for item in migration_probe.stdout.split(b"\0")
                if item
            }
            deleted_migrations = {
                item.decode("utf-8")
                for item in deleted_probe.stdout.split(b"\0")
                if item
            }
        except UnicodeDecodeError:
            fail("migration artifact inventory contains an invalid path")
        migration_paths = sorted(
            filesystem_migrations | (tracked_migrations - deleted_migrations)
        )
        if not migration_paths:
            fail("migration artifact set is empty")
        relative_paths.extend(migration_paths)

    records: list[dict[str, str]] = []
    resolved_root = repo_root.resolve(strict=True)
    for relative_path in relative_paths:
        candidate = repo_root / relative_path
        if candidate.is_symlink() or not candidate.is_file():
            fail(f"declared evidence artifact is missing or unsafe: {relative_path}")
        resolved = candidate.resolve(strict=True)
        if resolved != candidate:
            fail(f"declared evidence artifact contains a symlink component: {relative_path}")
        if os.path.commonpath((str(resolved), str(resolved_root))) != str(resolved_root):
            fail(f"declared evidence artifact escapes the repository: {relative_path}")
        records.append({"name": relative_path, "sha256": sha256_file(resolved)})

    if schema_path.resolve(strict=True) != (repo_root / relative_paths[1]).resolve(strict=True):
        fail("record schema path does not match the repository contract")
    return records


def command_policy(
    gate: str,
    command: list[str],
    repo_root: pathlib.Path,
) -> tuple[int, pathlib.Path, dict[str, Any], list[str], list[str], str | None]:
    nominal = [
        "env",
        "GOTOOLCHAIN=local",
        "GOPROXY=off",
        "go",
        "test",
        "-json",
        "-count=1",
        "-tags=integration capacity",
        "./integration_test",
        "-run",
        "^TestTournamentNominal60Minute$",
        "-timeout",
        "80m",
    ]
    peak = [
        "env",
        "GOTOOLCHAIN=local",
        "GOPROXY=off",
        "go",
        "test",
        "-json",
        "-count=1",
        "-tags=integration capacity",
        "./integration_test",
        "-run",
        "^TestTournamentPeakSoak$",
        "-timeout",
        "75m",
    ]

    if gate in {"capacity-nominal", "capacity-peak"}:
        expected = nominal if gate == "capacity-nominal" else peak
        if command != expected:
            fail(f"command does not match the {gate} allowlist")
        launcher = PINNED_ENV
        go_executable = PINNED_GO
        if pathlib.Path.cwd().resolve(strict=True) != (repo_root / "backend").resolve(strict=True):
            fail("capacity gates must run from the repository backend directory")
        identity = "TestTournamentNominal60Minute" if gate == "capacity-nominal" else "TestTournamentPeakSoak"
        record = {
            "allowlist_id": f"{gate}-v1",
            "kind": "go-test",
            "executable_basename": "env",
            "executable_sha256": sha256_file(launcher),
            "argument_count": len(command) - 1,
            "go_executable_basename": go_executable.name,
            "go_executable_sha256": sha256_file(go_executable),
            "go_test_identity": identity,
            "fixture_script_basename": None,
            "fixture_script_sha256": None,
        }
        execution = [str(launcher), *command[1:]]
        execution[3] = str(go_executable)
        return (
            4800 if gate == "capacity-nominal" else 4500,
            launcher,
            record,
            [],
            execution,
            gate,
        )

    if gate != "fixture-test":
        fail(f"unknown gate: {gate}")

    fixture_script = (repo_root / "scripts/release/tests/run-tournament-go-gate_test.sh").resolve(strict=True)
    fixed_prefix = ["bash", str(fixture_script), "--fixture-child"]
    fixture_suffixes = {
        "pass": ["pass"],
        "fail": ["fail"],
        "timeout": ["timeout"],
        "signal": ["signal"],
        "secret": ["secret", FIXTURE_CANARY],
        "capacity-report": ["capacity-report"],
        "capacity-policy-missing": ["capacity-policy", "nominal", "missing"],
        "capacity-policy-underlying-fail": [
            "capacity-policy",
            "nominal",
            "underlying-fail",
        ],
        "capacity-policy-positive-nominal": ["capacity-policy", "nominal", "positive"],
        "capacity-policy-positive-peak": ["capacity-policy", "peak", "positive"],
        "capacity-policy-duplicate": ["capacity-policy", "nominal", "duplicate"],
        "capacity-policy-invalid": ["capacity-policy", "nominal", "invalid"],
        "capacity-policy-profile-mismatch": [
            "capacity-policy",
            "nominal",
            "profile-mismatch",
        ],
        "capacity-policy-revision-mismatch": [
            "capacity-policy",
            "nominal",
            "revision-mismatch",
        ],
        "capacity-policy-threshold-fail": [
            "capacity-policy",
            "nominal",
            "threshold-fail",
        ],
        "capacity-policy-limit-mismatch": [
            "capacity-policy",
            "nominal",
            "limit-mismatch",
        ],
        "truncate": ["truncate"],
        "argv": ["argv", "argument with spaces", "literal;false"],
        "environment": ["environment"],
    }
    if not any(command == fixed_prefix + suffix for suffix in fixture_suffixes.values()):
        fail("command does not match the fixture-test allowlist")

    launcher = PINNED_BASH
    record = {
        "allowlist_id": "fixture-test-v1",
        "kind": "fixture",
        "executable_basename": launcher.name,
        "executable_sha256": sha256_file(launcher),
        "argument_count": len(command) - 1,
        "go_executable_basename": None,
        "go_executable_sha256": None,
        "go_test_identity": None,
        "fixture_script_basename": fixture_script.name,
        "fixture_script_sha256": sha256_file(fixture_script),
    }
    execution = [str(launcher), str(fixture_script), *command[2:]]
    fixture_arguments = command[len(fixed_prefix) :]
    contract_gate = None
    if fixture_arguments[:1] == ["capacity-policy"]:
        contract_gate = (
            "capacity-peak" if fixture_arguments[1] == "peak" else "capacity-nominal"
        )
    return 3, launcher, record, [FIXTURE_CANARY], execution, contract_gate


@dataclass
class Capture:
    head: bytearray = field(default_factory=bytearray)
    tail: bytearray = field(default_factory=bytearray)
    total_bytes: int = 0
    capacity_prefixes: int = 0
    prefix_tail: bytes = b""

    def append(self, block: bytes) -> None:
        prefix_window = self.prefix_tail + block
        self.capacity_prefixes += prefix_window.count(CAPACITY_REPORT_PREFIX_BYTES)
        self.prefix_tail = prefix_window[-(len(CAPACITY_REPORT_PREFIX_BYTES) - 1) :]
        self.total_bytes += len(block)
        head_remaining = CAPTURE_HEAD_LIMIT - len(self.head)
        if head_remaining > 0:
            self.head.extend(block[:head_remaining])
            block = block[head_remaining:]
        if block:
            self.tail.extend(block)
            if len(self.tail) > CAPTURE_TAIL_LIMIT:
                del self.tail[:-CAPTURE_TAIL_LIMIT]

    def materialized(self) -> bytes:
        if self.total_bytes <= CAPTURE_LIMIT:
            return bytes(self.head + self.tail)
        retained_tail = self.tail[-(CAPTURE_TAIL_LIMIT - len(CAPTURE_SEPARATOR)) :]
        return bytes(self.head + CAPTURE_SEPARATOR + retained_tail)


SENSITIVE_LINE = re.compile(
    r"(?i)(authorization|bearer|cookie|credential|password|passwd|secret|session|token|jwt|api[_-]?key|access[_-]?key|private[_-]?key)"
)
ENV_ASSIGNMENT = re.compile(r"^[A-Z_][A-Z0-9_]{1,63}=", re.ASCII)
LONG_TOKEN = re.compile(r"(?<![A-Za-z0-9+/=_-])[A-Za-z0-9+/=_-]{32,}(?![A-Za-z0-9+/=_-])")
CONTROL = re.compile(r"[^\x09\x0a\x0d\x20-\x7e]")


REPORT_LONG_TOKEN_PATHS = {
    ("identity", "revision", "commit"),
    ("identity", "build", "digest"),
    ("digests", "artifacts", "*", "digest"),
    ("digests", "images", "*", "digest"),
    ("digests", "images", "*", "reference"),
}


def has_sensitive_report_content(
    value: Any,
    sensitive_values: list[str],
    path: tuple[str, ...] = (),
) -> bool:
    if isinstance(value, dict):
        for key, child in value.items():
            if SENSITIVE_LINE.search(key) or has_sensitive_report_content(
                child,
                sensitive_values,
                (*path, key),
            ):
                return True
    elif isinstance(value, list):
        return any(
            has_sensitive_report_content(child, sensitive_values, (*path, "*"))
            for child in value
        )
    elif isinstance(value, str):
        if SENSITIVE_LINE.search(value) or ENV_ASSIGNMENT.search(value):
            return True
        if any(secret and secret in value for secret in sensitive_values):
            return True
        for match in LONG_TOKEN.finditer(value):
            if path not in REPORT_LONG_TOKEN_PATHS:
                return True
            token = match.group(0)
            if re.fullmatch(r"[0-9a-f]{40}|[0-9a-f]{64}", token) is None:
                return True
    return False


def strict_json_loads(text: str) -> Any:
    def unique_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        value: dict[str, Any] = {}
        for key, child in pairs:
            if key in value:
                raise ValueError(f"duplicate JSON key: {key}")
            value[key] = child
        return value

    def reject_constant(value: str) -> None:
        raise ValueError(f"non-finite JSON number: {value}")

    return json.loads(
        text,
        object_pairs_hook=unique_object,
        parse_constant=reject_constant,
    )


def validated_capacity_report(
    value: Any,
    sensitive_values: list[str],
    capacity_schema: dict[str, Any],
) -> tuple[str, dict[str, Any]] | None:
    if not isinstance(value, dict) or set(value) != CAPACITY_REPORT_ROOT_KEYS:
        return None
    if not isinstance(value["schema"], str) or not isinstance(value["version"], str):
        return None
    if not isinstance(value["thresholds"], list):
        return None
    if any(not isinstance(value[name], dict) for name in CAPACITY_REPORT_OBJECT_KEYS):
        return None
    try:
        validate_schema(value, capacity_schema, capacity_schema, "capacity_report")
    except RunnerError:
        return None
    if has_sensitive_report_content(value, sensitive_values):
        return None
    canonical = json.dumps(
        value,
        sort_keys=True,
        separators=(",", ":"),
        ensure_ascii=True,
        allow_nan=False,
    )
    if any(secret and secret in canonical for secret in sensitive_values):
        return None
    return CAPACITY_REPORT_PREFIX + canonical, value


@dataclass(frozen=True)
class CapacityMarker:
    start_line: int
    end_line: int
    text: str
    report: dict[str, Any]


def capacity_output_event(line: str, expected_test: str | None = None) -> str | None:
    try:
        wrapper = strict_json_loads(line)
    except (json.JSONDecodeError, ValueError):
        return None
    if not isinstance(wrapper, dict) or wrapper.get("Action") != "output":
        return None
    if expected_test is not None and (
        wrapper.get("Package") != CAPACITY_PACKAGE
        or wrapper.get("Test") != expected_test
    ):
        return None
    output = wrapper.get("Output")
    return output if isinstance(output, str) else None


def complete_capacity_marker(
    payload_text: str,
    sensitive_values: list[str],
    capacity_schema: dict[str, Any],
) -> tuple[str, dict[str, Any]] | None:
    payload_text = payload_text.rstrip("\r\n")
    if "\n" in payload_text or "\r" in payload_text:
        return None
    try:
        payload = strict_json_loads(payload_text)
    except (json.JSONDecodeError, ValueError):
        return None
    return validated_capacity_report(payload, sensitive_values, capacity_schema)


def capacity_marker_spans(
    lines: list[str],
    sensitive_values: list[str],
    capacity_schema: dict[str, Any],
    expected_test: str | None = None,
    allow_raw: bool = True,
) -> list[CapacityMarker]:
    markers: list[CapacityMarker] = []
    pending_start: int | None = None
    pending_payload = ""

    for index, line in enumerate(lines):
        visible = CONTROL.sub("?", line)
        output = capacity_output_event(visible, expected_test)

        if pending_start is not None:
            if output is None:
                pending_start = None
                pending_payload = ""
                continue
            pending_payload += output
            if output.endswith(("\n", "\r")):
                completed = complete_capacity_marker(
                    pending_payload,
                    sensitive_values,
                    capacity_schema,
                )
                if completed is not None:
                    marker_text, report = completed
                    markers.append(CapacityMarker(pending_start, index, marker_text, report))
                pending_start = None
                pending_payload = ""
            continue

        candidate = visible.rstrip("\r\n")
        if allow_raw and candidate.startswith(CAPACITY_REPORT_PREFIX):
            completed = complete_capacity_marker(
                candidate[len(CAPACITY_REPORT_PREFIX) :],
                sensitive_values,
                capacity_schema,
            )
            if completed is not None:
                marker_text, report = completed
                markers.append(CapacityMarker(index, index, marker_text, report))
            continue

        if output is None:
            continue
        marker_at = output.find(CAPACITY_REPORT_PREFIX)
        if marker_at < 0:
            continue
        leader = output[:marker_at]
        if leader and GO_TEST_LOG_PREFIX.fullmatch(leader) is None:
            continue
        pending_start = index
        pending_payload = output[marker_at + len(CAPACITY_REPORT_PREFIX) :]
        if output.endswith(("\n", "\r")):
            completed = complete_capacity_marker(
                pending_payload,
                sensitive_values,
                capacity_schema,
            )
            if completed is not None:
                marker_text, report = completed
                markers.append(CapacityMarker(index, index, marker_text, report))
            pending_start = None
            pending_payload = ""

    return markers


def allowlisted_go_event(line: str, expected_test: str) -> str | None:
    try:
        event = strict_json_loads(CONTROL.sub("?", line))
    except (json.JSONDecodeError, ValueError):
        return None
    if not isinstance(event, dict) or event.get("Package") != CAPACITY_PACKAGE:
        return None
    action = event.get("Action")
    if action not in {"start", "run", "pause", "cont", "pass", "fail", "skip", "bench"}:
        return None
    test_name = event.get("Test")
    if test_name is not None and test_name != expected_test:
        return None
    retained: dict[str, Any] = {"Action": action}
    if test_name is not None:
        retained["Test"] = test_name
    elapsed = event.get("Elapsed")
    if (
        isinstance(elapsed, (int, float))
        and not isinstance(elapsed, bool)
        and elapsed >= 0
        and (not isinstance(elapsed, float) or elapsed < float("inf"))
    ):
        retained["Elapsed"] = elapsed
    return json.dumps(retained, sort_keys=True, separators=(",", ":"), allow_nan=False)


def capacity_capture_record(capture: Capture, text: str) -> dict[str, Any]:
    encoded = text.encode("utf-8")
    sanitized_truncated = False
    if len(encoded) > CAPTURE_LIMIT:
        text = encoded[:CAPTURE_LIMIT].decode("utf-8", errors="ignore")
        encoded = text.encode("utf-8")
        sanitized_truncated = True
    return {
        "text": text,
        "truncated": capture.total_bytes > CAPTURE_LIMIT or sanitized_truncated,
        "stored_bytes": len(encoded),
        "total_bytes": capture.total_bytes,
        "sha256": hashlib.sha256(encoded).hexdigest(),
    }


def sanitize_capacity_stdout(
    capture: Capture,
    capacity_schema: dict[str, Any],
    expected_test: str,
) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    text = capture.materialized().decode("utf-8", errors="replace")
    lines = text.splitlines(keepends=True)
    capacity_markers = capacity_marker_spans(
        lines,
        [],
        capacity_schema,
        expected_test=expected_test,
        allow_raw=False,
    )
    markers_by_start = {marker.start_line: marker for marker in capacity_markers}
    covered_lines = {
        index
        for marker in capacity_markers
        for index in range(marker.start_line, marker.end_line + 1)
    }
    retained_lines: list[str] = []
    for index, line in enumerate(lines):
        marker = markers_by_start.get(index)
        if marker is not None:
            retained_lines.append(marker.text + "\n")
            continue
        if index in covered_lines:
            continue
        event = allowlisted_go_event(line, expected_test)
        if event is not None:
            retained_lines.append(event + "\n")
    return (
        capacity_capture_record(capture, "".join(retained_lines)),
        [marker.report for marker in capacity_markers],
    )


def omitted_capacity_stderr(capture: Capture) -> dict[str, Any]:
    summary = "[capacity stderr omitted]\n" if capture.total_bytes else ""
    return capacity_capture_record(capture, summary)


def sanitize_capture(
    capture: Capture,
    sensitive_values: list[str],
    capacity_schema: dict[str, Any] | None = None,
    allow_capacity_marker: bool = False,
) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    text = capture.materialized().decode("utf-8", errors="replace")
    lines = text.splitlines(keepends=True)
    capacity_markers = (
        capacity_marker_spans(lines, sensitive_values, capacity_schema)
        if allow_capacity_marker and capacity_schema is not None
        else []
    )
    markers_by_start = {marker.start_line: marker for marker in capacity_markers}
    covered_lines = {
        index
        for marker in capacity_markers
        for index in range(marker.start_line, marker.end_line + 1)
    }
    sanitized_lines: list[str] = []
    for index, line in enumerate(lines):
        marker = markers_by_start.get(index)
        if marker is not None:
            ending = "\n" if line.endswith(("\n", "\r")) else ""
            sanitized_lines.append(marker.text + ending)
            continue
        if index in covered_lines:
            continue
        visible = CONTROL.sub("?", line)
        if ENV_ASSIGNMENT.search(visible) or SENSITIVE_LINE.search(visible):
            sanitized_lines.append("[REDACTED]\n" if line.endswith(("\n", "\r")) else "[REDACTED]")
            continue
        for value in sensitive_values:
            if value:
                visible = visible.replace(value, "[REDACTED]")
        visible = LONG_TOKEN.sub("[REDACTED]", visible)
        sanitized_lines.append(visible)

    sanitized = "".join(sanitized_lines)
    encoded = sanitized.encode("utf-8")
    sanitized_truncated = False
    if len(encoded) > CAPTURE_LIMIT:
        sanitized = encoded[:CAPTURE_LIMIT].decode("utf-8", errors="ignore")
        encoded = sanitized.encode("utf-8")
        sanitized_truncated = True
    digest = hashlib.sha256(encoded).hexdigest()
    return (
        {
            "text": sanitized,
            "truncated": capture.total_bytes > CAPTURE_LIMIT or sanitized_truncated,
            "stored_bytes": len(encoded),
            "total_bytes": capture.total_bytes,
            "sha256": digest,
        },
        [marker.report for marker in capacity_markers],
    )


def capacity_report_contract_error(
    gate: str,
    reports: list[dict[str, Any]],
    marker_prefixes: int,
    source_revision: str,
    source_modified: bool,
    capacity_schema_sha256: str,
) -> str | None:
    if marker_prefixes != 1:
        return "capacity output must contain exactly one report marker"
    if len(reports) != 1:
        return "capacity report marker is incomplete or invalid"

    report = reports[0]
    expected_profile, expected_name = CAPACITY_GATE_WORKLOAD[gate]
    if report["workload"]["profile"] != expected_profile:
        return "capacity report profile does not match the gate"
    if report["workload"]["name"] != expected_name:
        return "capacity report workload does not match the gate"
    revision = report["identity"]["revision"]
    if revision["commit"] != source_revision or revision["dirty"] != source_modified:
        return "capacity report source identity does not match retained evidence"

    required_statuses = (
        report["operations"]["correctness"]["status"],
        report["latency_ms"]["status"],
        report["throughput"]["status"],
        report["lag"]["status"],
        report["reconnect"]["status"],
        report["resources"]["goroutines"]["status"],
        report["resources"]["heap_alloc_bytes"]["status"],
        report["database"]["status"],
        report["environment_equivalence"]["status"],
    )
    if any(status != "PASS" for status in required_statuses):
        return "capacity report contains a non-PASS status"
    if report["environment_equivalence"]["differences"]:
        return "capacity environment equivalence contains differences"

    operations = report["operations"]
    correctness = operations["correctness"]
    reconnect = report["reconnect"]
    if operations["total"] != operations["succeeded"] + operations["failed"]:
        return "capacity operation counters are inconsistent"
    if correctness["checks"] != correctness["passed"] + correctness["failed"]:
        return "capacity correctness counters are inconsistent"
    if reconnect["attempts"] != reconnect["succeeded"] + reconnect["failed"]:
        return "capacity reconnect counters are inconsistent"
    if reconnect["attempts"] != report["load"]["games"]:
        return "capacity reconnect attempts do not cover every game"

    latency = report["latency_ms"]
    if not (latency["p50"] <= latency["p95"] <= latency["p99"] <= latency["max"]):
        return "capacity latency percentiles are inconsistent"

    build = report["identity"]["build"]
    build_material = "\0".join(
        [
            revision["commit"],
            str(revision["dirty"]).lower(),
            build["go_version"],
            build["target"],
        ]
    ).encode("utf-8")
    expected_build_digest = "sha256:" + hashlib.sha256(build_material).hexdigest()
    if build["digest"] != expected_build_digest:
        return "capacity build digest is inconsistent"

    duration_seconds = 3600 if expected_profile == "nominal" else 2700
    workload_material = json.dumps(
        {
            "name": expected_name,
            "profile": expected_profile,
            "duration_seconds": duration_seconds,
            "participants": report["load"]["participants"],
            "games": report["load"]["games"],
            "workers": report["load"]["workers"],
            "operation_interval_ms": report["load"]["operation_interval_ms"],
        },
        separators=(",", ":"),
        ensure_ascii=True,
    ).encode("utf-8")
    expected_artifacts = {
        "tournament60-report.schema.json": "sha256:" + capacity_schema_sha256,
        expected_name + "-workload": "sha256:" + hashlib.sha256(workload_material).hexdigest(),
    }
    artifacts = report["digests"]["artifacts"]
    artifact_map = {item["name"]: item["digest"] for item in artifacts}
    if len(artifact_map) != len(artifacts) or artifact_map != expected_artifacts:
        return "capacity artifact digests are incomplete or inconsistent"
    images = report["digests"]["images"]
    if len(images) != 1 or images[0]["reference"] != "postgres:18-alpine":
        return "capacity image identity is inconsistent"

    expected_limits = {
        "nominal": {"latency": 250, "throughput": 12, "lag": 2000},
        "peak": {"latency": 400, "throughput": 60, "lag": 3000},
    }[expected_profile]
    if (
        latency["p99_limit"] != expected_limits["latency"]
        or report["throughput"]["minimum"] != expected_limits["throughput"]
        or report["lag"]["max_limit_ms"] != expected_limits["lag"]
        or report["resources"]["goroutines"]["limit"] != 128
        or report["resources"]["heap_alloc_bytes"]["limit"] != 512 * 1024 * 1024
        or report["database"]["pool_limit"] != 50
    ):
        return "capacity report limit contract failed"
    threshold_expectations = {
        "failed_operations": ("<=", 0, operations["failed"], "count"),
        "correctness_failures": ("<=", 0, correctness["failed"], "count"),
        "latency_p99": ("<=", expected_limits["latency"], latency["p99"], "milliseconds"),
        "throughput": (
            ">=",
            expected_limits["throughput"],
            report["throughput"]["operations_per_second"],
            "operations_per_second",
        ),
        "scheduling_lag_max": ("<=", expected_limits["lag"], report["lag"]["max_ms"], "milliseconds"),
        "reconnect_failures": ("<=", 0, reconnect["failed"], "count"),
        "goroutines_max": ("<=", 128, report["resources"]["goroutines"]["observed_max"], "count"),
        "heap_alloc_bytes_max": (
            "<=",
            512 * 1024 * 1024,
            report["resources"]["heap_alloc_bytes"]["observed_max"],
            "bytes",
        ),
        "database_connections_max": ("<=", 50, report["database"]["observed_max"], "count"),
    }
    thresholds = report["thresholds"]
    thresholds_by_metric = {item["metric"]: item for item in thresholds}
    if len(thresholds_by_metric) != len(thresholds):
        return "capacity report contains duplicate threshold metrics"
    if set(thresholds_by_metric) != CAPACITY_THRESHOLD_METRICS:
        return "capacity report threshold set is incomplete"
    for metric, expected in threshold_expectations.items():
        threshold = thresholds_by_metric[metric]
        actual = (
            threshold["comparator"],
            threshold["limit"],
            threshold["observed"],
            threshold["unit"],
        )
        if actual != expected or threshold["status"] != "PASS":
            return "capacity report threshold contract failed"
        comparator, limit, observed, _unit = expected
        if comparator == "<=" and observed > limit:
            return "capacity report threshold observation exceeds its limit"
        if comparator == ">=" and observed < limit:
            return "capacity report threshold observation is below its limit"
    return None


def signal_name(number: int | None) -> str | None:
    if number is None:
        return None
    try:
        return signal.Signals(number).name
    except ValueError:
        return f"SIG{number}"


def run_command(
    command: list[str],
    launcher: pathlib.Path,
    timeout_seconds: int,
    environment: dict[str, str],
) -> tuple[int, bool, int | None, str | None, Capture, Capture, dt.datetime, dt.datetime, int]:
    started_at = dt.datetime.now(dt.timezone.utc)
    started_monotonic = time.monotonic()
    received: list[int] = []
    state: dict[str, Any] = {"kill_deadline": None, "process": None}

    def forward(signum: int, _frame: Any) -> None:
        if not received:
            received.append(signum)
        active_process = state["process"]
        if active_process is not None and active_process.poll() is None:
            try:
                os.killpg(active_process.pid, signum)
            except ProcessLookupError:
                return
            state["kill_deadline"] = time.monotonic() + TERMINATION_GRACE_SECONDS

    previous_int = signal.signal(signal.SIGINT, forward)
    previous_term = signal.signal(signal.SIGTERM, forward)
    try:
        process = subprocess.Popen(
            command,
            executable=str(launcher),
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=environment,
            start_new_session=True,
            close_fds=True,
        )
    except OSError as exc:
        signal.signal(signal.SIGINT, previous_int)
        signal.signal(signal.SIGTERM, previous_term)
        fail(f"allowlisted command could not start: {exc}")
    state["process"] = process
    if received and process.poll() is None:
        try:
            os.killpg(process.pid, received[0])
        except ProcessLookupError:
            pass
        else:
            state["kill_deadline"] = time.monotonic() + TERMINATION_GRACE_SECONDS
    selector = selectors.DefaultSelector()
    stdout_capture = Capture()
    stderr_capture = Capture()
    assert process.stdout is not None
    assert process.stderr is not None
    os.set_blocking(process.stdout.fileno(), False)
    os.set_blocking(process.stderr.fileno(), False)
    selector.register(process.stdout, selectors.EVENT_READ, stdout_capture)
    selector.register(process.stderr, selectors.EVENT_READ, stderr_capture)
    deadline = started_monotonic + timeout_seconds
    timed_out = False
    kill_sent = False

    try:
        while selector.get_map() or process.poll() is None:
            now = time.monotonic()
            if not timed_out and process.poll() is None and now >= deadline:
                timed_out = True
                try:
                    os.killpg(process.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
                state["kill_deadline"] = now + TERMINATION_GRACE_SECONDS

            kill_deadline = state["kill_deadline"]
            if (
                kill_deadline is not None
                and not kill_sent
                and process.poll() is None
                and now >= kill_deadline
            ):
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                kill_sent = True

            events = selector.select(0.05)
            for key, _mask in events:
                try:
                    block = os.read(key.fileobj.fileno(), 8192)
                except BlockingIOError:
                    continue
                if block:
                    key.data.append(block)
                else:
                    selector.unregister(key.fileobj)
                    key.fileobj.close()
    finally:
        selector.close()
        state["process"] = None
        signal.signal(signal.SIGINT, previous_int)
        signal.signal(signal.SIGTERM, previous_term)

    return_code = process.wait()
    finished_monotonic = time.monotonic()
    finished_at = dt.datetime.now(dt.timezone.utc)
    termination = signal_name(-return_code) if return_code < 0 else None
    duration_ms = max(0, round((finished_monotonic - started_monotonic) * 1000))
    return (
        return_code,
        timed_out,
        received[0] if received else None,
        termination,
        stdout_capture,
        stderr_capture,
        started_at,
        finished_at,
        duration_ms,
    )


def json_type_matches(value: Any, expected: str) -> bool:
    if expected == "object":
        return isinstance(value, dict)
    if expected == "array":
        return isinstance(value, list)
    if expected == "string":
        return isinstance(value, str)
    if expected == "integer":
        return isinstance(value, int) and not isinstance(value, bool)
    if expected == "number":
        return isinstance(value, (int, float)) and not isinstance(value, bool)
    if expected == "boolean":
        return isinstance(value, bool)
    if expected == "null":
        return value is None
    return False


def resolve_ref(schema: dict[str, Any], reference: str) -> dict[str, Any]:
    if not reference.startswith("#/"):
        fail(f"unsupported schema reference: {reference}")
    node: Any = schema
    for part in reference[2:].split("/"):
        part = part.replace("~1", "/").replace("~0", "~")
        if not isinstance(node, dict) or part not in node:
            fail(f"unresolved schema reference: {reference}")
        node = node[part]
    if not isinstance(node, dict):
        fail(f"schema reference is not an object: {reference}")
    return node


def validate_schema(value: Any, rule: dict[str, Any], schema: dict[str, Any], path: str) -> None:
    if "$ref" in rule:
        validate_schema(value, resolve_ref(schema, rule["$ref"]), schema, path)
        return

    for candidate in rule.get("allOf", []):
        validate_schema(value, candidate, schema, path)
    if "anyOf" in rule:
        matched = False
        for candidate in rule["anyOf"]:
            try:
                validate_schema(value, candidate, schema, path)
            except RunnerError:
                continue
            matched = True
            break
        if not matched:
            fail(f"{path} does not match any allowed schema branch")
    if "if" in rule:
        condition_matches = True
        try:
            validate_schema(value, rule["if"], schema, path)
        except RunnerError:
            condition_matches = False
        branch = rule.get("then") if condition_matches else rule.get("else")
        if branch is not None:
            validate_schema(value, branch, schema, path)

    declared_type = rule.get("type")
    if declared_type is not None:
        allowed = declared_type if isinstance(declared_type, list) else [declared_type]
        if not any(json_type_matches(value, expected) for expected in allowed):
            fail(f"{path} has the wrong type")
    if "const" in rule and value != rule["const"]:
        fail(f"{path} does not match its schema constant")
    if "enum" in rule and value not in rule["enum"]:
        fail(f"{path} has an unsupported value")

    if isinstance(value, dict):
        properties = rule.get("properties", {})
        for required in rule.get("required", []):
            if required not in value:
                fail(f"{path}.{required} is required")
        if rule.get("additionalProperties") is False:
            extras = sorted(set(value) - set(properties))
            if extras:
                fail(f"{path} has unknown field: {extras[0]}")
        for name, child in value.items():
            if name in properties:
                validate_schema(child, properties[name], schema, f"{path}.{name}")

    if isinstance(value, list):
        minimum_items = rule.get("minItems")
        maximum_items = rule.get("maxItems")
        if minimum_items is not None and len(value) < minimum_items:
            fail(f"{path} has too few items")
        if maximum_items is not None and len(value) > maximum_items:
            fail(f"{path} has too many items")
        if rule.get("uniqueItems"):
            serialized_items = [
                json.dumps(item, sort_keys=True, separators=(",", ":"), allow_nan=False)
                for item in value
            ]
            if len(set(serialized_items)) != len(serialized_items):
                fail(f"{path} has duplicate items")
        if "items" in rule:
            for index, child in enumerate(value):
                validate_schema(child, rule["items"], schema, f"{path}[{index}]")

    if isinstance(value, str):
        pattern = rule.get("pattern")
        if pattern is not None and re.search(pattern, value) is None:
            fail(f"{path} does not match its schema pattern")
        minimum = rule.get("minLength")
        if minimum is not None and len(value) < minimum:
            fail(f"{path} is shorter than allowed")
        maximum = rule.get("maxLength")
        if maximum is not None and len(value) > maximum:
            fail(f"{path} is longer than allowed")
        if rule.get("format") == "date-time":
            try:
                dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
            except ValueError:
                fail(f"{path} is not an RFC 3339 timestamp")

    if isinstance(value, (int, float)) and not isinstance(value, bool):
        minimum = rule.get("minimum")
        maximum = rule.get("maximum")
        exclusive_minimum = rule.get("exclusiveMinimum")
        if minimum is not None and value < minimum:
            fail(f"{path} is less than allowed")
        if maximum is not None and value > maximum:
            fail(f"{path} is greater than allowed")
        if exclusive_minimum is not None and value <= exclusive_minimum:
            fail(f"{path} is not greater than allowed")


def atomic_write(directory: pathlib.Path, final_name: str, content: bytes) -> pathlib.Path:
    file_descriptor, temporary_name = tempfile.mkstemp(
        prefix=".tournament-go-gate-",
        suffix=".tmp",
        dir=directory,
    )
    temporary = pathlib.Path(temporary_name)
    try:
        os.fchmod(file_descriptor, 0o600)
        with os.fdopen(file_descriptor, "wb") as destination:
            destination.write(content)
            destination.flush()
            os.fsync(destination.fileno())
        final_path = directory / final_name
        os.link(temporary, final_path)
        temporary.unlink()
        directory_descriptor = os.open(directory, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory_descriptor)
        finally:
            os.close(directory_descriptor)
        return final_path
    except Exception:
        try:
            os.close(file_descriptor)
        except OSError:
            pass
        try:
            temporary.unlink()
        except FileNotFoundError:
            pass
        raise


def evidence_digest(record: dict[str, Any]) -> str:
    normalized = dict(record)
    normalized["evidence_sha256"] = "0" * 64
    serialized = json.dumps(
        normalized,
        sort_keys=True,
        separators=(",", ":"),
        ensure_ascii=True,
    ).encode("utf-8")
    return hashlib.sha256(serialized).hexdigest()


def retain_record(
    evidence_dir: pathlib.Path,
    gate: str,
    record: dict[str, Any],
    schema: dict[str, Any],
) -> pathlib.Path:
    stamp = record["started_at"].replace("-", "").replace(":", "").replace(".", "")
    final_path: pathlib.Path | None = None
    serialized = b""
    for counter in range(1, 1000):
        final_name = f"tournament-{gate}-{stamp}-{os.getpid()}-{counter:02d}.json"
        record["evidence_uri"] = f"tournament-evidence://{final_name}"
        record["evidence_sha256"] = evidence_digest(record)
        validate_schema(record, schema, schema, "record")
        serialized = (
            json.dumps(record, indent=2, sort_keys=True, ensure_ascii=True) + "\n"
        ).encode("utf-8")
        try:
            final_path = atomic_write(evidence_dir, final_name, serialized)
            break
        except FileExistsError:
            continue
    if final_path is None:
        fail("could not allocate a collision-safe evidence filename")

    file_digest = hashlib.sha256(serialized).hexdigest()
    checksum = f"{file_digest}  {final_path.name}\n".encode("ascii")
    try:
        atomic_write(evidence_dir, final_path.name + ".sha256", checksum)
    except Exception as exc:
        fail(f"record retained without checksum sidecar: {exc}")
    return final_path


repo_root = pathlib.Path(sys.argv[1])
schema_path = pathlib.Path(sys.argv[2])
capacity_schema_path = (
    repo_root / "backend/integration_test/testdata/capacity/tournament60-report.schema.json"
)
gate_name = sys.argv[3]
evidence_text = sys.argv[4]
command = sys.argv[5:]

try:
    validate_pinned_python()
    for executable_name, executable_path, executable_sha256 in (
        ("Go", PINNED_GO, PINNED_GO_SHA256),
        ("Git", PINNED_GIT, PINNED_GIT_SHA256),
        ("env", PINNED_ENV, PINNED_ENV_SHA256),
        ("Bash", PINNED_BASH, PINNED_BASH_SHA256),
    ):
        validate_pinned_executable(
            executable_name,
            executable_path,
            executable_sha256,
        )
    evidence_dir = validate_evidence_dir(evidence_text, repo_root)
    if (
        not schema_path.is_file()
        or schema_path.is_symlink()
        or schema_path.resolve(strict=True) != schema_path
    ):
        fail("record schema is missing or unsafe")
    try:
        schema = strict_json_loads(schema_path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError, ValueError) as exc:
        fail(f"record schema cannot be read: {exc}")
    if not isinstance(schema, dict):
        fail("record schema root must be an object")
    if schema.get("$schema") != "https://json-schema.org/draft/2020-12/schema":
        fail("record schema must declare Draft 2020-12")
    if schema.get("$id") != RECORD_SCHEMA_JSON_ID:
        fail("record schema identity does not match the canonical contract")
    if (
        schema.get("type") != "object"
        or schema.get("additionalProperties") is not False
        or not isinstance(schema.get("required"), list)
        or set(schema["required"]) != RECORD_ROOT_KEYS
        or not isinstance(schema.get("properties"), dict)
        or set(schema["properties"]) != RECORD_ROOT_KEYS
    ):
        fail("record schema root contract is invalid")
    if (
        not capacity_schema_path.is_file()
        or capacity_schema_path.is_symlink()
        or capacity_schema_path.resolve(strict=True) != capacity_schema_path
    ):
        fail("capacity report schema is missing or unsafe")
    try:
        capacity_schema = strict_json_loads(capacity_schema_path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError, ValueError) as exc:
        fail(f"capacity report schema cannot be read: {exc}")
    if not isinstance(capacity_schema, dict):
        fail("capacity report schema root must be an object")
    if capacity_schema.get("$schema") != "https://json-schema.org/draft/2020-12/schema":
        fail("capacity report schema must declare Draft 2020-12")
    if capacity_schema.get("$id") != "https://task-per-minute.local/schemas/tournament60-report.schema.json":
        fail("capacity report schema identity does not match the canonical contract")
    if (
        capacity_schema.get("type") != "object"
        or capacity_schema.get("additionalProperties") is not False
        or not isinstance(capacity_schema.get("required"), list)
        or set(capacity_schema["required"]) != CAPACITY_REPORT_ROOT_KEYS
        or not isinstance(capacity_schema.get("properties"), dict)
        or set(capacity_schema["properties"]) != CAPACITY_REPORT_ROOT_KEYS
    ):
        fail("capacity report schema root contract is invalid")

    environment = child_environment()
    source_revision, source_modified = source_identity(repo_root)
    artifacts = artifact_digest_records(
        gate_name,
        repo_root,
        schema_path,
    )
    (
        timeout_seconds,
        launcher,
        command_record,
        sensitive_values,
        execution_command,
        capacity_contract_gate,
    ) = command_policy(gate_name, command, repo_root)
    (
        return_code,
        timed_out,
        forwarded_number,
        termination_signal,
        stdout_capture,
        stderr_capture,
        started_at,
        finished_at,
        duration_ms,
    ) = run_command(execution_command, launcher, timeout_seconds, environment)

    if timed_out:
        outcome = "timeout"
    elif forwarded_number is not None or return_code < 0:
        outcome = "signal"
    elif return_code == 0:
        outcome = "pass"
    else:
        outcome = "fail"

    if capacity_contract_gate is not None:
        expected_test = (
            "TestTournamentPeakSoak"
            if capacity_contract_gate == "capacity-peak"
            else "TestTournamentNominal60Minute"
        )
        stdout_record, capacity_reports = sanitize_capacity_stdout(
            stdout_capture,
            capacity_schema,
            expected_test,
        )
        stderr_record = omitted_capacity_stderr(stderr_capture)
    else:
        stdout_record, capacity_reports = sanitize_capture(
            stdout_capture,
            sensitive_values,
            capacity_schema,
            allow_capacity_marker=True,
        )
        stderr_record, _stderr_reports = sanitize_capture(stderr_capture, sensitive_values)
    if outcome == "pass" and capacity_contract_gate is not None:
        contract_error = capacity_report_contract_error(
            capacity_contract_gate,
            capacity_reports,
            stdout_capture.capacity_prefixes,
            source_revision,
            source_modified,
            sha256_file(capacity_schema_path),
        )
        if contract_error is not None:
            outcome = "evidence-contract"

    exit_code = return_code if return_code >= 0 else 128 + (-return_code)
    result = "PASS" if outcome == "pass" else "FAIL"
    machine = platform.machine().lower()
    architecture = {"x86_64": "amd64", "aarch64": "arm64"}.get(machine, machine)

    record = {
        "schema": SCHEMA_ID,
        "schema_version": 1,
        "gate": gate_name,
        "source_revision": source_revision,
        "source_modified": source_modified,
        "artifact_digests": artifacts,
        "result": result,
        "exit_code": exit_code,
        "evidence_uri": "",
        "evidence_sha256": "0" * 64,
        "summary": f"allowlisted {gate_name} retained {outcome} outcome",
        "owner": f"uid:{os.getuid()}",
        "environment": {
            "os": platform.system().lower(),
            "arch": architecture,
            "cgo_enabled": environment["CGO_ENABLED"] == "1",
            "working_directory": "backend" if gate_name != "fixture-test" else "repository-fixture",
        },
        "timeout_seconds": timeout_seconds,
        "started_at": timestamp(started_at),
        "finished_at": timestamp(finished_at),
        "duration_ms": duration_ms,
        "outcome": outcome,
        "exit_status": return_code if return_code >= 0 else None,
        "termination_signal": termination_signal,
        "forwarded_signal": signal_name(forwarded_number),
        "timed_out": timed_out,
        "command": command_record,
        "stdout": stdout_record,
        "stderr": stderr_record,
    }
    final_path = retain_record(evidence_dir, gate_name, record, schema)
    print(f"tournament go gate retained: {final_path.name} ({outcome})")
except RunnerError as exc:
    print(f"tournament go gate: ERROR: {exc}", file=sys.stderr)
    sys.exit(1)
except (OSError, ValueError) as exc:
    print(f"tournament go gate: ERROR: evidence retention failed: {exc}", file=sys.stderr)
    sys.exit(1)
PY
