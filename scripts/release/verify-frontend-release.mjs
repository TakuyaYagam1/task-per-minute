#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { isDeepStrictEqual } from 'node:util';
import { validateImage } from './validate-frontend-image.mjs';

const defaultLockfile = fileURLToPath(new URL('../../frontend/package-lock.json', import.meta.url));
const defaultPolicy = Object.freeze({ version: 'v3.1.3', acceptedExecutableSha256: Object.freeze([
  '4629c757b7618056f8ddd7e2625ae9fdd94c0372a65049520bc7d9df9efc7f71', // Official Linux amd64 release.
]) });
const issuer = 'https://token.actions.githubusercontent.com';
const maxJsonBytes = 32 * 1024 * 1024;
const maxBundleBytes = 4 * 1024 * 1024;
const maxProcessBytes = 1024 * 1024;
const record = (value) => value !== null && typeof value === 'object' && !Array.isArray(value);
const check = (condition, message) => { if (!condition) throw new Error(message); };
const hex = (value, length) => typeof value === 'string' && value.length === length && /^[a-f0-9]+$/.test(value);

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
  check(directory || absolute !== path.parse(absolute).root, 'input must be a regular file');
  return absolute;
}

const stamp = (stat) => [stat.dev, stat.ino, stat.size, stat.mtimeNs, stat.ctimeNs].join(':');

function evidence(filename, json = false, maxBytes = maxJsonBytes) {
  const absolute = checkedPath(filename);
  const before = fs.lstatSync(absolute, { bigint: true });
  const fd = fs.openSync(absolute, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW | fs.constants.O_NONBLOCK);
  try {
    const opened = fs.fstatSync(fd, { bigint: true });
    check(opened.isFile() && stamp(before) === stamp(opened), 'input changed while opening');
    check(!json || opened.size <= BigInt(maxBytes), 'JSON exceeds bounded size limit');
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
    return { filename: absolute, sha256: hash.digest('hex'), stamp: stamp(opened), value };
  } finally { fs.closeSync(fd); }
}

function assertUnchanged(before) {
  const after = evidence(before.filename);
  check(before.sha256 === after.sha256 && before.stamp === after.stamp, 'input hash or file identity changed during verification');
}

function base64(value) {
  check(typeof value === 'string' && value.length > 0, 'bundle requires nonempty base64 bytes');
  const bytes = Buffer.from(value, 'base64');
  check(bytes.length > 0 && bytes.toString('base64') === value, 'bundle requires canonical base64 bytes');
  return bytes;
}

function validateBundle(bundle, indexDigest) {
  check(['application/vnd.dev.sigstore.bundle.v0.3+json', 'application/vnd.dev.sigstore.bundle+json;version=0.3']
    .includes(bundle.mediaType), 'only Sigstore v0.3 bundles are supported');
  check(record(bundle.messageSignature) && bundle.dsseEnvelope === undefined, 'bundle must contain only a message signature');
  const message = bundle.messageSignature;
  check(message.messageDigest?.algorithm === 'SHA2_256', 'bundle message digest must use SHA2_256');
  check(base64(message.messageDigest.digest).toString('hex') === indexDigest.slice(7), 'bundle message digest does not match the OCI index');
  base64(message.signature);
  const material = bundle.verificationMaterial;
  check(record(material) && record(material.certificate) && material.publicKey === undefined
    && material.x509CertificateChain === undefined, 'bundle requires a single keyless certificate');
  base64(material.certificate.rawBytes);
  check(Array.isArray(material.tlogEntries) && material.tlogEntries.length > 0 && material.tlogEntries.length <= 32
    && material.tlogEntries.every((entry) => record(entry) && record(entry.inclusionProof)), 'bundle requires transparency log inclusion evidence');
  // Structural checks are not signature verification. Cosign validates the
  // certificate, signature, claims, SCT and transparency evidence itself.
}

function newOutput(value, layout) {
  const output = absolutePath(value);
  check(output !== layout && !output.startsWith(`${layout}/`), 'output must not overlap OCI inputs');
  checkedPath(path.dirname(output), true);
  try { fs.lstatSync(output); throw new Error('output already exists; refusing overwrite'); } catch (error) { if (error.code !== 'ENOENT') throw error; }
  return output;
}

