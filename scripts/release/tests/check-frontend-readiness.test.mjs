import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { checkReadinessFiles, parseArguments } from '../check-frontend-readiness.mjs';

const cli = fileURLToPath(new URL('../check-frontend-readiness.mjs', import.meta.url));
const hash = (bytes) => createHash('sha256').update(bytes).digest('hex');
const checks = {
  'backend-readiness': ['readiness', 'schema_compatibility', 'single_process_realtime'],
  'frontend-ci': ['lint', 'typecheck', 'build', 'tests'],
  'full-stack': ['real_backend', 'same_origin', 'public_admin', 'http', 'websocket'],
  'frontend-security': ['secret_scan', 'dependency_review', 'image_scan'],
  'frontend-artifact': ['identity', 'sbom', 'provenance', 'signature'],
  rollout: ['configuration', 'proxy', 'health', 'browser'],
  recovery: ['previous_artifact', 'compatibility', 'procedure'],
};

// Constructed real-classified declarations exercise the parser only. They are
// not evidence of a build, backend readiness, signature or release approval.
function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'frontend-readiness-'));
  fs.chmodSync(root, 0o700);
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const evidenceDir = path.join(root, 'evidence');
  fs.mkdirSync(evidenceDir, { mode: 0o700 });
  const options = { manifest: path.join(root, 'manifest.json'), evidenceDir, output: path.join(root, 'report.json') };
  const identity = {
    source_revision: '1'.repeat(40), source_modified: false,
    frontend_image: `ghcr.io/example/task-per-minute-frontend@sha256:${'a'.repeat(64)}`,
    backend_revision: '2'.repeat(40), backend_image: `ghcr.io/example/backend@sha256:${'b'.repeat(64)}`,
    environment: 'staging',
  };
  const timestamp = new Date().toISOString();
  const records = Object.fromEntries(Object.entries(checks).map(([gate, names]) => [gate, {
    schema_version: 1, gate, status: 'pass', ...identity, finished_at: timestamp, evidence_kind: 'real',
    checks: Object.fromEntries(names.map((name) => [name, true])),
    ...(['frontend-ci', 'full-stack'].includes(gate) ? { tests: { executed: 3, skipped: 0, failed: 0 } } : {}),
  }]));
  const manifest = { schema_version: 1, ...identity, created_at: timestamp,
    owners: { release: 'Pavel', backend: 'Alexey', recovery: 'Elena', approver: 'Takuya' },
    gates: Object.keys(checks).map((id) => ({ id, record: `${id}.json`, sha256: '' })),
  };
  const recordPath = (id) => path.join(evidenceDir, `${id}.json`);
  const saveManifest = () => fs.writeFileSync(options.manifest, JSON.stringify(manifest), { mode: 0o600 });
  const saveRecord = (id) => {
    const bytes = JSON.stringify(records[id]);
    fs.writeFileSync(recordPath(id), bytes, { mode: 0o600 });
    manifest.gates.find((entry) => entry.id === id).sha256 = hash(bytes);
    saveManifest();
  };
  for (const id of Object.keys(checks)) saveRecord(id);
  const run = (overrides = {}, extra = []) => {
    const value = { ...options, ...overrides };
    return spawnSync(process.execPath, [cli, '--manifest', value.manifest, '--evidence-dir', value.evidenceDir,
      '--output', value.output, ...extra], { encoding: 'utf8', timeout: 5000,
      env: { PATH: '/usr/bin:/bin', RELEASE_OVERRIDE: 'PRIVATE_ENV_SENTINEL' } });
  };
  return { root, options, manifest, records, recordPath, saveManifest, saveRecord, run };
}

function noGo(f, gate, code) {
  const result = f.run();
  assert.equal(result.status, 1, result.stderr);
  const report = JSON.parse(fs.readFileSync(f.options.output, 'utf8'));
  assert.equal(report.decision, 'NO-GO');
  assert.equal(report.scope, 'evidence_consistency_only');
  assert.equal(report.release_authorized, false);
  if (code) assert.ok(report.issues.some((issue) => issue.gate === gate && issue.code === code), JSON.stringify(report.issues));
  if (Object.hasOwn(checks, gate)) assert.equal(report.gates.find((entry) => entry.id === gate).status, 'fail');
  return { result, report };
}

