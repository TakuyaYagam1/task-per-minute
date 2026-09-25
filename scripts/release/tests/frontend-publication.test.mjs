import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { runInNewContext } from 'node:vm';
import test from 'node:test';
import { preparePublication, validateBuildRun } from '../prepare-frontend-publication.mjs';
import { validateImage } from '../validate-frontend-image.mjs';

const root = fileURLToPath(new URL('../../../', import.meta.url));
const revision = 'a'.repeat(40);
const context = 'b'.repeat(64);
const repository = 'Example/tournament';
const base = '9ec4a2e289874ed0d722e1772ec2de45d2801541db8612f3638b26f128c69ac2';
const hash = (bytes) => createHash('sha256').update(bytes).digest('hex');
const write = (filename, value) => fs.writeFileSync(filename, JSON.stringify(value), { mode: 0o600 });
const read = (filename) => fs.readFileSync(path.join(root, filename), 'utf8');
const cli = path.join(root, 'scripts/release/prepare-frontend-publication.mjs');

function scratch(t) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'frontend-publication-'));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  return directory;
}

function buildRun() {
  return {
    id: 123, repository: { full_name: repository }, head_repository: { full_name: repository },
    event: 'push', head_branch: 'main', path: '.github/workflows/pipeline.yml',
    status: 'completed', conclusion: 'success', head_sha: revision,
  };
}
const runOptions = { repository, runId: '123', revision };

test('trusted main push and dispatch bind exact repository, revision and run', () => {
  for (const event of ['push', 'workflow_dispatch']) {
    assert.deepEqual(validateBuildRun({ ...buildRun(), event }, runOptions), {
      status: 'pass', repository, build_run_id: '123', source_revision: revision, artifact_name: `frontend-image-${revision}`,
    });
  }
});

for (const [name, mutate] of [
  ['run ID', (v) => { v.id++; }],
  ['string run ID', (v) => { v.id = '123'; }],
  ['foreign repository', (v) => { v.repository.full_name = 'Other/tournament'; }],
  ['fork head', (v) => { v.head_repository.full_name = 'Other/tournament'; }],
  ['missing head repository', (v) => { delete v.head_repository; }],
  ['pull request', (v) => { v.event = 'pull_request'; }],
  ['pull request target', (v) => { v.event = 'pull_request_target'; }],
  ['workflow run', (v) => { v.event = 'workflow_run'; }],
  ['foreign branch', (v) => { v.head_branch = 'release'; }],
  ['foreign workflow', (v) => { v.path = '.github/workflows/other.yml'; }],
  ['running build', (v) => { v.status = 'in_progress'; }],
  ['failed build', (v) => { v.conclusion = 'failure'; }],
  ['cancelled build', (v) => { v.conclusion = 'cancelled'; }],
  ['skipped build', (v) => { v.conclusion = 'skipped'; }],
  ['dispatch custom source', (v) => { v.event = 'workflow_dispatch'; v.head_sha = 'c'.repeat(40); }],
]) {
  test(`build metadata rejects ${name}`, () => {
    const value = buildRun(); mutate(value);
    assert.throws(() => validateBuildRun(value, runOptions));
  });
}

test('run input canonicalization rejects suffixes, traversal and unsafe integers', () => {
  for (const runId of ['0', '-1', '01', '123\n', '1.0', '9007199254740992', '1/../../2']) {
    assert.throws(() => validateBuildRun(buildRun(), { ...runOptions, runId }));
  }
  for (const sha of [revision + '\n', revision.toUpperCase(), revision.slice(1), 'z'.repeat(40)]) {
    assert.throws(() => validateBuildRun(buildRun(), { ...runOptions, revision: sha }));
  }
  for (const name of ['Example/..', 'Example/repo\n', 'Example/repo/other', 'Example/repo;echo']) {
    assert.throws(() => validateBuildRun(buildRun(), { ...runOptions, repository: name }));
  }
});

