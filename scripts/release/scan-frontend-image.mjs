#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { isDeepStrictEqual } from 'node:util';
import { validateImage } from './validate-frontend-image.mjs';

const policyFile = fileURLToPath(new URL('../../security/trivy/frontend-policy.json', import.meta.url));
const defaultLockfile = fileURLToPath(new URL('../../frontend/package-lock.json', import.meta.url));
const maxJsonBytes = 32 * 1024 * 1024;
const severities = ['UNKNOWN', 'LOW', 'MEDIUM', 'HIGH', 'CRITICAL'];
const sha = (bytes) => createHash('sha256').update(bytes).digest('hex');
const record = (value) => value !== null && typeof value === 'object' && !Array.isArray(value);
const check = (condition, message) => { if (!condition) throw new Error(message); };

function absolutePath(value) {
  check(typeof value === 'string' && path.isAbsolute(value) && !/[\x00-\x1f\x7f\\]/.test(value)
    && !value.split('/').includes('..'), 'an absolute path without traversal is required');
  return path.resolve(value);
}

function checkedPath(value, directory = false) {
  const absolute = absolutePath(value);
  let current = path.parse(absolute).root;
  for (const part of absolute.slice(current.length).split('/').filter(Boolean)) {
    current = path.join(current, part);
    const stat = fs.lstatSync(current);
    check(!stat.isSymbolicLink(), 'symlink paths are not allowed');
    check(current === absolute && !directory ? stat.isFile() : stat.isDirectory(), 'input must be a regular file or directory');
  }
  return absolute;
}

const stamp = (stat) => [stat.dev, stat.ino, stat.size, stat.mtimeNs, stat.ctimeNs].join(':');

function evidence(filename, json = false) {
  const absolute = checkedPath(filename);
  const before = fs.lstatSync(absolute, { bigint: true });
  const fd = fs.openSync(absolute, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW | fs.constants.O_NONBLOCK);
  try {
    const opened = fs.fstatSync(fd, { bigint: true });
    check(opened.isFile() && stamp(before) === stamp(opened), 'input changed while opening');
    check(!json || opened.size <= BigInt(maxJsonBytes), 'JSON exceeds bounded size limit');
    const hash = createHash('sha256');
    const buffer = Buffer.alloc(1024 * 1024);
    const chunks = [];
    let size = 0;
    for (;;) {
      const length = fs.readSync(fd, buffer, 0, buffer.length, null);
      if (!length) break;
      size += length;
      check(BigInt(size) <= opened.size, 'input grew while reading');
      hash.update(buffer.subarray(0, length));
      if (json) chunks.push(Buffer.from(buffer.subarray(0, length)));
    }
    checkedPath(absolute);
    check(BigInt(size) === opened.size && stamp(opened) === stamp(fs.fstatSync(fd, { bigint: true }))
      && stamp(opened) === stamp(fs.lstatSync(absolute, { bigint: true })), 'input changed while reading');
    let value;
    if (json) {
      try { value = JSON.parse(Buffer.concat(chunks).toString('utf8')); } catch { throw new Error('invalid JSON document'); }
      check(record(value), 'JSON document must be an object');
    }
    return { filename: absolute, sha256: hash.digest('hex'), stamp: stamp(opened), size, value };
  } finally { fs.closeSync(fd); }
}

function assertUnchanged(before) {
  const after = evidence(before.filename);
  check(before.sha256 === after.sha256 && before.stamp === after.stamp, 'input hash or file identity changed during scan');
}

function timestamp(value) {
  check(typeof value === 'string' && /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value), 'invalid database timestamp');
  const result = Date.parse(value);
  check(Number.isFinite(result) && new Date(result).toISOString().slice(0, 19) === value.slice(0, 19), 'invalid database timestamp');
  return result;
}

function freshDatabase(metadata, policy, now) {
  check(metadata.Version === policy.schema_version, 'unsupported database schema version');
  const updated = timestamp(metadata.UpdatedAt);
  const next = timestamp(metadata.NextUpdate);
  check(Number.isFinite(now) && updated <= now && now - updated <= policy.max_age_hours * 3_600_000
    && next > now && next > updated, 'database is stale, future-dated or expired');
}

