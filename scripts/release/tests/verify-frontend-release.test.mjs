import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { verifyRelease, parseArguments } from '../verify-frontend-release.mjs';
import { validateImage } from '../validate-frontend-image.mjs';

const hash = (bytes) => createHash('sha256').update(bytes).digest('hex');
const json = (value) => Buffer.from(JSON.stringify(value));
const revision = '1'.repeat(40);
const context = '2'.repeat(64);
const repository = 'example/task-per-minute';
const identity = `https://github.com/${repository}/.github/workflows/reusable-sign-tournament-images.yml@refs/heads/main`;
const issuer = 'https://token.actions.githubusercontent.com';
const base = '9ec4a2e289874ed0d722e1772ec2de45d2801541db8612f3638b26f128c69ac2';
const indexType = 'application/vnd.oci.image.index.v1+json';
const manifestType = 'application/vnd.oci.image.manifest.v1+json';
const cli = fileURLToPath(new URL('../verify-frontend-release.mjs', import.meta.url));

// All certificates, signatures and process results below are synthetic. These
// tests cover orchestration and rejection, not Fulcio/Rekor or crypto proof.
async function fixture(t, { dirty = false, omit = '' } = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'frontend-release-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const artifact = path.join(root, 'artifact');
  const layout = path.join(artifact, 'image');
  fs.mkdirSync(path.join(layout, 'blobs/sha256'), { recursive: true });
  const write = (file, value) => fs.writeFileSync(file, Buffer.isBuffer(value) ? value : json(value));
  const blob = (value, mediaType) => {
    const bytes = Buffer.isBuffer(value) ? value : json(value);
    const result = { mediaType, digest: `sha256:${hash(bytes)}`, size: bytes.length };
    write(path.join(layout, 'blobs/sha256', result.digest.slice(7)), bytes);
    return result;
  };
  const lockfile = path.join(root, 'package-lock.json');
  const lockBytes = json({ name: 'frontend-fixture', lockfileVersion: 3, packages: {} });
  write(lockfile, lockBytes);
  const layer = blob(Buffer.alloc(128), 'application/vnd.oci.image.layer.v1.tar');
  const config = blob({ architecture: 'amd64', os: 'linux', config: { Labels: {
    'org.opencontainers.image.revision': revision, 'io.task-per-minute.source.dirty': String(dirty),
    'io.task-per-minute.lockfile.sha256': hash(lockBytes), 'io.task-per-minute.context.sha256': context,
    'org.opencontainers.image.base.name': 'docker.io/library/node:24.21.0-alpine3.23',
    'org.opencontainers.image.base.digest': `sha256:${base}`,
  } } }, 'application/vnd.oci.image.config.v1+json');
  const image = { ...blob({ schemaVersion: 2, mediaType: manifestType, config, layers: [layer] }, manifestType), platform: { os: 'linux', architecture: 'amd64' } };
  const statement = (predicateType, predicate) => blob({ _type: 'https://in-toto.io/Statement/v1', subject: [], predicateType, predicate }, 'application/vnd.in-toto+json');
  const sbom = statement('https://spdx.dev/Document', { spdxVersion: 'SPDX-2.3', dataLicense: 'CC0-1.0',
    documentNamespace: 'https://example.invalid/spdx/frontend', packages: [{ name: 'fixture' }] });
  const provenance = statement('https://slsa.dev/provenance/v0.2', {
    buildType: 'https://mobyproject.org/buildkit@v1', invocation: { parameters: { args: {
      'build-arg:SOURCE_REVISION': revision, 'build-arg:SOURCE_DIRTY': String(dirty),
      'build-arg:LOCKFILE_SHA256': hash(lockBytes), 'build-arg:CONTEXT_SHA256': context,
    } } }, materials: [
      { uri: 'pkg:docker/node@24.21.0-alpine3.23?platform=linux%2Famd64', digest: { sha256: base } },
      { uri: 'http://buildkit-session/fixture', digest: { sha256: context } },
    ],
  });
  const attestations = blob({ schemaVersion: 2, mediaType: manifestType,
    artifactType: 'application/vnd.docker.attestation.manifest.v1+json', subject: image,
    config: blob({}, 'application/vnd.oci.empty.v1+json'),
    layers: [omit !== 'sbom' && sbom, omit !== 'provenance' && provenance].filter(Boolean),
  }, manifestType);
  const inner = blob({ schemaVersion: 2, mediaType: indexType, manifests: [image, attestations] }, indexType);
  write(path.join(layout, 'index.json'), { schemaVersion: 2, mediaType: indexType, manifests: [inner] });
  write(path.join(layout, 'oci-layout'), { imageLayoutVersion: '1.0.0' });
  const metadata = path.join(artifact, 'metadata.json');
  write(metadata, { 'containerimage.digest': inner.digest, 'containerimage.config.digest': config.digest });
  const request = { schema_version: 1, source_revision: revision, source_dirty: dirty,
    context_sha256: context, lockfile_sha256: hash(lockBytes), platform: 'linux/amd64' };
  write(path.join(artifact, 'request.json'), request);
  const persisted = path.join(artifact, 'identity.json');
  if (omit) write(persisted, {});
  else await validateImage({ layout, revision, contextSha256: context, sourceDirty: dirty, lockfile, metadata, output: persisted });
  const cosign = path.join(root, 'cosign');
  fs.writeFileSync(cosign, 'synthetic fixture, not a cryptographic executable', { mode: 0o700 });
  const bundle = path.join(artifact, 'signature.bundle.json');
  const bundleData = {
    mediaType: 'application/vnd.dev.sigstore.bundle.v0.3+json',
    verificationMaterial: { certificate: { rawBytes: Buffer.from('synthetic certificate').toString('base64') },
      tlogEntries: [{ inclusionProof: { checkpoint: { envelope: 'synthetic checkpoint' } } }] },
    messageSignature: { messageDigest: { algorithm: 'SHA2_256', digest: Buffer.from(inner.digest.slice(7), 'hex').toString('base64') },
      signature: Buffer.from('synthetic signature').toString('base64') },
  };
  write(bundle, bundleData);
  const calls = [];
  const state = { identity, issuer, repository, trigger: 'workflow_dispatch', ref: 'refs/heads/main' };
  const index = path.join(layout, 'blobs/sha256', inner.digest.slice(7));
  const run = (executable, args, options) => {
    calls.push({ executable, args, options });
    assert.equal(executable, cosign);
    assert.deepEqual(Object.keys(options.env).sort(), ['HOME', 'PATH', 'TMPDIR']);
    assert.equal(options.env.HOME, options.cwd);
    assert.equal(options.env.TMPDIR, options.cwd);
    assert.equal(options.shell, false);
    assert.equal(options.maxBuffer, 1024 * 1024);
    if (args[0] === 'version') return { status: 0, stdout: json({ gitVersion: state.version ?? 'v3.1.3' }), stderr: Buffer.alloc(0) };
    assert.deepEqual(args, ['verify-blob', '--bundle', bundle, '--certificate-identity', identity,
      '--certificate-oidc-issuer', issuer, '--certificate-github-workflow-repository', repository,
      '--certificate-github-workflow-trigger', 'workflow_dispatch', '--certificate-github-workflow-ref', 'refs/heads/main',
      '--timeout', '3m', index]);
    assert.equal(options.timeout, 180_000);
    state.mutate?.();
    if (state.result) return state.result;
    const matches = state.identity === identity && state.issuer === issuer && state.repository === repository
      && state.trigger === 'workflow_dispatch' && state.ref === 'refs/heads/main';
    return { status: matches ? 0 : 1, stdout: Buffer.alloc(0), stderr: Buffer.from('synthetic process details') };
  };
  const output = path.join(artifact, 'release.json');
  return { root, artifact, layout, inner, index, bundleData, calls, state, write, request,
    layer: path.join(layout, 'blobs/sha256', layer.digest.slice(7)),
    args: { artifact, imageRef: `ghcr.io/example/task-per-minute-frontend@${inner.digest}`, repository, revision, cosign, bundle, output, lockfile },
    hooks: { run, policy: { version: 'v3.1.3', acceptedExecutableSha256: [hash(fs.readFileSync(cosign))] } },
  };
}

