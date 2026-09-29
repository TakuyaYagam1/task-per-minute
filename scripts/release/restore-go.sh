#!/usr/bin/env bash
set -euo pipefail
umask 077

usage() {
  cat <<'EOF'
Usage: restore-go.sh [absolute-path-to-go1.26.8.linux-amd64.tar.gz]

Download or verify the official Go 1.26.8 archive, then add it to the local
Nix store. The optional archive path supports offline restoration.
EOF
}

if (($# > 1)); then
  usage >&2
  exit 2
fi

readonly version='1.26.8'
readonly archive_url='https://go.dev/dl/go1.26.8.linux-amd64.tar.gz'
readonly archive_sha256='d0f743b33e8d8945e6b1f432edd15785c70507121d6e2a723b21285eddf8b57b'
readonly expected_root='/nix/store/62rzn370ba6jc0sfvmb9a93s4619f6kv-go-1.26.8'
readonly expected_nar_hash='sha256:1s8nf1a8q1hqkimvhyk044r105dzwrplk19kxg6mwpbkjd24hlxx'
readonly expected_go_sha256='d9a2fa19c7ef8b57f420012c21f49f235c46f08a68c12077d9c753dbb6ccdc34'
readonly nix_bin='/run/current-system/sw/bin/nix'
readonly nix_store_bin='/run/current-system/sw/bin/nix-store'
readonly curl_bin='/run/current-system/sw/bin/curl'
readonly chmod_bin='/run/current-system/sw/bin/chmod'
readonly cp_bin='/run/current-system/sw/bin/cp'
readonly mkdir_bin='/run/current-system/sw/bin/mkdir'
readonly mktemp_bin='/run/current-system/sw/bin/mktemp'
readonly rm_bin='/run/current-system/sw/bin/rm'
readonly sha256sum_bin='/run/current-system/sw/bin/sha256sum'
readonly stat_bin='/run/current-system/sw/bin/stat'
readonly python_bin='/nix/store/gxzhl7aaiid7zp3y47jqqiq7zg5mqpwp-python3-3.14.6/bin/python3.14'
readonly python_sha256='465d82f95e8e1069347b0ebf288d14d37802a1ae6cb831c15953cba0859ff766'

fail() {
  printf 'restore-go: %s\n' "$1" >&2
  exit 1
}

for executable in \
  "$nix_bin" "$nix_store_bin" "$chmod_bin" "$cp_bin" \
  "$mkdir_bin" "$mktemp_bin" "$rm_bin" "$sha256sum_bin" "$stat_bin" \
  "$python_bin"
do
  [[ -f "$executable" && -x "$executable" ]] || fail "required tool is unavailable: $executable"
done
if (($# == 0)); then
  [[ -f "$curl_bin" && -x "$curl_bin" ]] || fail "required tool is unavailable: $curl_bin"
fi

python_mode="$("$stat_bin" -c '%a' "$python_bin")"
[[ "$python_mode" == '555' ]] || fail 'pinned Python mode differs from its immutable declaration'
python_digest="$("$sha256sum_bin" -- "$python_bin")"
python_digest="${python_digest%% *}"
[[ "$python_digest" == "$python_sha256" ]] || fail 'pinned Python digest differs from its immutable declaration'

home_dir="${HOME:-}"
[[ "$home_dir" == /* ]] || fail 'HOME must be an absolute path'
codex_dir="$home_dir/.codex"
tmp_root="$codex_dir/.tmp"
if [[ ! -e "$codex_dir" && ! -L "$codex_dir" ]]; then
  "$mkdir_bin" -m 0700 -- "$codex_dir"
fi
[[ -d "$codex_dir" && ! -L "$codex_dir" && -O "$codex_dir" ]] || fail 'Codex home must be a user-owned directory'
if [[ ! -e "$tmp_root" && ! -L "$tmp_root" ]]; then
  "$mkdir_bin" -m 0700 -- "$tmp_root"
fi
[[ -d "$tmp_root" && ! -L "$tmp_root" && -O "$tmp_root" ]] || fail 'Codex temp root must be a user-owned directory'
[[ "$("$stat_bin" -c '%a' "$tmp_root")" == '700' ]] || fail 'Codex temp root must have mode 0700'

work_dir=''
cleanup() {
  local exit_code=$?
  trap - EXIT
  if [[ -n "$work_dir" && "$work_dir" == "$tmp_root"/go-1.26.8.* && -d "$work_dir" && ! -L "$work_dir" ]]; then
    "$rm_bin" -rf -- "$work_dir"
  fi
  exit "$exit_code"
}
trap cleanup EXIT

work_dir="$("$mktemp_bin" -d -- "$tmp_root/go-1.26.8.XXXXXXXX")"
[[ -d "$work_dir" && ! -L "$work_dir" && -O "$work_dir" ]] || fail 'private work directory was not created safely'
[[ "$("$stat_bin" -c '%a' "$work_dir")" == '700' ]] || fail 'private work directory must have mode 0700'
archive="$work_dir/go1.26.8.linux-amd64.tar.gz"

if (($# == 1)); then
  archive_source="$1"
  [[ "$archive_source" == /* && -f "$archive_source" && ! -L "$archive_source" ]] || fail 'archive path must be an absolute regular file, not a symlink'
  "$cp_bin" -- "$archive_source" "$archive"
else
  "$curl_bin" \
    --fail \
    --silent \
    --show-error \
    --location \
    --proto '=https' \
    --proto-redir '=https' \
    --connect-timeout 20 \
    --max-time 600 \
    --max-redirs 5 \
    --max-filesize 200000000 \
    --retry 0 \
    --output "$archive" \
    "$archive_url"
fi
"$chmod_bin" 0600 -- "$archive"
archive_digest="$("$sha256sum_bin" -- "$archive")"
archive_digest="${archive_digest%% *}"
[[ "$archive_digest" == "$archive_sha256" ]] || fail 'official archive SHA-256 does not match the pinned release digest'

extract_dir="$work_dir/unpacked"
"$python_bin" -I - "$archive" "$extract_dir" <<'PY'
from __future__ import annotations

import os
import pathlib
import stat
import sys
import tarfile


archive = pathlib.Path(sys.argv[1])
destination = pathlib.Path(sys.argv[2])
if destination.exists() or destination.is_symlink():
    raise SystemExit("extraction destination already exists")
destination.mkdir(mode=0o700)
seen: set[str] = set()
file_names: set[str] = set()
expanded_bytes = 0
members: list[tarfile.TarInfo] = []

with tarfile.open(archive, "r:gz") as source:
    for member in source:
        raw_name = member.name
        name = raw_name[:-1] if raw_name.endswith("/") else raw_name
        parts = name.split("/")
        if (
            raw_name.startswith("/")
            or "\\" in raw_name
            or not name
            or any(part in {"", ".", ".."} for part in parts)
            or parts[0] != "go"
        ):
            raise SystemExit(f"unsafe archive path: {raw_name!r}")
        if name in seen:
            raise SystemExit(f"duplicate archive path: {name!r}")
        seen.add(name)
        if member.mode & 0o7000:
            raise SystemExit(f"special permission bits in archive: {name!r}")
        if member.isdir():
            pass
        elif member.isfile():
            expanded_bytes += member.size
            file_names.add(name)
        else:
            raise SystemExit(f"unsupported archive entry type: {name!r}")
        if len(seen) > 50000 or expanded_bytes > 500 * 1024 * 1024:
            raise SystemExit("archive exceeds the expected size limits")
        members.append(member)

    if "go" not in seen or "go/bin/go" not in file_names:
        raise SystemExit("archive does not contain the expected Go installation")
    root_member = next(member for member in members if member.name.rstrip("/") == "go")
    if not root_member.isdir():
        raise SystemExit("archive root is not a directory")
    source.extractall(destination, members=members, filter="data")

go_root = destination / "go"
go_executable = go_root / "bin" / "go"
if go_root.is_symlink() or not go_root.is_dir():
    raise SystemExit("extracted Go root is not a plain directory")
if go_executable.is_symlink() or not go_executable.is_file():
    raise SystemExit("extracted Go executable is not a plain file")
if stat.S_IMODE(go_executable.stat().st_mode) != 0o755:
    raise SystemExit("archive Go executable mode differs from the official release layout")
PY

go_source="$extract_dir/go"
go_digest="$("$sha256sum_bin" -- "$go_source/bin/go")"
go_digest="${go_digest%% *}"
[[ "$go_digest" == "$expected_go_sha256" ]] || fail 'Go executable SHA-256 does not match the pinned identity'
go_version="$(GOTOOLCHAIN=local GOENV=off "$go_source/bin/go" version)"
[[ "$go_version" == 'go version go1.26.8 linux/amd64' ]] || fail 'extracted Go runtime reports an unexpected version'

imported_root="$("$nix_bin" store add --offline --name "go-$version" "$go_source")"
[[ "$imported_root" == "$expected_root" ]] || fail "Nix produced an unexpected store root: $imported_root"
nar_hash="$("$nix_store_bin" -q --hash "$imported_root")"
[[ "$nar_hash" == "$expected_nar_hash" ]] || fail 'imported Nix root NAR hash differs from the pinned identity'
stored_go="$imported_root/bin/go"
[[ -f "$stored_go" && ! -L "$stored_go" ]] || fail 'imported Go executable is missing or not a regular file'
[[ "$("$stat_bin" -c '%a' "$stored_go")" == '555' ]] || fail 'imported Go executable mode differs from the immutable declaration'
stored_digest="$("$sha256sum_bin" -- "$stored_go")"
stored_digest="${stored_digest%% *}"
[[ "$stored_digest" == "$expected_go_sha256" ]] || fail 'imported Go executable digest differs from the pinned identity'
stored_version="$(GOTOOLCHAIN=local GOENV=off "$stored_go" version)"
[[ "$stored_version" == 'go version go1.26.8 linux/amd64' ]] || fail 'imported Go runtime reports an unexpected version'

printf 'root=%s\nnar_hash=%s\ngo_sha256=%s\nversion=%s\n' \
  "$imported_root" "$nar_hash" "$stored_digest" "$stored_version"
