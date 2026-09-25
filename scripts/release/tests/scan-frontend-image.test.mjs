import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { scanImage, parseArguments } from '../scan-frontend-image.mjs';
import { validateImage } from '../validate-frontend-image.mjs';

const hash = (bytes) => createHash('sha256').update(bytes).digest('hex');
const json = (value) => Buffer.from(JSON.stringify(value));
const now = Date.parse('2026-09-25T12:00:00Z');
const revision = '1'.repeat(40);
const context = '2'.repeat(64);
const base = '9ec4a2e289874ed0d722e1772ec2de45d2801541db8612f3638b26f128c69ac2';
const indexType = 'application/vnd.oci.image.index.v1+json';
const manifestType = 'application/vnd.oci.image.manifest.v1+json';
const configType = 'application/vnd.oci.image.config.v1+json';
const cli = fileURLToPath(new URL('../scan-frontend-image.mjs', import.meta.url));
const repositoryPolicy = JSON.parse(fs.readFileSync(new URL('../../../security/trivy/frontend-policy.json', import.meta.url), 'utf8'));

async function fixture(t, { runtime = {}, dirty = false } = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'frontend-scan-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const artifact = path.join(root, 'artifact');
  const layout = path.join(artifact, 'image');
  const cacheDir = path.join(root, 'cache');
  fs.mkdirSync(path.join(layout, 'blobs/sha256'), { recursive: true });
  fs.mkdirSync(path.join(cacheDir, 'db'), { recursive: true });
  const write = (file, value) => fs.writeFileSync(file, Buffer.isBuffer(value) ? value : json(value));
  const blob = (value, mediaType) => {
    const bytes = Buffer.isBuffer(value) ? value : json(value);
    const descriptor = { mediaType, digest: `sha256:${hash(bytes)}`, size: bytes.length };
    write(path.join(layout, 'blobs/sha256', descriptor.digest.slice(7)), bytes);
    return descriptor;
  };
  const lockfile = path.join(root, 'package-lock.json');
  const lockBytes = json({ name: 'frontend-fixture', lockfileVersion: 3, packages: {} });
  write(lockfile, lockBytes);
  const labels = {
    'org.opencontainers.image.revision': revision, 'io.task-per-minute.source.dirty': String(dirty),
    'io.task-per-minute.lockfile.sha256': hash(lockBytes), 'io.task-per-minute.context.sha256': context,
    'org.opencontainers.image.base.name': 'docker.io/library/node:24.21.0-alpine3.23',
    'org.opencontainers.image.base.digest': `sha256:${base}`,
  };
  const layer = blob(Buffer.alloc(1024), 'application/vnd.oci.image.layer.v1.tar');
  const config = blob({ architecture: 'amd64', os: 'linux', rootfs: { type: 'layers', diff_ids: [layer.digest] }, config: {
    User: 'nextjs', Entrypoint: ['docker-entrypoint.sh'], Cmd: ['node', 'server.js'], WorkingDir: '/app',
    Env: ['PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin', 'NODE_VERSION=24.21.0', 'YARN_VERSION=1.22.22', 'PORT=3000', 'HOSTNAME=0.0.0.0', 'BACKEND_PORT=8080', 'BACKEND_URL=http://backend:8080'],
    ...runtime, Labels: labels,
  } }, configType);
  const image = { ...blob({ schemaVersion: 2, mediaType: manifestType, config, layers: [layer] }, manifestType), platform: { os: 'linux', architecture: 'amd64' } };
  const statement = (predicateType, predicate) => ({ _type: 'https://in-toto.io/Statement/v1', subject: [], predicateType, predicate });
  const sbom = blob(statement('https://spdx.dev/Document', {
    spdxVersion: 'SPDX-2.3', dataLicense: 'CC0-1.0', documentNamespace: 'https://example.invalid/spdx/frontend', packages: [{ name: 'frontend-fixture' }],
  }), 'application/vnd.in-toto+json');
  const provenance = blob(statement('https://slsa.dev/provenance/v0.2', {
    buildType: 'https://mobyproject.org/buildkit@v1', invocation: { parameters: { args: {
      'build-arg:SOURCE_REVISION': revision, 'build-arg:SOURCE_DIRTY': String(dirty),
      'build-arg:LOCKFILE_SHA256': hash(lockBytes), 'build-arg:CONTEXT_SHA256': context,
    } } }, materials: [
      { uri: 'pkg:docker/node@24.21.0-alpine3.23?platform=linux%2Famd64', digest: { sha256: base } },
      { uri: 'http://buildkit-session/fixture', digest: { sha256: context } },
    ],
  }), 'application/vnd.in-toto+json');
  const attestation = blob({ schemaVersion: 2, mediaType: manifestType,
    artifactType: 'application/vnd.docker.attestation.manifest.v1+json', subject: image,
    config: blob({}, 'application/vnd.oci.empty.v1+json'), layers: [sbom, provenance],
  }, manifestType);
  const inner = blob({ schemaVersion: 2, mediaType: indexType, manifests: [image, attestation] }, indexType);
  write(path.join(layout, 'index.json'), { schemaVersion: 2, mediaType: indexType, manifests: [inner] });
  write(path.join(layout, 'oci-layout'), { imageLayoutVersion: '1.0.0' });
  const metadata = path.join(artifact, 'metadata.json');
  write(metadata, { 'containerimage.digest': inner.digest, 'containerimage.config.digest': config.digest });
  write(path.join(artifact, 'request.json'), { schema_version: 1, source_revision: revision, source_dirty: dirty,
    context_sha256: context, lockfile_sha256: hash(lockBytes), platform: 'linux/amd64' });
  await validateImage({ layout, revision, contextSha256: context, sourceDirty: dirty, lockfile, metadata, output: path.join(artifact, 'identity.json') });
  const db = path.join(cacheDir, 'db/trivy.db');
  write(db, Buffer.from('synthetic database bytes'));
  const dbMetadata = path.join(cacheDir, 'db/metadata.json');
  write(dbMetadata, { Version: 2, UpdatedAt: '2026-09-25T11:00:00Z', NextUpdate: '2026-09-26T11:00:00Z' });
  const scanReport = { SchemaVersion: 2, ArtifactType: 'container_image', Metadata: { ImageID: config.digest, OS: { Family: 'alpine', Name: '3.23.4' } },
    Results: [{ Target: 'fixture (alpine 3.23.4)', Class: 'os-pkgs', Type: 'alpine' }, { Target: 'Node.js', Class: 'lang-pkgs', Type: 'node-pkg' }] };
  const settings = path.join(root, 'settings.json');
  write(settings, { report: scanReport, exit: 0 });
  fs.mkdirSync(path.join(root, 'scanner'));
  const scanner = path.join(root, 'scanner/trivy');
  fs.writeFileSync(scanner, `#!${process.execPath}
const fs = require('node:fs');
const path = require('node:path');
const root = path.dirname(path.dirname(__filename));
const state = JSON.parse(fs.readFileSync(path.join(root, 'settings.json'), 'utf8'));
if (process.argv.includes('--version')) { process.stdout.write('Version: ' + (state.version || '0.72.0') + '\\n'); }
else {
 const args = process.argv.slice(2);
 const value = name => args[args.indexOf(name) + 1];
 const required = ['--offline-scan','--skip-db-update','--skip-java-db-update','--skip-check-update','--skip-vex-repo-update','--disable-telemetry','--skip-version-check','--show-suppressed'];
 if (required.some(flag => !args.includes(flag)) || value('--scanners') !== 'vuln' || value('--config') !== '/dev/null' || value('--ignorefile') !== '/dev/null' || !fs.statSync(value('--input')).isDirectory()) process.exit(33);
 if (process.env.SYNTHETIC_SECRET || process.env.DOCKER_CONFIG || process.env.TRIVY_SEVERITY || process.env.NODE_OPTIONS) process.exit(34);
 fs.writeFileSync(path.join(root, 'executed'), 'scan executed');
 process.stdout.write('SYNTHETIC_SCANNER_DETAIL\\n');
 if (state.mutate) fs.appendFileSync(state.mutate, 'changed');
 if (state.mode === 'timeout') setTimeout(() => {}, 10000);
 else {
  if (state.mode !== 'missing') fs.writeFileSync(value('--output'), state.mode === 'malformed' ? '{invalid' : JSON.stringify(state.report));
  if (state.proofCollision) fs.writeFileSync(path.join(process.cwd(), 'report.json'), 'preserve existing proof');
  process.exitCode = state.exit;
 }
}
`, { mode: 0o700 });
  const policy = structuredClone(repositoryPolicy);
  policy.scanner.accepted_executable_sha256 = [hash(fs.readFileSync(scanner))];
  const output = path.join(root, 'output');
  return { root, artifact, layout, db, dbMetadata, scanner, settings, output, config, inner, scanReport,
    layer: path.join(layout, 'blobs/sha256', layer.digest.slice(7)), write,
    args: { artifact, imageDigest: inner.digest, cacheDir, scanner, output, lockfile },
    hooks: { policy, now: () => now },
    change: (changes) => write(settings, { report: scanReport, exit: 0, ...changes }),
  };
}