test('constructed complete declarations produce a private report, never release authorization', (t) => {
  const f = fixture(t); const result = f.run();
  assert.equal(result.status, 0, result.stderr);
  const report = JSON.parse(fs.readFileSync(f.options.output, 'utf8'));
  assert.equal(report.decision, 'GO');
  assert.equal(report.scope, 'evidence_consistency_only');
  assert.equal(report.release_authorized, false);
  assert.deepEqual(report.issues, []);
  assert.deepEqual(report.gates, Object.keys(checks).map((id) => ({ id, status: 'pass' })));
  assert.equal(fs.statSync(f.options.output).mode & 0o777, 0o600);
  assert.deepEqual(JSON.parse(result.stdout), report);
  assert.equal(JSON.stringify(report).includes('Pavel'), false);
  assert.equal((result.stdout + result.stderr).includes('PRIVATE_ENV_SENTINEL'), false);
  assert.equal(fs.readdirSync(f.root).some((name) => name.startsWith('.frontend-readiness-')), false);
});

test('direct file API and strict CLI arguments use the same contract', (t) => {
  const f = fixture(t);
  assert.deepEqual(parseArguments(['--manifest', f.options.manifest, '--evidence-dir', f.options.evidenceDir,
    '--output', f.options.output]), f.options);
  assert.equal(checkReadinessFiles(f.options).decision, 'GO');
  for (const args of [[], ['--manifest'], ['--now', '1'], ['--allow-synthetic', 'true'], ['--force', 'true'],
    ['--trust', 'true'], ['--output', '/tmp/a', '--output', '/tmp/b'], ['--manifest=/tmp/a'],
    ['--manifest', 'relative', '--evidence-dir', '/tmp/evidence', '--output', '/tmp/report']]) {
    assert.throws(() => parseArguments(args), /arguments|path/);
  }
});

test('missing evidence is a gate-specific NO-GO, not an inferred pass', (t) => {
  const f = fixture(t); fs.unlinkSync(f.recordPath('backend-readiness'));
  noGo(f, 'backend-readiness', 'file_missing');
});

test('a missing evidence directory produces a sanitized negative report', (t) => {
  const f = fixture(t); f.options.evidenceDir = path.join(f.root, 'absent');
  noGo(f, 'backend-readiness', 'file_missing');
});

test('raw record hash mismatch fails even when parsed declarations are unchanged', (t) => {
  const f = fixture(t); fs.appendFileSync(f.recordPath('frontend-artifact'), '\n');
  noGo(f, 'frontend-artifact', 'hash_mismatch');
});

test('hashed malformed JSON is rejected without exposing the parser input', (t) => {
  const f = fixture(t); const bytes = '{"private":"PRIVATE_RECORD_SENTINEL",broken';
  fs.writeFileSync(f.recordPath('frontend-security'), bytes);
  f.manifest.gates.find((entry) => entry.id === 'frontend-security').sha256 = hash(bytes); f.saveManifest();
  const { result, report } = noGo(f, 'frontend-security', 'invalid_json');
  assert.equal((result.stdout + result.stderr + JSON.stringify(report)).includes('PRIVATE_RECORD_SENTINEL'), false);
});

test('malformed or missing manifest produces only sanitized errors', (t) => {
  for (const mode of ['malformed', 'missing']) {
    const f = fixture(t);
    if (mode === 'malformed') fs.writeFileSync(f.options.manifest, '{"private":"PRIVATE_MANIFEST_SENTINEL",broken');
    else fs.unlinkSync(f.options.manifest);
    const { result } = noGo(f, 'manifest', mode === 'malformed' ? 'invalid_json' : 'file_missing');
    assert.equal((result.stdout + result.stderr).includes('PRIVATE_MANIFEST_SENTINEL'), false);
    assert.equal((result.stdout + result.stderr).includes(f.root), false);
  }
});

test('IO layer never upgrades core failures, skips, synthetic evidence or inconsistent identities', (t) => {
  const mutations = [
    (f) => { f.records['full-stack'].status = 'skipped'; },
    (f) => { f.records['full-stack'].status = 'fail'; },
    (f) => { f.records['full-stack'].status = 'not_applicable'; },
    (f) => { f.records['full-stack'].evidence_kind = 'synthetic'; },
    (f) => { f.records['full-stack'].source_modified = true; },
    (f) => { f.records['full-stack'].source_revision = '3'.repeat(40); },
    (f) => { f.records['full-stack'].backend_revision = '4'.repeat(40); },
    (f) => { f.records['full-stack'].frontend_image = f.records['full-stack'].frontend_image.replace(/a/g, 'c'); },
    (f) => { f.records['full-stack'].tests.skipped = 1; },
    (f) => { f.records['full-stack'].finished_at = new Date(Date.now() - 25 * 3600_000).toISOString(); },
    (f) => { f.records['full-stack'].checks.websocket = false; },
  ];
  for (const mutate of mutations) {
    const f = fixture(t); mutate(f); f.saveRecord('full-stack'); noGo(f, 'full-stack');
  }
});

