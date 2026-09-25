import assert from 'node:assert/strict';
import test from 'node:test';
import { REQUIRED_GATES, evaluateReadiness } from '../frontend-readiness.mjs';

const now = Date.parse('2026-09-25T12:00:00.000Z');
const hour = 60 * 60 * 1000;
const minute = 60 * 1000;
const date = (value) => new Date(value).toISOString();
const checks = {
  'backend-readiness': { readiness: true, schema_compatibility: true, single_process_realtime: true },
  'frontend-ci': { lint: true, typecheck: true, build: true, tests: true },
  'full-stack': { real_backend: true, same_origin: true, public_admin: true, http: true, websocket: true },
  'frontend-security': { secret_scan: true, dependency_review: true, image_scan: true },
  'frontend-artifact': { identity: true, sbom: true, provenance: true, signature: true },
  rollout: { configuration: true, proxy: true, health: true, browser: true },
  recovery: { previous_artifact: true, compatibility: true, procedure: true },
};
const gateIds = Object.keys(checks);

// Constructed declarations, including evidence_kind="real", exercise the
// consistency algorithm only. They are not real gate executions or release GO
// evidence. File hashes are placeholders; actual hashing belongs to the CLI.
function fixture(clock = now) {
  const identities = {
    source_revision: '1'.repeat(40), source_modified: false,
    frontend_image: `ghcr.io/example/task-per-minute-frontend@sha256:${'a'.repeat(64)}`,
    backend_revision: '2'.repeat(40), backend_image: `registry.example.org/team/backend@sha256:${'b'.repeat(64)}`,
    environment: 'local-release',
  };
  const manifest = { schema_version: 1, ...identities, created_at: date(clock),
    owners: { release: 'Release owner', backend: 'Backend owner', recovery: 'Recovery owner', approver: 'Approver' },
    gates: gateIds.map((id) => ({ id, record: `${id}.json`, sha256: 'e'.repeat(64) })),
  };
  const records = Object.fromEntries(gateIds.map((gate) => [gate, {
    schema_version: 1, gate, status: 'pass', ...identities, finished_at: date(clock - minute),
    evidence_kind: 'real', checks: { ...checks[gate] },
    ...(['frontend-ci', 'full-stack'].includes(gate) ? { tests: { executed: 10, skipped: 0, failed: 0 } } : {}),
  }]));
  return { manifest, records, now: clock };
}

function denied(input, gate, code) {
  const result = evaluateReadiness(input);
  assert.equal(result.decision, 'NO-GO');
  assert.equal(result.release_authorized, false);
  if (gate && code) assert.ok(result.issues.some((issue) => issue.gate === gate && issue.code === code), `missing sanitized issue ${gate}/${code}`);
  return result;
}

test('constructed complete real-classified declarations give consistency GO, never release authorization', () => {
  const f = fixture(); const before = structuredClone(f);
  const result = evaluateReadiness(f);
  assert.deepEqual(result, { schema_version: 1, decision: 'GO', scope: 'evidence_consistency_only', release_authorized: false,
    source_revision: f.manifest.source_revision, frontend_image: f.manifest.frontend_image,
    backend_revision: f.manifest.backend_revision, backend_image: f.manifest.backend_image,
    environment: 'local-release', checked_at: '2026-09-25T12:00:00.000Z', issues: [],
    gates: gateIds.map((id) => ({ id, status: 'pass' })),
  });
  assert.deepEqual(f, before);
  assert.deepEqual(evaluateReadiness(f), result);
});

test('required gates are immutable and include external backend readiness and recovery', () => {
  assert.deepEqual(REQUIRED_GATES, ['backend-readiness', 'frontend-ci', 'full-stack', 'frontend-security', 'frontend-artifact', 'rollout', 'recovery']);
  assert.equal(Object.isFrozen(REQUIRED_GATES), true);
  assert.throws(() => REQUIRED_GATES.pop(), TypeError);
});

test('each missing required gate or record independently denies readiness', () => {
  for (const gate of gateIds) {
    const missingGate = fixture(); missingGate.manifest.gates = missingGate.manifest.gates.filter((entry) => entry.id !== gate);
    denied(missingGate, gate, 'missing_gate');
    const missingRecord = fixture(); delete missingRecord.records[gate];
    const result = denied(missingRecord, gate, 'missing_record');
    assert.equal(result.gates.find((entry) => entry.id === gate).status, 'fail');
    assert.equal(result.gates.filter((entry) => entry.status === 'pass').length, 6);
  }
});

