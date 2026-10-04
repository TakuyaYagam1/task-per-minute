# Frontend image evidence

`build-frontend-image.mjs` builds a local Linux/amd64 OCI image and checks its
SPDX SBOM and BuildKit provenance against the requested source identity. It
cannot publish. Run it from the repository root with an unused
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
Such a build cannot pass the manual release gate.

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

The ordinary pipeline has separate backend and frontend build jobs and digest
outputs. It has no registry login, publication, signing or deployment step.
The removed `--push-reference` option is rejected, including by the helper API.
No production operation is part of the local build command.

## Manual publication boundary

`reusable-publish-tournament-images.yml` is the manual entrypoint. Run it only
from protected `main`, with an exact source commit, successful CI run ID and
OCI index digest. It calls `reusable-sign-tournament-images.yml`; it does not
rebuild the image. Both privileged jobs use the `frontend-release` environment.
Before first use, the repository owner must configure environment reviewers,
prevent self-approval, restrict deployment branches to protected `main`, and
protect changes to workflows and release scripts. YAML cannot create these
repository settings or prove that reviewers were configured.

The signing job checks the CI run's repository, workflow path, event, branch,
source commit and successful conclusion. It rehashes the downloaded OCI layout,
requires a clean source identity, SBOM and provenance, and runs the vulnerability
gate with a fresh database. It then signs the exact index blob with Cosign.
The Sigstore bundle contains the certificate and transparency evidence; the
index transitively binds the runtime manifest, SBOM and provenance by digest.

The publication job revalidates the layout and bundle before registry login or
copy. Verification requires the exact signing workflow identity
`https://github.com/<owner>/<repository>/.github/workflows/reusable-sign-tournament-images.yml@refs/heads/main`
and issuer `https://token.actions.githubusercontent.com`. Wildcard identities,
key-only verification, ignored transparency checks and unsigned reports are
not substitutes. Signing uses `id-token: write` without package write access;
publication uses package write access without OIDC permission.

Publication copies the OCI index and its children by digest with ORAS, checks
the resulting registry digest, and attaches the detached Sigstore bundle as a
referrer. This is a signature over OCI index bytes, not a legacy Cosign image
signature tag. Consumers must retrieve the bundle, retain the complete OCI
layout, and run `verify-frontend-release.mjs`; a plain `cosign verify IMAGE`
is not the verification protocol for this artifact. No mutable release tag
or deployment is created by these workflows.

A successful signature proves approval by the expected protected workflow. It
does not turn builder-declared provenance into independent source inspection,
prove a SLSA level, or authorize deployment. A failed bundle attachment leaves
the release incomplete even when the immutable runtime bytes were copied;
do not deploy it. Retain the failed run evidence and repeat the protected
manual procedure for the same digest after resolving the failure.

Local contract tests use explicitly synthetic signature fixtures and test
adapters. They do not mint an OIDC identity or prove a hosted release occurred.
Actual signing, publication, environment approvals and production deployment
remain separate operations requiring explicit authorization.

## Reviewed executable inputs

Reviewed on 2026-09-25:

| Input | Immutable identity | Source and license |
| --- | --- | --- |
| Node 24.21.0, Alpine 3.23 | `docker.io/library/node@sha256:9ec4a2e289874ed0d722e1772ec2de45d2801541db8612f3638b26f128c69ac2` | [Node release](https://nodejs.org/en/blog/release/v24.21.0), [Official Docker Node source](https://github.com/nodejs/docker-node), MIT |
| BuildKit Syft scanner 1.12.0 | `docker.io/docker/buildkit-syft-scanner@sha256:ae4f3b554449e7e25548e7d8ccc029d17357348e30c6e3df01b92bc93654d6a9` | [Release](https://github.com/docker/buildkit-syft-scanner/releases/tag/v1.12.0), Apache-2.0 |
| Cosign 3.1.3, Linux/amd64 | Binary SHA-256 `4629c757b7618056f8ddd7e2625ae9fdd94c0372a65049520bc7d9df9efc7f71` | [Release](https://github.com/sigstore/cosign/releases/tag/v3.1.3), Apache-2.0 |
| ORAS 1.3.4, Linux/amd64 | Archive SHA-256 `f27adb935022d94df8dc77719c322dda592c78a0d57a6f7dcdd8d900b248c454`; binary `246c47e91bf2749a555ffe00a9824844c6df3a26d61974e3ce08f2077d79c556` | [Release](https://github.com/oras-project/oras/releases/tag/v1.3.4), Apache-2.0 |

The installer downloads only exact official release URLs, checks the fixed
hashes before executing binaries and extracts only the named archive members.
Cosign's published bundle was verified locally using the separately verified
Nix Cosign 3.1.3, identity `keyless@projectsigstore.iam.gserviceaccount.com`
and issuer `https://accounts.google.com`. That upstream tool identity is not
accepted as the application signing identity. The ORAS review establishes
official release checksum and source identity, not an independently verified
publisher signature. Its version reports source commit
`db9e29505c3059f2b8fde34ae8cae266c5c765e9`, matching the release tag.

Trivy's pins and vulnerability policy are defined in
[`security/trivy/frontend-policy.json`](../../security/trivy/frontend-policy.json). Updating a tool
version requires reviewing its source, license and executable hashes and
rerunning the contract tests; a matching version string alone is insufficient.

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