async function rejected(f, pattern, overrides = {}, hooks = {}) {
  await assert.rejects(() => scanImage({ ...f.args, ...overrides }, { ...f.hooks, ...hooks }), pattern);
  assert.equal(fs.existsSync(path.join(f.output, 'report.json')), false);
}

test('verified image and fresh DB execute the scanner and publish private PASS evidence', async (t) => {
  const f = await fixture(t);
  const report = await scanImage(f.args, f.hooks);
  assert.equal(report.status, 'pass');
  assert.equal(report.index_digest, f.inner.digest);
  assert.equal(report.config_digest, f.config.digest);
  assert.equal(report.source_revision, revision);
  assert.equal(report.source_dirty, false);
  assert.equal(report.database.sha256, hash(Buffer.from('synthetic database bytes')));
  assert.equal(report.scanner.sha256, f.hooks.policy.scanner.accepted_executable_sha256[0]);
  assert.equal(report.counts.CRITICAL, 0);
  assert.equal(fs.readFileSync(path.join(f.root, 'executed'), 'utf8'), 'scan executed');
  assert.deepEqual(JSON.parse(fs.readFileSync(path.join(f.output, 'report.json'), 'utf8')), report);
  for (const name of ['report.json', 'trivy.json', 'scan.log']) assert.equal(fs.statSync(path.join(f.output, name)).mode & 0o777, 0o600);
  assert.equal(fs.statSync(f.output).mode & 0o777, 0o700);
});

