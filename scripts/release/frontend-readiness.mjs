export const REQUIRED_GATES = Object.freeze(['backend-readiness', 'frontend-ci', 'full-stack',
  'frontend-security', 'frontend-artifact', 'rollout', 'recovery']);

const requiredChecks = {
  'backend-readiness': ['readiness', 'schema_compatibility', 'single_process_realtime'],
  'frontend-ci': ['lint', 'typecheck', 'build', 'tests'],
  'full-stack': ['real_backend', 'same_origin', 'public_admin', 'http', 'websocket'],
  'frontend-security': ['secret_scan', 'dependency_review', 'image_scan'],
  'frontend-artifact': ['identity', 'sbom', 'provenance', 'signature'],
  rollout: ['configuration', 'proxy', 'health', 'browser'],
  recovery: ['previous_artifact', 'compatibility', 'procedure'],
};
const identityKeys = ['source_revision', 'frontend_image', 'backend_revision', 'backend_image', 'environment'];
const manifestKeys = ['schema_version', ...identityKeys, 'source_modified', 'created_at', 'owners', 'gates'];
const recordKeys = ['schema_version', 'gate', 'status', ...identityKeys, 'source_modified', 'finished_at', 'evidence_kind', 'checks'];
const ownerKeys = ['release', 'backend', 'recovery', 'approver'];
const maxAge = 24 * 60 * 60 * 1000;
const clockSkew = 5 * 60 * 1000;

function record(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    && [Object.prototype, null].includes(Object.getPrototypeOf(value));
}

function exactKeys(value, keys) {
  return record(value) && Object.keys(value).length === keys.length && keys.every((key) => Object.hasOwn(value, key));
}

function hex(value, length) {
  return typeof value === 'string' && value.length === length && !/[^a-f0-9]/.test(value);
}

function frontendImage(value) {
  return typeof value === 'string' && value.length <= 256 && !/\s/.test(value)
    && /^ghcr\.io\/[a-z0-9](?:[a-z0-9-]{0,37}[a-z0-9])?\/task-per-minute-frontend@sha256:[a-f0-9]{64}$/.test(value);
}

function backendImage(value) {
  if (typeof value !== 'string' || value.length > 512 || /\s/.test(value)) return false;
  const parts = /^([^/]+)\/([a-z0-9]+(?:[._-][a-z0-9]+)*(?:\/[a-z0-9]+(?:[._-][a-z0-9]+)*)*)@sha256:([a-f0-9]{64})$/.exec(value);
  if (!parts) return false;
  const [host, port, extra] = parts[1].split(':');
  if (extra !== undefined || (!host.includes('.') && host !== 'localhost' && port === undefined)) return false;
  if (port !== undefined && (!/^[1-9][0-9]{0,4}$/.test(port) || Number(port) > 65535)) return false;
  return host.length <= 253 && host.split('.').every((label) => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label));
}

const identityValidators = {
  source_revision: (value) => hex(value, 40), frontend_image: frontendImage,
  backend_revision: (value) => hex(value, 40), backend_image: backendImage,
  environment: (value) => typeof value === 'string' && value.length <= 64 && !/[^a-z0-9-]/.test(value)
    && /^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/.test(value),
};

function owner(value) {
  return typeof value === 'string' && value.length > 0 && value.length <= 64 && value === value.trim()
    && /^[a-zA-Z][a-zA-Z0-9 ._-]*$/.test(value);
}

function timestamp(value) {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/.test(value)) return null;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) && new Date(parsed).toISOString() === value ? parsed : null;
}

function reference(value) {
  return typeof value === 'string' && value.length <= 128 && !value.includes('..')
    && /^[a-zA-Z0-9][a-zA-Z0-9._-]*\.json$/.test(value) && !/\s/.test(value);
}

function emptyReport() {
  return { schema_version: 1, decision: 'NO-GO', scope: 'evidence_consistency_only', release_authorized: false,
    source_revision: null, frontend_image: null, backend_revision: null, backend_image: null, environment: null,
    checked_at: null, issues: [], gates: REQUIRED_GATES.map((id) => ({ id, status: 'fail' })) };
}

/** Evaluate parsed declarations only. The caller must verify evidence file
 * hashes before supplying records. This does not execute producers, verify
 * signatures/provenance or authorize deployment. `now` is a numeric clock
 * injection for tests, not a manifest or CLI policy override. */
