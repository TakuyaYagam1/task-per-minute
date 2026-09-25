import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import { validateImage } from '../validate-frontend-image.mjs';

const media = {
  index: 'application/vnd.oci.image.index.v1+json',
  manifest: 'application/vnd.oci.image.manifest.v1+json',
  config: 'application/vnd.oci.image.config.v1+json',
  empty: 'application/vnd.oci.empty.v1+json',
  layer: 'application/vnd.oci.image.layer.v1.tar',
  statement: 'application/vnd.in-toto+json',
};
const revision = '1'.repeat(40);
const contextSha256 = '2'.repeat(64);
const base = '9ec4a2e289874ed0d722e1772ec2de45d2801541db8612f3638b26f128c69ac2';
const baseAmd64 = 'a01ebbfa28f5ac85e27044d661b4415d30508b2e64dc23eef236493b7a99f916';
const spdxType = 'https://spdx.dev/Document';
const slsaType = 'https://slsa.dev/provenance/v0.2';
const hash = (bytes) => createHash('sha256').update(bytes).digest('hex');
const jsonBytes = (value) => Buffer.from(JSON.stringify(value));

function fixture(t, options = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'frontend-validation-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const layout = path.join(root, 'image');
  fs.mkdirSync(path.join(layout, 'blobs', 'sha256'), { recursive: true });
  const lockfile = path.join(root, 'package-lock.json');
  const lockBytes = jsonBytes({ name: 'frontend-fixture', version: '1.0.0', lockfileVersion: 3, packages: {} });
  fs.writeFileSync(lockfile, lockBytes);
  const lockfileSha256 = hash(lockBytes);
  const blobPath = (descriptor) => path.join(layout, 'blobs', 'sha256', descriptor.digest.slice(7));
  const blob = (value, mediaType) => {
    const bytes = Buffer.isBuffer(value) ? value : jsonBytes(value);
    const descriptor = { mediaType, digest: `sha256:${hash(bytes)}`, size: bytes.length };
    fs.writeFileSync(blobPath(descriptor), bytes);
    return descriptor;
  };
  // Two zero records form a valid empty, uncompressed tar layer.
  const layer = blob(Buffer.alloc(1024), media.layer);
  const config = {
    architecture: 'amd64', os: 'linux',
    config: {
      User: 'nextjs', WorkingDir: '/app', Cmd: ['node', 'server.js'], Env: ['PORT=3000'],
      Labels: {
        'org.opencontainers.image.revision': revision,
        'io.task-per-minute.source.dirty': String(options.sourceDirty ?? false),
        'io.task-per-minute.lockfile.sha256': lockfileSha256,
        'io.task-per-minute.context.sha256': contextSha256,
        'org.opencontainers.image.base.digest': `sha256:${base}`,
        'org.opencontainers.image.base.name': 'docker.io/library/node:24.21.0-alpine3.23',
        ...options.labels,
      },
    },
    rootfs: { type: 'layers', diff_ids: [layer.digest] },
    history: [{ created_by: 'synthetic fixture' }],
  };
  const configDescriptor = blob(config, media.config);
  const imageManifest = { schemaVersion: 2, mediaType: media.manifest, config: configDescriptor, layers: [layer] };
  options.mutateImage?.(imageManifest);
  const image = { ...blob(imageManifest, media.manifest), platform: { os: 'linux', architecture: 'amd64' } };
  const statement = (predicateType, predicate) => ({
    _type: `https://in-toto.io/Statement/${options.statementVersion ?? 'v0.1'}`,
    subject: [{ name: 'frontend-fixture', digest: { sha256: image.digest.slice(7) } }],
    predicateType, predicate,
  });
  const sbom = statement(spdxType, {
    spdxVersion: 'SPDX-2.3', dataLicense: 'CC0-1.0', SPDXID: 'SPDXRef-DOCUMENT',
    name: 'frontend-fixture', documentNamespace: 'https://example.invalid/spdx/frontend-fixture',
    creationInfo: { created: '2026-09-25T00:00:00Z', creators: ['Tool: fixture'] },
    packages: [{
      name: 'frontend-fixture', SPDXID: 'SPDXRef-Frontend', versionInfo: '1.0.0',
      downloadLocation: 'NOASSERTION', filesAnalyzed: false,
      licenseConcluded: 'NOASSERTION', licenseDeclared: 'NOASSERTION', copyrightText: 'NOASSERTION',
    }],
    relationships: [{ spdxElementId: 'SPDXRef-DOCUMENT', relationshipType: 'DESCRIBES', relatedSpdxElement: 'SPDXRef-Frontend' }],
  });
  const provenance = statement(slsaType, {
    builder: { id: 'https://github.com/moby/buildkit' },
    buildType: 'https://mobyproject.org/buildkit@v1',
    invocation: {
      configSource: { uri: 'https://example.invalid/frontend.git', digest: { sha1: revision }, entryPoint: 'Dockerfile' },
      parameters: {
        frontend: 'dockerfile.v0',
        args: {
          'build-arg:SOURCE_REVISION': revision,
          'build-arg:SOURCE_DIRTY': String(options.sourceDirty ?? false),
          'build-arg:LOCKFILE_SHA256': lockfileSha256,
          'build-arg:CONTEXT_SHA256': contextSha256,
        },
      },
      environment: { platform: 'linux/amd64' },
    },
    metadata: {
      buildInvocationId: 'fixture', buildStartedOn: '2026-09-25T00:00:00Z', buildFinishedOn: '2026-09-25T00:00:01Z',
      completeness: { parameters: true, environment: true, materials: true }, reproducible: false,
    },
    materials: [
      { uri: 'pkg:docker/node@24.21.0-alpine3.23?platform=linux%2Famd64', digest: { sha256: base } },
      { uri: 'http://buildkit-session/synthetic', digest: { sha256: contextSha256 } },
    ],
  });
  // Native OCI artifacts bind SLSA v1 through the manifest's full subject descriptor.
  if (options.artifact && options.statementVersion === 'v1') provenance.subject = [];
  options.mutateStatements?.({ sbom, provenance });
  const layers = [
    ...(options.omitSbom ? [] : [sbom]),
    ...(options.omitProvenance ? [] : [provenance]),
  ].map((document) => ({
    ...blob(document, media.statement), annotations: { 'in-toto.io/predicate-type': document.predicateType },
  }));
  const attestationManifest = {
    schemaVersion: 2, mediaType: media.manifest,
    config: options.artifact ? blob({}, media.empty) : blob({
      architecture: 'unknown', os: 'unknown', config: {}, rootfs: { type: 'layers', diff_ids: [] },
    }, media.config),
    layers,
    ...(options.artifact ? { artifactType: 'application/vnd.docker.attestation.manifest.v1+json', subject: image } : {}),
  };
  options.mutateAttestation?.(attestationManifest);
  const attestation = {
    ...blob(attestationManifest, media.manifest),
    ...(options.artifact ? {} : {
      platform: { architecture: 'unknown', os: 'unknown' },
      annotations: { 'vnd.docker.reference.type': 'attestation-manifest', 'vnd.docker.reference.digest': image.digest },
    }),
  };
  const manifests = [image, attestation];
  if (options.extraImage) {
    const extraConfig = blob({ ...config, config: { ...config.config, Env: ['PORT=3001'] } }, media.config);
    manifests.push({ ...blob({ ...imageManifest, config: extraConfig }, media.manifest), platform: image.platform });
  }
  options.mutateDescriptors?.(manifests);
  const innerIndex = { schemaVersion: 2, mediaType: media.index, manifests };
  const inner = blob(innerIndex, media.index);
  const index = options.nested ? { schemaVersion: 2, mediaType: media.index, manifests: [inner] } : innerIndex;
  fs.writeFileSync(path.join(layout, 'index.json'), jsonBytes(index));
  fs.writeFileSync(path.join(layout, 'oci-layout'), jsonBytes({ imageLayoutVersion: '1.0.0' }));
  const metadata = path.join(root, 'metadata.json');
  fs.writeFileSync(metadata, jsonBytes({ 'containerimage.digest': inner.digest }));
  const output = path.join(root, 'identity.json');
  return {
    root, layout, lockfile, output, metadata, lockfileSha256, image, inner, configDescriptor, layer, attestation,
    sbomDescriptor: options.omitSbom ? undefined : layers[0],
    blobPath, blob,
    args: { layout, revision, lockfile, contextSha256, sourceDirty: options.sourceDirty ?? false, metadata, output },
  };
}

