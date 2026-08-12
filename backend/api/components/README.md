# OpenAPI Components

Split OpenAPI schemas for the task-per-minute backend, plus the security scheme definitions.

## ⚠️ Do NOT edit `schemas.yml` by hand

`schemas.yml` is **auto-generated** by `scripts/merge-schemas.py` during code generation
and **deleted** at the end of every codegen run. The source of truth is the
`schemas/` subdirectory - that is where you add or change types.

The file may briefly appear during `make gen-openapi` (it is needed by `routes/*.yml`
which `$ref` it before redocly inlines everything) but it is gitignored and removed by
the build pipeline once oapi-codegen is done.

## Generation commands

```bash
# Full pipeline: openapi + sqlc + wire + mockery (and cleanup of schemas.yml).
make generate     # alias of `make gen`

# OpenAPI only - same cleanup.
make openapi      # alias of `make gen-openapi`

# Just merge schemas/*.yml -> schemas.yml without running oapi-codegen.
# Useful when debugging schema-merge issues; you'll need to delete the file
# yourself afterwards or re-run `make openapi`.
make merge-schemas
```

## How it works

1. `scripts/merge-schemas.py` reads every YAML under `components/schemas/` and writes a
   single `components/schemas.yml`. Schema names are taken verbatim from the file
   contents - collisions cause the script to fail loudly.
2. `scripts/openapi-generate.sh` invokes `@redocly/cli bundle` to inline every `$ref`
   (including the freshly merged `schemas.yml`) into a single bundled spec in a
   tempdir.
3. `oapi-codegen` runs against the bundled spec for every config under
   `codegen/oapi-codegen-*.yml` (types, server, spec).
4. The script removes `schemas.yml` so the working tree stays clean. The bundled
   tempdir is wiped by a shell `trap`.

## Layout

```text
components/
├── schemas/               # source of truth - edit these
│   ├── admin_schemas.yml
│   ├── common_schemas.yml
│   ├── duel_schemas.yml
│   ├── player_schemas.yml
│   └── task_schemas.yml
├── schemas.yml            # AUTO-GENERATED, gitignored, recreated on every gen
├── security.yml           # security schemes (bearer/session-token), edited by hand
└── README.md              # this file

```

## Editing workflow

1. Add or change schemas in `components/schemas/<domain>_schemas.yml`.
2. Run `make openapi` (or `make generate` for the full pipeline).
3. Commit only the source YAMLs and the regenerated `*.gen.go` files.
   `schemas.yml` should never appear in `git status`.

## Routes

`routes/*.yml` keep referencing `../components/schemas.yml#/<TypeName>`. That works
because `schemas.yml` exists during `make openapi` for the duration of bundling and
then disappears. Do not switch the references to per-file paths - the merged file
intentionally hides the per-domain split from oapi-codegen so route fragments stay
adapter-agnostic.
