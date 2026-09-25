#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { evaluateReadiness, REQUIRED_GATES } from './frontend-readiness.mjs';

const MANIFEST_LIMIT = 64 * 1024;
const RECORD_LIMIT = 1024 * 1024;
const TOTAL_LIMIT = 8 * 1024 * 1024;
const required = new Set(REQUIRED_GATES);
const sha256 = (bytes) => createHash('sha256').update(bytes).digest('hex');
const object = (value) => value !== null && typeof value === 'object' && !Array.isArray(value);
const stamp = (stat) => [stat.dev, stat.ino, stat.size, stat.mtimeNs, stat.ctimeNs].join(':');
const identity = (stat) => `${stat.dev}:${stat.ino}`;

class FileError extends Error {
  constructor(code) { super(code); this.code = code; }
}
const requireValue = (value, code) => { if (!value) throw new FileError(code); };

function absolutePath(value) {
  requireValue(typeof value === 'string' && value.length > 0 && value.length <= 4096
    && path.isAbsolute(value) && !/[\\\x00-\x1f\x7f]/.test(value)
    && value === path.normalize(value) && !value.split(path.sep).some((part) => part === '.' || part === '..'), 'path_invalid');
  return value;
}

// lstat every component, including directory parents. Files are opened with
// O_NOFOLLOW below and checked against both their open descriptor and pathname.
function checkedPath(filename, directory = false) {
  absolutePath(filename);
  const root = path.parse(filename).root;
  let current = root;
  const parents = [];
  const parts = filename.slice(root.length).split(path.sep).filter(Boolean);
  const rootStat = fs.lstatSync(root, { bigint: true });
  requireValue(rootStat.isDirectory() && !rootStat.isSymbolicLink(), 'unsafe_path');
  parents.push(identity(rootStat));
  let stat = rootStat;
  for (let index = 0; index < parts.length; index++) {
    current = path.join(current, parts[index]);
    stat = fs.lstatSync(current, { bigint: true });
    const isDirectory = index < parts.length - 1 || directory;
    requireValue(!stat.isSymbolicLink() && (isDirectory ? stat.isDirectory() : stat.isFile()), 'unsafe_path');
    if (isDirectory) parents.push(identity(stat));
  }
  requireValue(directory ? stat.isDirectory() : stat.isFile(), 'unsafe_path');
  return { stat, parents: parents.join('/') };
}

function errorCode(error) {
  if (error instanceof FileError) return error.code;
  if (error?.code === 'ENOENT') return 'file_missing';
  if (['ELOOP', 'ENOTDIR'].includes(error?.code)) return 'unsafe_path';
  return 'io_error';
}

function readInput(filename, limit, gate) {
  const before = checkedPath(filename);
  requireValue(before.stat.size <= BigInt(limit), 'file_too_large');
  const fd = fs.openSync(filename, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW | fs.constants.O_NONBLOCK);
  try {
    const opened = fs.fstatSync(fd, { bigint: true });
    requireValue(opened.isFile() && stamp(opened) === stamp(before.stat), 'input_changed');
    const bytes = Buffer.alloc(Number(opened.size));
    let offset = 0;
    while (offset < bytes.length) {
      const count = fs.readSync(fd, bytes, offset, bytes.length - offset, null);
      if (!count) break;
      offset += count;
    }
    const after = checkedPath(filename);
    requireValue(offset === bytes.length && stamp(opened) === stamp(fs.fstatSync(fd, { bigint: true }))
      && stamp(opened) === stamp(after.stat) && before.parents === after.parents, 'input_changed');
    return { filename, limit, gate, bytes, hash: sha256(bytes), stamp: stamp(opened), parents: before.parents };
  } finally { fs.closeSync(fd); }
}

function parseJson(input) {
  try {
    const text = new TextDecoder('utf-8', { fatal: true }).decode(input.bytes);
    return JSON.parse(text);
  } catch { throw new FileError('invalid_json'); }
}

function safeRecordName(value) {
  return typeof value === 'string' && value.length <= 128 && !value.includes('..')
    && /^[a-zA-Z0-9][a-zA-Z0-9._-]*\.json$/.test(value) && !/[\r\n]/.test(value);
}

function failedReport() {
  return { schema_version: 1, decision: 'NO-GO', scope: 'evidence_consistency_only', release_authorized: false,
    source_revision: null, frontend_image: null, backend_revision: null, backend_image: null, environment: null,
    checked_at: new Date().toISOString(), issues: [], gates: REQUIRED_GATES.map((id) => ({ id, status: 'fail' })) };
}

// IO issues only downgrade the pure evaluator. Gate names come from the fixed
// allowlist (or 'manifest'); codes are static, never paths or raw parser errors.
function withFileIssues(report, issues) {
  if (issues.length === 0) return report;
  const all = [...report.issues];
  for (const issue of issues) {
    if (!all.some((item) => item.gate === issue.gate && item.code === issue.code)) all.push(issue);
  }
  const globalFailure = issues.some((issue) => issue.gate === 'manifest');
  return { ...report, decision: 'NO-GO', release_authorized: false, issues: all,
    gates: report.gates.map((gate) => ({ ...gate,
      status: globalFailure || issues.some((issue) => issue.gate === gate.id) ? 'fail' : gate.status })) };
}

function outputTarget(filename) {
  const output = absolutePath(filename);
  const parent = path.dirname(output);
  const parentState = checkedPath(parent, true);
  try {
    fs.lstatSync(output);
    throw new FileError('output_exists');
  } catch (error) { if (error.code !== 'ENOENT') throw error; }
  return { output, parent, parentState };
}

