#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd -- "$script_dir/../../.." && pwd -P)"
verifier="$repo_root/scripts/release/verify-codegen-tools.sh"
canonical_lock="$repo_root/security/tools/codegen-tools.lock.json"
schema="$repo_root/security/tools/codegen-tools.schema.json"
makefile="$repo_root/backend/Makefile"

for required in "$verifier" "$canonical_lock" "$schema" "$makefile"; do
  [[ -f "$required" ]] || { echo "missing test input: $required" >&2; exit 1; }
done

python_bin="$(command -v python3 || true)"
[[ -n "$python_bin" ]] || { echo "python3 is required" >&2; exit 1; }

source_bin_dir="${CODEGEN_TOOLS_BIN_DIR:-}"
if [[ -n "$source_bin_dir" ]]; then
  sqlc_source="$source_bin_dir/sqlc"
  wire_source="$source_bin_dir/wire"
else
  sqlc_source="$(command -v sqlc || true)"
  wire_source="$(command -v wire || true)"
fi
[[ -x "$sqlc_source" ]] || { echo "exact sqlc is required" >&2; exit 1; }
[[ -x "$wire_source" ]] || { echo "exact wire is required" >&2; exit 1; }

umask 077
work_dir="$(mktemp -d)"
trap 'rm -rf -- "$work_dir"' EXIT
mkdir -p "$work_dir/exact-bin" "$work_dir/archives"
cp -- "$sqlc_source" "$work_dir/exact-bin/sqlc"
cp -- "$wire_source" "$work_dir/exact-bin/wire"
chmod 0700 "$work_dir/exact-bin/sqlc" "$work_dir/exact-bin/wire"

manifest_patch() {
  local input_path="$1"
  local output_path="$2"
  local operation="$3"
  shift 3
  "$python_bin" - "$input_path" "$output_path" "$operation" "$@" <<'PY'
import json
import pathlib
import sys

input_path, output_path, operation, *values = sys.argv[1:]
manifest = json.loads(pathlib.Path(input_path).read_text(encoding="utf-8"))
tools = {tool["name"]: tool for tool in manifest["tools"]}

if operation == "executable-hash":
    tools["sqlc"]["executable"]["sha256"] = values[0]
elif operation == "archive-hashes":
    tools["sqlc"]["archive"]["sha256"] = values[0]
    tools["wire"]["archive"]["sha256"] = values[1]
elif operation == "unofficial-source":
    tools["sqlc"]["source"]["repository_url"] = "https://example.invalid/sqlc"
elif operation == "license-failure":
    tools["sqlc"]["source"]["license"]["spdx"] = "Apache-2.0"
elif operation == "missing-trust-decision":
    del tools["wire"]["trust_decision"]
else:
    raise SystemExit(f"unknown manifest patch: {operation}")

pathlib.Path(output_path).write_text(
    json.dumps(manifest, indent=2, ensure_ascii=True) + "\n",
    encoding="utf-8",
)
PY
}

create_fixture_archives() {
  local sqlc_binary="$1"
  local output_dir="$2"
  "$python_bin" - "$sqlc_binary" "$output_dir" <<'PY'
import io
import pathlib
import tarfile
import sys

sqlc_path = pathlib.Path(sys.argv[1])
output_dir = pathlib.Path(sys.argv[2])
output_dir.mkdir(parents=True, exist_ok=True)

sqlc_bytes = sqlc_path.read_bytes()
with tarfile.open(output_dir / "sqlc_1.31.1_linux_amd64.tar.gz", "w:gz") as bundle:
    member = tarfile.TarInfo("sqlc")
    member.mode = 0o755
    member.mtime = 0
    member.size = len(sqlc_bytes)
    bundle.addfile(member, io.BytesIO(sqlc_bytes))

with tarfile.open(output_dir / "wire_v0.7.0.tar.gz", "w:gz") as bundle:
    root = tarfile.TarInfo("wire-0.7.0/")
    root.type = tarfile.DIRTYPE
    root.mode = 0o755
    root.mtime = 0
    bundle.addfile(root)
    source = b"package main\n"
    member = tarfile.TarInfo("wire-0.7.0/cmd/wire/main.go")
    member.mode = 0o644
    member.mtime = 0
    member.size = len(source)
    bundle.addfile(member, io.BytesIO(source))
PY
}

create_missing_member_archive() {
  local sqlc_binary="$1"
  local output_path="$2"
  "$python_bin" - "$sqlc_binary" "$output_path" <<'PY'
import io
import pathlib
import tarfile
import sys

content = pathlib.Path(sys.argv[1]).read_bytes()
with tarfile.open(sys.argv[2], "w:gz") as bundle:
    member = tarfile.TarInfo("not-sqlc")
    member.mode = 0o755
    member.mtime = 0
    member.size = len(content)
    bundle.addfile(member, io.BytesIO(content))
PY
}

sha256_file() {
  sha256sum "$1" | awk '{print $1}'
}

case_index=0
expect_pass() {
  local label="$1"
  shift
  case_index=$((case_index + 1))
  local output="$work_dir/case-$case_index.out"
  if ! "$@" >"$output" 2>&1; then
    echo "FAIL: $label" >&2
    sed -n '1,80p' "$output" >&2
    exit 1
  fi
  echo "PASS: $label"
}