test('metadata CLI executes the same gate before download', (t) => {
  const file = path.join(scratch(t), 'run.json');
  write(file, buildRun());
  const args = [cli, 'validate-run', '--metadata', file, '--repository', repository, '--run-id', '123', '--revision', revision];
  const invoke = () => spawnSync(process.execPath, args, { encoding: 'utf8', env: { PATH: '/usr/bin:/bin' } });
  const valid = invoke();
  assert.equal(valid.status, 0, valid.stderr);
  assert.equal(JSON.parse(valid.stdout).artifact_name, `frontend-image-${revision}`);
  write(file, { ...buildRun(), head_sha: 'c'.repeat(40) });
  const invalid = invoke();
  assert.equal(invalid.status, 1);
  assert.equal(invalid.stdout, '');
  assert.match(invalid.stderr, /head SHA/);
});

async function fixture(t, { flat = false } = {}) {
  const directory = scratch(t);
  const artifact = path.join(directory, 'artifact');
  const layout = path.join(artifact, 'image');
  fs.mkdirSync(path.join(layout, 'blobs/sha256'), { recursive: true });
  const lockfile = path.join(directory, 'package-lock.json');
  fs.writeFileSync(lockfile, '{}\n');
  const lockHash = hash(fs.readFileSync(lockfile));
  const media = {
    index: 'application/vnd.oci.image.index.v1+json', manifest: 'application/vnd.oci.image.manifest.v1+json',
    config: 'application/vnd.oci.image.config.v1+json', layer: 'application/vnd.oci.image.layer.v1.tar',
    statement: 'application/vnd.in-toto+json',
  };
  const blob = (value, mediaType) => {
    const bytes = Buffer.isBuffer(value) ? value : Buffer.from(JSON.stringify(value));
    const descriptor = { mediaType, digest: `sha256:${hash(bytes)}`, size: bytes.length };
    fs.writeFileSync(path.join(layout, 'blobs/sha256', descriptor.digest.slice(7)), bytes);
    return descriptor;
  };
  const config = blob({ architecture: 'amd64', os: 'linux', config: { Labels: {
    'org.opencontainers.image.revision': revision, 'io.task-per-minute.source.dirty': 'false',
    'io.task-per-minute.lockfile.sha256': lockHash, 'io.task-per-minute.context.sha256': context,
    'org.opencontainers.image.base.name': 'docker.io/library/node:24.21.0-alpine3.23',
    'org.opencontainers.image.base.digest': `sha256:${base}`,
  } } }, media.config);
  const image = blob({ schemaVersion: 2, mediaType: media.manifest, config, layers: [blob(Buffer.alloc(1024), media.layer)] }, media.manifest);
  const statement = (predicateType, predicate) => blob({
    _type: 'https://in-toto.io/Statement/v1', subject: [], predicateType, predicate,
  }, media.statement);
  const attestation = blob({
    schemaVersion: 2, mediaType: media.manifest, artifactType: 'application/vnd.docker.attestation.manifest.v1+json',
    subject: image, config: { ...blob({}, 'application/vnd.oci.empty.v1+json'), data: 'e30=' },
    layers: [
      statement('https://spdx.dev/Document', {
        spdxVersion: 'SPDX-2.3', dataLicense: 'CC0-1.0', documentNamespace: 'https://example.invalid/spdx/fixture', packages: [{ name: 'fixture' }],
      }),
      statement('https://slsa.dev/provenance/v0.2', {
        buildType: 'https://mobyproject.org/buildkit@v1', invocation: { parameters: { args: {
          'build-arg:SOURCE_REVISION': revision, 'build-arg:SOURCE_DIRTY': 'false',
          'build-arg:LOCKFILE_SHA256': lockHash, 'build-arg:CONTEXT_SHA256': context,
        } } },
        materials: [
          { uri: 'pkg:docker/node@24.21.0-alpine3.23?platform=linux%2Famd64', digest: { sha256: base } },
          { uri: 'http://buildkit-session/fixture', digest: { sha256: context } },
        ],
      }),
    ],
  }, media.manifest);
  const inner = { schemaVersion: 2, mediaType: media.index, manifests: [image, attestation] };
  const index = blob(inner, media.index);
  write(path.join(layout, 'index.json'), flat ? inner : { schemaVersion: 2, mediaType: media.index, manifests: [index] });
  write(path.join(layout, 'oci-layout'), { imageLayoutVersion: '1.0.0' });
  const request = { schema_version: 1, source_revision: revision, source_dirty: false, lockfile_sha256: lockHash, context_sha256: context, platform: 'linux/amd64' };
  write(path.join(artifact, 'request.json'), request);
  write(path.join(artifact, 'metadata.json'), { 'containerimage.digest': index.digest });
  const identity = await validateImage({ layout, revision, sourceDirty: false, contextSha256: context, lockfile, metadata: path.join(artifact, 'metadata.json') });
  write(path.join(artifact, 'identity.json'), identity);
  return {
    directory, artifact, request, identity, index, layout,
    indexBlob: path.join(layout, 'blobs/sha256', index.digest.slice(7)),
    options: { artifact, repository, revision, imageDigest: index.digest, lockfile },
  };
}

