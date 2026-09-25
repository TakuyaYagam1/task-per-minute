#!/usr/bin/env node

import { constants } from 'node:fs';
import * as fs from 'node:fs/promises';
import { createHash } from 'node:crypto';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const OCI_INDEX = 'application/vnd.oci.image.index.v1+json';
const DOCKER_INDEX = 'application/vnd.docker.distribution.manifest.list.v2+json';
const OCI_MANIFEST = 'application/vnd.oci.image.manifest.v1+json';
const DOCKER_MANIFEST = 'application/vnd.docker.distribution.manifest.v2+json';
const OCI_ARTIFACT = 'application/vnd.oci.artifact.manifest.v1+json';
const EMPTY_CONFIG = 'application/vnd.oci.empty.v1+json';
const EMPTY_CONFIG_DIGEST = 'sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a';
const IN_TOTO = 'application/vnd.in-toto+json';
const SPDX = 'https://spdx.dev/Document';
const SLSA = 'https://slsa.dev/provenance/v0.2';
const BASE_NAME = 'node:24.21.0-alpine3.23';
const BASE_HASH = '9ec4a2e289874ed0d722e1772ec2de45d2801541db8612f3638b26f128c69ac2';
const BASE_AMD64_HASH = 'a01ebbfa28f5ac85e27044d661b4415d30508b2e64dc23eef236493b7a99f916';
const MAX_JSON_BYTES = 32 * 1024 * 1024;
const MAX_DESCRIPTORS = 1024;
const CONFIG_TYPES = new Set([
  'application/vnd.oci.image.config.v1+json',
  'application/vnd.docker.container.image.v1+json',
  EMPTY_CONFIG,
]);
const LAYER_TYPES = new Set([
  'application/vnd.oci.image.layer.v1.tar',
  'application/vnd.oci.image.layer.v1.tar+gzip',
  'application/vnd.oci.image.layer.v1.tar+zstd',
  'application/vnd.oci.image.layer.nondistributable.v1.tar',
  'application/vnd.oci.image.layer.nondistributable.v1.tar+gzip',
  'application/vnd.oci.image.layer.nondistributable.v1.tar+zstd',
  'application/vnd.docker.image.rootfs.diff.tar.gzip',
]);

function check(condition, message) {
  if (!condition) throw new Error(message);
}

