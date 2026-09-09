#!/usr/bin/env bash
set -euo pipefail

backend_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
generator="$backend_root/scripts/create-migration.sh"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/create-migration-test.XXXXXX")"
trap 'rm -rf -- "$test_root"' EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_rejected() {
  if "$@" >"$test_root/rejected.out" 2>&1; then
    fail "command unexpectedly succeeded: $*"
  fi
}

empty_dir="$test_root/empty"
mkdir -p -- "$empty_dir"
bash "$generator" "$empty_dir" create_players >"$test_root/empty.out"
[ -f "$empty_dir/000001_create_players.sql" ] ||
  fail 'empty directory did not start at version 000001'

generated="$empty_dir/000001_create_players.sql"
[ "$(grep -c '^-- +goose Up$' "$generated")" -eq 1 ] ||
  fail 'generated migration has an invalid Up section'
[ "$(grep -c '^-- +goose Down$' "$generated")" -eq 1 ] ||
  fail 'generated migration has an invalid Down section'
[ "$(grep -c '^-- +goose StatementBegin$' "$generated")" -eq 2 ] ||
  fail 'generated migration has invalid StatementBegin markers'
[ "$(grep -c '^-- +goose StatementEnd$' "$generated")" -eq 2 ] ||
  fail 'generated migration has invalid StatementEnd markers'

baseline_dir="$test_root/baseline"
mkdir -p -- "$baseline_dir"
for version in $(seq -w 1 11); do
  printf '%s\n' '-- fixture' >"$baseline_dir/0000${version}_domain_schema.sql"
done
bash "$generator" "$baseline_dir" add_player_badge >"$test_root/baseline.out"
[ -f "$baseline_dir/000012_add_player_badge.sql" ] ||
  fail 'eleven-file baseline did not advance to version 000012'

before_invalid="$(find "$baseline_dir" -maxdepth 1 -type f | wc -l)"
assert_rejected bash "$generator" "$baseline_dir" 'Not-Snake-Case'
after_invalid="$(find "$baseline_dir" -maxdepth 1 -type f | wc -l)"
[ "$before_invalid" -eq "$after_invalid" ] ||
  fail 'invalid name created a migration'

duplicate_dir="$test_root/duplicate"
mkdir -p -- "$duplicate_dir"
printf '%s\n' '-- fixture' >"$duplicate_dir/000012_first.sql"
printf '%s\n' '-- fixture' >"$duplicate_dir/000012_second.sql"
assert_rejected bash "$generator" "$duplicate_dir" add_index

wrong_width_dir="$test_root/wrong-width"
mkdir -p -- "$wrong_width_dir"
printf '%s\n' '-- fixture' >"$wrong_width_dir/00012_old_style.sql"
assert_rejected bash "$generator" "$wrong_width_dir" add_index

printf 'create migration tests passed\n'