async function accepted(f, overrides = {}) {
  const report = await validateImage({ ...f.args, ...overrides });
  assert.equal(report.status, 'pass');
  assert.equal(report.source_revision, revision);
  assert.equal(report.source_dirty, f.args.sourceDirty);
  assert.equal(report.lockfile_sha256, f.lockfileSha256);
  assert.equal(report.context_sha256, contextSha256);
  assert.equal(report.index_digest, f.inner.digest);
  if (overrides.output !== undefined || !Object.hasOwn(overrides, 'output')) {
    const output = overrides.output ?? f.output;
    const persisted = JSON.parse(fs.readFileSync(output, 'utf8'));
    assert.deepEqual(persisted, report);
    assert.equal(fs.statSync(output).mode & 0o777, 0o600);
  }
  return report;
}

async function rejected(f, overrides = {}) {
  const before = fs.readdirSync(f.root).sort();
  await assert.rejects(() => validateImage({ ...f.args, ...overrides }), Error);
  assert.equal(fs.existsSync(f.output), false, 'a failed validation must not publish proof');
  assert.deepEqual(fs.readdirSync(f.root).sort(), before, 'failure must not leave temporary report files');
}

test('valid legacy v0.1 attestations produce a private identity report', async (t) => {
  await accepted(fixture(t));
});

