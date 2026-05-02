#!/usr/bin/env bash
# Merge per-domain schema YAMLs into one file, then run oapi-codegen for
# every codegen/oapi-codegen-*.yml config we have (types, server, spec).
#
# Run from the backend/ root: bash scripts/openapi-generate.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

if [ -z "${OAPI_VERSION:-}" ]; then
  OAPI_VERSION="$(awk -F '\\?=' '/^OAPI_VERSION[[:space:]]*\\?=/ {gsub(/[[:space:]]/, "", $2); print $2; exit}' Makefile)"
fi
OAPI_VERSION="${OAPI_VERSION:-v2.5.1}"
OAPI="go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@${OAPI_VERSION}"

PYTHON_BIN="${PYTHON_BIN:-python3}"
"$PYTHON_BIN" scripts/merge-schemas.py

SPEC="internal/openapi/openapi.yml"

shopt -s nullglob
configs=(codegen/oapi-codegen-*.yml codegen/oapi-codegen-*.yaml)
shopt -u nullglob

if [ ${#configs[@]} -eq 0 ]; then
  echo "openapi-generate.sh: no codegen/oapi-codegen-*.yml configs yet."
  exit 0
fi

# oapi-codegen does not chase external $refs; inline them with @redocly/cli first.
BUNDLE_DIR="$(mktemp -d)"
trap 'rm -rf "$BUNDLE_DIR"' EXIT
BUNDLE="$BUNDLE_DIR/openapi.bundle.yml"

REDOCLY_VERSION="${REDOCLY_VERSION:-1.34.0}"
echo "openapi-generate.sh: bundling $SPEC -> $BUNDLE"
npx -y "@redocly/cli@${REDOCLY_VERSION}" bundle "$SPEC" -o "$BUNDLE" --ext yml >/dev/null

for cfg in "${configs[@]}"; do
  echo "openapi-generate.sh: $OAPI -config $cfg $BUNDLE"
  $OAPI -config "$cfg" "$BUNDLE"
done

# Drop the merged schemas.yml - it is a build artefact reconstructed by
# merge-schemas.py on every run. The bundled openapi.bundle.yml in $BUNDLE_DIR
# is auto-removed by the trap above.
rm -f internal/openapi/components/schemas.yml
