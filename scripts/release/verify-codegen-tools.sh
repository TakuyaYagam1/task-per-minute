#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: verify-codegen-tools.sh [options]

Verify the pinned sqlc and Wire binaries without downloading or installing them.

Options:
  --lock PATH       Tool lock manifest.
  --schema PATH     JSON schema for the manifest.
  --go-mod PATH     Backend go.mod containing the generator tool directives.
  --bin-dir PATH    Directory containing sqlc and wire. PATH is used by default.
  --archives PATH   Also verify the locked release archives in this directory.
  -h, --help        Show this help.
EOF
}

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd -- "$script_dir/../.." && pwd -P)"
lock_path="$repo_root/security/tools/codegen-tools.lock.json"
schema_path="$repo_root/security/tools/codegen-tools.schema.json"
go_mod_path="$repo_root/backend/go.mod"
bin_dir=""
archive_dir=""

while (($# > 0)); do
  case "$1" in
    --lock)
      (($# >= 2)) || { echo "codegen preflight: ERROR: --lock requires a path" >&2; exit 2; }
      lock_path="$2"
      shift 2
      ;;
    --schema)
      (($# >= 2)) || { echo "codegen preflight: ERROR: --schema requires a path" >&2; exit 2; }
      schema_path="$2"
      shift 2
      ;;
    --go-mod)
      (($# >= 2)) || { echo "codegen preflight: ERROR: --go-mod requires a path" >&2; exit 2; }
      go_mod_path="$2"
      shift 2
      ;;
    --bin-dir)
      (($# >= 2)) || { echo "codegen preflight: ERROR: --bin-dir requires a path" >&2; exit 2; }
      bin_dir="$2"
      shift 2
      ;;
    --archives)
      (($# >= 2)) || { echo "codegen preflight: ERROR: --archives requires a path" >&2; exit 2; }
      archive_dir="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "codegen preflight: ERROR: unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

python_bin="$(command -v python3 || true)"
if [[ -z "$python_bin" ]]; then
  echo "codegen preflight: ERROR: python3 is required" >&2
  exit 1
fi

exec "$python_bin" - "$schema_path" "$lock_path" "$go_mod_path" "$bin_dir" "$archive_dir" <<'PY'
from __future__ import annotations

import datetime as dt
import hashlib
import json
import os
import pathlib
import platform
import re
import shutil
import stat
import subprocess
import sys
import tarfile
from typing import Any


class VerificationError(Exception):
    pass


def fail(message: str) -> None:
    raise VerificationError(message)


def read_json(path_text: str, label: str) -> dict[str, Any]:
    path = pathlib.Path(path_text)
    if not path.is_file():
        fail(f"{label} is not a regular file")
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        fail(f"cannot parse {label}: {exc}")
    if not isinstance(value, dict):
        fail(f"{label} root must be an object")
    return value


def json_type_matches(value: Any, expected: str) -> bool:
    if expected == "object":
        return isinstance(value, dict)
    if expected == "array":
        return isinstance(value, list)
    if expected == "string":
        return isinstance(value, str)
    if expected == "integer":
        return isinstance(value, int) and not isinstance(value, bool)
    if expected == "boolean":
        return isinstance(value, bool)
    return False


def resolve_ref(schema: dict[str, Any], ref: str) -> dict[str, Any]:
    if not ref.startswith("#/"):
        fail(f"unsupported schema reference: {ref}")
    node: Any = schema
    for part in ref[2:].split("/"):
        part = part.replace("~1", "/").replace("~0", "~")
        if not isinstance(node, dict) or part not in node:
            fail(f"unresolved schema reference: {ref}")
        node = node[part]
    if not isinstance(node, dict):
        fail(f"schema reference is not an object: {ref}")
    return node


def validate_schema(value: Any, rule: dict[str, Any], schema: dict[str, Any], path: str) -> None:
    if "$ref" in rule:
        validate_schema(value, resolve_ref(schema, rule["$ref"]), schema, path)
        return

    expected_type = rule.get("type")
    if expected_type is not None and not json_type_matches(value, expected_type):
        fail(f"{path} must be {expected_type}")

    if "const" in rule and value != rule["const"]:
        fail(f"{path} must equal {rule['const']!r}")
    if "enum" in rule and value not in rule["enum"]:
        fail(f"{path} has an unsupported value")

    if isinstance(value, dict):
        required = rule.get("required", [])
        for key in required:
            if key not in value:
                fail(f"{path}.{key} is required")
        properties = rule.get("properties", {})
        if rule.get("additionalProperties") is False:
            extras = sorted(set(value) - set(properties))
            if extras:
                fail(f"{path} has unknown field: {extras[0]}")
        for key, child in value.items():
            if key in properties:
                validate_schema(child, properties[key], schema, f"{path}.{key}")

    if isinstance(value, list):
        minimum = rule.get("minItems")
        maximum = rule.get("maxItems")
        if minimum is not None and len(value) < minimum:
            fail(f"{path} has fewer than {minimum} items")
        if maximum is not None and len(value) > maximum:
            fail(f"{path} has more than {maximum} items")
        if rule.get("uniqueItems"):
            normalized = [json.dumps(item, sort_keys=True, separators=(",", ":")) for item in value]
            if len(normalized) != len(set(normalized)):
                fail(f"{path} contains duplicate items")
        if "items" in rule:
            for index, child in enumerate(value):
                validate_schema(child, rule["items"], schema, f"{path}[{index}]")

    if isinstance(value, str):
        minimum = rule.get("minLength")
        if minimum is not None and len(value) < minimum:
            fail(f"{path} is shorter than {minimum} characters")
        pattern_value = rule.get("pattern")
        if pattern_value is not None and re.search(pattern_value, value) is None:
            fail(f"{path} does not match its schema pattern")
        if rule.get("format") == "date":
            try:
                dt.date.fromisoformat(value)
            except ValueError:
                fail(f"{path} is not an ISO date")

    if isinstance(value, int) and not isinstance(value, bool):
        minimum = rule.get("minimum")
        if minimum is not None and value < minimum:
            fail(f"{path} is less than {minimum}")


def nested(value: dict[str, Any], dotted_path: str) -> Any:
    node: Any = value
    for part in dotted_path.split("."):
        if not isinstance(node, dict) or part not in node:
            fail(f"missing policy field: {dotted_path}")
        node = node[part]
    return node


def sha256_file(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def run_probe(command: list[str], label: str) -> str:
    env = {
        "LANG": "C",
        "LC_ALL": "C",
        "PATH": os.environ.get("PATH", ""),
    }
    try:
        completed = subprocess.run(
            command,
            check=False,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            env=env,
            timeout=10,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        fail(f"{label} could not run: {exc}")
    if completed.returncode != 0:
        fail(f"{label} exited with status {completed.returncode}")
    return completed.stdout.strip()


def go_build_info(go_binary: str, executable: pathlib.Path, tool: dict[str, Any]) -> None:
    output = run_probe([go_binary, "version", "-m", str(executable)], f"{tool['name']} Go build probe")
    lines = output.splitlines()
    if not lines:
        fail(f"{tool['name']} Go build probe returned no output")

    compiler_match = re.search(r": (go[0-9]+\.[0-9]+\.[0-9]+)$", lines[0])
    if compiler_match is None or compiler_match.group(1) != tool["executable"]["go_version"]:
        fail(f"{tool['name']} Go compiler version mismatch")

    fields: dict[str, list[str]] = {}
    for line in lines[1:]:
        parts = line.strip().split("\t")
        if len(parts) >= 2:
            fields[parts[0]] = parts[1:]

    command_module = fields.get("path", [None])[0]
    if command_module != tool["executable"]["command_module"]:
        fail(f"{tool['name']} command module mismatch")

    module_fields = fields.get("mod", [])
    if len(module_fields) < 2:
        fail(f"{tool['name']} Go module metadata is missing")
    if module_fields[0] != tool["executable"]["go_module"]:
        fail(f"{tool['name']} Go module path mismatch")
    if module_fields[1] != tool["executable"]["go_module_version"]:
        fail(f"{tool['name']} Go module version mismatch")

    expected_sum = tool["executable"]["build"]["module_sum"]
    if expected_sum != "not-applicable":
        if len(module_fields) < 3 or module_fields[2] != expected_sum:
            fail(f"{tool['name']} Go module sum mismatch")


def verify_archive(archive_root: pathlib.Path, tool: dict[str, Any]) -> None:
    archive = tool["archive"]
    path = archive_root / archive["file_name"]
    if not path.is_file():
        fail(f"{tool['name']} archive is missing")
    if sha256_file(path) != archive["sha256"]:
        fail(f"{tool['name']} archive SHA-256 mismatch")

    try:
        with tarfile.open(path, mode="r:gz") as bundle:
            members = bundle.getmembers()
            declared = archive["extraction_member"].rstrip("/")
            declared_member = None
            for member in members:
                name = member.name.rstrip("/")
                posix_path = pathlib.PurePosixPath(name)
                if member.name.startswith("/") or "\\" in member.name or ".." in posix_path.parts:
                    fail(f"{tool['name']} archive contains an unsafe path")
                if member.issym() or member.islnk() or member.isdev():
                    fail(f"{tool['name']} archive contains an unsafe member type")
                if name == declared:
                    declared_member = member

            if declared_member is None:
                fail(f"{tool['name']} declared extraction member is missing")
            if archive["kind"] == "prebuilt":
                if not declared_member.isfile():
                    fail(f"{tool['name']} declared executable member is not a file")
                if declared_member.mode & (stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH) == 0:
                    fail(f"{tool['name']} declared executable member is not executable")
                extracted = bundle.extractfile(declared_member)
                if extracted is None:
                    fail(f"{tool['name']} declared executable member cannot be read")
                member_digest = hashlib.sha256()
                for block in iter(lambda: extracted.read(1024 * 1024), b""):
                    member_digest.update(block)
                if member_digest.hexdigest() != tool["executable"]["sha256"]:
                    fail(f"{tool['name']} archive executable SHA-256 mismatch")
            elif not declared_member.isdir():
                fail(f"{tool['name']} declared source member is not a directory")
    except (OSError, tarfile.TarError) as exc:
        fail(f"{tool['name']} archive is invalid: {exc}")


def parse_go_mod_tool_versions(path_text: str) -> dict[str, str]:
    path = pathlib.Path(path_text)
    if not path.is_file():
        fail("backend go.mod is not a regular file")
    text = path.read_text(encoding="utf-8")
    tools = {
        "sqlc": (
            "github.com/sqlc-dev/sqlc",
            "github.com/sqlc-dev/sqlc/cmd/sqlc",
        ),
        "wire": (
            "github.com/google/wire",
            "github.com/google/wire/cmd/wire",
        ),
    }
    versions: dict[str, str] = {}
    for tool_name, (module_path, command_path) in tools.items():
        tool_match = re.search(rf"^\s*{re.escape(command_path)}\s*$", text, re.MULTILINE)
        if tool_match is None:
            fail(f"backend go.mod does not declare the {tool_name} tool")
        match = re.search(rf"^\s*{re.escape(module_path)}\s+(v\S+)", text, re.MULTILINE)
        if match is None:
            fail(f"backend go.mod does not require the {tool_name} module")
        versions[tool_name] = match.group(1)
    return versions


schema_path, lock_path, go_mod_path, bin_dir_text, archive_dir_text = sys.argv[1:]

try:
    schema = read_json(schema_path, "schema")
    lock = read_json(lock_path, "lock manifest")
    validate_schema(lock, schema, schema, "lock")

    expected_policy: dict[str, dict[str, Any]] = {
        "sqlc": {
            "version": "v1.31.1",
            "executable_name": "sqlc",
            "source.repository_url": "https://github.com/sqlc-dev/sqlc",
            "source.release_url": "https://github.com/sqlc-dev/sqlc/releases/tag/v1.31.1",
            "source.tag": "v1.31.1",
            "source.commit_sha": "a95e91d70ad9e1181253c333a1cfdd75ae4b95a5",
            "source.license.spdx": "MIT",
            "source.license.file_url": "https://raw.githubusercontent.com/sqlc-dev/sqlc/v1.31.1/LICENSE",
            "archive.kind": "prebuilt",
            "archive.url": "https://github.com/sqlc-dev/sqlc/releases/download/v1.31.1/sqlc_1.31.1_linux_amd64.tar.gz",
            "archive.file_name": "sqlc_1.31.1_linux_amd64.tar.gz",
            "archive.extraction_member": "sqlc",
            "executable.command_module": "github.com/sqlc-dev/sqlc/cmd/sqlc",
            "executable.go_module": "github.com/sqlc-dev/sqlc",
            "executable.go_module_version": "v1.31.1",
            "maintenance_review.status": "active",
            "security_review.result": "findings_accepted_for_offline_codegen",
            "trust_decision.status": "accepted_with_constraints",
        },
        "wire": {
            "version": "v0.7.0",
            "executable_name": "wire",
            "source.repository_url": "https://github.com/google/wire",
            "source.release_url": "https://github.com/google/wire/releases/tag/v0.7.0",
            "source.tag": "v0.7.0",
            "source.commit_sha": "9c25c9016f6825302537c4efdd5e897976f9c826",
            "source.license.spdx": "Apache-2.0",
            "source.license.file_url": "https://raw.githubusercontent.com/google/wire/v0.7.0/LICENSE",
            "archive.kind": "source",
            "archive.url": "https://github.com/google/wire/archive/refs/tags/v0.7.0.tar.gz",
            "archive.file_name": "wire_v0.7.0.tar.gz",
            "archive.extraction_member": "wire-0.7.0/",
            "executable.command_module": "github.com/google/wire/cmd/wire",
            "executable.go_module": "github.com/google/wire",
            "executable.go_module_version": "v0.7.0",
            "maintenance_review.status": "archived_unmaintained",
            "security_review.result": "pass",
            "trust_decision.status": "accepted_with_constraints",
        },
    }

    tools = lock["tools"]
    tools_by_name = {tool["name"]: tool for tool in tools}
    if len(tools_by_name) != len(tools) or set(tools_by_name) != set(expected_policy):
        fail("lock manifest must contain exactly one sqlc and one wire record")

    for name, policy in expected_policy.items():
        tool = tools_by_name[name]
        for field, expected in policy.items():
            actual = tool[field] if "." not in field else nested(tool, field)
            if actual != expected:
                fail(f"{name} policy mismatch: {field}")

    host_os = platform.system().lower()
    host_arch = {"x86_64": "amd64", "aarch64": "arm64"}.get(platform.machine(), platform.machine())
    if lock["platform"] != {"os": host_os, "arch": host_arch}:
        fail("host platform does not match the lock manifest")

    go_mod_versions = parse_go_mod_tool_versions(go_mod_path)
    for name, expected_version in go_mod_versions.items():
        if tools_by_name[name]["version"] != expected_version:
            fail(f"{name} version does not match backend go.mod")

    go_binary = shutil.which("go")
    if go_binary is None:
        fail("go is required for executable build metadata verification")

    bin_root = pathlib.Path(bin_dir_text) if bin_dir_text else None
    archive_root = pathlib.Path(archive_dir_text) if archive_dir_text else None
    for name in ("sqlc", "wire"):
        tool = tools_by_name[name]
        if bin_root is not None:
            executable = bin_root / tool["executable_name"]
        else:
            discovered = shutil.which(tool["executable_name"])
            if discovered is None:
                fail(f"{name} executable is not available")
            executable = pathlib.Path(discovered)

        try:
            resolved = executable.resolve(strict=True)
        except OSError as exc:
            fail(f"{name} executable cannot be resolved: {exc}")
        if not resolved.is_file() or not os.access(resolved, os.X_OK):
            fail(f"{name} executable is not a regular executable file")
        if sha256_file(resolved) != tool["executable"]["sha256"]:
            fail(f"{name} executable SHA-256 mismatch")

        probe = tool["executable"]["version_probe"]
        output = run_probe(
            [str(resolved), *probe["arguments"]],
            f"{name} version probe",
        )
        if probe["expected_stdout"] not in output.splitlines():
            fail(f"{name} version probe mismatch")
        go_build_info(go_binary, resolved, tool)

        if archive_root is not None:
            verify_archive(archive_root, tool)

        print(f"verified {name} {tool['version']}")

    if archive_root is None:
        print("archive verification skipped: pass --archives to verify local release archives")
    else:
        print("verified locked release archives")
    print("codegen tool preflight passed")
except VerificationError as exc:
    print(f"codegen preflight: ERROR: {exc}", file=sys.stderr)
    sys.exit(1)
PY
