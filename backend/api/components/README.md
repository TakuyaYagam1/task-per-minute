# OpenAPI Components

Split OpenAPI schemas and shared transport components for the task-per-minute backend.

## Generated merge

The source of truth is the `schemas/` subdirectory. The generator merges those
files into a private temporary `schemas.yml`, bundles every local reference,
and removes the temporary directory when generation finishes. There is no
repository-local merged-schema workflow or intermediate to maintain.

## Generation commands

```bash
# Full pipeline: openapi + sqlc + wire + mockery.
make generate     # alias of `make gen`

# OpenAPI only.
make openapi      # alias of `make gen-openapi`
```

## How it works

1. `scripts/openapi-generate.sh` calls `scripts/merge-schemas.py` with explicit
   source and temporary output paths. Schema names are taken verbatim from the
   source files - collisions cause the merge to fail loudly.
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
│   ├── leaderboard_schemas.yml
│   ├── player_schemas.yml
│   ├── task_schemas.yml
│   └── tournament_schemas.yml
├── parameters.yml         # shared request parameters
├── responses.yml          # shared error responses
├── security.yml           # admin/player cookie security schemes, edited by hand
└── README.md              # this file

```

## Editing workflow

1. Add or change schemas in `components/schemas/<domain>_schemas.yml`.
2. Run `make openapi` (or `make generate` for the full pipeline).
3. Commit the source YAMLs, regenerated `*.gen.go` files, and regenerated frontend
   API types.

## Routes

`routes/*.yml` keep referencing `../components/schemas.yml#/<TypeName>`. That works
because the generator copies `api/` into its tempdir and creates `schemas.yml` there
before bundling. Do not switch the references to per-file paths - the merged file
intentionally hides the per-domain split from generators so route fragments stay
adapter-agnostic.

## Naming

Tags follow the resource domain: `admin` is reserved for admin session
operations, while player management uses `player` and task management uses
`task`. Operation IDs use lower camel case with the verb first. Schema names
describe the resource or response view, not the authenticated actor that can
access it.
