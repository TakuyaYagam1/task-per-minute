# OpenAPI Components

Split OpenAPI schemas for the task-per-minute backend, plus the security scheme definitions.

## ⚠️ Do NOT edit `schemas.yml` by hand

`schemas.yml` is generated only inside the code generator's private temporary
directory. The source of truth is the `schemas/` subdirectory - that is where
you add or change types.

## Generation commands

```bash
# Full pipeline: openapi + sqlc + wire + mockery.
make generate     # alias of `make gen`

# OpenAPI only.
make openapi      # alias of `make gen-openapi`

# Just merge schemas/*.yml -> schemas.yml without running code generation.
# This compatibility target writes the ignored repository intermediate.
make merge-schemas
```

## How it works

1. `scripts/merge-schemas.py` reads every YAML under `components/schemas/` and writes a
   single mapping to the explicit temporary destination. Schema names are taken
   verbatim from the file contents - collisions cause the script to fail loudly.
2. `scripts/openapi-generate.sh` invokes `@redocly/cli bundle` to inline every `$ref`
   (including the freshly merged `schemas.yml`) into a single bundled spec in a
   tempdir.
3. `oapi-codegen` runs against the bundled spec for every config under
   `codegen/oapi-codegen-*.yml` (types, server, spec).
4. The same bundle produces frontend API types. The private tempdir is wiped by
   a shell `trap`, so generation leaves no repository intermediate.

## Layout

```text
components/
├── schemas/               # source of truth - edit these
│   ├── admin_schemas.yml
│   ├── common_schemas.yml
│   ├── duel_schemas.yml
│   ├── player_schemas.yml
│   └── task_schemas.yml
├── schemas.yml            # ignored compatibility output of make merge-schemas
├── security.yml           # security schemes (bearer/session-token), edited by hand
└── README.md              # this file

```

## Editing workflow

1. Add or change schemas in `components/schemas/<domain>_schemas.yml`.
2. Run `make openapi` (or `make generate` for the full pipeline).
3. Commit the source YAMLs, regenerated `*.gen.go` files, and regenerated frontend
   API types. `schemas.yml` should never appear in `git status`.

## Routes

`routes/*.yml` keep referencing `../components/schemas.yml#/<TypeName>`. That works
because the generator copies `api/` into its tempdir and creates `schemas.yml` there
before bundling. Do not switch the references to per-file paths - the merged file
intentionally hides the per-domain split from generators so route fragments stay
adapter-agnostic.
