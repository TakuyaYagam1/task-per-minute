# Frontend image evidence

`build-frontend-image.mjs` builds a local Linux/amd64 OCI image and checks its
SPDX SBOM and BuildKit provenance against the requested source identity. It
does not publish by default. Run it from the repository root with an unused
output directory:

```sh
node scripts/release/build-frontend-image.mjs --output /absolute/path/to/image-evidence
```

The wrapper needs Linux `/proc`, GNU tar, Buildx 0.35.0, an already running
local builder, and network access to the
pinned image registries and the npm registry. Use an empty Docker client
configuration for local validation. Do not pass secrets as build arguments.
The accepted public build variables are the frontend and backend ports,
backend URL, and the existing `NEXT_PUBLIC_*` URL settings.

Only selected tracked frontend files enter the build context. Environment
files, credentials, preview content, fixtures and generated build output do
not enter it. The wrapper binds the captured context bytes and package lock
to SHA-256 values. The Git revision must equal HEAD. A changed frontend tree
requires `--allow-dirty`; that fact remains visible in the image and evidence.
Such a build cannot be published by the wrapper.

An ordinary Dockerfile or Compose developer build without identity arguments
still works, but is not a verified release artifact. Supplying only part of
the identity is an error. The release verifier rejects missing identity,
stale revisions, changed blobs, and attestations for another image.

## Outputs and trust boundary

The output directory contains the OCI layout, Buildx metadata, the build
request, and `identity.json`. The verifier follows and hashes OCI descriptors,
checks the runtime configuration and labels, and requires both attestations
to bind to the runtime manifest through in-toto or an OCI artifact subject.
It checks the provenance tar-context material against the captured context
digest, and compares the exported index identity
with Buildx metadata. Keep the complete layout with the report; a report alone
is not proof of an image's contents.

Provenance is an unsigned statement from the builder, not an independent
signature or a claim of a SLSA level. Pinning inputs and recording source
identity make the build traceable; they do not guarantee byte-identical
indexes, whose attestations contain build timestamps. Vulnerability policy,
signing, deployment and rollback are separate gates.

The reusable workflow has separate backend and frontend jobs and digest
outputs. Only its explicit `--push-reference` path can publish a clean
frontend build. Local validation must not use that option. No production
operation is part of the local build command.

## Reviewed executable inputs

Reviewed on 2026-09-25:

| Input | Immutable identity | Source and license |
| --- | --- | --- |
| Node 24.21.0, Alpine 3.23 | `docker.io/library/node@sha256:9ec4a2e289874ed0d722e1772ec2de45d2801541db8612f3638b26f128c69ac2` | [Node release](https://nodejs.org/en/blog/release/v24.21.0), [Official Docker Node source](https://github.com/nodejs/docker-node), MIT |
| BuildKit Syft scanner 1.12.0 | `docker.io/docker/buildkit-syft-scanner@sha256:ae4f3b554449e7e25548e7d8ccc029d17357348e30c6e3df01b92bc93654d6a9` | [Release](https://github.com/docker/buildkit-syft-scanner/releases/tag/v1.12.0), Apache-2.0 |

The Node multi-platform index above resolves Linux/amd64 to manifest
`sha256:a01ebbfa28f5ac85e27044d661b4415d30508b2e64dc23eef236493b7a99f916`.
The validator accepts only this index or platform manifest as the Node base
material; the retired Node 24.15.0 pin is not accepted.

The scanner release points to source commit
`aba762345737fbb33224d0670260708bfbaadb99`. Its published provenance identifies
the annotated release tag object `e131763ad439b53d72810211577e7526bfac5d20`.
The Linux scanner image contains a single scanner binary and runs through
BuildKit's [SBOM protocol](https://docs.docker.com/build/metadata/attestations/sbom/).
It does not need host credentials or a host container socket. It is a build
tool, not an application runtime dependency. Registry integrity and these
source references do not by themselves constitute signature verification.

The application lockfile remains the npm dependency authority. Installation
disables package lifecycle scripts, audit calls and update notifications.
The runner uses an unprivileged application user and Node for its healthcheck;
it does not install packages from a mutable Alpine repository during build.
Only the builder retains npm. The runner removes global npm and corepack under
`/usr/local/lib/node_modules`, Yarn 1.22.22 at `/opt/yarn-v1.22.22`, and their
exact `/usr/local/bin` entrypoints. Application dependencies and the lockfile
are unchanged. A fresh image scan remains required after rebuilding.

## Offline contract tests

```sh
node --test scripts/release/tests/build-frontend-image.test.mjs \
  scripts/release/tests/validate-frontend-image.test.mjs \
  scripts/release/tests/frontend-image-workflow.test.mjs
```

These tests use synthetic data. They do not replace building the real image,
loading its verified runtime manifest, checking `/health`, and opening the
application in a browser. Keep generated evidence out of Git.

Compose defines an explicit Node `/health` probe. A runtime importing an OCI
layout may ignore the Docker-specific healthcheck extension, so check the
configured probe as well as the HTTP endpoint when validating an import.