test('only HIGH and CRITICAL findings fail the gate without publishing PASS', async (t) => {
  for (const severity of ['HIGH', 'CRITICAL', 'MEDIUM', 'LOW']) {
    const f = await fixture(t);
    f.scanReport.Results[1].Vulnerabilities = [{ VulnerabilityID: 'CVE-2026-12345', PkgName: 'fixture', InstalledVersion: '1.0.0', Severity: severity }];
    f.change({ exit: 1 });
    if (['HIGH', 'CRITICAL'].includes(severity)) await rejected(f, /severity|HIGH|CRITICAL/);
    else assert.equal((await scanImage(f.args, f.hooks)).counts[severity], 1);
  }
});

test('CI evidence may be a sibling of image without changing wrapper inputs', async (t) => {
  const f = await fixture(t);
  const names = ['request.json', 'identity.json', 'metadata.json', 'image/index.json'];
  const before = names.map((name) => hash(fs.readFileSync(path.join(f.artifact, name))));
  const output = path.join(f.artifact, 'security');
  assert.equal((await scanImage({ ...f.args, output }, f.hooks)).status, 'pass');
  assert.equal(fs.existsSync(path.join(output, 'report.json')), true);
  assert.deepEqual(names.map((name) => hash(fs.readFileSync(path.join(f.artifact, name)))), before);
});

test('exit 1 with zero findings and exit 0 with findings are both invalid', async (t) => {
  const empty = await fixture(t); empty.change({ exit: 1 }); await rejected(empty, /exit status/);
  const nonempty = await fixture(t);
  nonempty.scanReport.Results[0].Vulnerabilities = [{ VulnerabilityID: 'CVE-2026-12345', PkgName: 'fixture', Severity: 'LOW' }];
  nonempty.change({ exit: 0 }); await rejected(nonempty, /exit status/);
});

test('scanner subprocess does not inherit a parent environment value', async (t) => {
  const f = await fixture(t);
  const previous = process.env.SYNTHETIC_SECRET;
  process.env.SYNTHETIC_SECRET = 'SYNTHETIC_PARENT_ONLY';
  try { assert.equal((await scanImage(f.args, f.hooks)).status, 'pass'); }
  finally { if (previous === undefined) delete process.env.SYNTHETIC_SECRET; else process.env.SYNTHETIC_SECRET = previous; }
});