test('preparation recomputes full OCI identity and returns digest-only destination', async (t) => {
  const f = await fixture(t);
  assert.deepEqual(await preparePublication(f.options), {
    status: 'pass', source_revision: revision, image_digest: f.index.digest,
    image_ref: `ghcr.io/example/task-per-minute-frontend@${f.index.digest}`, index_blob: f.indexBlob,
  });
  assert.deepEqual(fs.readdirSync(f.artifact).sort(), ['identity.json', 'image', 'metadata.json', 'request.json']);
});

for (const [name, mutate, message] of [
  ['dirty source', (f) => { f.request.source_dirty = true; }, /source must be clean/],
  ['stale source', (f) => { f.request.source_revision = 'c'.repeat(40); }, /revision must match/],
  ['stale lockfile', (f) => { f.request.lockfile_sha256 = 'c'.repeat(64); }, /lockfile\/context/],
  ['invalid context', (f) => { f.request.context_sha256 += '\n'; }, /lockfile\/context/],
  ['wrong platform', (f) => { f.request.platform = 'linux/arm64'; }, /platform/],
]) {
  test(`preparation rejects ${name}`, async (t) => {
    const f = await fixture(t); mutate(f); write(path.join(f.artifact, 'request.json'), f.request);
    await assert.rejects(() => preparePublication(f.options), message);
  });
}

test('preparation rejects stale identity, actual lock bytes and explicit digest mismatch', async (t) => {
  const f = await fixture(t);
  await assert.rejects(() => preparePublication({ ...f.options, imageDigest: `sha256:${'c'.repeat(64)}` }), /requested digest/);
  for (const bad of [`${f.index.digest}\n`, `sha256:${'A'.repeat(64)}`, 'latest']) {
    await assert.rejects(() => preparePublication({ ...f.options, imageDigest: bad }), /canonical/);
  }
  write(path.join(f.artifact, 'identity.json'), { ...f.identity, status: 'fail' });
  await assert.rejects(() => preparePublication(f.options), /persisted identity/);
  write(path.join(f.artifact, 'identity.json'), f.identity);
  fs.appendFileSync(f.options.lockfile, '\n');
  await assert.rejects(() => preparePublication(f.options), /lockfile\/context/);
});

test('published index must exist as a matching regular blob even in a flat layout', async (t) => {
  const f = await fixture(t, { flat: true });
  fs.unlinkSync(f.indexBlob);
  await assert.rejects(() => preparePublication(f.options), { code: 'ENOENT' });
});

test('preparation rejects symlinks and malformed JSON without outputs', async (t) => {
  const f = await fixture(t);
  const request = path.join(f.artifact, 'request.json');
  const moved = path.join(f.directory, 'request.json');
  fs.renameSync(request, moved); fs.symlinkSync(moved, request);
  await assert.rejects(() => preparePublication(f.options), /symlink/);
  fs.unlinkSync(request); fs.writeFileSync(request, '{');
  await assert.rejects(() => preparePublication(f.options), /invalid JSON/);
  await assert.rejects(() => preparePublication({ ...f.options, artifact: `${f.artifact}\ninjected=x` }), /control characters/);
});

test('GitHub outputs are written only to the explicit runner file after successful preparation', async (t) => {
  const f = await fixture(t);
  const output = path.join(f.directory, 'output'); fs.writeFileSync(output, 'existing=value\n');
  const args = [cli, 'prepare', '--artifact', f.artifact, '--repository', repository, '--revision', revision,
    '--digest', f.index.digest, '--lockfile', f.options.lockfile, '--github-output', output];
  const invoke = (githubOutput) => spawnSync(process.execPath, args, { encoding: 'utf8', env: { PATH: '/usr/bin:/bin', GITHUB_OUTPUT: githubOutput } });
  assert.equal(invoke(path.join(f.directory, 'other')).status, 1);
  assert.equal(fs.readFileSync(output, 'utf8'), 'existing=value\n');
  const result = invoke(output); assert.equal(result.status, 0, result.stderr);
  assert.equal(fs.readFileSync(output, 'utf8'), `existing=value\nimage_ref=ghcr.io/example/task-per-minute-frontend@${f.index.digest}\nindex_blob=${f.indexBlob}\n`);
});

