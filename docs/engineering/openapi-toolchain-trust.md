# OpenAPI toolchain trust record

This record covers the two development-only executables used by the OpenAPI
generator and the YAML compatibility library required by the pinned Redocly
package. It does not approve these packages for untrusted schemas, runtime use,
or network access. Evidence was retrieved on 2026-09-02 from the official npm
registry, Go module services, GitHub API, and OpenSSF Scorecard API.

## Machine-checked policy

The release validator reads this block. Keep every key unique and refresh
security evidence within 120 days.

```text
policy.format=openapi-toolchain-trust-v1
evidence.retrieved=2026-09-02
redocly.package=@redocly/cli
redocly.version=1.34.0
redocly.publisher=Redocly npm scope; version published by npm account romanhotsiy
redocly.repository=https://github.com/Redocly/redocly-cli
redocly.registry=https://registry.npmjs.org/
redocly.license=MIT
redocly.integrity=sha512-Kg/t9zMjZB5cyb0YQLa+gne5E5Rz6wZP/goug1+2qaR17UqeupidBzwqDdr3lszEK3q2A37g4+W7pvdBOkiGQA==
redocly.tarball_sha1=dc4f88cf3047e4abc1412a233b02379cb1f599b6
redocly.security_evidence_date=2026-09-02
redocly.scorecard_url=
redocly.scorecard_justification=OpenSSF returned HTTP 404 for this repository on 2026-09-02; official npm signature, GitHub maintenance metadata, and GitHub repository advisories are the bounded substitute
redocly.trust_decision=accepted-bounded
redocly.residual_risk=required version has known advisories and a large transitive npm tree; use only bundle on reviewed local schemas with telemetry disabled
yaml.package=yaml
yaml.version=2.9.0
yaml.publisher=eemeli npm account
yaml.repository=https://github.com/eemeli/yaml
yaml.registry=https://registry.npmjs.org/
yaml.license=ISC
yaml.integrity=sha512-2AvhNX3mb8zd6Zy7INTtSpl1F15HW6Wnqj0srWlkKLcpYl/gMIMJiyuGq2KeI2YFxUPjdlB+3Lc10seMLtL4cA==
yaml.tarball_sha1=78274afd93598a1dfdd6130df6a566defcbf9aa4
yaml.security_evidence_date=2026-09-02
yaml.trust_decision=accepted-compatibility
yaml.residual_risk=the package exposes an unused CLI; this project resolves only its library API for the pinned Redocly bundle
oapi.module=github.com/oapi-codegen/oapi-codegen/v2
oapi.command=github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen
oapi.version=v2.5.1
oapi.publisher=oapi-codegen GitHub organization
oapi.repository=https://github.com/oapi-codegen/oapi-codegen
oapi.proxy=https://proxy.golang.org
oapi.sumdb=https://sum.golang.org
oapi.license=Apache-2.0
oapi.module_sum=h1:5vHNY1uuPBRBWqB2Dp0G7YB03phxLQZupZTIZaeorjc=
oapi.mod_sum=h1:ro0npU1BWkcGpCgGD9QwPp44l5OIZ94tB3eabnT7DjQ=
oapi.security_evidence_date=2026-08-31
oapi.scorecard_url=https://api.securityscorecards.dev/projects/github.com/oapi-codegen/oapi-codegen
oapi.scorecard_observed_at=2026-08-31T05:18:06Z
oapi.scorecard_score=7.4
oapi.scorecard_justification=
oapi.trust_decision=accepted-bounded
oapi.residual_risk=required version is affected by a generated-code injection advisory; run only on reviewed repository schemas and review generated diffs
```

## Redocly CLI 1.34.0

The npm version endpoint identifies `@redocly/cli` 1.34.0, the
`Redocly/redocly-cli` source repository, MIT license, npm publisher account,
tarball URL, SHA-512 integrity, SHA-1 shasum, and npm registry signature. The
package was published on 2025-03-19. Its `gitHead`,
`4159a9e7b5efa841e20e570ff406d933bcd51a92`, resolves in the official source
repository to a commit from the same date.

The repository was active and not archived when checked. The npm registry had
newer releases, including the 1.x archive line, so 1.34.0 is maintained only as
an immutable compatibility pin here. OpenSSF had no Scorecard result for this
repository. This record uses the exact failed Scorecard lookup plus official
npm and GitHub evidence instead of assigning an invented score.

GitHub lists two advisories relevant to the required package version:

- GHSA-657c-g7qc-r9j2, medium severity, covers path traversal in `split` and
  lists 1.34.17 as the patched 1.x release.
- GHSA-xw2f-5386-m542, high severity, covers Arazzo `$faker` expression code
  execution in `respect`. The generator invokes only `bundle`, never `split`
  or `respect`, and accepts only reviewed local repository schemas.