test('empty manifest gates and records cannot infer readiness from completed tasks', () => {
  const f = fixture(); f.manifest.gates = []; f.records = {};
  const result = denied(f);
  assert.equal(result.gates.filter((entry) => entry.status === 'fail').length, 7);
  for (const gate of gateIds) assert.ok(result.issues.some((issue) => issue.gate === gate && issue.code === 'missing_gate'));
});

test('malformed top-level inputs, manifest and records produce sanitized NO-GO instead of throwing', () => {
  for (const value of [undefined, null, false, 17, 'PRIVATE_SENTINEL', [], {}, { manifest: null, records: null, now }]) {
    const result = evaluateReadiness(value);
    assert.equal(result.decision, 'NO-GO'); assert.equal(result.release_authorized, false);
    assert.equal(JSON.stringify(result).includes('PRIVATE_SENTINEL'), false);
  }
  for (const value of [null, [], 10, 'PRIVATE_SENTINEL']) {
    const f = fixture(); f.records.rollout = value; denied(f, 'rollout', 'invalid_record');
  }
});

test('strict manifest, record, owners, checks and test keys reject additions and omissions', () => {
  const mutations = [
    (f) => { f.manifest.extra = 'PRIVATE_SENTINEL'; }, (f) => { delete f.manifest.created_at; },
    (f) => { f.manifest.owners.extra = 'PRIVATE_SENTINEL'; },
    (f) => { f.records.rollout.extra = 'PRIVATE_SENTINEL'; },
    (f) => { delete f.records.rollout.finished_at; },
    (f) => { f.records.rollout.checks.extra = true; },
    (f) => { delete f.records.rollout.checks.browser; },
    (f) => { f.records['frontend-ci'].tests.extra = 0; },
    (f) => { f.records.recovery.tests = { executed: 10, skipped: 0, failed: 0 }; },
  ];
  for (const mutate of mutations) {
    const f = fixture(); mutate(f); const result = denied(f);
    assert.equal(JSON.stringify(result).includes('PRIVATE_SENTINEL'), false);
  }
});

test('all four owners must be present, nonempty and conventional safe names', () => {
  for (const owner of ['release', 'backend', 'recovery', 'approver']) {
    for (const value of [undefined, '', ' ', 'x'.repeat(65), '../PRIVATE_SENTINEL', 'Name\nPRIVATE_SENTINEL']) {
      const f = fixture(); f.manifest.owners[owner] = value; denied(f, 'manifest', 'invalid_owners');
    }
  }
});

test('dirty or non-boolean source state in manifest or any record fails closed', () => {
  for (const value of [true, 'false', 0, null, undefined]) {
    const f = fixture(); f.manifest.source_modified = value; denied(f);
    const g = fixture(); g.records.recovery.source_modified = value; denied(g);
  }
});

test('every identity must match independently, including external backend revision and digest', () => {
  const replacements = { source_revision: '3'.repeat(40), frontend_image: `ghcr.io/example/task-per-minute-frontend@sha256:${'c'.repeat(64)}`,
    backend_revision: '4'.repeat(40), backend_image: `registry.example.org/team/backend@sha256:${'d'.repeat(64)}`, environment: 'different-environment' };
  for (const gate of gateIds) for (const [field, value] of Object.entries(replacements)) {
    const f = fixture(); f.records[gate][field] = value; denied(f, gate, `${field}_mismatch`);
  }
});

test('invalid identity values are redacted and mutable images never pass', () => {
  for (const [field, value] of [
    ['source_revision', 'PRIVATE_SENTINEL'], ['source_revision', 'A'.repeat(40)],
    ['backend_revision', 'main'], ['frontend_image', 'ghcr.io/example/task-per-minute-frontend:latest'],
    ['frontend_image', `ghcr.io/example/backend@sha256:${'a'.repeat(64)}`],
    ['backend_image', `backend@sha256:${'b'.repeat(64)}`], ['backend_image', 'https://PRIVATE_SENTINEL@registry/image'],
    ['environment', '../PRIVATE_SENTINEL'], ['environment', 'space here'],
  ]) {
    const f = fixture(); f.manifest[field] = value; const result = denied(f);
    assert.equal(result[field], null); assert.equal(JSON.stringify(result).includes('PRIVATE_SENTINEL'), false);
  }
});