async function rejected(f, pattern, overrides = {}, hooks = {}) {
  await assert.rejects(() => verifyRelease({ ...f.args, ...overrides }, { ...f.hooks, ...hooks }), pattern);
  assert.equal(fs.existsSync(f.args.output), false, 'failure must not publish a proof');
}

test('synthetic verifier binds the exact index bundle and publishes private, explicitly synthetic evidence', async (t) => {
  const f = await fixture(t);
  const report = await verifyRelease(f.args, f.hooks);
  assert.equal(report.status, 'pass');
  assert.equal(report.signature_kind, 'sigstore_blob_bundle');
  assert.equal(report.signature_verified, false, 'injected runners cannot produce crypto proof');
  assert.equal(report.verification_mode, 'synthetic_test');
  assert.equal(report.certificate_identity, identity);
  assert.equal(report.certificate_oidc_issuer, issuer);
  assert.equal(report.index_digest, f.inner.digest);
  assert.equal(report.source_revision, revision);
  assert.equal(report.source_dirty, false);
  assert.equal(report.bundle_sha256, hash(fs.readFileSync(f.args.bundle)));
  assert.equal(report.cosign.sha256, f.hooks.policy.acceptedExecutableSha256[0]);
  assert.equal(report.publication_verified, false);
  assert.equal(f.calls.length, 2);
  assert.deepEqual(JSON.parse(fs.readFileSync(f.args.output)), report);
  assert.equal(fs.statSync(f.args.output).mode & 0o777, 0o600);
  assert.equal(fs.existsSync(f.calls[0].options.cwd), false, 'private process scratch is removed');
});

