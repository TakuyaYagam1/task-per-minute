#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { isDeepStrictEqual } from 'node:util';
import { validateImage } from './validate-frontend-image.mjs';

const check = (condition, message) => { if (!condition) throw new Error(message); };
const record = (value) => value !== null && typeof value === 'object' && !Array.isArray(value);
const hex = (value, length) => typeof value === 'string' && value.length === length && /^[a-f0-9]+$/.test(value);
const sha256 = (bytes) => createHash('sha256').update(bytes).digest('hex');
const stamp = (stat) => [stat.dev, stat.ino, stat.size, stat.mtimeNs, stat.ctimeNs].join(':');

function repositoryName(value) {
  check(typeof value === 'string' && value.length <= 140 && !/\s/.test(value)
    && /^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,38})\/[a-zA-Z0-9_.-]{1,100}$/.test(value)
    && !['.', '..'].includes(value.split('/')[1]), 'repository must be an exact owner/repository name');
  return value;
}

function absolutePath(value) {
  check(typeof value === 'string' && path.isAbsolute(value) && !/[\x00-\x1f\x7f\\]/.test(value)
    && !value.split('/').includes('..'), 'an absolute path without control characters or traversal is required');
  return path.resolve(value);
}

function checkedPath(value, directory = false) {
  const absolute = absolutePath(value);
  let current = path.parse(absolute).root;
  for (const part of absolute.slice(current.length).split('/').filter(Boolean)) {
    current = path.join(current, part);
    const stat = fs.lstatSync(current);
    check(!stat.isSymbolicLink(), 'symlink paths are not allowed');
    check(current === absolute && !directory ? stat.isFile() : stat.isDirectory(), 'unexpected filesystem object');
  }
  return absolute;
}

function readEvidence(filename, json = true) {
  const absolute = checkedPath(filename);
  const before = fs.lstatSync(absolute, { bigint: true });
  check(before.size <= 32n * 1024n * 1024n, 'input exceeds bounded JSON/lockfile size');
  const fd = fs.openSync(absolute, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW | fs.constants.O_NONBLOCK);
  try {
    const opened = fs.fstatSync(fd, { bigint: true });
    check(opened.isFile() && stamp(opened) === stamp(before), 'input changed while opening');
    const bytes = Buffer.alloc(Number(opened.size));
    let read = 0;
    while (read < bytes.length) {
      const count = fs.readSync(fd, bytes, read, bytes.length - read, null);
      check(count > 0, 'input shortened while reading');
      read += count;
    }
    checkedPath(absolute);
    check(stamp(opened) === stamp(fs.fstatSync(fd, { bigint: true }))
      && stamp(opened) === stamp(fs.lstatSync(absolute, { bigint: true })), 'input changed while reading');
    let value;
    if (json) {
      try { value = JSON.parse(bytes.toString('utf8')); } catch { throw new Error('invalid JSON document'); }
      check(record(value), 'JSON document must be an object');
    }
    return { filename: absolute, sha256: sha256(bytes), stamp: stamp(opened), value };
  } finally { fs.closeSync(fd); }
}

/** Validate trusted-run metadata before permitting an artifact download. */
export function validateBuildRun(metadata, { repository, runId, revision }) {
  repositoryName(repository);
  check(hex(revision, 40), 'source revision must be 40 lowercase hex characters');
  check(typeof runId === 'string' && runId.length <= 16 && /^[1-9][0-9]*$/.test(runId)
    && Number.isSafeInteger(Number(runId)) && String(Number(runId)) === runId, 'build run ID must be a canonical positive safe integer');
  check(record(metadata) && Number.isSafeInteger(metadata.id) && String(metadata.id) === runId, 'build run ID mismatch');
  check(metadata.repository?.full_name === repository && metadata.head_repository?.full_name === repository, 'build run repository mismatch');
  check(['push', 'workflow_dispatch'].includes(metadata.event), 'build run event is not trusted');
  check(metadata.head_branch === 'main' && metadata.path === '.github/workflows/pipeline.yml', 'build run must use the main pipeline');
  check(metadata.status === 'completed' && metadata.conclusion === 'success', 'build run must have completed successfully');
  check(metadata.head_sha === revision, 'build run head SHA does not match the requested source revision');
  return { status: 'pass', repository, build_run_id: runId, source_revision: revision, artifact_name: `frontend-image-${revision}` };
}