The trust decision is `accepted-bounded` because the version is a task
constraint and its use is local, offline, and command-limited. The advisories
still apply to the package and must be reconsidered before adding another
Redocly command or accepting untrusted input.

Official evidence:

- https://registry.npmjs.org/@redocly%2fcli/1.34.0
- https://registry.npmjs.org/@redocly%2fcli
- https://api.github.com/repos/Redocly/redocly-cli
- https://api.github.com/repos/Redocly/redocly-cli/commits/4159a9e7b5efa841e20e570ff406d933bcd51a92
- https://api.securityscorecards.dev/projects/github.com/Redocly/redocly-cli
- https://api.github.com/repos/Redocly/redocly-cli/security-advisories/GHSA-657c-g7qc-r9j2
- https://api.github.com/repos/Redocly/redocly-cli/security-advisories/GHSA-xw2f-5386-m542

## YAML compatibility library 2.9.0

Redocly CLI 1.34.0 loads the bundled Redoc 2.4.0 documentation command graph
at startup. That graph requires the `yaml` library but does not declare a
top-level dependency that Node can resolve from the locked install. Without an
explicit compatibility dependency, even `redocly bundle` exits before parsing
the command.

The frontend therefore pins `yaml` 2.9.0 exactly. The official npm record maps
it to `eemeli/yaml`, ISC license, SHA-512 integrity and SHA-1 values recorded
above, and publication on 2026-05-11. This version also satisfies the existing
optional `postcss-load-config` peer range. Its package exposes a `yaml` binary,
but the generator never invokes it; only Redocly's library resolution uses the
package. Lifecycle scripts remain disabled during lock preparation and install.

Official evidence:

- https://registry.npmjs.org/yaml/2.9.0
- https://github.com/eemeli/yaml

## oapi-codegen v2.5.1

The Go proxy version record maps v2.5.1 to the official repository, tag, and
commit `1401fbe26ce7e128e9963786742490ff444e3795`. The checksum database lookup
provides the module and go.mod h1 values recorded above. The tagged license is
Apache-2.0. The separate tools module locks the command package without adding
it to the application module.

The repository was active and not archived when checked. OpenSSF Scorecard
reported 7.4 on 2026-08-31. Its selected checks reported Maintained 10,
Pinned-Dependencies 10, Token-Permissions 10, and Vulnerabilities 3. GitHub
advisory GHSA-rjwr-m7qx-3fjr covers versions through v2.7.0 and describes code
injection through an OpenAPI server description. This required v2.5.1 pin is
therefore accepted only for reviewed repository schemas. Generated Go remains
review material and is not executed by the generator.

Official evidence:

- https://proxy.golang.org/github.com/oapi-codegen/oapi-codegen/v2/@v/v2.5.1.info
- https://proxy.golang.org/github.com/oapi-codegen/oapi-codegen/v2/@v/v2.5.1.mod
- https://sum.golang.org/lookup/github.com/oapi-codegen/oapi-codegen/v2@v2.5.1
- https://api.github.com/repos/oapi-codegen/oapi-codegen
- https://api.github.com/repos/oapi-codegen/oapi-codegen/contents/LICENSE?ref=v2.5.1
- https://api.securityscorecards.dev/projects/github.com/oapi-codegen/oapi-codegen
- https://api.github.com/repos/oapi-codegen/oapi-codegen/security-advisories/GHSA-rjwr-m7qx-3fjr

## Integrity and operating boundary

`frontend/package-lock.json` locks the complete npm resolution. Every resolved
archive must use `https://registry.npmjs.org/` and have a valid SHA-512
integrity value. Lock preparation uses the credential-free user config and
disables lifecycle scripts. The generator requires the already installed
local `node_modules/.bin/redocly` entrypoint and checks its package identity.
The trust validator also requires the exact YAML compatibility library and
rejects a different source, version, checksum, license, or install script.

`backend/tools/openapi/go.sum` locks the complete Go module resolution. The
generator runs `go mod verify` and builds the command with `GOPROXY=off`,
`GOSUMDB=off`, `GOTOOLCHAIN=local`, `GOWORK=off`, and `-mod=readonly`. A missing
cache entry fails instead of downloading or installing a tool.

The generator copies OpenAPI inputs into its own temporary directory, rejects
remote `$ref` values, writes the merged schema only in that directory, and
cleans only that directory. It disables Redocly telemetry and the update
notifier. It does not use `npx`, `npm exec`, `go run`, global installs, mutable
version variables, or repository merge intermediates.

This trust record is not an SBOM, signature verification system, or guarantee
that transitive dependencies are vulnerability-free. Refresh the evidence and
revisit both pins before expanding tool capability or input trust.