test('manifest requirements, owner presence and freshness cannot be waived by the CLI', (t) => {
  for (const mutate of [
    (m) => { m.gates = []; }, (m) => { m.gates.pop(); },
    (m) => { m.gates.push({ ...m.gates[0] }); }, (m) => { delete m.owners; },
    (m) => { m.source_modified = true; },
    (m) => { m.created_at = new Date(Date.now() - 25 * 3600_000).toISOString(); },
    (m) => { m.created_at = new Date(Date.now() + 10 * 60_000).toISOString(); },
  ]) {
    const f = fixture(t); mutate(f.manifest); f.saveManifest(); noGo(f);
  }
});

test('record basenames reject traversal, absolute paths, controls and non-JSON names', (t) => {
  for (const name of ['../private.json', '/tmp/private.json', 'dir/record.json', 'dir\\record.json',
    '..json', '.hidden.json', 'record.json\n', 'record.JSON', 'record.txt']) {
    const f = fixture(t); f.manifest.gates[0].record = name; f.saveManifest();
    noGo(f, 'backend-readiness', 'record_path_invalid');
  }
});

test('unknown gate names and arbitrary input fields are never reflected into reports', (t) => {
  const f = fixture(t);
  f.manifest.gates[0].id = 'PRIVATE_GATE_SENTINEL';
  f.manifest.private = 'PRIVATE_FIELD_SENTINEL'; f.saveManifest();
  const { result, report } = noGo(f);
  assert.equal((result.stdout + result.stderr + JSON.stringify(report)).includes('PRIVATE_'), false);
});

test('malformed hashes fail closed, including a trailing newline', (t) => {
  for (const value of ['a'.repeat(64) + '\n', 'A'.repeat(64), 'a'.repeat(63), 'sha256:' + 'a'.repeat(64)]) {
    const f = fixture(t); f.manifest.gates[0].sha256 = value; f.saveManifest();
    noGo(f, 'backend-readiness', 'record_hash_invalid');
  }
});

test('input symlinks and directory parents are rejected without following their targets', (t) => {
  for (const kind of ['manifest', 'record', 'evidence-parent', 'manifest-parent']) {
    const f = fixture(t);
    if (kind === 'record') {
      const filename = f.recordPath('backend-readiness'); fs.renameSync(filename, filename + '.saved');
      fs.symlinkSync(filename + '.saved', filename);
    } else if (kind === 'evidence-parent') {
      const alias = path.join(f.root, 'alias'); fs.symlinkSync(f.options.evidenceDir, alias); f.options.evidenceDir = alias;
    } else if (kind === 'manifest-parent') {
      const alias = path.join(f.root, 'alias'); fs.symlinkSync(f.root, alias); f.options.manifest = path.join(alias, 'manifest.json');
    } else {
      const alias = path.join(f.root, 'alias.json'); fs.symlinkSync(f.options.manifest, alias); f.options.manifest = alias;
    }
    noGo(f, kind.startsWith('manifest') ? 'manifest' : 'backend-readiness', 'unsafe_path');
  }
});

test('directories in place of regular input files are rejected', (t) => {
  const f = fixture(t); const filename = f.recordPath('backend-readiness');
  fs.unlinkSync(filename); fs.mkdirSync(filename); noGo(f, 'backend-readiness', 'unsafe_path');
});

test('manifest and record byte limits are enforced before JSON allocation', (t) => {
  for (const kind of ['manifest', 'record']) {
    const f = fixture(t); const filename = kind === 'manifest' ? f.options.manifest : f.recordPath('backend-readiness');
    fs.writeFileSync(filename, Buffer.alloc((kind === 'manifest' ? 64 * 1024 : 1024 * 1024) + 1, 32));
    noGo(f, kind === 'manifest' ? 'manifest' : 'backend-readiness', 'file_too_large');
  }
});

test('seven bounded records and a manifest at their byte limits remain below the total budget', (t) => {
  const f = fixture(t);
  for (const entry of f.manifest.gates) {
    const json = JSON.stringify(f.records[entry.id]);
    const bytes = json.padEnd(1024 * 1024, ' ');
    fs.writeFileSync(f.recordPath(entry.id), bytes); entry.sha256 = hash(bytes);
  }
  fs.writeFileSync(f.options.manifest, JSON.stringify(f.manifest).padEnd(64 * 1024, ' '));
  assert.equal(checkReadinessFiles(f.options).decision, 'GO');
});