test('valid legacy v1 statements retain the same source identity', async (t) => {
  await accepted(fixture(t, { statementVersion: 'v1' }));
});

test('OCI artifact subject binds native SLSA v1 with an empty statement subject', async (t) => {
  await accepted(fixture(t, { artifact: true, statementVersion: 'v1' }));
});

test('empty subjects require a verified modern binding and are never valid for legacy statements', async (t) => {
  for (const statementVersion of ['v0.1', 'v1']) {
    for (const field of ['sbom', 'provenance']) {
      await rejected(fixture(t, {
        statementVersion, mutateStatements: (documents) => { documents[field].subject = []; },
      }));
    }
  }
  for (const mutateAttestation of [
    (manifest) => { delete manifest.subject; },
    (manifest) => { manifest.subject = { ...manifest.subject, size: manifest.subject.size + 1 }; },
    (manifest) => { manifest.subject = { ...manifest.subject, digest: `sha256:${'3'.repeat(64)}` }; },
  ]) {
    await rejected(fixture(t, { artifact: true, statementVersion: 'v1', mutateAttestation }));
  }
});

test('nested layout reports the exported inner index matching Buildx metadata', async (t) => {
  const f = fixture(t, { nested: true });
  assert.notEqual(`sha256:${hash(fs.readFileSync(path.join(f.layout, 'index.json')))}`, f.inner.digest);
  await accepted(f);
});

test('metadata and output are optional and dirty provenance remains a boolean', async (t) => {
  const f = fixture(t, { sourceDirty: true });
  await accepted(f, { metadata: undefined, output: undefined });
  assert.equal(fs.existsSync(f.output), false);
});

test('missing SBOM cannot publish proof', async (t) => {
  await rejected(fixture(t, { omitSbom: true }));
});

test('missing provenance cannot publish proof', async (t) => {
  await rejected(fixture(t, { omitProvenance: true }));
});

test('stale in-toto subjects are rejected for either required predicate', async (t) => {
  for (const artifact of [false, true]) {
    for (const field of ['sbom', 'provenance']) {
      await rejected(fixture(t, {
        artifact, statementVersion: 'v1',
        mutateStatements: (documents) => { documents[field].subject = [{ name: 'unrelated-image', digest: { sha256: '3'.repeat(64) } }]; },
      }));
    }
  }
});

test('stale legacy reference and modern subject links are rejected', async (t) => {
  await rejected(fixture(t, { mutateDescriptors: (descriptors) => { descriptors[1].annotations['vnd.docker.reference.digest'] = `sha256:${'3'.repeat(64)}`; } }));
  await rejected(fixture(t, { artifact: true, mutateAttestation: (manifest) => { manifest.subject = { ...manifest.subject, digest: `sha256:${'3'.repeat(64)}` }; } }));
});

test('stale source revision is rejected in labels and provenance independently', async (t) => {
  await rejected(fixture(t, { labels: { 'org.opencontainers.image.revision': '4'.repeat(40) } }));
  await rejected(fixture(t, { mutateStatements: ({ provenance }) => { provenance.predicate.invocation.parameters.args['build-arg:SOURCE_REVISION'] = '4'.repeat(40); } }));
});

test('lock identity uses actual lock bytes and rejects stale labels or arguments', async (t) => {
  const f = fixture(t);
  fs.appendFileSync(f.lockfile, '\n');
  await rejected(f);
  await rejected(fixture(t, { labels: { 'io.task-per-minute.lockfile.sha256': '5'.repeat(64) } }));
  await rejected(fixture(t, { mutateStatements: ({ provenance }) => { provenance.predicate.invocation.parameters.args['build-arg:LOCKFILE_SHA256'] = '5'.repeat(64); } }));
});