function publishReport(target, report, inputs, issues) {
  const checkParent = () => requireValue(checkedPath(target.parent, true).parents === target.parentState.parents, 'output_parent_changed');
  checkParent();
  const scratch = fs.mkdtempSync(path.join(target.parent, '.frontend-readiness-'));
  const scratchState = checkedPath(scratch, true);
  const temporary = path.join(scratch, 'report.json');
  let created = false;
  try {
    // No asynchronous work between this snapshot recheck and publication. Both
    // bytes and metadata must still match, even when JSON parses identically.
    for (const input of inputs) {
      try {
        const after = readInput(input.filename, input.limit, input.gate);
        requireValue(after.hash === input.hash && after.stamp === input.stamp
          && after.parents === input.parents, 'input_changed');
      } catch { issues.push({ gate: input.gate, code: 'input_changed' }); }
    }
    report = withFileIssues(report, issues);
    checkParent();
    const fd = fs.openSync(temporary, fs.constants.O_WRONLY | fs.constants.O_CREAT | fs.constants.O_EXCL | fs.constants.O_NOFOLLOW, 0o600);
    created = true;
    try {
      fs.fchmodSync(fd, 0o600);
      fs.writeFileSync(fd, `${JSON.stringify(report, null, 2)}\n`);
      fs.fsyncSync(fd);
    } finally { fs.closeSync(fd); }
    checkParent();
    requireValue(checkedPath(scratch, true).parents === scratchState.parents, 'output_parent_changed');
    // Same-filesystem hard linking is atomic and refuses every existing target,
    // including dangling symlinks. rename would silently overwrite a report.
    fs.linkSync(temporary, target.output);
    return report;
  } finally {
    // Never clean up a replacement directory belonging to another writer.
    if (checkedPath(scratch, true).parents === scratchState.parents) {
      if (created) fs.unlinkSync(temporary);
      fs.rmdirSync(scratch);
    }
  }
}

/** Offline consistency check only. No producer verification, authorization,
 * environment lookup, network access or subprocess invocation occurs here.
 * Parsed input failures produce a NO-GO report; unsafe output paths throw a
 * sanitized FileError. Missing/hash/IO issues extend core issues without waivers.
 */
export function checkReadinessFiles(options) {
  try {
    requireValue(object(options) && Object.keys(options).length === 3
      && ['manifest', 'evidenceDir', 'output'].every((name) => Object.hasOwn(options, name)), 'arguments_invalid');
    const target = outputTarget(options.output);
    const inputs = []; const issues = []; const records = Object.create(null);
    let total = 0; let manifest;
    const read = (filename, limit, gate) => {
      const input = readInput(filename, limit, gate);
      total += input.bytes.length;
      requireValue(total <= TOTAL_LIMIT, 'total_too_large');
      inputs.push(input);
      return input;
    };
    try { manifest = parseJson(read(options.manifest, MANIFEST_LIMIT, 'manifest')); }
    catch (error) { issues.push({ gate: 'manifest', code: errorCode(error) }); }

    if (object(manifest) && Array.isArray(manifest.gates)) {
      for (const id of REQUIRED_GATES) {
        const entries = manifest.gates.filter((entry) => object(entry) && entry.id === id);
        if (entries.length === 0) continue; // The pure evaluator reports missing gates.
        if (entries.length !== 1) { issues.push({ gate: id, code: 'gate_duplicate' }); continue; }
        const entry = entries[0];
        if (!safeRecordName(entry.record)) { issues.push({ gate: id, code: 'record_path_invalid' }); continue; }
        if (typeof entry.sha256 !== 'string' || entry.sha256.length !== 64 || !/^[a-f0-9]{64}$/.test(entry.sha256)) {
          issues.push({ gate: id, code: 'record_hash_invalid' }); continue;
        }
        try {
          checkedPath(options.evidenceDir, true);
          const input = read(path.join(options.evidenceDir, entry.record), RECORD_LIMIT, id);
          requireValue(input.hash === entry.sha256, 'hash_mismatch');
          records[id] = parseJson(input);
        } catch (error) { issues.push({ gate: id, code: errorCode(error) }); }
      }
      // No unknown gate ever controls a pathname or appears in an IO issue.
      if (manifest.gates.some((entry) => !object(entry) || !required.has(entry.id))) {
        issues.push({ gate: 'manifest', code: 'gate_invalid' });
      }
    }
    let report;
    try { report = evaluateReadiness({ manifest, records }); }
    catch { report = failedReport(); issues.push({ gate: 'manifest', code: 'evaluation_failed' }); }
    return publishReport(target, report, inputs, issues);
  } catch (error) { throw new FileError(errorCode(error)); }
}

export function parseArguments(args) {
  requireValue(Array.isArray(args), 'arguments_invalid');
  const names = new Map([['--manifest', 'manifest'], ['--evidence-dir', 'evidenceDir'], ['--output', 'output']]);
  const options = {};
  for (let index = 0; index < args.length; index += 2) {
    const name = names.get(args[index]); const value = args[index + 1];
    requireValue(name && !Object.hasOwn(options, name) && typeof value === 'string' && !value.startsWith('--'), 'arguments_invalid');
    options[name] = absolutePath(value);
  }
  requireValue(Object.keys(options).length === names.size, 'arguments_invalid');
  return options;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.umask(0o077);
  try {
    const report = checkReadinessFiles(parseArguments(process.argv.slice(2)));
    process.stdout.write(`${JSON.stringify(report)}\n`);
    process.exitCode = report.decision === 'GO' ? 0 : 1;
  } catch (error) {
    process.stderr.write(`frontend readiness check failed: ${errorCode(error)}\n`);
    process.exitCode = 1;
  }
}