test('missing image and scanner are refused before scan execution', async (t) => {
  for (const field of ['artifact', 'scanner']) {
    const f = await fixture(t);
    await rejected(f, /ENOENT|missing|regular/, { [field]: path.join(f.root, 'absent') });
    assert.equal(fs.existsSync(path.join(f.root, 'executed')), false);
  }
});

test('requested index must be canonical and equal the verified exported index', async (t) => {
  for (const imageDigest of ['latest', `sha256:${'A'.repeat(64)}`, `sha256:${'3'.repeat(64)}`]) {
    const f = await fixture(t);
    await rejected(f, /digest|index/, { imageDigest });
  }
});

test('tampered layers and stale persisted identity cannot pass', async (t) => {
  for (const target of ['layer', 'identity']) {
    const f = await fixture(t);
    if (target === 'layer') fs.appendFileSync(f.layer, 'tampered');
    else { const p = path.join(f.artifact, 'identity.json'); const value = JSON.parse(fs.readFileSync(p)); value.config_digest = `sha256:${'4'.repeat(64)}`; f.write(p, value); }
    await rejected(f, /descriptor|identity|digest/);
  }
});

test('current lockfile and wrapper request identities are binding', async (t) => {
  for (const target of ['lock', 'request']) {
    const f = await fixture(t);
    if (target === 'lock') fs.appendFileSync(f.args.lockfile, '\n');
    else { const p = path.join(f.artifact, 'request.json'); const value = JSON.parse(fs.readFileSync(p)); value.lockfile_sha256 = '5'.repeat(64); f.write(p, value); }
    await rejected(f, /lock|mismatch|request/);
  }
});

test('configured root users and non-Node startup commands are rejected', async (t) => {
  for (const runtime of [{ User: '' }, { User: 'root' }, { User: '0:1001' }, { User: '00' }, { Cmd: ['sh', '-c', 'node server.js'] }, { Entrypoint: ['sh'] }]) {
    await rejected(await fixture(t, { runtime }), /runtime|root|Node|entrypoint/);
  }
});

test('runtime environment rejects unknown keys, duplicate keys and credential-bearing URLs', async (t) => {
  for (const Env of [['SECRET=synthetic'], ['PORT=3000', 'PORT=4000'], ['BACKEND_URL=https://user:password@example.invalid'], ['BACKEND_URL=https://example.invalid?token=synthetic'], ['BACKEND_URL=https://example.invalid#synthetic'], ['PORT=65536'], ['NODE_OPTIONS=--inspect'], ['PATH=/untrusted/bin']]) {
    await rejected(await fixture(t, { runtime: { Env } }), /environment|runtime|URL|port/);
  }
});

test('stale, future, expired and invalid DB timestamps cannot pass', async (t) => {
  for (const metadata of [
    { Version: 1, UpdatedAt: '2026-09-25T11:00:00Z', NextUpdate: '2026-09-26T11:00:00Z' },
    { Version: 2, UpdatedAt: '2026-09-24T11:00:00Z', NextUpdate: '2026-09-26T11:00:00Z' },
    { Version: 2, UpdatedAt: '2026-09-25T13:00:00Z', NextUpdate: '2026-09-26T13:00:00Z' },
    { Version: 2, UpdatedAt: '2026-09-25T10:00:00Z', NextUpdate: '2026-09-25T11:00:00Z' },
    { Version: 2, UpdatedAt: 'invalid', NextUpdate: '2026-09-26T11:00:00Z' },
    { Version: 2, UpdatedAt: '2026-02-31T11:00:00Z', NextUpdate: '2026-09-26T11:00:00Z' },
  ]) { const f = await fixture(t); f.write(f.dbMetadata, metadata); await rejected(f, /database|DB|timestamp/); }
});

test('DB must exist and be nonempty', async (t) => {
  for (const missing of [true, false]) {
    const f = await fixture(t);
    if (missing) fs.unlinkSync(f.db); else f.write(f.db, Buffer.alloc(0));
    await rejected(f, /ENOENT|database|DB/);
  }
});

test('unapproved executable hashes and incorrect version output fail closed', async (t) => {
  const f = await fixture(t);
  await rejected(f, /scanner|hash/, {}, { policy: repositoryPolicy });
  const other = await fixture(t); other.change({ version: '0.71.0' });
  await rejected(other, /version/);
});