expect_fail() {
  local label="$1"
  local expected="$2"
  shift 2
  case_index=$((case_index + 1))
  local output="$work_dir/case-$case_index.out"
  if "$@" >"$output" 2>&1; then
    echo "FAIL: $label unexpectedly passed" >&2
    exit 1
  fi
  if ! grep -Fq -- "$expected" "$output"; then
    echo "FAIL: $label did not report '$expected'" >&2
    sed -n '1,80p' "$output" >&2
    exit 1
  fi
  echo "PASS: $label"
}

expect_pass \
  "exact binaries" \
  bash "$verifier" --lock "$canonical_lock" --schema "$schema" --makefile "$makefile" --bin-dir "$work_dir/exact-bin"

create_fixture_archives "$work_dir/exact-bin/sqlc" "$work_dir/archives"
sqlc_archive_hash="$(sha256_file "$work_dir/archives/sqlc_1.31.1_linux_amd64.tar.gz")"
wire_archive_hash="$(sha256_file "$work_dir/archives/wire_v0.7.0.tar.gz")"
fixture_lock="$work_dir/archive-fixture.lock.json"
manifest_patch "$canonical_lock" "$fixture_lock" archive-hashes "$sqlc_archive_hash" "$wire_archive_hash"
expect_pass \
  "exact archive members" \
  bash "$verifier" --lock "$fixture_lock" --schema "$schema" --makefile "$makefile" --bin-dir "$work_dir/exact-bin" --archives "$work_dir/archives"

mkdir -p "$work_dir/wrong-version-bin"
cp -- "$work_dir/exact-bin/wire" "$work_dir/wrong-version-bin/wire"
cat >"$work_dir/wrong-version-bin/sqlc" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "version" ]]; then
  echo "v0.0.0"
  exit 0
fi
exit 1
EOF
chmod 0700 "$work_dir/wrong-version-bin/sqlc" "$work_dir/wrong-version-bin/wire"
wrong_version_lock="$work_dir/wrong-version.lock.json"
manifest_patch \
  "$canonical_lock" \
  "$wrong_version_lock" \
  executable-hash \
  "$(sha256_file "$work_dir/wrong-version-bin/sqlc")"
expect_fail \
  "wrong version" \
  "sqlc version probe mismatch" \
  bash "$verifier" --lock "$wrong_version_lock" --schema "$schema" --makefile "$makefile" --bin-dir "$work_dir/wrong-version-bin"

mkdir -p "$work_dir/executable-tamper-bin"
cp -- "$work_dir/exact-bin/sqlc" "$work_dir/executable-tamper-bin/sqlc"
cp -- "$work_dir/exact-bin/wire" "$work_dir/executable-tamper-bin/wire"
printf 'tamper' >>"$work_dir/executable-tamper-bin/sqlc"
chmod 0700 "$work_dir/executable-tamper-bin/sqlc" "$work_dir/executable-tamper-bin/wire"
expect_fail \
  "executable tamper" \
  "sqlc executable SHA-256 mismatch" \
  bash "$verifier" --lock "$canonical_lock" --schema "$schema" --makefile "$makefile" --bin-dir "$work_dir/executable-tamper-bin"

cp -R -- "$work_dir/archives" "$work_dir/archive-tamper"
printf 'tamper' >>"$work_dir/archive-tamper/sqlc_1.31.1_linux_amd64.tar.gz"
expect_fail \
  "archive tamper" \
  "sqlc archive SHA-256 mismatch" \
  bash "$verifier" --lock "$fixture_lock" --schema "$schema" --makefile "$makefile" --bin-dir "$work_dir/exact-bin" --archives "$work_dir/archive-tamper"

cp -R -- "$work_dir/archives" "$work_dir/missing-member"
create_missing_member_archive \
  "$work_dir/exact-bin/sqlc" \
  "$work_dir/missing-member/sqlc_1.31.1_linux_amd64.tar.gz"
missing_member_lock="$work_dir/missing-member.lock.json"
manifest_patch \
  "$canonical_lock" \
  "$missing_member_lock" \
  archive-hashes \
  "$(sha256_file "$work_dir/missing-member/sqlc_1.31.1_linux_amd64.tar.gz")" \
  "$wire_archive_hash"
expect_fail \
  "undeclared member" \
  "sqlc declared extraction member is missing" \
  bash "$verifier" --lock "$missing_member_lock" --schema "$schema" --makefile "$makefile" --bin-dir "$work_dir/exact-bin" --archives "$work_dir/missing-member"

unofficial_lock="$work_dir/unofficial-source.lock.json"
manifest_patch "$canonical_lock" "$unofficial_lock" unofficial-source
expect_fail \
  "unofficial source" \
  "sqlc policy mismatch: source.repository_url" \
  bash "$verifier" --lock "$unofficial_lock" --schema "$schema" --makefile "$makefile" --bin-dir "$work_dir/exact-bin"

license_lock="$work_dir/license-failure.lock.json"
manifest_patch "$canonical_lock" "$license_lock" license-failure
expect_fail \
  "license failure" \
  "sqlc policy mismatch: source.license.spdx" \
  bash "$verifier" --lock "$license_lock" --schema "$schema" --makefile "$makefile" --bin-dir "$work_dir/exact-bin"

missing_trust_lock="$work_dir/missing-trust.lock.json"
manifest_patch "$canonical_lock" "$missing_trust_lock" missing-trust-decision
expect_fail \
  "missing trust decision" \
  "trust_decision is required" \
  bash "$verifier" --lock "$missing_trust_lock" --schema "$schema" --makefile "$makefile" --bin-dir "$work_dir/exact-bin"

echo "verify-codegen-tools tests passed"