test('both standard Sigstore v0.3 media type spellings are accepted', async (t) => {
  const f = await fixture(t);
  f.bundleData.mediaType = 'application/vnd.dev.sigstore.bundle+json;version=0.3';
  f.write(f.args.bundle, f.bundleData);
  assert.equal((await verifyRelease(f.args, f.hooks)).status, 'pass');
});

test('missing bundle is refused without executing Cosign', async (t) => {
  const f = await fixture(t);
  await rejected(f, /absolute|ENOENT|bundle/, { bundle: path.join(f.root, 'absent') });
  assert.equal(f.calls.length, 0);
});

test('a JSON verified boolean cannot substitute for a signed bundle', async (t) => {
  const f = await fixture(t); f.write(f.args.bundle, { verified: true, status: 'pass' });
  await rejected(f, /bundle/); assert.equal(f.calls.length, 0);
});

test('bundle format, keyless certificate, signature, log and exact message digest are mandatory', async (t) => {
  const changes = [
    (b) => { b.mediaType = 'application/vnd.dev.sigstore.bundle.v0.2+json'; },
    (b) => { b.dsseEnvelope = {}; },
    (b) => { delete b.messageSignature; },
    (b) => { b.messageSignature.signature = ''; },
    (b) => { b.messageSignature.messageDigest.algorithm = 'SHA2_512'; },
    (b) => { b.messageSignature.messageDigest.digest = Buffer.alloc(32).toString('base64'); },
    (b) => { b.verificationMaterial.certificate.rawBytes = '!'; },
    (b) => { b.verificationMaterial.publicKey = { hint: 'untrusted' }; },
    (b) => { b.verificationMaterial.tlogEntries = []; },
  ];
  for (const change of changes) {
    const f = await fixture(t); change(f.bundleData); f.write(f.args.bundle, f.bundleData);
    await rejected(f, /bundle|signature|certificate|digest|base64|transparency/); assert.equal(f.calls.length, 0);
  }
});

