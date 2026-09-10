# OpenAPI toolchain trust record

This record covers the development-only executables used by the OpenAPI
generator and the YAML compatibility library required by the frontend tool
graph. It does not approve these packages for untrusted schemas, runtime use,
or unrestricted network access. Evidence was refreshed on 2026-09-10 from the
official npm registry, Go module services, GitHub, and OpenSSF Scorecard.

## Machine-checked policy

The release validator reads this block. Keep every key unique and refresh
security evidence within 120 days.

```text
policy.format=openapi-toolchain-trust-v1
evidence.retrieved=2026-09-10
redocly.package=@redocly/cli
redocly.version=2.51.2
redocly.publisher=Redocly npm scope; version published by npm account romanhotsiy
redocly.repository=https://github.com/Redocly/redocly-cli
redocly.registry=https://registry.npmjs.org/
redocly.license=MIT
redocly.integrity=sha512-pviW1gfsjCAuIVutmQcihlhlgoivfNzisBIR61EwSSREHGZ08Sbtjp/h/HPDkVavWwdzR/gs2wgHrdXLd6qU1A==
redocly.tarball_sha1=def2580b471a8ae9e3239f46f30670e1c46f2785
redocly.security_evidence_date=2026-09-06
redocly.scorecard_url=
redocly.scorecard_justification=OpenSSF returned HTTP 404 on 2026-09-06; official npm provenance, GitHub maintenance metadata, repository advisories, and a clean npm audit are the bounded substitute
redocly.trust_decision=accepted-bounded
redocly.residual_risk=development CLI with a transitive dependency graph; run only bundle and lint on reviewed local schemas with telemetry and update checks disabled
yaml.package=yaml
yaml.version=2.9.0
yaml.publisher=eemeli npm account
yaml.repository=https://github.com/eemeli/yaml
yaml.registry=https://registry.npmjs.org/
yaml.license=ISC
yaml.integrity=sha512-2AvhNX3mb8zd6Zy7INTtSpl1F15HW6Wnqj0srWlkKLcpYl/gMIMJiyuGq2KeI2YFxUPjdlB+3Lc10seMLtL4cA==
yaml.tarball_sha1=78274afd93598a1dfdd6130df6a566defcbf9aa4
yaml.security_evidence_date=2026-09-06
yaml.trust_decision=accepted-compatibility
yaml.residual_risk=the package exposes an unused CLI; this project resolves only its library API for the locked development tool graph
js_yaml.package=js-yaml
js_yaml.version=4.3.2
js_yaml.publisher=vitaly npm account
js_yaml.repository=https://github.com/nodeca/js-yaml
js_yaml.registry=https://registry.npmjs.org/
js_yaml.license=MIT
js_yaml.integrity=sha512-SFNOvSJ+Dgf/9An904Yx+CgSlIPCkIpao4qo51lpee25TIRejdH3rhR4EZMGoNx3/TP3O+wzWuiTFl4sqbltzA==
js_yaml.tarball_sha1=8e44fb14a2643c59726bb15787b5f1512cb3d3fb
js_yaml.security_evidence_date=2026-09-10
js_yaml.advisory=https://github.com/advisories/GHSA-2883-xcg3-v3hh
js_yaml.trust_decision=accepted-patched
js_yaml.residual_risk=development parser shared by OpenAPI and lint tools; retain the exact root override until every direct owner accepts 4.3.2 or later
openapi_typescript.package=openapi-typescript
openapi_typescript.version=7.13.0
openapi_typescript.publisher=npm maintainers drewpowers and gzm0
openapi_typescript.repository=https://github.com/openapi-ts/openapi-typescript
openapi_typescript.registry=https://registry.npmjs.org/
openapi_typescript.license=MIT
openapi_typescript.integrity=sha512-EFP392gcqXS7ntPvbhBzbF8TyBA+baIYEm791Hy5YkjDYKTnk/Tn5OQeKm5BIZvJihpp8Zzr4hzx0Irde1LNGQ==
openapi_typescript.tarball_sha1=5d0dc5e95d648fba85b15351a0b91c5815d8360b
openapi_typescript.security_evidence_date=2026-09-06
openapi_typescript.trust_decision=accepted-bounded
openapi_typescript.residual_risk=development generator with a transitive dependency graph; run only against reviewed local bundled schemas and inspect generated diffs
oapi.module=github.com/oapi-codegen/oapi-codegen/v2
oapi.command=github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen
oapi.version=v2.8.0
oapi.publisher=oapi-codegen GitHub organization
oapi.repository=https://github.com/oapi-codegen/oapi-codegen
oapi.proxy=https://proxy.golang.org
oapi.sumdb=https://sum.golang.org
oapi.license=Apache-2.0
oapi.module_sum=h1:s4hxMxuqtR8jPzXkBTtFwY/SBuj3gEAYikmbBSdtLMM=
oapi.mod_sum=h1:yae2TI9IYB5vxQ35gFrpXh9L5H1eJv4MAUK1jumGMTo=
oapi.security_evidence_date=2026-09-06
oapi.scorecard_url=https://api.securityscorecards.dev/projects/github.com/oapi-codegen/oapi-codegen
oapi.scorecard_observed_at=2026-08-31T05:18:06Z
oapi.scorecard_score=7.4
oapi.scorecard_justification=
oapi.trust_decision=accepted-bounded
oapi.residual_risk=code generation executes against repository input; retain reviewed local-only schemas, offline generation, generated-diff review, and pinned checksums
```

## Redocly CLI 2.51.2

The npm version record identifies the official Redocly repository, MIT
license, publishing accounts, tarball, SHA-512 integrity, SHA-1 digest, and
source commit `8938c898beaf8849054f6660427bfafe03b89a54`. The package was
published on 2026-09-04. It requires Node 22.12 or newer, which is satisfied by
the repository Node 24.15 baseline.