function execute(run, binary, args, scratch, timeout, description) {
  let result;
  try {
    result = run(binary, args, { cwd: scratch, env: { HOME: scratch, TMPDIR: scratch, PATH: '/usr/bin:/bin' },
      shell: false, timeout, killSignal: 'SIGKILL', maxBuffer: maxProcessBytes, stdio: ['ignore', 'pipe', 'pipe'] });
  } catch { throw new Error(`${description} failed`); }
  const bounded = (value) => value === undefined || value === null
    || ((typeof value === 'string' || Buffer.isBuffer(value)) && Buffer.byteLength(value) <= maxProcessBytes);
  check(result && !result.error && !result.signal && result.status === 0
    && bounded(result.stdout) && bounded(result.stderr), `${description} failed, timed out or exceeded output limit`);
  return result;
}

/** Test-only runner/policy injection exercises orchestration, not cryptography.
 * Injected runs are labeled synthetic and never set signature_verified=true.
 * The CLI has no overrides and executes only the pinned Cosign binary. */
export async function verifyRelease(options, { run = spawnSync, policy: testPolicy } = {}) {
  const { repository, revision, imageRef } = options;
  check(typeof repository === 'string' && repository.length <= 140 && !/\s/.test(repository)
    && /^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,37}[a-zA-Z0-9])?\/[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,99}$/.test(repository), 'invalid GitHub repository');
  check(hex(revision, 40), 'revision must be 40 lowercase hex characters');
  const prefix = `ghcr.io/${repository.split('/')[0].toLowerCase()}/task-per-minute-frontend@sha256:`;
  check(typeof imageRef === 'string' && imageRef.startsWith(prefix) && hex(imageRef.slice(prefix.length), 64), 'image reference must be the canonical frontend index target');
  const expectedIndex = `sha256:${imageRef.slice(prefix.length)}`;
  const certificateIdentity = `https://github.com/${repository}/.github/workflows/reusable-sign-tournament-images.yml@refs/heads/main`;
  const artifact = checkedPath(options.artifact, true);
  const layout = checkedPath(path.join(artifact, 'image'), true);
  const output = newOutput(options.output, layout);
  const parent = path.dirname(output);
  const parentStat = fs.statSync(parent, { bigint: true });
  const inputs = [];
  function input(file, json = false, maxBytes) { const value = evidence(file, json, maxBytes); inputs.push(value); return value; }
  const request = input(path.join(artifact, 'request.json'), true).value;
  const persisted = input(path.join(artifact, 'identity.json'), true).value;
  const metadata = input(path.join(artifact, 'metadata.json'), true);
  const lock = input(options.lockfile ?? defaultLockfile);
  check(request.schema_version === 1 && request.platform === 'linux/amd64' && request.lockfile_sha256 === lock.sha256, 'wrapper request or lockfile identity mismatch');
  check(request.source_revision === revision, 'wrapper source revision does not match expected revision');
  check(request.source_dirty === false, 'release requires clean source, not dirty or unspecified source');
  input(path.join(layout, 'index.json'), true);
  input(path.join(layout, 'oci-layout'), true);
  const validation = { layout, revision, contextSha256: request.context_sha256, sourceDirty: false,
    lockfile: lock.filename, metadata: metadata.filename };
  const identity = await validateImage(validation);
  check(isDeepStrictEqual(persisted, identity), 'persisted image identity differs from recomputed identity');
  check(identity.index_digest === expectedIndex, 'requested index does not match the verified image');
  const index = input(path.join(layout, 'blobs/sha256', expectedIndex.slice(7)), true);
  check(`sha256:${index.sha256}` === expectedIndex, 'index blob digest mismatch');
  const bundle = input(options.bundle, true, maxBundleBytes);
  validateBundle(bundle.value, expectedIndex);
  const binary = input(options.cosign);
  fs.accessSync(binary.filename, fs.constants.X_OK);
  const policy = structuredClone(testPolicy ?? defaultPolicy);
  check(policy.version === 'v3.1.3' && Array.isArray(policy.acceptedExecutableSha256)
    && policy.acceptedExecutableSha256.length > 0 && policy.acceptedExecutableSha256.every((value) => hex(value, 64)), 'invalid Cosign policy');
  check(policy.acceptedExecutableSha256.includes(binary.sha256), 'Cosign executable hash is not allowed by policy');
  const scratch = fs.mkdtempSync(path.join(parent, '.frontend-release-'));
  try {
    const version = execute(run, binary.filename, ['version', '--json'], scratch, 5000, 'Cosign version check');
    let versionValue;
    try { versionValue = JSON.parse(version.stdout.toString()); } catch { throw new Error('invalid Cosign version JSON'); }
    check(versionValue?.gitVersion === policy.version, 'Cosign version mismatch');
    assertUnchanged(binary);
    const args = ['verify-blob', '--bundle', bundle.filename, '--certificate-identity', certificateIdentity,
      '--certificate-oidc-issuer', issuer, '--certificate-github-workflow-repository', repository,
      '--certificate-github-workflow-trigger', 'workflow_dispatch', '--certificate-github-workflow-ref', 'refs/heads/main',
      '--timeout', '3m', index.filename];
    execute(run, binary.filename, args, scratch, 180_000, 'Cosign verification');
    for (const item of inputs) assertUnchanged(item);
    check(isDeepStrictEqual(await validateImage(validation), identity), 'OCI identity changed during verification');
    for (const item of inputs) assertUnchanged(item);
    const synthetic = run !== spawnSync || testPolicy !== undefined;
    const report = {
      ...identity, schema_version: 1, status: 'pass', evidence_type: synthetic ? 'synthetic_test' : 'signed_local_oci_index',
      verification_mode: synthetic ? 'synthetic_test' : 'cosign', signature_verified: !synthetic,
      signature_kind: 'sigstore_blob_bundle', image_ref: imageRef, bundle_sha256: bundle.sha256,
      certificate_identity: certificateIdentity, certificate_oidc_issuer: issuer,
      certificate_github_workflow_repository: repository, certificate_github_workflow_trigger: 'workflow_dispatch',
      certificate_github_workflow_ref: 'refs/heads/main', cosign: { version: policy.version, sha256: binary.sha256 },
      publication_verified: false,
    };
    const temporary = path.join(scratch, 'report.json');
    const fd = fs.openSync(temporary, 'wx', 0o600);
    try { fs.writeFileSync(fd, `${JSON.stringify(report, null, 2)}\n`); fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
    newOutput(output, layout);
    const currentParent = fs.statSync(parent, { bigint: true });
    check(parentStat.dev === currentParent.dev && parentStat.ino === currentParent.ino, 'output parent changed');
    // Hard-link publication is atomic and, unlike rename, cannot overwrite.
    fs.linkSync(temporary, output);
    return report;
  } finally { fs.rmSync(scratch, { recursive: true, force: true }); }
}