test('wrong certificate identity, issuer and GitHub claims fail the synthetic engine', async (t) => {
  for (const key of ['identity', 'issuer', 'repository', 'trigger', 'ref']) {
    const f = await fixture(t); f.state[key] = 'wrong';
    await rejected(f, /Cosign verification/); assert.equal(f.calls.length, 2);
  }
});

test('nonzero, timeout, spawn failure and excessive output never publish a proof', async (t) => {
  for (const result of [
    { status: 1, stdout: json({ verified: true }) }, { status: null, error: { code: 'ETIMEDOUT' } },
    { status: null, error: { code: 'ENOENT' } }, { status: 0, stdout: Buffer.alloc(1024 * 1024 + 1) },
    { status: 0, stderr: Buffer.alloc(1024 * 1024 + 1) },
  ]) {
    const f = await fixture(t); f.state.result = result; await rejected(f, /Cosign verification/);
  }
});

test('explicit source revision and clean source are mandatory', async (t) => {
  const f = await fixture(t); await rejected(f, /revision|source/, { revision: '9'.repeat(40) });
  const dirty = await fixture(t, { dirty: true }); await rejected(dirty, /clean|dirty/);
});

test('canonical target repository and index are exact, with no option injection', async (t) => {
  for (const imageRef of ['--key=untrusted', 'ghcr.io/example/task-per-minute-frontend:latest',
    `ghcr.io/other/task-per-minute-frontend@${'sha256:' + 'a'.repeat(64)}`,
    `ghcr.io/example/task-per-minute-frontend@sha256:${'a'.repeat(64)}`]) {
    const f = await fixture(t); await rejected(f, /image|index|canonical/, { imageRef });
  }
  for (const value of ['--key=untrusted', 'owner/repo\n', 'owner/../repo']) {
    const f = await fixture(t); await rejected(f, /repository/, { repository: value });
  }
});

test('missing SPDX or provenance is rejected by recomputation before Cosign', async (t) => {
  for (const omit of ['sbom', 'provenance']) {
    const f = await fixture(t, { omit }); await rejected(f, /SPDX|provenance/); assert.equal(f.calls.length, 0);
  }
});

test('current lockfile, persisted identity and Buildx metadata must match recomputation', async (t) => {
  for (const target of ['lockfile', 'identity.json', 'metadata.json', 'request.json']) {
    const f = await fixture(t);
    if (target === 'lockfile') fs.appendFileSync(f.args.lockfile, 'changed');
    else {
      const file = path.join(f.artifact, target); const value = JSON.parse(fs.readFileSync(file));
      if (target === 'identity.json') value.sbom_digest = `sha256:${'f'.repeat(64)}`;
      if (target === 'metadata.json') value['containerimage.digest'] = `sha256:${'f'.repeat(64)}`;
      if (target === 'request.json') value.context_sha256 = 'f'.repeat(64);
      f.write(file, value);
    }
    await rejected(f, /lockfile|identity|digest|label/); assert.equal(f.calls.length, 0);
  }
});

test('tampered OCI bytes are rejected before execution', async (t) => {
  const f = await fixture(t); fs.appendFileSync(f.layer, 'changed');
  await rejected(f, /size|SHA256|digest/); assert.equal(f.calls.length, 0);
});

test('only the pinned executable hash and exact version can execute verification', async (t) => {
  const hashCase = await fixture(t);
  await rejected(hashCase, /hash/, {}, { policy: { version: 'v3.1.3', acceptedExecutableSha256: ['0'.repeat(64)] } });
  assert.equal(hashCase.calls.length, 0);
  const versionCase = await fixture(t); versionCase.state.version = 'v3.1.2';
  await rejected(versionCase, /version/); assert.equal(versionCase.calls.length, 1);
});