test('canonical revisions, hashes and environment reject a trailing control character', () => {
  for (const field of ['source_revision', 'backend_revision']) {
    const f = fixture(); f.manifest[field] = `${'a'.repeat(39)}\n`;
    const result = denied(f); assert.equal(result[field], null);
  }
  const hash = fixture(); hash.manifest.gates[0].sha256 = `${'a'.repeat(63)}\n`;
  denied(hash, 'backend-readiness', 'invalid_gate_reference');
  for (const suffix of ['\n', '\r', '\r\n', '\0', '\x1f', '\x7f']) {
    const env = fixture(); env.manifest.environment = `local-release${suffix}`;
    assert.equal(denied(env).environment, null);
    const record = fixture(); record.records.recovery.environment = `local-release${suffix}`;
    denied(record, 'recovery', 'environment_mismatch');
  }
});

test('every failed, skipped or not-applicable required record is NO-GO with no waiver', () => {
  for (const gate of gateIds) for (const status of ['fail', 'skipped', 'not_applicable', 'complete', true]) {
    const f = fixture(); f.records[gate].status = status; denied(f, gate, 'status_not_pass');
  }
});

test('synthetic and unknown evidence classifications are not real release evidence', () => {
  for (const gate of gateIds) for (const evidenceKind of ['synthetic', 'trusted', '', null]) {
    const f = fixture(); f.records[gate].evidence_kind = evidenceKind; denied(f, gate, 'evidence_not_real');
  }
});

test('all required checks must be literal true, not truthy or capacity-only substitutes', () => {
  for (const [gate, expected] of Object.entries(checks)) for (const check of Object.keys(expected)) {
    for (const value of [false, 'true', 1, null]) {
      const f = fixture(); f.records[gate].checks[check] = value; denied(f, gate, 'checks_not_pass');
    }
  }
  const f = fixture(); f.records['backend-readiness'].checks = { capacity: true };
  denied(f, 'backend-readiness', 'invalid_checks');
});

test('CI and full-stack test counts require executed positive safe integer with no failures or skips', () => {
  for (const gate of ['frontend-ci', 'full-stack']) for (const value of [
    undefined, null, {}, { executed: 0, skipped: 0, failed: 0 }, { executed: 1.5, skipped: 0, failed: 0 },
    { executed: Number.MAX_SAFE_INTEGER + 1, skipped: 0, failed: 0 }, { executed: 10, skipped: 1, failed: 0 },
    { executed: 10, skipped: 0, failed: 1 }, { executed: '10', skipped: 0, failed: 0 },
  ]) {
    const f = fixture(); f.records[gate].tests = value; denied(f, gate, 'invalid_tests');
  }
});

test('unknown, duplicate and mismatched gate IDs cannot hide required evidence', () => {
  const unknown = fixture(); unknown.manifest.gates.push({ id: 'PRIVATE_SENTINEL', record: 'extra.json', sha256: 'a'.repeat(64) });
  assert.equal(JSON.stringify(denied(unknown, 'manifest', 'unknown_gate')).includes('PRIVATE_SENTINEL'), false);
  const duplicate = fixture(); duplicate.manifest.gates.push({ ...duplicate.manifest.gates[0] });
  denied(duplicate, 'backend-readiness', 'duplicate_gate');
  const mismatch = fixture(); mismatch.records.rollout.gate = 'recovery'; denied(mismatch, 'rollout', 'gate_mismatch');
  const extra = fixture(); extra.records.PRIVATE_SENTINEL = {};
  assert.equal(JSON.stringify(denied(extra, 'manifest', 'unknown_record')).includes('PRIVATE_SENTINEL'), false);
});