function validatePolicy(policy) {
  check(policy.schema_version === 1 && policy.scanner?.name === 'trivy' && policy.scanner.version === '0.72.0', 'unsupported scanner policy');
  check(Array.isArray(policy.scanner.accepted_executable_sha256) && policy.scanner.accepted_executable_sha256.length > 0
    && policy.scanner.accepted_executable_sha256.every((value) => /^[a-f0-9]{64}$/.test(value)), 'invalid scanner hashes in policy');
  check(policy.database?.schema_version === 2 && policy.database.max_age_hours === 24
    && Array.isArray(policy.deny_severities) && isDeepStrictEqual([...policy.deny_severities].sort(), ['CRITICAL', 'HIGH']), 'unsupported database or severity policy');
}

function runtimeConfig(config) {
  check(record(config), 'runtime config is required');
  const user = config.User;
  check(typeof user === 'string' && /^[a-zA-Z0-9_-]+(?::[a-zA-Z0-9_-]+)?$/.test(user)
    && !/^(?:root|0+)(?::|$)/i.test(user), 'runtime must configure a non-root user');
  const entrypoint = config.Entrypoint ?? [];
  check(isDeepStrictEqual(config.Cmd, ['node', 'server.js']) && Array.isArray(entrypoint)
    && (entrypoint.length === 0 || (entrypoint.length === 1
      && ['docker-entrypoint.sh', '/usr/local/bin/docker-entrypoint.sh'].includes(entrypoint[0]))), 'runtime requires the Node server entrypoint');
  const validators = {
    PATH: (v) => v === '/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin',
    NODE_VERSION: (v) => /^\d+\.\d+\.\d+$/.test(v), YARN_VERSION: (v) => /^\d+\.\d+\.\d+$/.test(v),
    PORT: (v) => /^[1-9][0-9]{0,4}$/.test(v) && Number(v) <= 65535,
    BACKEND_PORT: (v) => /^[1-9][0-9]{0,4}$/.test(v) && Number(v) <= 65535,
    HOSTNAME: (v) => ['0.0.0.0', '::', 'localhost'].includes(v),
    NODE_ENV: (v) => v === 'production', NEXT_TELEMETRY_DISABLED: (v) => v === '1',
    BACKEND_URL: (v) => {
      if (!/^https?:\/\//.test(v) || /[\s\\?#\x00-\x1f\x7f]/.test(v) || v.split('/')[2].includes('@')) return false;
      try { const url = new URL(v); return !!url.hostname && !url.username && !url.password; } catch { return false; }
    },
  };
  check(Array.isArray(config.Env) && config.Env.length <= 32, 'runtime environment must be bounded');
  const seen = new Set();
  for (const item of config.Env) {
    check(typeof item === 'string' && item.length <= 4096 && item.includes('='), 'invalid runtime environment entry');
    const equal = item.indexOf('=');
    const name = item.slice(0, equal);
    const value = item.slice(equal + 1);
    check(Object.hasOwn(validators, name) && !seen.has(name) && validators[name](value), 'runtime environment key or public value is not allowed');
    seen.add(name);
  }
}

function scanResults(report, configDigest) {
  check(report.SchemaVersion === 2 && report.ArtifactType === 'container_image', 'invalid scanner report schema');
  check(report.Metadata?.ImageID === configDigest, 'scanner ImageID does not match verified config');
  check(Array.isArray(report.Results) && report.Results.length > 0 && report.Results.length <= 4096, 'scanner report has no bounded results');
  let visited = 0;
  function noSuppression(value, depth = 0) {
    check(depth < 64 && ++visited <= 1_000_000, 'scanner report structure exceeds limit');
    if (value === null || typeof value !== 'object') return;
    for (const [key, child] of Object.entries(value)) {
      if (/suppress|ignor|modifiedfindings/i.test(key)) {
        const empty = child === null || child === false || child === 0 || child === ''
          || (Array.isArray(child) && child.length === 0) || (record(child) && Object.keys(child).length === 0);
        check(empty, 'ignored, suppressed or modified findings are not allowed');
      }
      noSuppression(child, depth + 1);
    }
  }
  noSuppression(report);
  const counts = Object.fromEntries(severities.map((severity) => [severity, 0]));
  const results = report.Results.map((result) => {
    check(record(result) && typeof result.Target === 'string' && result.Target.length > 0
      && ['os-pkgs', 'lang-pkgs'].includes(result.Class) && typeof result.Type === 'string', 'invalid scanned result');
    const vulnerabilities = result.Vulnerabilities ?? [];
    check(Array.isArray(vulnerabilities), 'invalid scanner vulnerability results');
    for (const item of vulnerabilities) {
      check(record(item) && typeof item.VulnerabilityID === 'string' && item.VulnerabilityID.length > 0
        && typeof item.PkgName === 'string' && item.PkgName.length > 0 && severities.includes(item.Severity), 'invalid vulnerability entry in report');
      counts[item.Severity]++;
    }
    return { class: result.Class, type: result.Type, target_sha256: sha(result.Target), findings: vulnerabilities.length };
  });
  check(results.some((result) => result.class === 'os-pkgs' && result.type === 'alpine'), 'scanner report lacks Alpine OS results');
  check(results.some((result) => result.class === 'lang-pkgs' && result.type === 'node-pkg'), 'scanner report lacks Node.js library results');
  return { counts, results, total: Object.values(counts).reduce((sum, count) => sum + count, 0) };
}

function publish(output, report) {
  checkedPath(output, true);
  const temporary = path.join(output, '.report.tmp');
  const target = path.join(output, 'report.json');
  const fd = fs.openSync(temporary, 'wx', 0o600);
  try {
    fs.writeFileSync(fd, `${JSON.stringify(report, null, 2)}\n`);
    fs.fsyncSync(fd);
    checkedPath(output, true);
    fs.linkSync(temporary, target);
  } finally { fs.closeSync(fd); fs.unlinkSync(temporary); }
}

/** Test callers may inject a fixture policy, clock and short process timeout.
 * The CLI exposes none of these hooks and always loads repository policy. */
export async function scanImage(options, { policy: testPolicy, now = Date.now, timeoutMs = 600_000 } = {}) {
  check(/^sha256:[a-f0-9]{64}$/.test(options.imageDigest ?? ''), 'image digest must be a canonical SHA256 index digest');
  const artifact = checkedPath(options.artifact, true);
  const layout = checkedPath(path.join(artifact, 'image'), true);
  const cacheDir = checkedPath(options.cacheDir, true);
  const scanner = checkedPath(options.scanner);
  fs.accessSync(scanner, fs.constants.X_OK);
  const output = absolutePath(options.output);
  check(![layout, cacheDir, path.dirname(scanner)].some((parent) => output === parent || output.startsWith(`${parent}/`)), 'output must not overlap OCI, cache or scanner inputs');
  checkedPath(path.dirname(output), true);
  try { fs.lstatSync(output); throw new Error('output already exists'); } catch (error) { if (error.code !== 'ENOENT') throw error; }
  const policyEvidence = testPolicy === undefined ? evidence(policyFile, true) : null;
  const policy = structuredClone(testPolicy ?? policyEvidence.value);
  validatePolicy(policy);
  const policySha = policyEvidence?.sha256 ?? sha(JSON.stringify(policy));
  const inputs = [];
  function input(file, json = false) { const value = evidence(file, json); inputs.push(value); return value; }
  const request = input(path.join(artifact, 'request.json'), true).value;
  const persisted = input(path.join(artifact, 'identity.json'), true).value;
  const metadata = input(path.join(artifact, 'metadata.json'), true);
  const lock = input(options.lockfile ?? defaultLockfile);
  check(request.schema_version === 1 && request.platform === 'linux/amd64' && request.lockfile_sha256 === lock.sha256, 'wrapper request or lockfile identity mismatch');
  const validateOptions = { layout, revision: request.source_revision, contextSha256: request.context_sha256,
    sourceDirty: request.source_dirty, lockfile: lock.filename, metadata: metadata.filename };
  const identity = await validateImage(validateOptions);
  check(isDeepStrictEqual(persisted, identity), 'persisted image identity differs from recomputed identity');
  check(identity.index_digest === options.imageDigest, 'requested index digest does not match the verified image');
  const config = input(path.join(layout, 'blobs/sha256', identity.config_digest.slice(7)), true);
  check(`sha256:${config.sha256}` === identity.config_digest, 'runtime config digest changed');
  runtimeConfig(config.value.config);
  const binary = input(scanner);
  check(policy.scanner.accepted_executable_sha256.includes(binary.sha256), 'scanner executable hash is not allowed by policy');
  const database = input(path.join(cacheDir, 'db/trivy.db'));
  check(database.size > 0, 'database must be nonempty');
  const dbMetadata = input(path.join(cacheDir, 'db/metadata.json'), true);
  freshDatabase(dbMetadata.value, policy.database, now());
  fs.mkdirSync(output, { mode: 0o700 });
  const log = fs.openSync(path.join(output, 'scan.log'), 'wx', 0o600);
  const scanFile = path.join(output, 'trivy.json');
  try {
    const env = { PATH: '/usr/bin:/bin', HOME: output, TMPDIR: output, LC_ALL: 'C' };
    const version = spawnSync(scanner, ['--config', '/dev/null', '--version'], {
      cwd: output, env, timeout: 5000, maxBuffer: 1024 * 1024, stdio: ['ignore', 'pipe', log],
    });
    check(!version.error && version.status === 0 && version.stdout?.toString().trim() === `Version: ${policy.scanner.version}`, 'scanner version check failed');
    assertUnchanged(binary);
    const args = ['image', '--config', '/dev/null', '--cache-dir', cacheDir, '--input', layout, '--platform', 'linux/amd64',
      '--scanners', 'vuln', '--pkg-types', 'os,library', '--offline-scan', '--skip-db-update', '--skip-java-db-update',
      '--skip-check-update', '--skip-vex-repo-update', '--disable-telemetry', '--skip-version-check', '--ignorefile', '/dev/null',
      '--show-suppressed', '--format', 'json', '--list-all-pkgs=false', '--severity', severities.join(','),
      '--output', scanFile, '--exit-code', '1', '--no-progress', '--timeout', '10m'];
    const execution = spawnSync(scanner, args, { cwd: output, env, timeout: timeoutMs, killSignal: 'SIGKILL', stdio: ['ignore', log, log] });
    check(!execution.error && [0, 1].includes(execution.status), 'scanner failed or timed out');
    checkedPath(scanFile);
    fs.chmodSync(scanFile, 0o600);
    const scan = evidence(scanFile, true);
    const results = scanResults(scan.value, identity.config_digest);
    check(execution.status === (results.total > 0 ? 1 : 0), 'scanner exit status and report findings disagree');
    for (const item of inputs) assertUnchanged(item);
    if (policyEvidence) assertUnchanged(policyEvidence);
    check(isDeepStrictEqual(await validateImage(validateOptions), identity), 'OCI identity changed during scan');
    freshDatabase(dbMetadata.value, policy.database, now());
    check(!policy.deny_severities.some((severity) => results.counts[severity] > 0), 'HIGH or CRITICAL severity findings deny frontend gate');
    assertUnchanged(scan);
    const report = {
      schema_version: 1, status: 'pass', scope: 'frontend_oci', ...identity,
      scanned_at: new Date(now()).toISOString(), policy_sha256: policySha,
      scanner: { name: 'trivy', version: policy.scanner.version, sha256: binary.sha256 },
      database: { version: dbMetadata.value.Version, updated_at: dbMetadata.value.UpdatedAt, next_update: dbMetadata.value.NextUpdate,
        sha256: database.sha256, metadata_sha256: dbMetadata.sha256, max_age_hours: policy.database.max_age_hours },
      scanner_exit: execution.status, scanner_report_sha256: scan.sha256,
      runtime_config_verified: true, counts: results.counts, scanned_results: results.results,
    };
    publish(output, report);
    return report;
  } finally {
    fs.closeSync(log);
    // A failed scan can leave diagnostics, never a newly published PASS report.
    if (fs.existsSync(scanFile)) { checkedPath(scanFile); fs.chmodSync(scanFile, 0o600); }
  }
}

export function parseArguments(args) {
  const names = new Map([['--artifact', 'artifact'], ['--image-digest', 'imageDigest'], ['--cache-dir', 'cacheDir'],
    ['--scanner', 'scanner'], ['--output', 'output'], ['--lockfile', 'lockfile']]);
  const options = {};
  for (let index = 0; index < args.length; index += 2) {
    const name = names.get(args[index]);
    check(name && !Object.hasOwn(options, name) && typeof args[index + 1] === 'string'
      && !args[index + 1].startsWith('--'), 'unknown, duplicate or incomplete CLI argument');
    options[name] = args[index + 1];
  }
  for (const name of ['artifact', 'imageDigest', 'cacheDir', 'scanner', 'output']) check(options[name], `missing required argument: ${name}`);
  return options;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.umask(0o077);
  try {
    const options = parseArguments(process.argv.slice(2));
    const report = await scanImage(options);
    process.stdout.write(`${JSON.stringify({ status: report.status, index_digest: report.index_digest, counts: report.counts, report: path.join(options.output, 'report.json') })}\n`);
  } catch (error) {
    process.stderr.write(`frontend scan gate failed: ${error.code ?? error.message}\n`);
    process.exitCode = 1;
  }
}
