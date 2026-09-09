#!/usr/bin/env bash
set -euo pipefail

fail() {
  printf 'create migration: %s\n' "$*" >&2
  exit 1
}

if [ "$#" -ne 2 ]; then
  fail 'usage: create-migration.sh <migration-dir> <snake_case_name>'
fi

migration_dir="$1"
migration_name="$2"

[ -d "$migration_dir" ] || fail "migration directory does not exist: $migration_dir"
[[ "$migration_name" =~ ^[a-z][a-z0-9]*(_[a-z0-9]+)*$ ]] ||
  fail 'name must be lower snake_case'

declare -A seen_versions=()
latest_version=0
shopt -s nullglob
migration_files=("$migration_dir"/[0-9]*_*.sql)

for migration_file in "${migration_files[@]}"; do
  filename="${migration_file##*/}"
  version="${filename%%_*}"
  [[ "$version" =~ ^[0-9]{6}$ ]] ||
    fail "migration must use a six-digit version: $filename"

  numeric_version=$((10#$version))
  if [[ -n "${seen_versions[$numeric_version]:-}" ]]; then
    fail "duplicate migration version $version: ${seen_versions[$numeric_version]} and $filename"
  fi
  seen_versions[$numeric_version]="$filename"

  if ((numeric_version > latest_version)); then
    latest_version=$numeric_version
  fi
done

next_version=$((latest_version + 1))
((next_version <= 999999)) || fail 'six-digit migration sequence is exhausted'
printf -v version_prefix '%06d' "$next_version"
target="$migration_dir/${version_prefix}_${migration_name}.sql"

if ! (set -o noclobber; printf '%s\n' \
  '-- +goose Up' \
  '-- +goose StatementBegin' \
  '' \
  '-- +goose StatementEnd' \
  '' \
  '-- +goose Down' \
  '-- +goose StatementBegin' \
  '' \
  '-- +goose StatementEnd' >"$target"); then
  fail "migration already exists: $target"
fi

printf 'Created %s\n' "$target"