test('invalid UTF-8 cannot be silently replaced before JSON evaluation', (t) => {
  const f = fixture(t); const bytes = Buffer.from([0x7b, 0x22, 0xff, 0x22, 0x3a, 0x31, 0x7d]);
  fs.writeFileSync(f.recordPath('backend-readiness'), bytes);
  f.manifest.gates[0].sha256 = hash(bytes); f.saveManifest();
  noGo(f, 'backend-readiness', 'invalid_json');
});

test('existing output files, directories and dangling or live symlinks are never overwritten', (t) => {
  for (const kind of ['file', 'directory', 'symlink', 'dangling']) {
    const f = fixture(t); const saved = path.join(f.root, 'saved'); fs.writeFileSync(saved, 'preserve');
    if (kind === 'file') fs.writeFileSync(f.options.output, 'preserve');
    else if (kind === 'directory') fs.mkdirSync(f.options.output);
    else fs.symlinkSync(kind === 'symlink' ? saved : path.join(f.root, 'absent'), f.options.output);
    const result = f.run(); assert.equal(result.status, 1); assert.equal(result.stdout, '');
    assert.equal(fs.readFileSync(saved, 'utf8'), 'preserve');
    if (kind === 'file') assert.equal(fs.readFileSync(f.options.output, 'utf8'), 'preserve');
  }
});

test('output parents must already exist and cannot be symlinks or use traversal', (t) => {
  const f = fixture(t); const alias = path.join(f.root, 'alias'); fs.symlinkSync(f.root, alias);
  for (const output of [path.join(alias, 'report.json'), path.join(f.root, 'missing', 'report.json'),
    `${f.root}/evidence/../report.json`, `${f.root}/./report.json`, 'relative.json']) {
    const result = f.run({ output }); assert.equal(result.status, 1); assert.equal(result.stdout, '');
    assert.equal((result.stderr + result.stdout).includes(f.root), false);
  }
  assert.equal(fs.existsSync(f.options.output), false);
});

test('input changes between evaluation and publication cannot retain GO', (t) => {
  const f = fixture(t); const original = fs.mkdtempSync;
  t.mock.method(fs, 'mkdtempSync', (...args) => {
    const directory = original(...args);
    fs.appendFileSync(f.recordPath('frontend-artifact'), '\n');
    return directory;
  });
  const report = checkReadinessFiles(f.options);
  assert.equal(report.decision, 'NO-GO');
  assert.ok(report.issues.some((issue) => issue.gate === 'frontend-artifact' && issue.code === 'input_changed'));
  assert.equal(report.gates.find((entry) => entry.id === 'frontend-artifact').status, 'fail');
});

test('byte-identical input replacement still fails the inode and metadata freeze', (t) => {
  const f = fixture(t); const original = fs.mkdtempSync;
  t.mock.method(fs, 'mkdtempSync', (...args) => {
    const directory = original(...args);
    const bytes = fs.readFileSync(f.options.manifest);
    fs.renameSync(f.options.manifest, path.join(f.root, 'manifest.saved'));
    fs.writeFileSync(f.options.manifest, bytes, { flag: 'wx', mode: 0o600 });
    return directory;
  });
  const report = checkReadinessFiles(f.options);
  assert.equal(report.decision, 'NO-GO');
  assert.ok(report.issues.some((issue) => issue.gate === 'manifest' && issue.code === 'input_changed'));
  assert.ok(report.gates.every((entry) => entry.status === 'fail'));
});

test('atomic publication does not replace an output created after preflight', (t) => {
  const f = fixture(t); const original = fs.linkSync;
  t.mock.method(fs, 'linkSync', (...args) => {
    fs.writeFileSync(f.options.output, 'preserve', { flag: 'wx' });
    return original(...args);
  });
  assert.throws(() => checkReadinessFiles(f.options));
  assert.equal(fs.readFileSync(f.options.output, 'utf8'), 'preserve');
  assert.equal(fs.readdirSync(f.root).some((name) => name.startsWith('.frontend-readiness-')), false);
});

test('CLI rejects extra, duplicate and override flags without output or input disclosure', (t) => {
  const f = fixture(t);
  for (const args of [['--output', '/PRIVATE_ARGUMENT_SENTINEL'], ['--now', '0'],
    ['--force', 'true'], ['--allow-synthetic', 'true'], ['--trust', 'true'], ['PRIVATE_ARGUMENT_SENTINEL']]) {
    const result = f.run({}, args);
    assert.equal(result.status, 1); assert.equal(result.stdout, '');
    assert.equal(result.stderr.includes('PRIVATE_ARGUMENT_SENTINEL'), false);
    assert.equal(fs.existsSync(f.options.output), false);
  }
});