test('context identity must agree between expected value, labels and provenance', async (t) => {
  await rejected(fixture(t), { contextSha256: '6'.repeat(64) });
  await rejected(fixture(t, { labels: { 'io.task-per-minute.context.sha256': '6'.repeat(64) } }));
  await rejected(fixture(t, { mutateStatements: ({ provenance }) => { provenance.predicate.invocation.parameters.args['build-arg:CONTEXT_SHA256'] = '6'.repeat(64); } }));
});

test('context digest must also identify the actual BuildKit tar material', async (t) => {
  for (const mutateStatements of [
    ({ provenance }) => { provenance.predicate.materials[1].digest.sha256 = '6'.repeat(64); },
    ({ provenance }) => { provenance.predicate.materials.pop(); },
    ({ provenance }) => { provenance.predicate.materials[1].uri = 'https://example.invalid/unrelated-source'; },
  ]) {
    await rejected(fixture(t, { artifact: true, statementVersion: 'v1', mutateStatements }));
  }
});

test('node base pin must match both labels and provenance materials', async (t) => {
  await rejected(fixture(t, { labels: { 'org.opencontainers.image.base.digest': `sha256:${'7'.repeat(64)}` } }));
  await rejected(fixture(t, { mutateStatements: ({ provenance }) => { provenance.predicate.materials[0].digest.sha256 = '7'.repeat(64); } }));
});

test('approved Node 24.21 base accepts the exact index or amd64 material identity', async (t) => {
  for (const digest of [base, baseAmd64]) {
    for (const uri of [
      'pkg:docker/node@24.21.0-alpine3.23?platform=linux%2Famd64',
      `docker-image://docker.io/library/node:24.21.0-alpine3.23@sha256:${digest}`,
    ]) {
      await accepted(fixture(t, {
        mutateStatements: ({ provenance }) => { provenance.predicate.materials[0] = { uri, digest: { sha256: digest } }; },
      }));
    }
  }
});

test('retired Node 24.15 base cannot pass with internally consistent old evidence', async (t) => {
  const retired = 'd1b3b4da11eefd5941e7f0b9cf17783fc99d9c6fc34884a665f40a06dbdfc94f';
  await rejected(fixture(t, {
    labels: {
      'org.opencontainers.image.base.name': 'docker.io/library/node:24.15.0-alpine3.23',
      'org.opencontainers.image.base.digest': `sha256:${retired}`,
    },
    mutateStatements: ({ provenance }) => {
      provenance.predicate.materials[0] = {
        uri: 'pkg:docker/node@24.15.0-alpine3.23?platform=linux%2Famd64', digest: { sha256: retired },
      };
    },
  }));
});

test('new labels cannot authorize a retired, wrong-version or wrong-platform base material', async (t) => {
  for (const material of [
    { uri: 'pkg:docker/node@24.21.0-alpine3.23?platform=linux%2Famd64', digest: { sha256: 'd1b3b4da11eefd5941e7f0b9cf17783fc99d9c6fc34884a665f40a06dbdfc94f' } },
    { uri: 'pkg:docker/node@24.21.0-alpine3.23?platform=linux%2Famd64', digest: { sha256: '8e2c930fda481a6ec141fe5a88e8c249c69f8102fe98af505f38c081649ea749' } },
    { uri: 'pkg:docker/node@24.15.0-alpine3.23?platform=linux%2Famd64', digest: { sha256: base } },
    { uri: 'pkg:docker/node@24.21.0-alpine3.23?platform=linux%2Farm64', digest: { sha256: baseAmd64 } },
  ]) {
    await rejected(fixture(t, {
      mutateStatements: ({ provenance }) => { provenance.predicate.materials[0] = material; },
    }));
  }
});

test('metadata digest cannot point at an unrelated index or the layout wrapper', async (t) => {
  for (const wrapper of [false, true]) {
    const f = fixture(t, { nested: true });
    const digest = wrapper ? hash(fs.readFileSync(path.join(f.layout, 'index.json'))) : '8'.repeat(64);
    fs.writeFileSync(f.metadata, jsonBytes({ 'containerimage.digest': `sha256:${digest}` }));
    await rejected(f);
  }
});