function githubOutputs(filename, fields) {
  check(typeof process.env.GITHUB_OUTPUT === 'string' && filename === process.env.GITHUB_OUTPUT,
    'GitHub output must be the runner-provided environment file');
  const absolute = checkedPath(filename);
  const before = fs.lstatSync(absolute, { bigint: true });
  const contents = Object.entries(fields).map(([key, value]) => {
    check(/^[a-z_]+$/.test(key) && typeof value === 'string' && !/[\x00-\x1f\x7f]/.test(value), 'invalid GitHub output value');
    return `${key}=${value}\n`;
  }).join('');
  const fd = fs.openSync(absolute, fs.constants.O_WRONLY | fs.constants.O_APPEND | fs.constants.O_NOFOLLOW | fs.constants.O_NONBLOCK);
  try {
    const opened = fs.fstatSync(fd, { bigint: true });
    check(opened.isFile() && stamp(before) === stamp(opened), 'GitHub output changed while opening');
    fs.writeFileSync(fd, contents);
  } finally { fs.closeSync(fd); }
}

/** Recompute OCI identity using explicit dispatch inputs, never request commands.
 * This is preparation, not a signature verification or publication decision. */
export async function preparePublication({ artifact, repository, revision, imageDigest, lockfile, githubOutput }) {
  repositoryName(repository);
  check(hex(revision, 40), 'source revision must be 40 lowercase hex characters');
  check(typeof imageDigest === 'string' && imageDigest.length === 71 && imageDigest.startsWith('sha256:')
    && hex(imageDigest.slice(7), 64), 'image digest must be a canonical SHA256 index digest');
  const root = checkedPath(artifact, true);
  const request = readEvidence(path.join(root, 'request.json'));
  const persisted = readEvidence(path.join(root, 'identity.json'));
  const metadata = readEvidence(path.join(root, 'metadata.json'));
  const lock = readEvidence(lockfile, false);
  check(request.value.schema_version === 1 && request.value.platform === 'linux/amd64', 'invalid build request schema or platform');
  check(request.value.source_revision === revision && request.value.source_dirty === false, 'build request revision must match and source must be clean');
  check(request.value.lockfile_sha256 === lock.sha256 && hex(request.value.context_sha256, 64), 'build request lockfile/context identity mismatch');
  const identity = await validateImage({
    layout: path.join(root, 'image'), revision, sourceDirty: false,
    lockfile: lock.filename, contextSha256: request.value.context_sha256, metadata: metadata.filename,
  });
  check(isDeepStrictEqual(persisted.value, identity), 'persisted identity differs from recomputed OCI identity');
  check(identity.index_digest === imageDigest, 'requested digest does not match the verified published index');
  const index = readEvidence(path.join(root, 'image', 'blobs', 'sha256', imageDigest.slice(7)));
  check(`sha256:${index.sha256}` === imageDigest && index.value.schemaVersion === 2 && Array.isArray(index.value.manifests),
    'published index blob must exist and match the requested digest');
  for (const before of [request, persisted, metadata, lock, index]) {
    const after = readEvidence(before.filename, false);
    check(after.stamp === before.stamp && after.sha256 === before.sha256, 'publication input changed during validation');
  }
  const result = {
    status: 'pass', source_revision: revision, image_digest: imageDigest,
    image_ref: `ghcr.io/${repository.split('/')[0].toLowerCase()}/task-per-minute-frontend@${imageDigest}`,
    index_blob: index.filename,
  };
  if (githubOutput !== undefined) githubOutputs(githubOutput, { image_ref: result.image_ref, index_blob: result.index_blob });
  return result;
}

function argumentsFor(args) {
  const mode = args.shift();
  const names = mode === 'validate-run'
    ? new Map([['--metadata', 'metadata'], ['--repository', 'repository'], ['--run-id', 'runId'], ['--revision', 'revision']])
    : new Map([['--artifact', 'artifact'], ['--repository', 'repository'], ['--revision', 'revision'],
      ['--digest', 'imageDigest'], ['--lockfile', 'lockfile'], ['--github-output', 'githubOutput']]);
  check(['validate-run', 'prepare'].includes(mode), 'expected validate-run or prepare command');
  const options = {};
  for (let index = 0; index < args.length; index += 2) {
    const name = names.get(args[index]);
    check(name && !Object.hasOwn(options, name) && typeof args[index + 1] === 'string' && args[index + 1].length > 0
      && !args[index + 1].startsWith('--'), 'unknown, duplicate or incomplete argument');
    options[name] = args[index + 1];
  }
  for (const name of names.values()) if (name !== 'githubOutput') check(options[name] !== undefined, `missing required argument: ${name}`);
  return { mode, options };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const { mode, options } = argumentsFor(process.argv.slice(2));
    const result = mode === 'validate-run' ? validateBuildRun(readEvidence(options.metadata).value, options) : await preparePublication(options);
    process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
  } catch (error) {
    process.stderr.write(`frontend publication preparation failed: ${error.code ?? error.message}\n`);
    process.exitCode = 1;
  }
}