test('manifest references require safe JSON basenames and canonical SHA256 strings', () => {
  for (const record of ['../PRIVATE_SENTINEL.json', '/tmp/PRIVATE_SENTINEL.json', 'dir/file.json', 'dir\\file.json', '.env.json', 'a..b.json', 'file.txt', 'name\n.json']) {
    const f = fixture(); f.manifest.gates[0].record = record; denied(f, 'backend-readiness', 'invalid_gate_reference');
  }
  for (const hash of ['', 'A'.repeat(64), '0'.repeat(63), `sha256:${'a'.repeat(64)}`, null]) {
    const f = fixture(); f.manifest.gates[0].sha256 = hash; denied(f, 'backend-readiness', 'invalid_gate_reference');
  }
});

test('timestamps must round-trip canonical UTC milliseconds and reject impossible dates', () => {
  for (const value of ['2026-09-25T12:00:00Z', '2026-09-25T12:00:00.000+00:00', '2026-02-30T12:00:00.000Z', 'PRIVATE_SENTINEL', null]) {
    const f = fixture(); f.manifest.created_at = value; denied(f, 'manifest', 'invalid_created_at');
    const g = fixture(); g.records.rollout.finished_at = value; denied(g, 'rollout', 'invalid_finished_at');
  }
});

test('manifest and records each expire after exactly 24 hours without a freshness override', () => {
  const boundary = fixture(); boundary.manifest.created_at = date(now - 24 * hour);
  for (const record of Object.values(boundary.records)) record.finished_at = date(now - 24 * hour);
  assert.equal(evaluateReadiness(boundary).decision, 'GO');
  const staleManifest = fixture(); staleManifest.manifest.created_at = date(now - 24 * hour - 1); denied(staleManifest, 'manifest', 'stale_manifest');
  for (const gate of gateIds) { const f = fixture(); f.records[gate].finished_at = date(now - 24 * hour - 1); denied(f, gate, 'stale_record'); }
});

test('future skew and manifest-to-record sequencing each have a fixed five-minute limit', () => {
  const boundary = fixture(); boundary.manifest.created_at = date(now + 5 * minute);
  for (const record of Object.values(boundary.records)) record.finished_at = date(now + 5 * minute);
  assert.equal(evaluateReadiness(boundary).decision, 'GO');
  const futureManifest = fixture(); futureManifest.manifest.created_at = date(now + 5 * minute + 1); denied(futureManifest, 'manifest', 'future_manifest');
  const futureRecord = fixture(); futureRecord.records.rollout.finished_at = date(now + 5 * minute + 1); denied(futureRecord, 'rollout', 'future_record');
  const sequence = fixture(); sequence.manifest.created_at = date(now - 10 * minute);
  for (const record of Object.values(sequence.records)) record.finished_at = date(now - 5 * minute);
  assert.equal(evaluateReadiness(sequence).decision, 'GO');
  sequence.records.rollout.finished_at = date(now - 5 * minute + 1); denied(sequence, 'rollout', 'record_after_manifest');
});

test('invalid clock injection fails closed; omitted clock uses current time', () => {
  for (const value of [NaN, Infinity, -1, '2026-09-25', null, 1e30]) {
    const f = fixture(); f.now = value; const result = denied(f, 'manifest', 'invalid_clock'); assert.equal(result.checked_at, null);
  }
  const f = fixture(Date.now()); delete f.now;
  assert.equal(evaluateReadiness(f).decision, 'GO');
});

test('malformed input never exposes owners, raw records, filenames or supplied status prose', () => {
  const f = fixture();
  f.manifest.owners.release = 'PRIVATE_SENTINEL'; f.manifest.gates[0].record = 'PRIVATE_SENTINEL.json';
  f.records['backend-readiness'].status = 'PRIVATE_SENTINEL'; f.records['backend-readiness'].extra = { payload: 'PRIVATE_SENTINEL' };
  const result = denied(f); const text = JSON.stringify(result);
  assert.equal(text.includes('PRIVATE_SENTINEL'), false);
  assert.ok(result.issues.every((issue) => ['manifest', ...gateIds].includes(issue.gate) && /^[a-z_]+$/.test(issue.code)));
  assert.equal(Object.hasOwn(result, 'owners'), false);
  assert.equal(Object.hasOwn(result, 'records'), false);
  const accessor = { get manifest() { throw new Error('PRIVATE_SENTINEL'); } };
  assert.equal(JSON.stringify(denied(accessor)).includes('PRIVATE_SENTINEL'), false);
});