test('modified content-addressed blobs are rejected even at the original size', async (t) => {
  for (const name of ['configDescriptor', 'layer', 'sbomDescriptor']) {
    const f = fixture(t);
    const target = f.blobPath(f[name]);
    const bytes = fs.readFileSync(target);
    if (name === 'layer') bytes[bytes.length - 1] ^= 1;
    else {
      const offset = bytes.indexOf('fixture');
      assert.ok(offset >= 0);
      bytes[offset] = 'F'.charCodeAt(0);
      assert.doesNotThrow(() => JSON.parse(bytes), 'tampering must preserve valid JSON and identity fields');
    }
    fs.writeFileSync(target, bytes);
    await rejected(f);
  }
});

test('descriptor sizes are checked for both source and attestation blobs', async (t) => {
  await rejected(fixture(t, { mutateImage: (manifest) => { manifest.config.size++; } }));
  await rejected(fixture(t, { mutateImage: (manifest) => { manifest.layers[0].size++; } }));
  await rejected(fixture(t, { mutateAttestation: (manifest) => { manifest.layers[0].size++; } }));
});

test('symlinked blob files and parent directories are rejected', async (t) => {
  for (const parent of [false, true]) {
    const f = fixture(t);
    const target = parent ? path.join(f.layout, 'blobs', 'sha256') : f.blobPath(f.configDescriptor);
    const moved = path.join(f.root, 'moved-blob');
    fs.renameSync(target, moved);
    fs.symlinkSync(moved, target);
    await rejected(f);
  }
});

test('descriptor traversal is rejected without creating proof outside the layout', async (t) => {
  const f = fixture(t, { mutateDescriptors: (descriptors) => { descriptors[0].digest = 'sha256:../../outside'; } });
  const outside = path.join(f.root, 'outside');
  fs.writeFileSync(outside, 'synthetic sentinel');
  await rejected(f);
  assert.equal(fs.readFileSync(outside, 'utf8'), 'synthetic sentinel');
});

test('a second runnable image cannot hide behind valid attestations for the first', async (t) => {
  await rejected(fixture(t, { extraImage: true }));
});

test('SPDX must contain packages and valid required document fields', async (t) => {
  for (const change of [
    (document) => { document.packages = []; },
    (document) => { delete document.documentNamespace; },
    (document) => { document.spdxVersion = 'SPDX-0.0'; },
    (document) => { document.dataLicense = 'NOASSERTION'; },
  ]) {
    await rejected(fixture(t, { mutateStatements: ({ sbom }) => change(sbom.predicate) }));
  }
});

test('SLSA provenance requires all identity arguments and the BuildKit build type', async (t) => {
  for (const key of ['SOURCE_REVISION', 'SOURCE_DIRTY', 'LOCKFILE_SHA256', 'CONTEXT_SHA256']) {
    await rejected(fixture(t, { mutateStatements: ({ provenance }) => { delete provenance.predicate.invocation.parameters.args[`build-arg:${key}`]; } }));
  }
  await rejected(fixture(t, { mutateStatements: ({ provenance }) => { provenance.predicate.buildType = 'https://example.invalid/unknown-builder'; } }));
});

test('sourceDirty input is strictly boolean and cannot disagree with image evidence', async (t) => {
  for (const sourceDirty of ['false', 'true', 0, 1, null]) await rejected(fixture(t), { sourceDirty });
  await rejected(fixture(t), { sourceDirty: true });
  await rejected(fixture(t, { labels: { 'io.task-per-minute.source.dirty': 'true' } }));
  await rejected(fixture(t, { mutateStatements: ({ provenance }) => { provenance.predicate.invocation.parameters.args['build-arg:SOURCE_DIRTY'] = false; } }));
});

test('atomic report publication never overwrites an existing file or symlink', async (t) => {
  for (const symlink of [false, true]) {
    const f = fixture(t);
    const target = symlink ? path.join(f.root, 'existing-report') : f.output;
    fs.writeFileSync(target, 'preserve existing report', { mode: 0o600 });
    if (symlink) fs.symlinkSync(target, f.output);
    const before = fs.readdirSync(f.root).sort();
    await assert.rejects(() => validateImage(f.args), Error);
    assert.equal(fs.readFileSync(target, 'utf8'), 'preserve existing report');
    assert.equal(fs.lstatSync(f.output).isSymbolicLink(), symlink);
    assert.deepEqual(fs.readdirSync(f.root).sort(), before);
  }
});
