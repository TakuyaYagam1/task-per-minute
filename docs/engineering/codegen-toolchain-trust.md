# Code Generation Toolchain Trust

This repository accepts exactly two Linux amd64 code generation tools through
the reviewed lock at `security/tools/codegen-tools.lock.json`:

| Tool | Version | Distribution | Trust status |
| --- | --- | --- | --- |
| sqlc | v1.31.1 | Upstream Linux amd64 release archive | Accepted with offline-only constraints |
| Wire | v0.7.0 | Verified source release and pinned Go build | Accepted with archived-upstream constraints |

The JSON schema is `security/tools/codegen-tools.schema.json`. The lock records
the official repository, release tag, signed commit, SPDX license and license
digest, release archive digest, declared extraction member, executable digest,
Go build identity, maintenance review, security review, and trust decision.
A version string or a matching name on `PATH` is never sufficient.

## Source and maintenance review

sqlc v1.31.1 comes from the
[official sqlc repository](https://github.com/sqlc-dev/sqlc) and its
[v1.31.1 release](https://github.com/sqlc-dev/sqlc/releases/tag/v1.31.1).
The release commit is signed, the Linux archive digest published by GitHub
matches the lock, and the release archive contains only the `sqlc` executable.
The release is MIT licensed. The repository was active when reviewed on
2026-08-28.

Wire v0.7.0 comes from the
[official Wire repository](https://github.com/google/wire) and its
[v0.7.0 release](https://github.com/google/wire/releases/tag/v0.7.0). The
release commit is signed and the source archive digest and extraction root are
locked. Upstream publishes no prebuilt Wire executable, so the accepted binary
is a pinned `go install github.com/google/wire/cmd/wire@v0.7.0` build made with
Go 1.26.5 from a reviewed local module proxy. Both the module sum and executable
digest are locked. Wire is Apache-2.0 licensed. Google archived the repository
in 2025 and states that it is no longer maintained.

## Security decision

The review checked the public project advisory pages and ran `govulncheck` in
binary symbol mode against the exact accepted executables using the official Go
vulnerability database.

- Wire reported no known reachable symbol vulnerabilities.
- The official sqlc binary reported 29 known reachable findings in its Go
  standard library and dependencies. The complete dated finding IDs are kept
  in the lock manifest.

sqlc remains accepted because the approved generator version is required for
reproducible output and is used only in a credential-free, network-disabled
environment against reviewed repository-owned schema and SQL inputs. This is a
constrained development-tool decision, not approval to expose the binary as a
service or run it on unreviewed third-party input. Repeat the vulnerability and
maintenance review before accepting any replacement artifact.

Wire remains accepted because its generator output is checked into Git, the
source and build are frozen, and every `wire_gen.go` change requires human diff
review. Its unmaintained status is an explicit residual risk. Replacing Wire is
a separate architecture change.

## Offline preflight

Provisioning or downloading tools is deliberately outside the verifier. Put
the already reviewed binaries in one directory and run:

```bash
bash scripts/release/verify-codegen-tools.sh --bin-dir /absolute/path/to/reviewed/bin
```

To verify local copies of both release archives as well:

```bash
bash scripts/release/verify-codegen-tools.sh \
  --bin-dir /absolute/path/to/reviewed/bin \
  --archives /absolute/path/to/reviewed/archives
```

The verifier performs no network request and no install. It rejects schema
drift, unknown fields, unofficial sources, wrong licenses, missing trust
decisions, Makefile version drift, platform mismatch, archive tamper, unsafe or
missing extraction members, executable tamper, version-probe mismatch, Go
module mismatch, module-sum mismatch, and Go compiler mismatch.

## Canonical generation

Use the same verified absolute paths for generation so that the Makefile cannot
fall back to another `PATH` entry or to its default `go run` Wire command:

```bash
tool_dir=/absolute/path/to/reviewed/bin
bash scripts/release/verify-codegen-tools.sh --bin-dir "$tool_dir"
make -C backend \
  SQLC="$tool_dir/sqlc" \
  WIRE="$tool_dir/wire" \
  gen-sqlc gen-wire
```

Generation is not acceptance. Inspect all generated changes, confirm that only
the canonical sqlc and Wire outputs changed, and run the repository validation
gates. Never hand-edit generated sqlc files or `wire_gen.go`.

## Lock update policy

Changing a version, source, archive, member, build command, license, executable,
or trust decision requires a separate supply-chain review. Obtain artifacts
from the official source, verify them before extraction, extract only declared
members, repeat maintenance and vulnerability checks, update the schema only
for an intentional contract change, and rerun the negative fixture suite.

The lock contains no credentials and does not authorize download, install,
network access, generation, deployment, or publication. A failed preflight is a
hard stop. Recovery is to restore a previously reviewed lock and exact binaries
or complete a new reviewed toolchain update; do not weaken a check to reuse a
mismatched host tool.