export function parseArguments(args) {
  const names = new Map([['--artifact', 'artifact'], ['--image-ref', 'imageRef'], ['--repository', 'repository'],
    ['--revision', 'revision'], ['--cosign', 'cosign'], ['--bundle', 'bundle'], ['--output', 'output'], ['--lockfile', 'lockfile']]);
  const options = {};
  for (let index = 0; index < args.length; index += 2) {
    const name = names.get(args[index]);
    check(name && !Object.hasOwn(options, name) && typeof args[index + 1] === 'string'
      && !args[index + 1].startsWith('--'), 'unknown, duplicate or incomplete CLI argument');
    options[name] = args[index + 1];
  }
  for (const name of ['artifact', 'imageRef', 'repository', 'revision', 'cosign', 'bundle', 'output']) check(options[name], `missing required argument: ${name}`);
  return options;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.umask(0o077);
  try {
    const options = parseArguments(process.argv.slice(2));
    const report = await verifyRelease(options);
    process.stdout.write(`${JSON.stringify({ status: report.status, signature_verified: report.signature_verified,
      signature_kind: report.signature_kind, index_digest: report.index_digest, report: options.output })}\n`);
  } catch (error) {
    process.stderr.write(`frontend release verification failed: ${error.code ?? error.message}\n`);
    process.exitCode = 1;
  }
}
