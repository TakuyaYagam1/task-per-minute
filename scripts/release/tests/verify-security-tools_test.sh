#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd -- "$script_dir/../../.." && pwd -P)"
verifier="$repo_root/scripts/release/verify-security-tools.sh"
schema="$repo_root/security/tools/release-tools.schema.json"
lock="$repo_root/security/tools/release-tools.lock.json"
fixture_root="$(mktemp -d)"
trap 'rm -rf -- "$fixture_root"' EXIT

python3 - "$verifier" "$lock" "$schema" <<'PY'
import hashlib
import pathlib
import re
import sys


verifier = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
paths = {
    "LOCK": pathlib.Path(sys.argv[2]),
    "SCHEMA": pathlib.Path(sys.argv[3]),
}
constants = dict(
    re.findall(
        r'^CANONICAL_(LOCK|SCHEMA)_SHA256 = "([0-9a-f]{64})"$',
        verifier,
        re.MULTILINE,
    )
)
expected = {
    name: hashlib.sha256(path.read_bytes()).hexdigest()
    for name, path in paths.items()
}
if constants != expected:
    raise SystemExit(f"canonical trust anchors do not match: {constants!r} != {expected!r}")
PY

make_fixture() {
  local case_root="$1"
  local mutation="$2"
  python3 - "$case_root" "$mutation" <<'PY'
from __future__ import annotations

import hashlib
import io
import json
import os
import pathlib
import stat
import sys
import tarfile


case_root = pathlib.Path(sys.argv[1])
mutation = sys.argv[2]
runtime_root = case_root / "runtime"
archive_root = case_root / "archives"
project_root = case_root / "project"
runtime_root.mkdir(parents=True)
archive_root.mkdir(parents=True)
(project_root / "frontend/node_modules/playwright-core").mkdir(parents=True)

versions = {
    "go": "1.26.5",
    "node": "24.18.1",
    "npm": "11.16.0",
    "playwright": "1.59.1",
    "chromium": "149.0.7827.55",
    "jq": "1.8.2",
    "yq": "4.53.3",
    "docker": "29.6.2",
    "docker-compose": "5.3.1",
    "govulncheck": "1.6.0",
    "gitleaks": "8.30.1",
    "semgrep": "1.161.0",
    "trivy": "0.72.0",
}

sources = {
    "go": ("https://go.googlesource.com/go", "BSD-3-Clause"),
    "node": ("https://github.com/nodejs/node", "MIT"),
    "npm": ("https://github.com/npm/cli", "Artistic-2.0"),
    "playwright": ("https://github.com/microsoft/playwright", "Apache-2.0"),
    "chromium": ("https://chromium.googlesource.com/chromium/src", "BSD-3-Clause"),
    "jq": ("https://github.com/jqlang/jq", "MIT"),
    "yq": ("https://github.com/mikefarah/yq", "MIT"),
    "docker": ("https://github.com/docker/cli", "Apache-2.0"),
    "docker-compose": ("https://github.com/docker/compose", "Apache-2.0"),
    "govulncheck": ("https://github.com/golang/vuln", "BSD-3-Clause"),
    "gitleaks": ("https://github.com/gitleaks/gitleaks", "MIT"),
    "semgrep": ("https://github.com/semgrep/semgrep", "LGPL-2.1-or-later"),
    "trivy": ("https://github.com/aquasecurity/trivy", "Apache-2.0"),
}


def sha256(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


runtime_identities = {
    "go": ("version", "go version go1.26.5 linux/amd64"),
    "node": ("--version", "v24.18.1"),
    "npm": ("--version", "11.16.0"),
    "playwright": ("--version", "Version 1.59.1"),
    "chromium": ("--version", "Google Chrome for Testing 149.0.7827.55"),
    "jq": ("--version", "jq-1.8.2"),
    "yq": ("--version", "yq (https://github.com/mikefarah/yq/) version v4.53.3"),
    "docker": ("--version", "Docker version 29.6.2, build v29.6.2"),
    "docker-compose": ("version", "Docker Compose version 5.3.1"),
    "govulncheck": ("-version", "Go: go1.26.5\nScanner: govulncheck@1.6.0\nDB: https://vuln.go.dev"),
    "gitleaks": ("version", "8.30.1"),
    "semgrep": ("--version", "1.161.0"),
    "trivy": ("--version", "Version: 0.72.0"),
}


def tool_script(argument: str, output: str) -> bytes:
    return (
        "#!/bin/sh\n"
        f"if [ \"$#\" -eq 1 ] && [ \"${{1:-}}\" = '{argument}' ]; then\n"
        f"  printf '%s\\n' '{output}'\n"
        "  exit 0\n"
        "fi\n"
        "exit 64\n"
    ).encode()


tools = []
tool_paths: dict[str, pathlib.Path] = {}
for name, version in versions.items():
    root = runtime_root / name
    executable = root / "bin" / name
    executable.parent.mkdir(parents=True)
    argument, output = runtime_identities[name]
    executable.write_bytes(tool_script(argument, output))
    executable.chmod(0o555)
    tool_paths[name] = executable
    provisioning: dict[str, object] = {
        "kind": "test_root",
        "immutable_root": str(root),
        "relative_path": f"bin/{name}",
        "executable_sha256": sha256(executable),
    }
    tools.append({
        "name": name,
        "version": version,
        "source": {
            "repository_url": sources[name][0],
            "release_url": f"https://releases.example.invalid/{name}/{version}",
            "license": {
                "spdx": sources[name][1],
                "reviewed_at": "2026-09-02",
                "status": "accepted",
                "evidence_url": f"https://licenses.example.invalid/{name}/{version}",
            },
        },
        "runtime_identity": {
            "arguments": [argument],
            "expected_output": output,
            "timeout_seconds": 3,
        },
        "provisioning": provisioning,
        "trust_decision": {
            "status": "accepted",
            "rationale": "Synthetic fixture is content-pinned inside the isolated test-only root.",
        },
    })

tools_by_name = {tool["name"]: tool for tool in tools}
jq_bytes = tool_paths["jq"].read_bytes()
jq_archive = archive_root / "jq.tar.gz"


def write_jq_archive(extra_member: bool) -> None:
    with tarfile.open(jq_archive, mode="w:gz") as bundle:
        member = tarfile.TarInfo("bin/jq")
        member.mode = 0o555
        member.size = len(jq_bytes)
        bundle.addfile(member, io.BytesIO(jq_bytes))
        if extra_member:
            extra = b"undeclared\n"
            extra_info = tarfile.TarInfo("README")
            extra_info.mode = 0o444
            extra_info.size = len(extra)
            bundle.addfile(extra_info, io.BytesIO(extra))


write_jq_archive(False)
tools_by_name["jq"]["provisioning"] = {
    "kind": "archive",
    "immutable_root": str(runtime_root / "jq"),
    "relative_path": "bin/jq",
    "executable_sha256": sha256(tool_paths["jq"]),
    "archive_file": "jq.tar.gz",
    "archive_sha256": sha256(jq_archive),
    "archive_format": "tar.gz",
    "extraction_member": "bin/jq",
}

package_lock = project_root / "frontend/package-lock.json"
package_lock.write_text(json.dumps({
    "name": "fixture",
    "lockfileVersion": 3,
    "packages": {"node_modules/playwright-core": {"version": "1.59.1"}},
}, sort_keys=True), encoding="utf-8")
browsers_json = project_root / "frontend/node_modules/playwright-core/browsers.json"
browsers_json.write_text(json.dumps({
    "browsers": [{
        "name": "chromium",
        "revision": "1217",
        "browserVersion": "149.0.7827.55",
    }],
}, sort_keys=True), encoding="utf-8")

image_policy = {
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
images = []
inventory = []
for name, (source, requested_tag, repository) in image_policy.items():
    repository_digest = hashlib.sha256(f"{name}-repository".encode()).hexdigest()
    config_digest = "sha256:" + hashlib.sha256(f"{name}-config".encode()).hexdigest()
    reference = f"{repository}@sha256:{repository_digest}"
    images.append({
        "status": "accepted",
        "name": name,
        "requested_tag": requested_tag,
        "reference": reference,
        "platform": "linux/amd64",
        "config_digest": config_digest,
        "source": source,
        "license": {
            "spdx": "Apache-2.0",
            "reviewed_at": "2026-09-02",
            "status": "accepted",
            "evidence_url": f"https://licenses.example.invalid/images/{name}",
        },
    })
    inventory.append({
        "reference": reference,
        "repo_digests": [reference],
        "os": "linux",
        "architecture": "amd64",
        "config_digest": config_digest,
    })

trivy_root = runtime_root / "trivy-db"
trivy_root.mkdir()
metadata = trivy_root / "metadata.json"
database = trivy_root / "trivy.db"
updated_at = "2026-09-01T00:00:00Z"
metadata.write_text(json.dumps({"UpdatedAt": updated_at, "Version": 2}, sort_keys=True), encoding="utf-8")
database.write_bytes(b"synthetic trivy vulnerability database\n")

lock = {
    "schema": "security/tools/release-tools.schema.json",
    "schema_version": 1,
    "reviewed_at": "2026-09-02",
    "platform": {"os": "linux", "arch": "amd64"},
    "tools": tools,
    "playwright_chromium": {
        "status": "accepted",
        "package_lock_relative_path": "frontend/package-lock.json",
        "package_lock_sha256": sha256(package_lock),
        "playwright_package_version": "1.59.1",
        "browsers_json_relative_path": "frontend/node_modules/playwright-core/browsers.json",
        "browsers_json_sha256": sha256(browsers_json),
        "chromium_revision": "1217",
        "chromium_browser_version": "149.0.7827.55",
        "chromium_executable_sha256": sha256(tool_paths["chromium"]),
    },
    "validation_images": images,
    "trivy_database": {
        "status": "accepted",
        "source": "https://github.com/aquasecurity/trivy-db",
        "immutable_root": str(trivy_root),
        "metadata_relative_path": "metadata.json",
        "database_relative_path": "trivy.db",
        "metadata_sha256": sha256(metadata),
        "database_sha256": sha256(database),
        "updated_at": updated_at,
        "max_age_hours": 168,
    },
}

if mutation == "version_mismatch":
    tool_paths["go"].chmod(0o755)
    tool_paths["go"].write_bytes(tool_script("version", "go wrong-version"))
    tool_paths["go"].chmod(0o555)
    tools_by_name["go"]["provisioning"]["executable_sha256"] = sha256(tool_paths["go"])
elif mutation == "runtime_args_tamper":
    side_effect = case_root / "runtime-command-ran"
    output = runtime_identities["docker"][1]
    tool_paths["docker"].chmod(0o755)
    tool_paths["docker"].write_text(
        "#!/bin/sh\n"
        f": >'{side_effect}'\n"
        f"printf '%s\\n' '{output}'\n",
        encoding="utf-8",
    )
    tool_paths["docker"].chmod(0o555)
    tools_by_name["docker"]["provisioning"]["executable_sha256"] = sha256(tool_paths["docker"])
    tools_by_name["docker"]["runtime_identity"]["arguments"] = ["system", "prune", "--force"]
elif mutation == "runtime_output_tamper":
    side_effect = case_root / "runtime-command-ran"
    tool_paths["docker"].chmod(0o755)
    tool_paths["docker"].write_text(
        "#!/bin/sh\n"
        f": >'{side_effect}'\n"
        "printf '%s\\n' 'manifest chosen output'\n",
        encoding="utf-8",
    )
    tool_paths["docker"].chmod(0o555)
    tools_by_name["docker"]["provisioning"]["executable_sha256"] = sha256(tool_paths["docker"])
    tools_by_name["docker"]["runtime_identity"]["expected_output"] = "manifest chosen output"
elif mutation == "missing_tool":
    tool_paths["node"].unlink()
elif mutation == "executable_tamper":
    tool_paths["yq"].chmod(0o755)
    with tool_paths["yq"].open("ab") as output:
        output.write(b"# tampered\n")
    tool_paths["yq"].chmod(0o555)
elif mutation == "archive_tamper":
    with jq_archive.open("ab") as output:
        output.write(b"tampered")
elif mutation == "undeclared_member":
    write_jq_archive(True)
    tools_by_name["jq"]["provisioning"]["archive_sha256"] = sha256(jq_archive)
elif mutation == "unofficial_source":
    tools_by_name["go"]["source"]["repository_url"] = "https://mirror.example.invalid/go"
elif mutation == "license_failure":
    tools_by_name["node"]["source"]["license"]["status"] = "unresolved"
elif mutation == "playwright_mismatch":
    lock["playwright_chromium"]["chromium_browser_version"] = "148.0.0.0"
elif mutation == "playwright_package_digest":
    lock["playwright_chromium"]["package_lock_sha256"] = "0" * 64
elif mutation == "playwright_package_version":
    lock["playwright_chromium"]["playwright_package_version"] = "1.58.0"
elif mutation == "playwright_browsers_digest":
    lock["playwright_chromium"]["browsers_json_sha256"] = "0" * 64
elif mutation == "playwright_revision":
    lock["playwright_chromium"]["chromium_revision"] = "9999"
elif mutation == "playwright_executable_digest":
    lock["playwright_chromium"]["chromium_executable_sha256"] = "0" * 64
elif mutation == "playwright_package_type":
    package_lock.write_text(json.dumps({"packages": []}), encoding="utf-8")
    lock["playwright_chromium"]["package_lock_sha256"] = sha256(package_lock)
elif mutation == "playwright_browsers_type":
    browsers_json.write_text(json.dumps({"browsers": ["invalid"]}), encoding="utf-8")
    lock["playwright_chromium"]["browsers_json_sha256"] = sha256(browsers_json)
elif mutation == "image_tag":
    lock["validation_images"][0]["reference"] = "caddy:2-alpine"
elif mutation == "image_requested_tag":
    lock["validation_images"][0]["requested_tag"] = "caddy:latest"
elif mutation == "image_wrong_repository":
    digest = lock["validation_images"][0]["reference"].split("@sha256:", 1)[1]
    reference = f"docker.io/example/caddy@sha256:{digest}"
    lock["validation_images"][0]["reference"] = reference
    inventory[0]["reference"] = reference
    inventory[0]["repo_digests"] = [reference]
elif mutation == "image_wrong_digest":
    inventory[0]["repo_digests"] = ["docker.io/library/caddy@sha256:" + "0" * 64]
elif mutation == "image_missing":
    inventory.pop(0)
elif mutation == "trivy_metadata_digest":
    lock["trivy_database"]["metadata_sha256"] = "0" * 64
elif mutation == "trivy_db_digest":
    lock["trivy_database"]["database_sha256"] = "0" * 64
elif mutation == "trivy_stale":
    updated_at = "2026-08-01T00:00:00Z"
    metadata.write_text(json.dumps({"UpdatedAt": updated_at, "Version": 2}, sort_keys=True), encoding="utf-8")
    lock["trivy_database"]["updated_at"] = updated_at
    lock["trivy_database"]["metadata_sha256"] = sha256(metadata)
elif mutation not in {
    "pass",
    "path_poison",
    "duplicate_lock",
    "nonfinite_lock",
    "symlink_lock",
    "schema_id_mismatch",
}:
    raise SystemExit(f"unknown fixture mutation: {mutation}")

lock_text = json.dumps(lock, indent=2) + "\n"
if mutation == "duplicate_lock":
    lock_text = lock_text.replace(
        '  "schema_version": 1,',
        '  "schema_version": 1,\n  "schema_version": 1,',
        1,
    )
elif mutation == "nonfinite_lock":
    lock_text = lock_text.replace('"max_age_hours": 168', '"max_age_hours": NaN', 1)
lock_path = case_root / "lock.json"
lock_path.write_text(lock_text, encoding="utf-8")
if mutation == "symlink_lock":
    target = case_root / "lock-target.json"
    lock_path.replace(target)
    lock_path.symlink_to(target.name)
(case_root / "images.json").write_text(json.dumps({"images": inventory}, indent=2) + "\n", encoding="utf-8")
PY
}

run_fixture() {
  local name="$1"
  local mutation="$2"
  local expected_status="$3"
  local expected_text="$4"
  local case_root="$fixture_root/$name"
  local output="$case_root/output.txt"
  mkdir -p -- "$case_root"
  make_fixture "$case_root" "$mutation"

  local schema_path="$schema"
  if [[ "$mutation" == "schema_id_mismatch" ]]; then
    schema_path="$case_root/schema.json"
    python3 - "$schema" "$schema_path" <<'PY'
import json
import pathlib
import sys

schema = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
schema["$id"] = "https://example.invalid/weakened-schema.json"
pathlib.Path(sys.argv[2]).write_text(json.dumps(schema), encoding="utf-8")
PY
  fi

  local -a command=(
    bash "$verifier"
    --lock "$case_root/lock.json"
    --schema "$schema_path"
    --project-root "$case_root/project"
    --test-root "$case_root/runtime"
    --archive-root "$case_root/archives"
    --image-inventory "$case_root/images.json"
    --now "2026-09-02T00:00:00Z"
  )

  local run_status=0
  if [[ "$mutation" == "path_poison" ]]; then
    local poison="$case_root/poison"
    mkdir -p -- "$poison"
    for tool in python3 dirname go node npm playwright chromium jq yq docker docker-compose govulncheck gitleaks semgrep trivy; do
      printf '#!/bin/sh\nexit 97\n' >"$poison/$tool"
      chmod 755 "$poison/$tool"
    done
    printf 'raise RuntimeError("cwd module shadow executed")\n' >"$case_root/platform.py"
    (
      cd -- "$case_root"
      TPM_SECURITY_PREFLIGHT_TEST_MODE=1 PATH="$poison:$PATH" "${command[@]}"
    ) >"$output" 2>&1 || run_status=$?
  else
    TPM_SECURITY_PREFLIGHT_TEST_MODE=1 "${command[@]}" >"$output" 2>&1 || run_status=$?
  fi

  if [[ "$expected_status" == "pass" ]]; then
    if ((run_status != 0)); then
      cat "$output" >&2
      echo "verify-security-tools test: $name unexpectedly failed" >&2
      exit 1
    fi
  elif ((run_status == 0)); then
    cat "$output" >&2
    echo "verify-security-tools test: $name unexpectedly passed" >&2
    exit 1
  fi
  if grep -F "release security preflight:" "$output" >/dev/null || grep -F "NO-GO" "$output" >/dev/null; then
    cat "$output" >&2
    echo "verify-security-tools test: $name emitted a canonical-looking fixture result" >&2
    exit 1
  fi
  if [[ "$mutation" == runtime_* && -e "$case_root/runtime-command-ran" ]]; then
    cat "$output" >&2
    echo "verify-security-tools test: $name executed a manifest-controlled runtime command" >&2
    exit 1
  fi
  grep -F "$expected_text" "$output" >/dev/null || {
    cat "$output" >&2
    echo "verify-security-tools test: $name did not report expected diagnostic" >&2
    exit 1
  }
}

run_canonical_digest_reject() {
  local target="$1"
  local case_root="$fixture_root/canonical-$target-digest"
  local output="$case_root/output.txt"
  local run_status=0
  mkdir -p -- "$case_root/scripts/release" "$case_root/security/tools"
  cp -- "$verifier" "$case_root/scripts/release/verify-security-tools.sh"
  cp -- "$lock" "$case_root/security/tools/release-tools.lock.json"
  cp -- "$schema" "$case_root/security/tools/release-tools.schema.json"
  printf '\n' >>"$case_root/security/tools/release-tools.$target.json"

  bash "$case_root/scripts/release/verify-security-tools.sh" >"$output" 2>&1 || run_status=$?
  if ((run_status == 0)); then
    cat "$output" >&2
    echo "verify-security-tools test: canonical $target digest tamper unexpectedly passed" >&2
    exit 1
  fi
  grep -F "release tool $target SHA-256 does not match the canonical trust anchor" "$output" >/dev/null || {
    cat "$output" >&2
    echo "verify-security-tools test: canonical $target digest tamper lacked its diagnostic" >&2
    exit 1
  }
  if grep -F "verified tool " "$output" >/dev/null; then
    cat "$output" >&2
    echo "verify-security-tools test: canonical $target tamper reached an executable probe" >&2
    exit 1
  fi
  grep -F "release security preflight: NO-GO" "$output" >/dev/null || {
    cat "$output" >&2
    echo "verify-security-tools test: canonical $target digest tamper lacked NO-GO" >&2
    exit 1
  }
}

run_fixture pass pass pass "release security fixture: PASS"
run_fixture version-mismatch version_mismatch fail "go runtime/version identity mismatch"
run_fixture runtime-args-tamper runtime_args_tamper fail "docker runtime probe arguments policy mismatch"
run_fixture runtime-output-tamper runtime_output_tamper fail "docker runtime probe output policy mismatch"
run_fixture missing-tool missing_tool fail "node executable is missing"
run_fixture executable-tamper executable_tamper fail "yq executable SHA-256 mismatch"
run_fixture archive-tamper archive_tamper fail "jq archive SHA-256 mismatch"
run_fixture undeclared-member undeclared_member fail "jq archive contains an undeclared extraction member"
run_fixture unofficial-source unofficial_source fail "go uses an unofficial source"
run_fixture license-failure license_failure fail "node license review is incompatible or unresolved"
run_fixture playwright-mismatch playwright_mismatch fail "Playwright Chromium browser version mismatch"
run_fixture playwright-package-digest playwright_package_digest fail "Playwright package-lock identity mismatch"
run_fixture playwright-package-version playwright_package_version fail "Playwright package-lock version mismatch"
run_fixture playwright-browsers-digest playwright_browsers_digest fail "Playwright browsers.json identity mismatch"
run_fixture playwright-revision playwright_revision fail "Playwright Chromium revision mismatch"
run_fixture playwright-executable-digest playwright_executable_digest fail "Playwright/Chromium executable digest mismatch"
run_fixture playwright-package-type playwright_package_type fail "Playwright package-lock has invalid structure"
run_fixture playwright-browsers-type playwright_browsers_type fail "Playwright browsers.json has invalid structure"
run_fixture duplicate-lock duplicate_lock fail "duplicate JSON key: schema_version"
run_fixture nonfinite-lock nonfinite_lock fail "non-finite JSON value: NaN"
run_fixture symlink-lock symlink_lock fail "release tool lock contains a symlink path component"
run_fixture schema-id-mismatch schema_id_mismatch fail "release tool schema id mismatch"
run_fixture image-tag image_tag fail "must match exactly one schema branch"
run_fixture image-requested-tag image_requested_tag fail "caddy validation image requested tag mismatch"
run_fixture image-wrong-repository image_wrong_repository fail "caddy validation image repository is not the reviewed release repository"
run_fixture image-wrong-digest image_wrong_digest fail "caddy validation image repository digest mismatch"
run_fixture image-missing image_missing fail "caddy validation image is missing"
run_fixture trivy-metadata trivy_metadata_digest fail "Trivy metadata SHA-256 mismatch"
run_fixture trivy-database trivy_db_digest fail "Trivy vulnerability DB SHA-256 mismatch"
run_fixture trivy-stale trivy_stale fail "Trivy vulnerability DB is stale"
run_fixture path-poison path_poison pass "release security fixture: PASS"
run_canonical_digest_reject lock
run_canonical_digest_reject schema

echo "verify-security-tools TS-08 fixtures passed"