// A deliberately restricted YAML reader for these workflows: mappings, step
// sequences and block scalars only. Unsupported syntax fails the test rather
// than treating arbitrary YAML as a bag of security-related strings.
function parseWorkflow(source) {
  const lines = source.split('\n'); let cursor = 0;
  const indent = (line) => line.search(/\S/);
  const skip = () => { while (cursor < lines.length && (!lines[cursor].trim() || lines[cursor].trimStart().startsWith('#'))) cursor++; };
  const scalar = (value) => {
    const plain = value.replace(/ +#.*$/, '');
    if (plain === 'true') return true;
    if (plain === 'false') return false;
    if (/^[0-9]+$/.test(plain)) return Number(plain);
    return plain;
  };
  function block(level) {
    skip(); const sequence = lines[cursor]?.slice(level).startsWith('- ');
    const result = sequence ? [] : {};
    while (cursor < lines.length) {
      skip(); if (cursor >= lines.length || indent(lines[cursor]) < level) break;
      assert.equal(indent(lines[cursor]), level, 'unexpected YAML indentation');
      let line = lines[cursor++].slice(level);
      if (sequence) { assert.ok(line.startsWith('- ')); line = line.slice(2); }
      const match = /^([a-zA-Z_][a-zA-Z0-9_-]*):(?: (.*))?$/.exec(line);
      assert.ok(match, `unsupported YAML line: ${line}`);
      const [, key, raw] = match;
      let value;
      if (raw === '|' || raw === '>-') {
        const body = [];
        while (cursor < lines.length && (!lines[cursor].trim() || indent(lines[cursor]) > level)) body.push(lines[cursor++].slice(level + 2));
        value = body.join('\n').trimEnd();
      } else if (raw !== undefined) value = scalar(raw);
      else { skip(); value = cursor < lines.length && indent(lines[cursor]) > level ? block(indent(lines[cursor])) : {}; }
      if (sequence) {
        const item = { [key]: value }; skip();
        if (cursor < lines.length && indent(lines[cursor]) > level) Object.assign(item, block(level + 2));
        result.push(item);
      } else { assert.ok(!Object.hasOwn(result, key), `duplicate YAML key: ${key}`); result[key] = value; }
    }
    return result;
  }
  return block(0);
}

const publish = parseWorkflow(read('.github/workflows/reusable-publish-tournament-images.yml'));
const sign = parseWorkflow(read('.github/workflows/reusable-sign-tournament-images.yml'));
const step = (job, name) => {
  const matches = job.steps.filter((value) => value.name === name);
  assert.equal(matches.length, 1, `expected one ${name} step`); return matches[0];
};
const before = (job, first, last) => assert.ok(job.steps.indexOf(step(job, first)) < job.steps.indexOf(step(job, last)), `${first} must precede ${last}`);

test('publication is manual-only and signer is callable-only with exact string inputs', () => {
  assert.deepEqual(Object.keys(publish.on), ['workflow_dispatch']);
  assert.deepEqual(Object.keys(sign.on), ['workflow_call']);
  for (const inputs of [publish.on.workflow_dispatch.inputs, sign.on.workflow_call.inputs]) {
    assert.deepEqual(Object.keys(inputs).sort(), ['build_run_id', 'image_digest', 'source_revision']);
    for (const input of Object.values(inputs)) { assert.equal(input.type, 'string'); assert.equal(input.required, true); }
  }
  assert.deepEqual(publish.permissions, { contents: 'read' });
  assert.deepEqual(sign.permissions, { contents: 'read' });
});

test('every privileged job requires manual protected main and the exact approved caller', () => {
  const caller = 'Example/tournament/.github/workflows/reusable-publish-tournament-images.yml@refs/heads/main';
  const github = { event_name: 'workflow_dispatch', ref: 'refs/heads/main', ref_protected: true, workflow_ref: caller, repository };
  for (const job of [publish.jobs.sign, publish.jobs.publish, sign.jobs.sign]) {
    assert.ok(job.if.startsWith('${{ ') && job.if.endsWith(' }}'));
    const evaluate = (context) => runInNewContext(job.if.slice(4, -3), { github: context, format: (format, value) => format.replace('{0}', value) }, { timeout: 100 });
    assert.equal(evaluate(github), true);
    for (const change of [{ event_name: 'push' }, { event_name: 'pull_request' }, { ref: 'refs/heads/other' },
      { ref_protected: false }, { workflow_ref: caller.replace('reusable-publish', 'other') }]) assert.equal(evaluate({ ...github, ...change }), false);
  }
});

test('signing and registry privileges are disjoint and both require the protected environment', () => {
  const signing = { contents: 'read', actions: 'read', 'id-token': 'write' };
  assert.deepEqual(publish.jobs.sign.permissions, signing);
  assert.deepEqual(sign.jobs.sign.permissions, signing);
  assert.deepEqual(publish.jobs.publish.permissions, { contents: 'read', actions: 'read', packages: 'write' });
  assert.equal(publish.jobs.sign.uses, './.github/workflows/reusable-sign-tournament-images.yml');
  assert.equal(publish.jobs.publish.needs, 'sign');
  assert.equal(sign.jobs.sign.environment, 'frontend-release');
  assert.equal(publish.jobs.publish.environment, 'frontend-release');
  assert.equal(publish.jobs.sign.secrets, undefined);
});

test('trusted code checkout and all third-party actions are immutable', () => {
  const pins = {
    'actions/checkout': 'de0fac2e4500dabe0009e67214ff5f5447ce83dd',
    'actions/setup-node': '48b55a011bda9f5d6aeb4c2d9c7362e8dae4041e',
    'actions/download-artifact': '634f93cb2916e3fdff6788551b99b062d0335ce0',
    'actions/upload-artifact': 'ea165f8d65b6e75b540449e92b4886f43607fa02',
  };
  for (const job of [sign.jobs.sign, publish.jobs.publish]) {
    const checkout = step(job, 'checkout trusted release code');
    assert.equal(checkout.with.ref, '${{ github.sha }}'); assert.equal(checkout.with['persist-credentials'], false);
    for (const item of job.steps) {
      if (item.uses) { const [name, pin] = item.uses.split('@'); assert.equal(pin, pins[name]); }
      assert.equal(item['continue-on-error'], undefined);
      assert.equal(item.if, undefined, 'release steps cannot bypass previous failures');
    }
  }
});

test('build metadata gates the exact source run download, signed download stays in this run', () => {
  const job = sign.jobs.sign;
  before(job, 'validate trusted build run before download', 'download exact trusted frontend artifact');
  const validation = step(job, 'validate trusted build run before download');
  assert.match(validation.run, /gh api --hostname github.com/);
  assert.match(validation.run, /prepare-frontend-publication\.mjs validate-run/);
  assert.equal(validation.env.GH_TOKEN, '${{ github.token }}');
  const download = step(job, 'download exact trusted frontend artifact');
  assert.equal(download.with.name, 'frontend-image-${{ inputs.source_revision }}');
  assert.equal(download.with['run-id'], '${{ inputs.build_run_id }}');
  assert.equal(download.with['github-token'], '${{ github.token }}');
  const signed = step(publish.jobs.publish, 'download exact signed artifact from this run');
  assert.equal(signed.with['run-id'], '${{ github.run_id }}');
  assert.equal(signed.with.name, '${{ needs.sign.outputs.signed_artifact_name }}');
});

test('fresh scans, detached signature and verification precede publication credentials', () => {
  const signer = sign.jobs.sign; const publisher = publish.jobs.publish;
  before(signer, 'validate frontend publication inputs', 'scan exact frontend image for release');
  before(signer, 'refresh scanner database', 'scan exact frontend image for release');
  before(signer, 'scan exact frontend image for release', 'sign verified OCI index blob');
  before(signer, 'sign verified OCI index blob', 'verify detached signature and release gate');
  before(signer, 'verify detached signature and release gate', 'upload signed frontend artifact');
  const signing = step(signer, 'sign verified OCI index blob');
  assert.match(signing.run, /! -e "\$ARTIFACT_DIR\/signature.sigstore.json" && ! -L/);
  assert.match(signing.run, /sign-blob --yes --oidc-provider github-actions/);
  assert.match(signing.run, /--bundle "\$ARTIFACT_DIR\/signature.sigstore.json" "\$INDEX_BLOB"/);
  assert.equal(signing.env.INDEX_BLOB, '${{ steps.prepare.outputs.index_blob }}');
  before(publisher, 'validate frontend publication inputs again', 'rescan exact frontend image after approval');
  before(publisher, 'refresh scanner database after approval', 'rescan exact frontend image after approval');
  before(publisher, 'rescan exact frontend image after approval', 'verify detached signature before registry credentials');
  before(publisher, 'verify detached signature before registry credentials', 'publish exact digest and attach detached signature');
  for (const [job, scanName, directory] of [[signer, 'scan exact frontend image for release', 'signing-security'], [publisher, 'rescan exact frontend image after approval', 'publication-security']]) {
    const scan = step(job, scanName);
    assert.match(scan.run, /scan-frontend-image\.mjs/);
    assert.ok(scan.run.includes(`--output "$ARTIFACT_DIR/${directory}"`));
    assert.match(scan.run, /--image-digest "\$IMAGE_DIGEST"/);
  }
});

test('publication copies only the verified digest and attaches the bundle without tags or deploy', () => {
  const copy = step(publish.jobs.publish, 'publish exact digest and attach detached signature');
  assert.match(copy.run, /printf '%s' "\$REGISTRY_TOKEN" \| "\$TOOLS_DIR\/oras" login ghcr.io/);
  assert.match(copy.run, /--password-stdin --registry-config/);
  assert.match(copy.run, /copy --from-oci-layout "\$ARTIFACT_DIR\/image@\$IMAGE_DIGEST" "\$IMAGE_REF"/);
  assert.match(copy.run, /copy --from-oci-layout[^\n]*\\\n\s+--to-registry-config "\$REGISTRY_CONFIG"/);
  assert.match(copy.run, /\[\[ "\$RESOLVED_DIGEST" == "\$IMAGE_DIGEST" \]\] \|\| exit 1/);
  assert.match(copy.run, /attach --artifact-type application\/vnd.dev.sigstore.bundle.v0.3\+json/);
  assert.equal(copy.env.IMAGE_REF, '${{ steps.prepare.outputs.image_ref }}');
  assert.equal(copy.env.REGISTRY_TOKEN, '${{ github.token }}');
  for (const job of [sign.jobs.sign, publish.jobs.publish]) {
    for (const item of job.steps) {
      assert.doesNotMatch(item.run ?? '', /docker (?:build|push|tag)|oras" tag|cosign" sign |--password[ =]|kubectl|ssh |compose/);
      if (item !== copy) assert.equal(item.env?.REGISTRY_TOKEN, undefined);
    }
  }
});

test('installer pins archives and executables, extracts only regular entries and isolates Python', () => {
  const installer = read('scripts/release/install-frontend-release-tools.sh');
  assert.equal([...installer.matchAll(/python3 -I - /g)].length, 2);
  for (const digest of ['4629c757b7618056f8ddd7e2625ae9fdd94c0372a65049520bc7d9df9efc7f71',
    'f27adb935022d94df8dc77719c322dda592c78a0d57a6f7dcdd8d900b248c454',
    '246c47e91bf2749a555ffe00a9824844c6df3a26d61974e3ce08f2077d79c556',
    'bbb64b9695866ce4a7a8f5c9592002c5961cab378577fa3f8a040df362b9b2ea',
    '0e69edd134a3c338baa1a6806920773615d682b18cbc6a0cba2a3b658ef9b63e']) assert.ok(installer.includes(digest));
  assert.match(installer, /curl --disable --fail/);
  assert.match(installer, /--connect-timeout 20 --max-time 180/);
  assert.match(installer, /entries\[0\].isfile\(\)/);
  assert.match(installer, /entry.name == name/);
  assert.doesNotMatch(installer, /extractall|archive.extract\(/);
  const result = spawnSync('bash', [path.join(root, 'scripts/release/install-frontend-release-tools.sh'), '--output', 'relative'], { encoding: 'utf8', env: { PATH: process.env.PATH ?? '/usr/bin:/bin' } });
  assert.equal(result.error, undefined);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /usage:/);
});