test('scanner errors and timeouts leave diagnostics but never PASS evidence', async (t) => {
  const f = await fixture(t); f.change({ exit: 2 }); await rejected(f, /scanner|exit/);
  const slow = await fixture(t); slow.change({ mode: 'timeout' }); await rejected(slow, /scanner|timeout/, {}, { timeoutMs: 100 });
});

test('malformed, absent and empty scanner reports fail closed', async (t) => {
  for (const changes of [{ mode: 'malformed' }, { mode: 'missing' }, { report: {} }, { report: { SchemaVersion: 2, Results: [] } }]) {
    const f = await fixture(t); f.change(changes); await rejected(f, /JSON|report|ENOENT|result/);
  }
});

test('scanner ImageID must match the verified runtime config', async (t) => {
  const f = await fixture(t); f.scanReport.Metadata.ImageID = `sha256:${'6'.repeat(64)}`; f.change({});
  await rejected(f, /ImageID|config/);
});

test('an OS-only report cannot claim frontend library coverage', async (t) => {
  const f = await fixture(t);
  f.scanReport.Results = f.scanReport.Results.filter((result) => result.Class === 'os-pkgs');
  f.change({});
  await rejected(f, /Node|library|node-pkg/);
});

test('ignored and suppressed findings cannot produce PASS', async (t) => {
  for (const field of ['ExperimentalModifiedFindings', 'IgnoredVulnerabilities', 'SuppressedFindings']) {
    const f = await fixture(t); f.scanReport.Results[0][field] = [{ VulnerabilityID: 'CVE-2026-12345' }]; f.change({});
    await rejected(f, /suppress|ignor|modified/i);
  }
});

test('scanner, DB and OCI mutations during the scan invalidate evidence', async (t) => {
  for (const target of ['scanner', 'db', 'layer']) {
    const f = await fixture(t); f.change({ mutate: f[target] }); await rejected(f, /changed|descriptor|hash|digest/);
  }
});

test('input symlinks and path traversal are rejected', async (t) => {
  for (const target of ['scanner', 'dbMetadata', 'layer']) {
    const f = await fixture(t); const moved = `${f[target]}.moved`; fs.renameSync(f[target], moved); fs.symlinkSync(moved, f[target]);
    await rejected(f, /symlink/);
  }
  const f = await fixture(t); await rejected(f, /traversal|path/, { artifact: `${f.root}/other/../artifact` });
});

test('existing or unsafe output paths are never reused', async (t) => {
  const f = await fixture(t); fs.mkdirSync(f.output); fs.writeFileSync(path.join(f.output, 'sentinel'), 'preserve');
  await rejected(f, /exist|output/); assert.equal(fs.readFileSync(path.join(f.output, 'sentinel'), 'utf8'), 'preserve');
  const other = await fixture(t); await rejected(other, /output|overlap/, { output: path.join(other.layout, 'scan') });
  const scannerOutput = await fixture(t); await rejected(scannerOutput, /output|overlap/, { output: path.join(path.dirname(scannerOutput.scanner), 'scan') });
});

test('atomic publication refuses a report created while the scanner was running', async (t) => {
  const f = await fixture(t); f.change({ proofCollision: true });
  await assert.rejects(() => scanImage(f.args, f.hooks), /exist|EEXIST|report/);
  assert.equal(fs.readFileSync(path.join(f.output, 'report.json'), 'utf8'), 'preserve existing proof');
});

test('CLI rejects policy or scanner injection flags and prints only sanitized errors', async (t) => {
  assert.throws(() => parseArguments(['--policy', '/synthetic']), /argument|option/);
  const f = await fixture(t);
  const args = Object.entries({ '--artifact': f.artifact, '--image-digest': f.args.imageDigest, '--cache-dir': f.args.cacheDir, '--scanner': f.scanner, '--output': f.output, '--lockfile': f.args.lockfile }).flat();
  const child = spawnSync(process.execPath, [cli, ...args], { env: { PATH: process.env.PATH, SYNTHETIC_SECRET: 'DO_NOT_PRINT' }, encoding: 'utf8' });
  assert.equal(child.status, 1);
  assert.equal(child.stdout, '');
  assert.doesNotMatch(child.stderr, /DO_NOT_PRINT|SYNTHETIC_SCANNER_DETAIL/);
  assert.equal(fs.existsSync(path.join(f.root, 'executed')), false, 'CLI cannot use test policy overrides');
});
