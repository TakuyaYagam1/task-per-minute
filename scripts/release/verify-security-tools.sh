#!/usr/bin/env bash
set -euo pipefail

script_source="${BASH_SOURCE[0]}"
if [[ "$script_source" == */* ]]; then
  script_parent="${script_source%/*}"
else
  script_parent='.'
fi
script_dir="$(cd -- "$script_parent" && pwd -P)"
repo_root="$(cd -- "$script_dir/../.." && pwd -P)"
schema_path="$repo_root/security/tools/release-tools.schema.json"
lock_path="$repo_root/security/tools/release-tools.lock.json"
project_root="$repo_root"
test_root=""
archive_root=""
image_inventory=""
now_override=""
canonical=1
report_label='release security preflight'

if (($# > 0)); then
  if [[ "${TPM_SECURITY_PREFLIGHT_TEST_MODE:-}" != "1" ]]; then
    echo "release security preflight: ERROR: canonical mode accepts no arguments" >&2
    exit 2
  fi
  canonical=0
  report_label='release security fixture'
  while (($# > 0)); do
    case "$1" in
      --lock)
        (($# >= 2)) || { echo "$report_label: ERROR: --lock requires a path" >&2; exit 2; }
        lock_path="$2"
        shift 2
        ;;
      --schema)
        (($# >= 2)) || { echo "$report_label: ERROR: --schema requires a path" >&2; exit 2; }
        schema_path="$2"
        shift 2
        ;;
      --project-root)
        (($# >= 2)) || { echo "$report_label: ERROR: --project-root requires a path" >&2; exit 2; }
        project_root="$2"
        shift 2
        ;;
      --test-root)
        (($# >= 2)) || { echo "$report_label: ERROR: --test-root requires a path" >&2; exit 2; }
        test_root="$2"
        shift 2
        ;;
      --archive-root)
        (($# >= 2)) || { echo "$report_label: ERROR: --archive-root requires a path" >&2; exit 2; }
        archive_root="$2"
        shift 2
        ;;
      --image-inventory)
        (($# >= 2)) || { echo "$report_label: ERROR: --image-inventory requires a path" >&2; exit 2; }
        image_inventory="$2"
        shift 2
        ;;
      --now)
        (($# >= 2)) || { echo "$report_label: ERROR: --now requires a value" >&2; exit 2; }
        now_override="$2"
        shift 2
        ;;
      *)
        echo "$report_label: ERROR: unknown test argument: $1" >&2
        exit 2
        ;;
    esac
  done
fi

python_bin='/nix/store/gxzhl7aaiid7zp3y47jqqiq7zg5mqpwp-python3-3.14.6/bin/python3.14'
if [[ ! -f "$python_bin" || ! -x "$python_bin" || -L "$python_bin" ]]; then
  echo "$report_label: ERROR: reviewed Python verifier runtime is unavailable" >&2
  exit 1
fi

exec "$python_bin" -I - \
  "$schema_path" \
  "$lock_path" \
  "$project_root" \
  "$test_root" \
  "$archive_root" \
  "$image_inventory" \
  "$now_override" \
  "$canonical" <<'PY'
from __future__ import annotations

import datetime as dt
import hashlib
import json
import os
import pathlib
import platform
import re
import stat
import subprocess
import sys
import tarfile
from typing import Any


class VerificationError(Exception):
    pass


class SchemaError(VerificationError):
    pass


class StrictJsonError(ValueError):
    pass


CANONICAL_SCHEMA_ID = "https://task-per-minute.local/schemas/release-tools.schema.json"
CANONICAL_LOCK_SCHEMA = "security/tools/release-tools.schema.json"
CANONICAL_LOCK_SCHEMA_VERSION = 1
CANONICAL_LOCK_SHA256 = "3fd7094c94ec2bd53732a11930f27adc83383c11c6008bd8e27ede1f97b7df2c"
CANONICAL_SCHEMA_SHA256 = "e88e299e4eaf307ce59b7f1f9ddeaa4074384e183a05aac6cb627a8381aedc86"
CANONICAL_ROOT_FIELDS = frozenset(
    {
        "schema",
        "schema_version",
        "reviewed_at",
        "platform",
        "tools",
        "playwright_chromium",
        "validation_images",
        "trivy_database",
    }
)


TOOL_POLICY: dict[str, tuple[str, str, set[str], tuple[str, ...], str]] = {
    "go": (
        "1.26.8",
        "https://go.googlesource.com/go",
        {"BSD-3-Clause"},
        ("version",),
        "go version go1.26.8 linux/amd64",
    ),
    "node": ("24.18.1", "https://github.com/nodejs/node", {"MIT"}, ("--version",), "v24.18.1"),
    "npm": ("11.16.0", "https://github.com/npm/cli", {"Artistic-2.0"}, ("--version",), "11.16.0"),
    "playwright": (
        "1.59.1",
        "https://github.com/microsoft/playwright",
        {"Apache-2.0"},
        ("--version",),
        "Version 1.59.1",
    ),
    "chromium": (
        "149.0.7827.55",
        "https://chromium.googlesource.com/chromium/src",
        {"BSD-3-Clause"},
        ("--version",),
        "Google Chrome for Testing 149.0.7827.55",
    ),
    "jq": ("1.8.2", "https://github.com/jqlang/jq", {"MIT"}, ("--version",), "jq-1.8.2"),
    "yq": (
        "4.53.3",
        "https://github.com/mikefarah/yq",
        {"MIT"},
        ("--version",),
        "yq (https://github.com/mikefarah/yq/) version v4.53.3",
    ),
    "docker": (
        "29.6.2",
        "https://github.com/docker/cli",
        {"Apache-2.0"},
        ("--version",),
        "Docker version 29.6.2, build v29.6.2",
    ),
    "docker-compose": (
        "5.3.1",
        "https://github.com/docker/compose",
        {"Apache-2.0"},
        ("version",),
        "Docker Compose version 5.3.1",
    ),
    "govulncheck": (
        "1.6.0",
        "https://github.com/golang/vuln",
        {"BSD-3-Clause"},
        ("-version",),
        "Go: go1.26.5\nScanner: govulncheck@1.6.0\nDB: https://vuln.go.dev",
    ),
    "gitleaks": (
        "8.30.1",
        "https://github.com/gitleaks/gitleaks",
        {"MIT"},
        ("version",),
        "8.30.1",
    ),
    "semgrep": (
        "1.161.0",
        "https://github.com/semgrep/semgrep",
        {"LGPL-2.1-or-later"},
        ("--version",),
        "1.161.0",
    ),
    "trivy": (
        "0.72.0",
        "https://github.com/aquasecurity/trivy",
        {"Apache-2.0"},
        ("--version",),
        "Version: 0.72.0",
    ),
}

IMAGE_POLICY = {
    "caddy": (
        "https://github.com/caddyserver/caddy-docker",
        "caddy:2-alpine",
        "docker.io/library/caddy",
    ),
    "postgres": (
        "https://github.com/docker-library/postgres",
        "postgres:18.3-alpine3.23",
        "docker.io/library/postgres",
    ),
    "redis": (
        "https://github.com/docker-library/redis",
        "redis:8.6.2-alpine3.23",
        "docker.io/library/redis",
    ),
    "seaweedfs": (
        "https://github.com/seaweedfs/seaweedfs",
        "chrislusf/seaweedfs:4.20",
        "docker.io/chrislusf/seaweedfs",
    ),
}
TRIVY_DATABASE_SOURCE = "https://github.com/aquasecurity/trivy-db"


def exact_regular_file(path_text: str, label: str) -> pathlib.Path:
    path = pathlib.Path(path_text)
    if not path.is_absolute():
        raise VerificationError(f"{label} must be an absolute path")
    current = pathlib.Path(path.anchor)
    for part in path.parts[1:]:
        current /= part
        try:
            mode = current.lstat().st_mode
        except OSError as exc:
            raise VerificationError(f"{label} cannot be resolved: {exc}") from exc
        if stat.S_ISLNK(mode):
            raise VerificationError(f"{label} contains a symlink path component")
    try:
        resolved = path.resolve(strict=True)
    except OSError as exc:
        raise VerificationError(f"{label} cannot be resolved: {exc}") from exc
    if path != resolved or not stat.S_ISREG(resolved.stat().st_mode):
        raise VerificationError(f"{label} must be an exact regular file")
    return resolved


def strict_json_loads(text: str) -> Any:
    def unique_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        value: dict[str, Any] = {}
        for key, child in pairs:
            if key in value:
                raise StrictJsonError(f"duplicate JSON key: {key}")
            value[key] = child
        return value

    def reject_constant(value: str) -> None:
        raise StrictJsonError(f"non-finite JSON value: {value}")

    return json.loads(
        text,
        object_pairs_hook=unique_object,
        parse_constant=reject_constant,
    )


def load_json(
    path_text: str,
    label: str,
    expected_sha256: str | None = None,
) -> dict[str, Any]:
    path = exact_regular_file(path_text, label)
    try:
        raw = path.read_bytes()
        if expected_sha256 is not None and hashlib.sha256(raw).hexdigest() != expected_sha256:
            raise VerificationError(f"{label} SHA-256 does not match the canonical trust anchor")
        value = strict_json_loads(raw.decode("utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError, StrictJsonError) as exc:
        raise VerificationError(f"cannot parse {label}: {exc}") from exc
    if not isinstance(value, dict):
        raise VerificationError(f"{label} root must be an object")
    return value


def validate_canonical_contract(schema: dict[str, Any], lock: dict[str, Any]) -> None:
    if schema.get("$id") != CANONICAL_SCHEMA_ID:
        raise SchemaError("release tool schema id mismatch")
    if schema.get("type") != "object" or schema.get("additionalProperties") is not False:
        raise SchemaError("release tool schema root invariants mismatch")
    required = schema.get("required")
    properties = schema.get("properties")
    if (
        not isinstance(required, list)
        or set(required) != CANONICAL_ROOT_FIELDS
        or not isinstance(properties, dict)
        or set(properties) != CANONICAL_ROOT_FIELDS
    ):
        raise SchemaError("release tool schema root fields mismatch")
    if lock.get("schema") != CANONICAL_LOCK_SCHEMA:
        raise SchemaError("release tool lock schema identity mismatch")
    if lock.get("schema_version") != CANONICAL_LOCK_SCHEMA_VERSION:
        raise SchemaError("release tool lock schema version mismatch")


def json_type_matches(value: Any, expected: str) -> bool:
    return {
        "object": isinstance(value, dict),
        "array": isinstance(value, list),
        "string": isinstance(value, str),
        "integer": isinstance(value, int) and not isinstance(value, bool),
        "boolean": isinstance(value, bool),
    }.get(expected, False)


def resolve_ref(schema: dict[str, Any], ref: str) -> dict[str, Any]:
    if not ref.startswith("#/"):
        raise SchemaError(f"unsupported schema reference: {ref}")
    node: Any = schema
    for part in ref[2:].split("/"):
        part = part.replace("~1", "/").replace("~0", "~")
        if not isinstance(node, dict) or part not in node:
            raise SchemaError(f"unresolved schema reference: {ref}")
        node = node[part]
    if not isinstance(node, dict):
        raise SchemaError(f"schema reference is not an object: {ref}")
    return node


def validate_schema(value: Any, rule: dict[str, Any], schema: dict[str, Any], path: str) -> None:
    if "$ref" in rule:
        validate_schema(value, resolve_ref(schema, rule["$ref"]), schema, path)
        return

    if "oneOf" in rule:
        matches = 0
        for candidate in rule["oneOf"]:
            try:
                validate_schema(value, candidate, schema, path)
                matches += 1
            except SchemaError:
                pass
        if matches != 1:
            raise SchemaError(f"{path} must match exactly one schema branch")
        return

    expected_type = rule.get("type")
    if expected_type is not None and not json_type_matches(value, expected_type):
        raise SchemaError(f"{path} must be {expected_type}")
    if "const" in rule and value != rule["const"]:
        raise SchemaError(f"{path} must equal {rule['const']!r}")
    if "enum" in rule and value not in rule["enum"]:
        raise SchemaError(f"{path} has an unsupported value")

    if isinstance(value, dict):
        properties = rule.get("properties", {})
        for key in rule.get("required", []):
            if key not in value:
                raise SchemaError(f"{path}.{key} is required")
        if rule.get("additionalProperties") is False:
            extras = sorted(set(value) - set(properties))
            if extras:
                raise SchemaError(f"{path} has unknown field: {extras[0]}")
        for key, child in value.items():
            if key in properties:
                validate_schema(child, properties[key], schema, f"{path}.{key}")

    if isinstance(value, list):
        minimum = rule.get("minItems")
        maximum = rule.get("maxItems")
        if minimum is not None and len(value) < minimum:
            raise SchemaError(f"{path} has fewer than {minimum} items")
        if maximum is not None and len(value) > maximum:
            raise SchemaError(f"{path} has more than {maximum} items")
        if "items" in rule:
            for index, child in enumerate(value):
                validate_schema(child, rule["items"], schema, f"{path}[{index}]")

    if isinstance(value, str):
        minimum = rule.get("minLength")
        if minimum is not None and len(value) < minimum:
            raise SchemaError(f"{path} is shorter than {minimum} characters")
        pattern_value = rule.get("pattern")
        if pattern_value is not None and re.search(pattern_value, value) is None:
            raise SchemaError(f"{path} does not match its schema pattern")
        if rule.get("format") == "date":
            try:
                dt.date.fromisoformat(value)
            except ValueError as exc:
                raise SchemaError(f"{path} is not an ISO date") from exc
        if rule.get("format") == "date-time":
            parse_timestamp(value, path)

    if isinstance(value, int) and not isinstance(value, bool):
        if "minimum" in rule and value < rule["minimum"]:
            raise SchemaError(f"{path} is less than {rule['minimum']}")
        if "maximum" in rule and value > rule["maximum"]:
            raise SchemaError(f"{path} is greater than {rule['maximum']}")


def parse_timestamp(value: str, label: str) -> dt.datetime:
    try:
        parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise VerificationError(f"{label} is not an ISO UTC timestamp") from exc
    if parsed.tzinfo is None or parsed.utcoffset() != dt.timedelta(0):
        raise VerificationError(f"{label} must be UTC")
    return parsed


def sha256_file(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def safe_root(path_text: str, label: str) -> pathlib.Path:
    path = pathlib.Path(path_text)
    try:
        resolved = path.resolve(strict=True)
    except OSError as exc:
        raise VerificationError(f"{label} cannot be resolved: {exc}") from exc
    if path != resolved or path.is_symlink() or not path.is_dir():
        raise VerificationError(f"{label} must be an exact non-symlink directory")
    return resolved


def safe_member(root: pathlib.Path, relative_text: str, label: str) -> pathlib.Path:
    relative = pathlib.PurePosixPath(relative_text)
    if relative.is_absolute() or ".." in relative.parts:
        raise VerificationError(f"{label} has an unsafe relative path")
    current = root
    for part in relative.parts:
        current = current / part
        try:
            mode = current.lstat().st_mode
        except OSError as exc:
            raise VerificationError(f"{label} is missing: {current}") from exc
        if stat.S_ISLNK(mode):
            raise VerificationError(f"{label} contains a symlink component")
    try:
        current.relative_to(root)
    except ValueError as exc:
        raise VerificationError(f"{label} escapes its declared root") from exc
    return current


def run_exact(command: list[str], timeout: int, label: str) -> str:
    env = {
        "HOME": "/nonexistent",
        "LANG": "C",
        "LC_ALL": "C",
        "NO_COLOR": "1",
        "PATH": "/nonexistent",
        "TRIVY_OFFLINE_SCAN": "true",
        "TRIVY_SKIP_DB_UPDATE": "true",
        "TRIVY_SKIP_JAVA_DB_UPDATE": "true",
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
            timeout=timeout,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise VerificationError(f"{label} could not run within its bound: {exc}") from exc
    if completed.returncode != 0:
        raise VerificationError(f"{label} exited with status {completed.returncode}")
    return completed.stdout.strip()


def verify_archive(archive_root: pathlib.Path, tool: dict[str, Any], executable_digest: str) -> None:
    provisioning = tool["provisioning"]
    archive = safe_member(archive_root, provisioning["archive_file"], f"{tool['name']} archive")
    if not archive.is_file():
        raise VerificationError(f"{tool['name']} archive is not a regular file")
    if sha256_file(archive) != provisioning["archive_sha256"]:
        raise VerificationError(f"{tool['name']} archive SHA-256 mismatch")
    declared = provisioning["extraction_member"]
    try:
        with tarfile.open(archive, mode="r:gz") as bundle:
            regular_members: list[tarfile.TarInfo] = []
            for member in bundle.getmembers():
                pure = pathlib.PurePosixPath(member.name)
                if pure.is_absolute() or ".." in pure.parts or "\\" in member.name:
                    raise VerificationError(f"{tool['name']} archive contains an unsafe path")
                if member.issym() or member.islnk() or member.isdev():
                    raise VerificationError(f"{tool['name']} archive contains an unsafe member type")
                if member.isfile():
                    regular_members.append(member)
            if [member.name for member in regular_members] != [declared]:
                raise VerificationError(f"{tool['name']} archive contains an undeclared extraction member")
            extracted = bundle.extractfile(regular_members[0])
            if extracted is None:
                raise VerificationError(f"{tool['name']} declared extraction member cannot be read")
            digest = hashlib.sha256(extracted.read()).hexdigest()
            if digest != executable_digest:
                raise VerificationError(f"{tool['name']} archive member SHA-256 mismatch")
    except (OSError, tarfile.TarError) as exc:
        raise VerificationError(f"{tool['name']} archive is invalid: {exc}") from exc


def verify_tool(
    tool: dict[str, Any],
    canonical: bool,
    test_root: pathlib.Path | None,
    archive_root: pathlib.Path | None,
) -> tuple[pathlib.Path, str]:
    name = tool["name"]
    provisioning = tool["provisioning"]
    if tool["trust_decision"]["status"] != "accepted":
        raise VerificationError(f"{name} trust decision is unresolved")
    if tool["source"]["license"]["status"] != "accepted":
        raise VerificationError(f"{name} license review is not accepted")
    if provisioning["kind"] == "unresolved":
        raise VerificationError(f"{name} provisioning is unresolved: {provisioning['reason']}")
    if provisioning["kind"] == "test_root" and canonical:
        raise VerificationError(f"{name} uses test-only provisioning in canonical mode")

    root = safe_root(provisioning["immutable_root"], f"{name} immutable root")
    if provisioning["kind"] == "test_root":
        if test_root is None:
            raise VerificationError(f"{name} test root was not declared to the verifier")
        try:
            root.relative_to(test_root)
        except ValueError as exc:
            raise VerificationError(f"{name} test provisioning escapes --test-root") from exc
    if provisioning["kind"] == "nix_store":
        if not str(root).startswith("/nix/store/"):
            raise VerificationError(f"{name} Nix root is outside /nix/store")
        nix_store = pathlib.Path("/run/current-system/sw/bin/nix-store")
        if not nix_store.is_file():
            raise VerificationError("fixed nix-store verifier is unavailable")
        nar_hash = run_exact(
            [str(nix_store), "-q", "--hash", str(root)],
            10,
            f"{name} NAR identity probe",
        )
        if nar_hash != provisioning["nar_hash"]:
            raise VerificationError(f"{name} NAR identity mismatch")

    executable = safe_member(root, provisioning["relative_path"], f"{name} executable")
    mode = executable.stat().st_mode
    if not stat.S_ISREG(mode) or mode & (stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH) == 0:
        raise VerificationError(f"{name} executable is not a regular executable file")
    if mode & (stat.S_IWGRP | stat.S_IWOTH):
        raise VerificationError(f"{name} executable is group/world writable")
    digest = sha256_file(executable)
    if digest != provisioning["executable_sha256"]:
        raise VerificationError(f"{name} executable SHA-256 mismatch")
    if provisioning["kind"] == "archive":
        if archive_root is None:
            raise VerificationError(f"{name} archive root is required")
        verify_archive(archive_root, tool, digest)

    identity = tool["runtime_identity"]
    output = run_exact(
        [str(executable), *identity["arguments"]],
        identity["timeout_seconds"],
        f"{name} runtime identity probe",
    )
    if output != identity["expected_output"]:
        raise VerificationError(f"{name} runtime/version identity mismatch")
    return executable, digest


def project_member(root: pathlib.Path, relative_text: str, label: str) -> pathlib.Path:
    return safe_member(root, relative_text, label)


def verify_playwright_binding(
    binding: dict[str, Any],
    tools: dict[str, dict[str, Any]],
    verified: dict[str, tuple[pathlib.Path, str]],
    project_root: pathlib.Path,
) -> None:
    package_lock = project_member(
        project_root,
        binding["package_lock_relative_path"],
        "Playwright package lock",
    )
    if not package_lock.is_file() or sha256_file(package_lock) != binding["package_lock_sha256"]:
        raise VerificationError("Playwright package-lock identity mismatch")
    try:
        package_data = strict_json_loads(package_lock.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError, StrictJsonError) as exc:
        raise VerificationError(f"Playwright package-lock cannot be parsed: {exc}") from exc
    if not isinstance(package_data, dict) or not isinstance(package_data.get("packages"), dict):
        raise VerificationError("Playwright package-lock has invalid structure")
    package_record = package_data["packages"].get("node_modules/playwright-core")
    if not isinstance(package_record, dict) or not isinstance(package_record.get("version"), str):
        raise VerificationError("Playwright package-lock has invalid structure")
    package_version = package_record["version"]
    if package_version != binding["playwright_package_version"]:
        raise VerificationError("Playwright package-lock version mismatch")

    browsers_path = project_member(
        project_root,
        binding["browsers_json_relative_path"],
        "Playwright browsers.json",
    )
    if not browsers_path.is_file() or sha256_file(browsers_path) != binding["browsers_json_sha256"]:
        raise VerificationError("Playwright browsers.json identity mismatch")
    try:
        browsers_data = strict_json_loads(browsers_path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError, StrictJsonError) as exc:
        raise VerificationError(f"Playwright browsers.json cannot be parsed: {exc}") from exc
    if not isinstance(browsers_data, dict) or not isinstance(browsers_data.get("browsers"), list):
        raise VerificationError("Playwright browsers.json has invalid structure")
    browsers = browsers_data["browsers"]
    if any(not isinstance(item, dict) for item in browsers):
        raise VerificationError("Playwright browsers.json has invalid structure")
    chromium_records = [item for item in browsers if item.get("name") == "chromium"]
    if len(chromium_records) != 1:
        raise VerificationError("Playwright browsers.json must contain one Chromium record")
    chromium_record = chromium_records[0]
    if chromium_record.get("revision") != binding["chromium_revision"]:
        raise VerificationError("Playwright Chromium revision mismatch")
    if chromium_record.get("browserVersion") != binding["chromium_browser_version"]:
        raise VerificationError("Playwright Chromium browser version mismatch")
    if tools["playwright"]["version"] != binding["playwright_package_version"]:
        raise VerificationError("Playwright runtime/package-lock mismatch")
    chromium_digest = verified.get("chromium", (None, ""))[1]
    if chromium_digest != binding["chromium_executable_sha256"]:
        raise VerificationError("Playwright/Chromium executable digest mismatch")

    if binding["status"] != "accepted":
        expected = binding["chromium_browser_version"]
        observed = binding["observed_browser_version"]
        if tools["chromium"]["version"] != observed:
            raise VerificationError("observed Chromium runtime version mismatch")
        if expected != observed:
            raise VerificationError(
                f"Playwright/Chromium mismatch: expected {expected}, observed {observed}"
            )
        raise VerificationError(f"Playwright/Chromium trust is unresolved: {binding['reason']}")
    if tools["chromium"]["version"] != binding["chromium_browser_version"]:
        raise VerificationError("Playwright/Chromium runtime version mismatch")


def load_image_inventory(path_text: str) -> dict[str, dict[str, Any]]:
    data = load_json(path_text, "test image inventory")
    records = data.get("images")
    if not isinstance(records, list):
        raise VerificationError("test image inventory images must be an array")
    result: dict[str, dict[str, Any]] = {}
    for record in records:
        if not isinstance(record, dict) or not isinstance(record.get("reference"), str):
            raise VerificationError("test image inventory has an invalid record")
        result[record["reference"]] = record
    return result


def docker_inspect(docker: pathlib.Path, reference: str) -> dict[str, Any]:
    output = run_exact(
        [str(docker), "image", "inspect", reference],
        10,
        f"Docker image inspect for {reference}",
    )
    try:
        records = json.loads(output)
    except json.JSONDecodeError as exc:
        raise VerificationError(f"Docker inspect returned invalid JSON for {reference}") from exc
    if not isinstance(records, list) or len(records) != 1 or not isinstance(records[0], dict):
        raise VerificationError(f"Docker inspect did not return one record for {reference}")
    record = records[0]
    return {
        "reference": reference,
        "repo_digests": record.get("RepoDigests", []),
        "os": record.get("Os"),
        "architecture": record.get("Architecture"),
        "config_digest": record.get("Id"),
    }


def verify_images(
    images: list[dict[str, Any]],
    verified: dict[str, tuple[pathlib.Path, str]],
    inventory_path: str,
) -> list[str]:
    errors: list[str] = []
    inventory = load_image_inventory(inventory_path) if inventory_path else None
    docker = verified.get("docker", (None, ""))[0]
    for image in images:
        name = image["name"]
        expected_source, expected_tag, expected_repository = IMAGE_POLICY[name]
        if image["source"] != expected_source:
            errors.append(f"{name} validation image uses an unofficial source")
        if image["requested_tag"] != expected_tag:
            errors.append(f"{name} validation image requested tag mismatch")
        if image["status"] != "accepted":
            errors.append(f"{name} validation image is unresolved and tag-only: {image['requested_tag']}")
            continue
        if image["license"]["status"] != "accepted":
            errors.append(f"{name} validation image license review is not accepted")
            continue
        reference = image["reference"]
        if "@sha256:" not in reference:
            errors.append(f"{name} validation image is not digest-pinned")
            continue
        repository = reference.split("@sha256:", 1)[0]
        if repository != expected_repository:
            errors.append(f"{name} validation image repository is not the reviewed release repository")
            continue
        try:
            if inventory is not None:
                if reference not in inventory:
                    raise VerificationError(f"{name} validation image is missing")
                actual = inventory[reference]
            else:
                if docker is None:
                    raise VerificationError("verified Docker CLI is unavailable for read-only image inspect")
                actual = docker_inspect(docker, reference)
            if reference not in actual.get("repo_digests", []):
                raise VerificationError(f"{name} validation image repository digest mismatch")
            actual_platform = f"{actual.get('os')}/{actual.get('architecture')}"
            if actual_platform != image["platform"]:
                raise VerificationError(f"{name} validation image platform mismatch")
            if actual.get("config_digest") != image["config_digest"]:
                raise VerificationError(f"{name} validation image config digest mismatch")
        except VerificationError as exc:
            errors.append(str(exc))
    return errors


def verify_trivy_database(
    database: dict[str, Any],
    test_root: pathlib.Path | None,
    now: dt.datetime,
) -> None:
    if database["source"] != TRIVY_DATABASE_SOURCE:
        raise VerificationError("Trivy vulnerability DB uses an unofficial source")
    max_age = dt.timedelta(hours=database["max_age_hours"])
    if database["status"] != "accepted":
        updated_date = dt.date.fromisoformat(database["updated_at_date"])
        newest_possible = dt.datetime.combine(updated_date, dt.time.max, tzinfo=dt.timezone.utc)
        if now - newest_possible > max_age:
            raise VerificationError(
                f"Trivy vulnerability DB is stale: metadata date {updated_date.isoformat()}"
            )
        raise VerificationError(f"Trivy vulnerability DB trust is unresolved: {database['reason']}")

    root = safe_root(database["immutable_root"], "Trivy DB immutable root")
    if test_root is not None:
        try:
            root.relative_to(test_root)
        except ValueError as exc:
            raise VerificationError("Trivy DB root escapes --test-root") from exc
    metadata = safe_member(root, database["metadata_relative_path"], "Trivy metadata")
    db_file = safe_member(root, database["database_relative_path"], "Trivy vulnerability DB")
    if not metadata.is_file() or sha256_file(metadata) != database["metadata_sha256"]:
        raise VerificationError("Trivy metadata SHA-256 mismatch")
    if not db_file.is_file() or sha256_file(db_file) != database["database_sha256"]:
        raise VerificationError("Trivy vulnerability DB SHA-256 mismatch")
    try:
        metadata_data = json.loads(metadata.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise VerificationError(f"Trivy metadata cannot be parsed: {exc}") from exc
    if metadata_data.get("UpdatedAt") != database["updated_at"]:
        raise VerificationError("Trivy metadata UpdatedAt mismatch")
    updated_at = parse_timestamp(database["updated_at"], "Trivy updated_at")
    if updated_at > now + dt.timedelta(minutes=5):
        raise VerificationError("Trivy vulnerability DB timestamp is in the future")
    if now - updated_at > max_age:
        raise VerificationError("Trivy vulnerability DB is stale")


schema_path, lock_path, project_root_text, test_root_text, archive_root_text, inventory_path, now_text, canonical_text = sys.argv[1:]
canonical = canonical_text == "1"
report_label = "release security preflight" if canonical else "release security fixture"
rejection_label = "NO-GO" if canonical else "REJECTED"

try:
    schema = load_json(
        schema_path,
        "release tool schema",
        CANONICAL_SCHEMA_SHA256 if canonical else None,
    )
    if schema.get("$schema") != "https://json-schema.org/draft/2020-12/schema":
        raise SchemaError("release tool schema must declare Draft 2020-12")
    lock = load_json(
        lock_path,
        "release tool lock",
        CANONICAL_LOCK_SHA256 if canonical else None,
    )
    validate_canonical_contract(schema, lock)
    validate_schema(lock, schema, schema, "lock")

    project_root = safe_root(project_root_text, "project root")
    test_root = safe_root(test_root_text, "test root") if test_root_text else None
    archive_root = safe_root(archive_root_text, "archive root") if archive_root_text else None
    if canonical and any((test_root_text, archive_root_text, inventory_path, now_text)):
        raise VerificationError("canonical mode cannot use fixture overrides")

    errors: list[str] = []
    tools_list = lock["tools"]
    tools = {tool["name"]: tool for tool in tools_list}
    if len(tools) != len(tools_list) or set(tools) != set(TOOL_POLICY):
        errors.append("tool lock must contain the exact release security tool set once")
    host_arch = {"x86_64": "amd64", "aarch64": "arm64"}.get(platform.machine(), platform.machine())
    if lock["platform"] != {"os": platform.system().lower(), "arch": host_arch}:
        errors.append("host platform does not match the release tool lock")

    for name, tool in tools.items():
        (
            expected_version,
            expected_source,
            allowed_licenses,
            expected_arguments,
            expected_output,
        ) = TOOL_POLICY[name]
        if canonical and tool["version"] != expected_version:
            errors.append(f"{name} canonical version mismatch")
        if tool["source"]["repository_url"] != expected_source:
            errors.append(f"{name} uses an unofficial source")
        license_review = tool["source"]["license"]
        if license_review["spdx"] not in allowed_licenses or license_review["status"] != "accepted":
            errors.append(f"{name} license review is incompatible or unresolved")
        identity = tool["runtime_identity"]
        if identity["arguments"] != list(expected_arguments):
            errors.append(f"{name} runtime probe arguments policy mismatch")
        if identity["expected_output"] != expected_output:
            errors.append(f"{name} runtime probe output policy mismatch")

    if errors:
        for error in errors:
            print(f"{report_label}: ERROR: {error}", file=sys.stderr)
        print(f"{report_label}: {rejection_label} ({len(errors)} trust failures)", file=sys.stderr)
        sys.exit(1)

    verified: dict[str, tuple[pathlib.Path, str]] = {}
    for name in sorted(tools):
        try:
            verified[name] = verify_tool(tools[name], canonical, test_root, archive_root)
            print(f"verified tool {name} {tools[name]['version']}")
        except VerificationError as exc:
            errors.append(str(exc))

    if {"playwright", "chromium"}.issubset(tools):
        try:
            verify_playwright_binding(lock["playwright_chromium"], tools, verified, project_root)
        except VerificationError as exc:
            errors.append(str(exc))

    image_names = [image["name"] for image in lock["validation_images"]]
    if len(set(image_names)) != len(image_names) or set(image_names) != set(IMAGE_POLICY):
        errors.append("validation image lock must contain the exact image set once")
    errors.extend(verify_images(lock["validation_images"], verified, inventory_path))

    now = parse_timestamp(now_text, "--now") if now_text else dt.datetime.now(dt.timezone.utc)
    try:
        verify_trivy_database(lock["trivy_database"], test_root, now)
    except VerificationError as exc:
        errors.append(str(exc))

    if errors:
        for error in errors:
            print(f"{report_label}: ERROR: {error}", file=sys.stderr)
        print(f"{report_label}: {rejection_label} ({len(errors)} trust failures)", file=sys.stderr)
        sys.exit(1)
    print(f"{report_label}: PASS")
except VerificationError as exc:
    print(f"{report_label}: ERROR: {exc}", file=sys.stderr)
    print(f"{report_label}: {rejection_label}", file=sys.stderr)
    sys.exit(1)
PY