function record(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function text(value) {
  return typeof value === 'string' && value.trim().length > 0;
}

function digest(value) {
  return typeof value === 'string' && value.length === 71 && /^sha256:[a-f0-9]{64}$/.test(value);
}

function inputPath(value) {
  check(text(value) && !value.includes('\0'), 'a nonempty filesystem path is required');
  check(!value.split(/[\\/]/).includes('..'), 'path traversal is not allowed');
  return path.resolve(value);
}

// Reject symlinks in every existing component, not just the blob basename.
async function checkedPath(value, directory = false) {
  const absolute = inputPath(value);
  const root = path.parse(absolute).root;
  const components = absolute.slice(root.length).split(path.sep).filter(Boolean);
  let current = root;
  let stat = await fs.lstat(root);
  for (let index = 0; index < components.length; index += 1) {
    current = path.join(current, components[index]);
    stat = await fs.lstat(current);
    check(!stat.isSymbolicLink(), 'symlink paths are not allowed');
    if (index < components.length - 1 || directory) {
      check(stat.isDirectory(), 'path component is not a directory');
    }
  }
  check(directory ? stat.isDirectory() : stat.isFile(), 'input must be a regular file or directory of the expected kind');
  return { absolute, stat };
}

function unchanged(before, after) {
  return before.dev === after.dev && before.ino === after.ino && before.size === after.size
    && before.mtimeMs === after.mtimeMs && before.ctimeMs === after.ctimeMs;
}

// Layers and the lockfile are hashed incrementally. Only bounded JSON is kept
// in memory; archives are never extracted, interpreted, or executed.
async function readFileEvidence(filename, { json = false, expected } = {}) {
  const { absolute, stat } = await checkedPath(filename);
  const handle = await fs.open(absolute, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  try {
    const opened = await handle.stat();
    check(opened.isFile() && unchanged(stat, opened), 'input changed while opening');
    if (json) check(opened.size <= MAX_JSON_BYTES, 'JSON exceeds the bounded size limit');
    if (expected) check(opened.size === expected.size, 'descriptor size mismatch');
    const hash = createHash('sha256');
    const chunks = [];
    let size = 0;
    for await (const chunk of handle.createReadStream({ autoClose: false, highWaterMark: 1024 * 1024 })) {
      size += chunk.length;
      check(size <= opened.size, 'input grew while reading');
      hash.update(chunk);
      if (json) chunks.push(chunk);
    }
    const actualDigest = `sha256:${hash.digest('hex')}`;
    check(size === opened.size && unchanged(opened, await handle.stat()), 'input changed while reading');
    const after = await checkedPath(absolute);
    check(unchanged(opened, after.stat), 'input path changed while reading');
    if (expected) check(actualDigest === expected.digest, 'descriptor SHA256 mismatch');
    let value;
    if (json) {
      try {
        value = JSON.parse(Buffer.concat(chunks).toString('utf8'));
      } catch {
        throw new Error('invalid JSON document');
      }
      check(record(value), 'JSON document must be an object');
    }
    return { digest: actualDigest, size, value };
  } finally {
    await handle.close();
  }
}

function descriptor(value) {
  check(record(value) && digest(value.digest), 'descriptor requires a canonical SHA256 digest');
  check(Number.isSafeInteger(value.size) && value.size >= 0, 'descriptor requires a nonnegative safe size');
  check(text(value.mediaType), 'descriptor mediaType is required');
  check(value.urls === undefined || (Array.isArray(value.urls) && value.urls.length === 0), 'external descriptor URLs are not supported');
  // BuildKit's OCI artifact config embeds exactly {}. Still require and hash
  // the local blob; inline data never substitutes for descriptor verification.
  check(value.data === undefined || (value.mediaType === EMPTY_CONFIG
    && value.digest === EMPTY_CONFIG_DIGEST && value.size === 2 && value.data === 'e30='),
  'inline descriptor data must be the exact empty OCI config');
  if (value.annotations !== undefined) check(record(value.annotations), 'invalid descriptor annotations');
  return value;
}

function list(value, name, nonempty = false) {
  check(Array.isArray(value) && value.length <= MAX_DESCRIPTORS && (!nonempty || value.length > 0), `invalid ${name} list`);
  return value;
}

function matchingStatement(statement, runtimeDigest, boundArtifactSubject = false) {
  check(['https://in-toto.io/Statement/v0.1', 'https://in-toto.io/Statement/v1'].includes(statement._type), 'unsupported in-toto statement version');
  const subjects = list(statement.subject, 'statement subject', !boundArtifactSubject);
  check(subjects.every((subject) => record(subject) && text(subject.name)
    && record(subject.digest) && subject.digest.sha256 === runtimeDigest.slice(7)), 'attestation subject does not match the runtime manifest digest');
  check(record(statement.predicate) && text(statement.predicateType), 'attestation predicate is required');
}

function validateSpdx(predicate) {
  check(/^SPDX-2\.\d+$/.test(predicate.spdxVersion), 'SPDX version is required');
  check(predicate.dataLicense === 'CC0-1.0', 'SPDX document license must be CC0-1.0');
  check(text(predicate.documentNamespace) && /^https?:\/\/\S+$/.test(predicate.documentNamespace), 'SPDX document namespace is required');
  check(Array.isArray(predicate.packages) && predicate.packages.length > 0
    && predicate.packages.every((item) => record(item) && text(item.name)), 'SPDX must contain nonempty packages');
}

function baseMaterial(material) {
  if (!record(material) || !text(material.uri) || !record(material.digest)
    || /\s/.test(material.uri)
    || ![BASE_HASH, BASE_AMD64_HASH].includes(material.digest.sha256)) return false;
  const [identity, qualifiers, extra] = material.uri.split('?');
  if (extra !== undefined) return false;
  if (qualifiers !== undefined) {
    const parameters = new URLSearchParams(qualifiers);
    const platforms = parameters.getAll('platform');
    if (platforms.length > 1 || (platforms.length === 1 && platforms[0] !== 'linux/amd64')) return false;
  }
  if (/^pkg:docker\/(?:docker\.io\/library\/|index\.docker\.io\/library\/|library\/)?node@24\.21\.0-alpine3\.23$/.test(identity)) return true;
  const reference = identity.replace(/^docker-image:\/\//, '');
  return [BASE_NAME, `docker.io/library/${BASE_NAME}`, `index.docker.io/library/${BASE_NAME}`]
    .some((name) => reference === name || reference === `${name}@sha256:${material.digest.sha256}`);
}

function validateProvenance(predicate, expected) {
  check(predicate.buildType === 'https://mobyproject.org/buildkit@v1', 'provenance must describe a BuildKit v1 build');
  const args = predicate.invocation?.parameters?.args;
  check(record(args), 'provenance invocation build arguments are required');
  for (const [name, value] of Object.entries(expected)) {
    check(args[`build-arg:${name}`] === value, `provenance build-arg:${name} mismatch`);
  }
  check(Array.isArray(predicate.materials) && predicate.materials.some(baseMaterial), 'provenance lacks the pinned Node base material');
  check(predicate.materials.some((material) => record(material) && typeof material.uri === 'string'
    && !/\s/.test(material.uri)
    && /^http:\/\/buildkit-session\/[^\s?#]+$/.test(material.uri)
    && record(material.digest) && material.digest.sha256 === expected.CONTEXT_SHA256),
  'provenance lacks the exact BuildKit tar context material digest');
}

async function outputPath(output) {
  const absolute = inputPath(output);
  await checkedPath(path.dirname(absolute), true);
  try {
    await fs.lstat(absolute);
  } catch (error) {
    if (error.code === 'ENOENT') return absolute;
    throw error;
  }
  throw new Error('output already exists; refusing file or symlink overwrite');
}

async function publishReport(output, report) {
  const absolute = await outputPath(output);
  const parent = path.dirname(absolute);
  const scratch = await fs.mkdtemp(path.join(parent, '.frontend-image-report-'));
  const temporary = path.join(scratch, 'report.json');
  try {
    const handle = await fs.open(temporary, 'wx', 0o600);
    try {
      await handle.writeFile(`${JSON.stringify(report, null, 2)}\n`);
      await handle.sync();
    } finally {
      await handle.close();
    }
    await checkedPath(parent, true);
    // link is atomic and fails with EEXIST, unlike rename which can overwrite.
    await fs.link(temporary, absolute);
  } finally {
    await fs.unlink(temporary).catch((error) => { if (error.code !== 'ENOENT') throw error; });
    await fs.rmdir(scratch);
  }
}

/** Validate local unsigned BuildKit evidence. This does not verify source
 * contents, signatures, builder trust, vulnerability status, or a SLSA level. */
export async function validateImage({ layout, revision, lockfile, contextSha256, sourceDirty, metadata, output }) {
  check(typeof revision === 'string' && revision.length === 40 && /^[a-f0-9]{40}$/.test(revision), 'revision must be 40 lowercase hex characters');
  check(typeof contextSha256 === 'string' && contextSha256.length === 64 && /^[a-f0-9]{64}$/.test(contextSha256), 'context SHA256 must be 64 lowercase hex characters');
  check(typeof sourceDirty === 'boolean', 'sourceDirty must be boolean');
  if (output !== undefined) await outputPath(output);
  const root = (await checkedPath(layout, true)).absolute;
  const layoutVersion = await readFileEvidence(path.join(root, 'oci-layout'), { json: true });
  check(layoutVersion.value.imageLayoutVersion === '1.0.0', 'unsupported OCI layout version');
  const lock = await readFileEvidence(lockfile);
  const lockHash = lock.digest.slice(7);
  const expected = {
    SOURCE_REVISION: revision, SOURCE_DIRTY: String(sourceDirty),
    LOCKFILE_SHA256: lockHash, CONTEXT_SHA256: contextSha256,
  };
  const layoutIndex = await readFileEvidence(path.join(root, 'index.json'), { json: true });
  const indexes = [];
  const manifests = [];
  const seenManifests = new Set();
  let descriptorCount = 0;
  async function blob(raw, json = false) {
    const item = descriptor(raw);
    descriptorCount += 1;
    check(descriptorCount <= MAX_DESCRIPTORS, 'OCI descriptor count exceeds the limit');
    return readFileEvidence(path.join(root, 'blobs', 'sha256', item.digest.slice(7)), { json, expected: item });
  }
  async function walkIndex(index, indexDigest, depth, ancestors = new Set()) {
    check(depth <= 8 && !ancestors.has(indexDigest), 'OCI index nesting exceeds the limit or contains a cycle');
    check(index.schemaVersion === 2 && (index.mediaType === undefined || [OCI_INDEX, DOCKER_INDEX].includes(index.mediaType)), 'unsupported OCI index');
    const current = { digest: indexDigest, depth, manifests: new Set() };
    indexes.push(current);
    const nextAncestors = new Set([...ancestors, indexDigest]);
    for (const item of list(index.manifests, 'index manifests', true)) {
      const data = await blob(item, true);
      if ([OCI_INDEX, DOCKER_INDEX].includes(item.mediaType)) {
        check(data.value.mediaType === undefined || data.value.mediaType === item.mediaType, 'index descriptor mediaType mismatch');
        const children = await walkIndex(data.value, item.digest, depth + 1, nextAncestors);
        for (const child of children) current.manifests.add(child);
      } else {
        check([OCI_MANIFEST, DOCKER_MANIFEST, OCI_ARTIFACT].includes(item.mediaType), 'unsupported manifest mediaType');
        check(!seenManifests.has(item.digest), 'duplicate manifest reference is ambiguous');
        seenManifests.add(item.digest);
        check(data.value.mediaType === item.mediaType, 'manifest descriptor mediaType mismatch');
        check(data.value.schemaVersion === 2 || (item.mediaType === OCI_ARTIFACT && data.value.schemaVersion === undefined), 'unsupported manifest schema version');
        manifests.push({ descriptor: item, value: data.value });
        current.manifests.add(item.digest);
      }
    }
    return current.manifests;
  }
  await walkIndex(layoutIndex.value, layoutIndex.digest, 0);

  const images = [];
  const attestations = [];
  for (const item of manifests) {
    const { value, descriptor: manifestDescriptor } = item;
    const annotations = [manifestDescriptor.annotations ?? {}, value.annotations ?? {}];
    check(annotations.every(record), 'invalid manifest annotations');
    const legacy = annotations.some((entry) => entry['vnd.docker.reference.type'] !== undefined
      || entry['vnd.docker.reference.digest'] !== undefined);
    const attestation = legacy || value.subject !== undefined || value.artifactType !== undefined || manifestDescriptor.mediaType === OCI_ARTIFACT;
    if (value.artifactType !== undefined) {
      check(['application/vnd.docker.attestation.manifest.v1+json', IN_TOTO].includes(value.artifactType), 'unsupported attestation artifactType');
    }
    if (manifestDescriptor.mediaType !== OCI_ARTIFACT) {
      check(CONFIG_TYPES.has(value.config?.mediaType), 'unsupported image config mediaType');
      item.config = await blob(value.config, true);
    }
    item.layers = list(manifestDescriptor.mediaType === OCI_ARTIFACT ? value.blobs : value.layers, 'manifest layers', attestation);
    if (attestation) {
      item.bindings = [];
      for (const entry of annotations) {
        if (entry['vnd.docker.reference.type'] !== undefined || entry['vnd.docker.reference.digest'] !== undefined) {
          check(entry['vnd.docker.reference.type'] === 'attestation-manifest'
            && digest(entry['vnd.docker.reference.digest']), 'invalid legacy attestation binding');
          item.bindings.push(entry['vnd.docker.reference.digest']);
        }
      }
      if (value.subject !== undefined) {
        await blob(value.subject, true);
        check([OCI_MANIFEST, DOCKER_MANIFEST].includes(value.subject.mediaType), 'attestation subject must be an image manifest');
        item.bindings.push(value.subject.digest);
      }
      check(item.bindings.length > 0, 'attestation manifest has no runtime subject binding');
      attestations.push(item);
    } else {
      check(item.config?.value.os === 'linux' && item.config.value.architecture === 'amd64', 'runtime image must be linux/amd64');
      if (manifestDescriptor.platform !== undefined) {
        check(record(manifestDescriptor.platform) && manifestDescriptor.platform.os === 'linux'
          && manifestDescriptor.platform.architecture === 'amd64'
          && !manifestDescriptor.platform.variant, 'runtime descriptor platform mismatch');
      }
      for (const layer of item.layers) {
        check(LAYER_TYPES.has(layer.mediaType), 'unsupported runtime layer mediaType');
        await blob(layer);
      }
      images.push(item);
    }
  }
  check(images.length === 1, 'exactly one linux/amd64 runtime image is required');
  const image = images[0];
  const imageDigest = image.descriptor.digest;
  const labels = image.config.value.config?.Labels;
  check(record(labels), 'runtime image labels are required');
  const requiredLabels = {
    'org.opencontainers.image.revision': revision,
    'io.task-per-minute.source.dirty': String(sourceDirty),
    'io.task-per-minute.lockfile.sha256': lockHash,
    'io.task-per-minute.context.sha256': contextSha256,
    'org.opencontainers.image.base.name': `docker.io/library/${BASE_NAME}`,
    'org.opencontainers.image.base.digest': `sha256:${BASE_HASH}`,
  };
  for (const [name, value] of Object.entries(requiredLabels)) {
    check(labels[name] === value, `runtime label ${name} mismatch`);
  }
  const sboms = [];
  const provenances = [];
  for (const attestation of attestations) {
    check(attestation.bindings.every((binding) => binding === imageDigest), 'attestation manifest has a stale or foreign runtime subject');
    const subject = attestation.value.subject;
    if (subject !== undefined) {
      check(subject.digest === imageDigest && subject.size === image.descriptor.size
        && subject.mediaType === image.descriptor.mediaType, 'artifact subject must match the exact runtime descriptor');
    }
    // Modern OCI artifacts bind the statement through their manifest subject.
    // Legacy annotations alone cannot authorize an empty statement subject.
    const boundArtifactSubject = subject !== undefined
      && [OCI_MANIFEST, OCI_ARTIFACT].includes(attestation.descriptor.mediaType)
      && attestation.value.artifactType !== undefined;
    for (const layer of attestation.layers) {
      check(layer.mediaType === IN_TOTO, 'attestation layer must be in-toto JSON');
      const statement = (await blob(layer, true)).value;
      matchingStatement(statement, imageDigest, boundArtifactSubject);
      const declared = layer.annotations?.['in-toto.io/predicate-type'];
      check(declared === undefined || declared === statement.predicateType, 'attestation predicate annotation mismatch');
      if (statement.predicateType === SPDX) {
        validateSpdx(statement.predicate);
        sboms.push(layer.digest);
      } else if (statement.predicateType === SLSA) {
        validateProvenance(statement.predicate, expected);
        provenances.push(layer.digest);
      } else {
        throw new Error('unsupported attestation predicate type');
      }
    }
  }
  check(sboms.length === 1, 'exactly one SPDX SBOM statement is required');
  check(provenances.length === 1, 'exactly one SLSA v0.2 provenance statement is required');
  const published = indexes.filter((index) => manifests.every((item) => index.manifests.has(item.descriptor.digest)))
    .sort((left, right) => right.depth - left.depth)[0];
  check(published, 'cannot identify the published image index');
  if (metadata !== undefined) {
    const value = typeof metadata === 'string' ? (await readFileEvidence(metadata, { json: true })).value : metadata;
    check(record(value) && value['containerimage.digest'] === published.digest, 'metadata containerimage.digest does not match the published index');
    if (value['containerimage.config.digest'] !== undefined) {
      check(value['containerimage.config.digest'] === image.value.config.digest, 'metadata config digest mismatch');
    }
  }
  const report = {
    status: 'pass', source_revision: revision, source_dirty: sourceDirty,
    lockfile_sha256: lockHash, context_sha256: contextSha256,
    image_digest: imageDigest, config_digest: image.value.config.digest,
    index_digest: published.digest, layout_index_digest: layoutIndex.digest,
    sbom_digest: sboms[0], provenance_digest: provenances[0], platform: 'linux/amd64',
    evidence_type: 'local_unsigned_build', source_proof: 'declared_build_inputs_only',
  };
  if (output !== undefined) await publishReport(output, report);
  return report;
}

function cliOptions(args) {
  const names = new Map([
    ['--layout', 'layout'], ['--revision', 'revision'], ['--lockfile', 'lockfile'],
    ['--context-sha256', 'contextSha256'], ['--source-dirty', 'sourceDirty'],
    ['--metadata', 'metadata'], ['--output', 'output'],
  ]);
  const options = {};
  for (let index = 0; index < args.length; index += 2) {
    const name = names.get(args[index]);
    check(name && options[name] === undefined && text(args[index + 1])
      && !args[index + 1].startsWith('--'), 'unknown, duplicate, or incomplete CLI argument');
    options[name] = args[index + 1];
  }
  for (const name of ['layout', 'revision', 'lockfile', 'contextSha256', 'sourceDirty', 'output']) {
    check(options[name] !== undefined, `missing required argument: ${name}`);
  }
  check(['true', 'false'].includes(options.sourceDirty), '--source-dirty must be true or false');
  options.sourceDirty = options.sourceDirty === 'true';
  return options;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const report = await validateImage(cliOptions(process.argv.slice(2)));
    process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
  } catch (error) {
    // Filesystem failures may include input paths; emit only the code, not
    // document contents, host paths, stack traces, or inherited environment.
    process.stderr.write(`frontend image validation failed: ${error.code ?? error.message}\n`);
    process.exitCode = 1;
  }
}