test('bundle, index, executable, lock and other OCI mutations during verification fail closed', async (t) => {
  for (const target of ['bundle', 'index', 'cosign', 'lockfile', 'layer', 'identity.json', 'metadata.json', 'request.json']) {
    const f = await fixture(t);
    const file = f.args[target] ?? f[target] ?? path.join(f.artifact, target);
    f.state.mutate = () => fs.appendFileSync(file, 'changed');
    await rejected(f, /changed|size|JSON|SHA256|digest/);
  }
});

test('symlink leaves and parents, traversal and OCI output paths are refused', async (t) => {
  for (const key of ['bundle', 'cosign', 'lockfile', 'artifact']) {
    const f = await fixture(t); const alias = path.join(f.root, `alias-${key}`);
    fs.symlinkSync(f.args[key], alias); await rejected(f, /symlink/, { [key]: alias });
  }
  const parent = await fixture(t); const alias = path.join(parent.root, 'alias'); fs.symlinkSync(parent.artifact, alias);
  await rejected(parent, /symlink/, { bundle: path.join(alias, 'signature.bundle.json') });
  const traversal = await fixture(t); await rejected(traversal, /traversal/, { output: `${traversal.root}/../proof.json` });
  const overlap = await fixture(t); await rejected(overlap, /overlap/, { output: path.join(overlap.layout, 'proof.json') });
});

test('malformed or oversized bundle JSON is refused before execution', async (t) => {
  for (const bytes of [Buffer.from('{broken'), Buffer.from('[]'), Buffer.alloc(4 * 1024 * 1024 + 1)]) {
    const f = await fixture(t); f.write(f.args.bundle, bytes);
    await rejected(f, /JSON|size/); assert.equal(f.calls.length, 0);
  }
});

test('existing report, symlink and publication collision never overwrite user data', async (t) => {
  for (const mode of ['existing', 'symlink', 'collision']) {
    const f = await fixture(t); const protectedFile = path.join(f.root, 'preserve'); fs.writeFileSync(protectedFile, 'preserve');
    if (mode === 'existing') fs.writeFileSync(f.args.output, 'preserve');
    if (mode === 'symlink') fs.symlinkSync(protectedFile, f.args.output);
    if (mode === 'collision') f.state.mutate = () => fs.writeFileSync(f.args.output, 'preserve');
    await assert.rejects(() => verifyRelease(f.args, f.hooks), /exists|EEXIST|overwrite/);
    assert.equal(fs.readFileSync(f.args.output, 'utf8'), 'preserve');
    assert.equal(fs.readFileSync(protectedFile, 'utf8'), 'preserve');
  }
});

test('CLI requires bundle and exposes no runner, policy, key or insecure override', async (t) => {
  const f = await fixture(t);
  const cliArgs = Object.entries({ '--artifact': f.args.artifact, '--image-ref': f.args.imageRef,
    '--repository': repository, '--revision': revision, '--cosign': f.args.cosign,
    '--bundle': f.args.bundle, '--output': f.args.output, '--lockfile': f.args.lockfile }).flat();
  assert.deepEqual(parseArguments(cliArgs), f.args);
  for (const flag of ['--policy', '--runner', '--key', '--insecure-ignore-tlog', '--bundle']) {
    assert.throws(() => parseArguments([...cliArgs, flag, 'untrusted']), /unknown|duplicate/);
  }
  assert.throws(() => parseArguments(cliArgs.filter((v, i) => i !== 10 && i !== 11)), /bundle/);
  const result = spawnSync(process.execPath, [cli, ...cliArgs], { env: { PATH: '/usr/bin:/bin' }, encoding: 'utf8', timeout: 10_000 });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /hash/);
  assert.equal(result.stdout, '');
  assert.equal(fs.existsSync(f.args.output), false);
});
