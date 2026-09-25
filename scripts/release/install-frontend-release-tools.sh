#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

if [[ $# -ne 2 || "$1" != --output || "$2" != /* ]]; then
  echo 'usage: install-frontend-release-tools.sh --output ABSOLUTE_NEW_DIRECTORY' >&2
  exit 1
fi
release_tools_dir="$2"
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || { echo 'only Linux x86_64 is supported' >&2; exit 1; }

# Create one new directory. Refuse existing paths, symlink parents and traversal.
python3 -I - "$release_tools_dir" <<'PY'
import os
import pathlib
import stat
import sys

target = sys.argv[1]
if any(ord(char) < 32 or ord(char) == 127 for char in target) or '\\' in target or '..' in target.split('/'):
    raise SystemExit('invalid output path')
parts = pathlib.Path(target).parts
current = pathlib.Path(parts[0])
for part in parts[1:-1]:
    current /= part
    if not stat.S_ISDIR(os.lstat(current).st_mode):
        raise SystemExit('output parent must be a regular directory, not a symlink')
os.mkdir(target, mode=0o700)
PY

download() {
  curl --disable --fail --location --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --connect-timeout 20 --max-time 180 --retry 2 --silent --show-error --output "$release_tools_dir/$1" "$2"
  printf '%s  %s\n' "$3" "$release_tools_dir/$1" | sha256sum --check --status
}

download cosign https://github.com/sigstore/cosign/releases/download/v3.1.3/cosign-linux-amd64 \
  4629c757b7618056f8ddd7e2625ae9fdd94c0372a65049520bc7d9df9efc7f71
download oras.tar.gz https://github.com/oras-project/oras/releases/download/v1.3.4/oras_1.3.4_linux_amd64.tar.gz \
  f27adb935022d94df8dc77719c322dda592c78a0d57a6f7dcdd8d900b248c454
download trivy.tar.gz https://github.com/aquasecurity/trivy/releases/download/v0.72.0/trivy_0.72.0_Linux-64bit.tar.gz \
  bbb64b9695866ce4a7a8f5c9592002c5961cab378577fa3f8a040df362b9b2ea

# Do not extract archive paths, links, ownership, permissions or other entries.
python3 -I - "$release_tools_dir" <<'PY'
import pathlib
import shutil
import sys
import tarfile

root = pathlib.Path(sys.argv[1])
for name in ('oras', 'trivy'):
    with tarfile.open(root / (name + '.tar.gz'), 'r:gz') as archive:
        entries = [entry for entry in archive.getmembers() if entry.name == name]
        if len(entries) != 1 or not entries[0].isfile() or not 0 < entries[0].size <= 250 * 1024 * 1024:
            raise SystemExit('archive must contain one bounded regular tool entry')
        with archive.extractfile(entries[0]) as source, (root / name).open('xb') as output:
            shutil.copyfileobj(source, output, length=1024 * 1024)
PY
printf '%s  %s\n' 246c47e91bf2749a555ffe00a9824844c6df3a26d61974e3ce08f2077d79c556 "$release_tools_dir/oras" | sha256sum --check --status
printf '%s  %s\n' 0e69edd134a3c338baa1a6806920773615d682b18cbc6a0cba2a3b658ef9b63e "$release_tools_dir/trivy" | sha256sum --check --status
chmod 700 "$release_tools_dir/cosign" "$release_tools_dir/oras" "$release_tools_dir/trivy"