export function evaluateReadiness(input = {}) {
  const report = emptyReport();
  const issue = (gate, code) => {
    if (!report.issues.some((item) => item.gate === gate && item.code === code)) report.issues.push({ gate, code });
  };
  try {
    if (!record(input)) { issue('manifest', 'invalid_input'); return report; }
    const { manifest, records } = input;
    const now = input.now === undefined ? Date.now() : input.now;
    const validClock = Number.isSafeInteger(now) && now >= 0 && now <= 253402300799999;
    if (validClock) report.checked_at = new Date(now).toISOString();
    else issue('manifest', 'invalid_clock');
    if (!record(manifest)) { issue('manifest', 'invalid_manifest'); return report; }
    if (!exactKeys(manifest, manifestKeys)) issue('manifest', 'invalid_manifest');
    if (manifest.schema_version !== 1) issue('manifest', 'invalid_schema_version');
    for (const key of identityKeys) {
      if (identityValidators[key](manifest[key])) report[key] = manifest[key];
      else issue('manifest', `invalid_${key}`);
    }
    if (manifest.source_modified !== false) issue('manifest', 'source_modified');
    if (!exactKeys(manifest.owners, ownerKeys) || !ownerKeys.every((key) => owner(manifest.owners[key]))) issue('manifest', 'invalid_owners');
    const created = timestamp(manifest.created_at);
    if (created === null) issue('manifest', 'invalid_created_at');
    else if (validClock) {
      if (now - created > maxAge) issue('manifest', 'stale_manifest');
      if (created - now > clockSkew) issue('manifest', 'future_manifest');
    }
    const declared = new Set();
    if (!Array.isArray(manifest.gates) || manifest.gates.length > 64) issue('manifest', 'invalid_gates');
    else for (const entry of manifest.gates) {
      if (!record(entry) || !REQUIRED_GATES.includes(entry.id)) { issue('manifest', 'unknown_gate'); continue; }
      const gate = entry.id;
      if (declared.has(gate)) issue(gate, 'duplicate_gate');
      declared.add(gate);
      if (!exactKeys(entry, ['id', 'record', 'sha256']) || !reference(entry.record) || !hex(entry.sha256, 64)) issue(gate, 'invalid_gate_reference');
    }
    for (const gate of REQUIRED_GATES) if (!declared.has(gate)) issue(gate, 'missing_gate');
    const validRecords = record(records);
    if (!validRecords) issue('manifest', 'invalid_records');
    else if (Object.keys(records).some((gate) => !REQUIRED_GATES.includes(gate))) issue('manifest', 'unknown_record');
    for (const gate of REQUIRED_GATES) {
      if (!validRecords || !Object.hasOwn(records, gate)) { issue(gate, 'missing_record'); continue; }
      const evidence = records[gate];
      if (!record(evidence)) { issue(gate, 'invalid_record'); continue; }
      const hasTests = gate === 'frontend-ci' || gate === 'full-stack';
      if (!exactKeys(evidence, [...recordKeys, ...(hasTests ? ['tests'] : [])]) || evidence.schema_version !== 1) issue(gate, 'invalid_record');
      if (evidence.gate !== gate) issue(gate, 'gate_mismatch');
      if (evidence.status !== 'pass') issue(gate, 'status_not_pass');
      if (evidence.source_modified !== false) issue(gate, 'source_modified');
      for (const key of identityKeys) if (!identityValidators[key](evidence[key]) || evidence[key] !== manifest[key]) issue(gate, `${key}_mismatch`);
      if (evidence.evidence_kind !== 'real') issue(gate, 'evidence_not_real');
      const finished = timestamp(evidence.finished_at);
      if (finished === null) issue(gate, 'invalid_finished_at');
      else {
        if (validClock && now - finished > maxAge) issue(gate, 'stale_record');
        if (validClock && finished - now > clockSkew) issue(gate, 'future_record');
        if (created !== null && finished - created > clockSkew) issue(gate, 'record_after_manifest');
      }
      if (!exactKeys(evidence.checks, requiredChecks[gate])) issue(gate, 'invalid_checks');
      else if (!requiredChecks[gate].every((key) => evidence.checks[key] === true)) issue(gate, 'checks_not_pass');
      if (hasTests && (!exactKeys(evidence.tests, ['executed', 'skipped', 'failed'])
        || !Number.isSafeInteger(evidence.tests.executed) || evidence.tests.executed <= 0
        || evidence.tests.skipped !== 0 || evidence.tests.failed !== 0)) issue(gate, 'invalid_tests');
    }
    const invalidManifest = report.issues.some((item) => item.gate === 'manifest');
    for (const gate of report.gates) {
      if (!invalidManifest && !report.issues.some((item) => item.gate === gate.id)) gate.status = 'pass';
    }
    if (report.issues.length === 0) report.decision = 'GO';
    return report;
  } catch {
    // Non-JSON API callers can supply accessors or other malformed objects.
    // Never return an exception message that may contain their input values.
    const failed = emptyReport();
    failed.issues.push({ gate: 'manifest', code: 'invalid_input' });
    return failed;
  }
}