The previous 1.34.0 pin was affected by path traversal and expression execution
advisories. Version 2.51.2 is outside the affected ranges for
GHSA-657c-g7qc-r9j2 and GHSA-xw2f-5386-m542. The complete locked npm graph was
audited after the upgrade with no remaining findings.

Official evidence:

- https://registry.npmjs.org/@redocly%2fcli/2.51.2
- https://api.github.com/repos/Redocly/redocly-cli
- https://api.github.com/repos/Redocly/redocly-cli/commits/8938c898beaf8849054f6660427bfafe03b89a54
- https://api.github.com/repos/Redocly/redocly-cli/security-advisories
- https://api.securityscorecards.dev/projects/github.com/Redocly/redocly-cli

## YAML compatibility library 2.9.0

The direct YAML pin remains at the current 2.9.0 release. The official npm
record maps it to `eemeli/yaml`, the ISC license, and the integrity values
recorded above. The generator does not invoke its CLI. Dependency preparation
and installation disable lifecycle scripts.

Official evidence:

- https://registry.npmjs.org/yaml/2.9.0
- https://github.com/eemeli/yaml

## js-yaml 4.3.2 transitive patch

The complete audit on 2026-09-10 reported two development-only findings and
no production findings. Both entries had the same root cause:

| Audit entry | Production reachable | Development reachable | Root owner | Transitive owner | Resolution |
| --- | --- | --- | --- | --- | --- |
| `js-yaml`, advisory 1193727 | No | Yes | `openapi-typescript`, `eslint`, and `eslint-config-next` | `@redocly/openapi-core`, `eslint`, and `@eslint/eslintrc` | Exact root override to 4.3.2 |
| `@redocly/openapi-core` | No | Yes | `openapi-typescript` | `@redocly/openapi-core` -> `js-yaml` | Resolved by the same 4.3.2 override |

The advisory affects js-yaml 4.x releases before 4.3.2 and permits excessive
CPU use while parsing crafted merge sources. Version 4.3.2 contains the fix.
The npm record maps that release to the official `nodeca/js-yaml` repository,
the MIT license, source commit `79ca68d90f333fbe6d9e42827527e62636200191`,
and the integrity values recorded above. The lockfile override is limited to a
patch release and removes both audit findings without an exception.

Official evidence:

- https://registry.npmjs.org/js-yaml/4.3.2
- https://github.com/nodeca/js-yaml/releases/tag/4.3.2
- https://github.com/advisories/GHSA-2883-xcg3-v3hh

## openapi-typescript 7.13.0

The npm version record identifies the official openapi-ts repository, MIT
license, publishing maintainers, tarball, SHA-512 integrity, SHA-1 digest, npm
signature, and SLSA provenance attestation. The package is pinned exactly in
both the manifest and lockfile. The dependency advisory gate reported no
findings and no reviewed exceptions after the toolchain update.

Generation explicitly sets `--default-non-nullable false`. Version 7.13.0
otherwise treats properties with a default value as required TypeScript
fields, even when the OpenAPI schema omits them from `required`. The explicit
compatibility option keeps generated request types aligned with the canonical
OpenAPI required lists and with backend defaulting behavior.

Official evidence:

- https://registry.npmjs.org/openapi-typescript/7.13.0
- https://github.com/openapi-ts/openapi-typescript
- https://openapi-ts.dev/cli

## oapi-codegen v2.8.0

The Go proxy and checksum database map v2.8.0 to the official repository tag
and commit `de2d8b2b0afb287198554eb305bb0d2687d26a85`. The release requires Go
1.24.4 or newer; the isolated tools module records Go 1.25 and the repository
uses Go 1.26. The module and go.mod checksums are pinned above.

Version 2.8.0 follows the v2.7.x fixes for generated-code injection through
server descriptions, enum values, route paths, and type imports. The generator
still treats repository schemas as reviewed code input and never accepts remote
references.

The server and model generators temporarily set
`compatibility.enable-auth-scopes-on-context: true`. Existing authentication
middleware reads the generated scope constants from request context, while
v2.8 disables that legacy context population by default. The current contract
uses one security scheme per protected operation, so this compatibility mode
does not flatten an OR or AND security expression. A later transport-only
change can move the middleware to the generated `AuthenticationFunc` hook and
remove the compatibility setting without changing application contracts.

Official evidence:

- https://proxy.golang.org/github.com/oapi-codegen/oapi-codegen/v2/@v/v2.8.0.info
- https://proxy.golang.org/github.com/oapi-codegen/oapi-codegen/v2/@v/v2.8.0.mod
- https://sum.golang.org/lookup/github.com/oapi-codegen/oapi-codegen/v2@v2.8.0
- https://github.com/oapi-codegen/oapi-codegen/releases/tag/v2.8.0
- https://api.github.com/repos/oapi-codegen/oapi-codegen/security-advisories
- https://api.securityscorecards.dev/projects/github.com/oapi-codegen/oapi-codegen

## Operating boundary

`frontend/package-lock.json` locks the complete npm resolution to HTTPS npm
registry archives with SHA-512 integrity. Lock preparation and installation use
the credential-free npm config and disable lifecycle scripts. The generator
requires installed local binaries whose identities match the lock.

`backend/tools/openapi/go.sum` locks the isolated Go tool graph. Normal
generation runs `go mod verify` and builds with local toolchain, offline module
resolution, read-only module mode, and no workspace override. Missing cached
dependencies fail closed.

The generator copies reviewed local OpenAPI inputs into a private temporary
directory, rejects remote references, disables telemetry and update notices,
and removes only that temporary directory. Generated Go and TypeScript output
remains review material and must pass the parity and contract gates.
