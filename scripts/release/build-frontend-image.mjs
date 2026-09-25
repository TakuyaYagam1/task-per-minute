import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const MAX_BYTES = 256 * 1024 * 1024;
const snapshots = new WeakMap();
const requiredFiles = new Set(['Dockerfile', '.dockerignore', 'package.json', 'package-lock.json']);
const scanner = 'docker.io/docker/buildkit-syft-scanner@sha256:ae4f3b554449e7e25548e7d8ccc029d17357348e30c6e3df01b92bc93654d6a9';
const publicDefaults = Object.freeze({
  NEXT_PUBLIC_API_URL: '', NEXT_PUBLIC_ADMIN_API_URL: '', NEXT_PUBLIC_SOURCE_FILE_ORIGINS: '',
  BACKEND_URL: 'http://backend:8080',
  BACKEND_PORT: '8080', FRONTEND_PORT: '3000',
});
const excludedDirectories = new Set([
  '.git', '.svn', '.hg', '.ssh', '.aws', '.docker', '.codex', '.claude', '.agents',
  'prd', 'private', 'secrets', 'credentials', 'preview', 'node_modules', '.next',
  'e2e', 'fixtures', '__fixture__', '__fixtures__', 'test-fixtures', 'testdata',
  'out', 'dist', 'build', 'coverage', 'test-results', 'playwright-report', 'blob-report',
  '.cache', '.turbo', '.vercel', '.vscode', '.idea', 'logs',
]);

const hash = (bytes, algorithm = 'sha256') => createHash(algorithm).update(bytes).digest('hex');
const blobHash = (bytes) => hash(Buffer.concat([Buffer.from(`blob ${bytes.length}\0`), bytes]), 'sha1');
const ordered = (names) => [...names].sort((a, b) => Buffer.compare(Buffer.from(a), Buffer.from(b)));

function toolEnvironment() {
  return {
    PATH: process.env.PATH || '/usr/bin:/bin', LC_ALL: 'C',
    GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null',
    GIT_OPTIONAL_LOCKS: '0', GIT_TERMINAL_PROMPT: '0',
  };
}

function execute(program, args, options = {}) {
  const { logFd, captureOutput = true, ...childOptions } = options;
  const result = spawnSync(program, args, {
    maxBuffer: MAX_BYTES,
    stdio: captureOutput ? ['pipe', 'pipe', 'pipe'] : ['pipe', logFd, logFd],
    ...childOptions,
  });
  if (captureOutput && logFd !== undefined) {
    if (result.stdout?.length) fs.writeSync(logFd, result.stdout);
    if (result.stderr?.length) fs.writeSync(logFd, result.stderr);
  }
  // Never include child output in an error printed to the terminal.
  if (result.error || result.status !== 0) throw new Error(`${path.basename(program)} command failed (exit ${result.status ?? 'unavailable'})`);
  return result.stdout || Buffer.alloc(0);
}

function git(cwd, args) {
  return execute('git', ['--literal-pathspecs', '-c', 'core.fsmonitor=false', '-c', 'core.hooksPath=/dev/null', ...args], {
    cwd, env: toolEnvironment(), timeout: 30_000,
  });
}

function excluded(name) {
  const parts = name.toLowerCase().split('/');
  return parts.some((part) => excludedDirectories.has(part) || part.startsWith('.env') ||
    /^(?:\.npmrc|\.netrc|\.yarnrc(?:\.yml)?|\.gitignore|\.ds_store|playwright\.config\.[cm]?[jt]s)$/.test(part) ||
    /(?:^|[._-])(?:credentials?|secrets?|flags?)(?:[._-]|$)/.test(part) ||
    /^id_(?:rsa|dsa|ecdsa|ed25519)(?:\.pub)?$/.test(part) ||
    /\.(?:pem|key|crt|cer|p12|pfx|jks|keystore|tsbuildinfo|next)$/.test(part) ||
    /\.log(?:\..*)?$/.test(part));
}

function validName(name) {
  if (!name || path.posix.isAbsolute(name) || /[\0-\x1f\x7f\\\ufffd]/.test(name) ||
      name.split('/').some((part) => !part || part === '.' || part === '..')) {
    throw new Error('invalid tracked source path');
  }
  return name;
}

function ignoreRules(bytes) {
  return bytes.toString('utf8').split(/\r?\n/).flatMap((raw) => {
    let rule = raw.trim();
    if (!rule || rule.startsWith('#') || rule === '.') return [];
    const include = rule.startsWith('!');
    if (include) rule = rule.slice(1);
    rule = rule.replace(/^\/+|\/+$/g, '');
    if (!rule || /[\[\]\\\0]/.test(rule) || rule.split('/').includes('..')) {
      throw new Error('unsupported dockerignore pattern; refusing an ambiguous context');
    }
    let expression = '';
    for (let index = 0; index < rule.length; index++) {
      const char = rule[index];
      if (char === '*' && rule[index + 1] === '*') {
        index++;
        if (rule[index + 1] === '/') { expression += '(?:.*/)?'; index++; }
        else expression += '.*';
      } else if (char === '*') expression += '[^/]*';
      else if (char === '?') expression += '[^/]';
      else expression += char.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    }
    return [{ include, basename: !rule.includes('/'), pattern: new RegExp(`^${expression}$`) }];
  });
}

function selected(name, rules) {
  if (excluded(name)) return false;
  // Docker needs both control files even when COPY excludes them.
  if (requiredFiles.has(name)) return true;
  const parts = name.split('/');
  const prefixes = parts.map((_, index) => parts.slice(0, index + 1).join('/'));
  let ignored = false;
  for (const rule of rules) {
    if ((rule.basename ? parts : prefixes).some((part) => rule.pattern.test(part))) ignored = !rule.include;
  }
  return !ignored;
}

// O_NOFOLLOW on each directory component also prevents parent-symlink reads.
// The descriptor stays open until tar has consumed /proc/<pid>/fd/<fd>.
function openRegular(absolute) {
  const parts = path.resolve(absolute).split('/').filter(Boolean);
  let directory = fs.openSync('/', fs.constants.O_RDONLY | fs.constants.O_DIRECTORY);
  try {
    for (const part of parts.slice(0, -1)) {
      const next = fs.openSync(`/proc/self/fd/${directory}/${part}`,
        fs.constants.O_RDONLY | fs.constants.O_DIRECTORY | fs.constants.O_NOFOLLOW);
      fs.closeSync(directory);
      directory = next;
    }
    const fd = fs.openSync(`/proc/self/fd/${directory}/${parts.at(-1)}`,
      fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW | fs.constants.O_NONBLOCK);
    if (!fs.fstatSync(fd).isFile()) {
      fs.closeSync(fd);
      throw new Error('source is not a regular file');
    }
    return fd;
  } catch (error) {
    if (error.code === 'ENOENT') return null;
    throw new Error('source must be a regular file beneath non-symlink directories');
  } finally {
    fs.closeSync(directory);
  }
}

function readRegular(absolute) {
  const fd = openRegular(absolute);
  if (fd === null) return null;
  try {
    const before = fs.fstatSync(fd, { bigint: true });
    if (before.size > BigInt(MAX_BYTES)) throw new Error('source exceeds context size limit');
    const bytes = fs.readFileSync(fd);
    const after = fs.fstatSync(fd, { bigint: true });
    if (before.size !== after.size || before.mtimeNs !== after.mtimeNs || before.ctimeNs !== after.ctimeNs) {
      throw new Error('source changed while freezing context');
    }
    return { fd, bytes, mode: (after.mode & 0o111n) ? '100755' : '100644' };
  } catch (error) {
    fs.closeSync(fd);
    throw error;
  }
}

function inventory(root, frontendPrefix) {
  const decode = (output, index) => new Map(output.toString('utf8').split('\0').filter(Boolean).map((record) => {
    const match = record.match(index ? /^([0-7]{6}) ([0-9a-f]{40}) ([0-3])\t([\s\S]+)$/ : /^([0-7]{6}) (?:blob|commit) ([0-9a-f]{40})\t([\s\S]+)$/);
    if (!match) throw new Error('unsupported Git source entry');
    const fullName = match[index ? 4 : 3];
    if (!fullName.startsWith(frontendPrefix)) throw new Error('Git path escaped frontend scope');
    return [validName(fullName.slice(frontendPrefix.length)), { mode: match[1], hash: match[2], stage: index ? match[3] : '0' }];
  }));
  return {
    head: decode(git(root, ['ls-tree', '-r', '-z', 'HEAD', '--', frontendPrefix]), false),
    index: decode(git(root, ['ls-files', '--stage', '-z', '--', frontendPrefix]), true),
  };
}

function selectInventory(source, rules) {
  return Object.fromEntries(['head', 'index'].map((kind) => [kind, ordered(source[kind].keys())
    .filter((name) => selected(name, rules)).map((name) => {
      const entry = source[kind].get(name);
      if (!['100644', '100755'].includes(entry.mode) || entry.stage !== '0') {
        throw new Error('selected Git source must be regular and have no symlinks, submodules or conflicts');
      }
      return [name, entry];
    })]));
}

function fileIdentity(name, file) {
  return { path: name, mode: file.mode, sha256: hash(file.bytes), size: file.bytes.length };
}

function verifyTar(tar, files, epoch) {
  let offset = 0;
  let index = 0;
  const stringAt = (header, start, length) => header.subarray(start, start + length).toString('utf8').replace(/\0.*$/s, '');
  while (offset + 512 <= tar.length && tar[offset] !== 0) {
    const header = tar.subarray(offset, offset + 512);
    const prefix = stringAt(header, 345, 155);
    const name = `${prefix ? `${prefix}/` : ''}${stringAt(header, 0, 100)}`;
    const size = parseInt(stringAt(header, 124, 12).trim(), 8);
    const file = files[index++];
    if (!file || name !== file.path || ![0, 48].includes(header[156]) || size !== file.bytes.length ||
        parseInt(stringAt(header, 100, 8).trim(), 8) !== parseInt(file.mode.slice(-3), 8) ||
        parseInt(stringAt(header, 136, 12).trim(), 8) !== epoch ||
        !tar.subarray(offset + 512, offset + 512 + size).equals(file.bytes)) {
      throw new Error('tar differs from frozen source bytes or modes');
    }
    offset += 512 + Math.ceil(size / 512) * 512;
  }
  if (index !== files.length) throw new Error('tar is missing frozen source entries');
}

/** Freeze only tracked, allowed frontend inputs. No files are copied to disk. */
export function prepareContext(frontendRoot, { revision, allowDirty = false } = {}) {
  const frontend = path.resolve(frontendRoot);
  const root = git(frontend, ['rev-parse', '--show-toplevel']).toString().trim();
  const relative = path.relative(root, frontend).split(path.sep).join('/');
  if (!relative || relative.startsWith('../') || path.isAbsolute(relative)) throw new Error('frontend must be inside its Git repository');
  const prefix = `${relative}/`;
  const headRevision = git(root, ['rev-parse', '--verify', 'HEAD']).toString().trim();
  if (!/^[0-9a-f]{40}$/.test(revision ?? headRevision) || (revision && revision !== headRevision)) {
    throw new Error('requested revision must be the exact lowercase HEAD SHA');
  }
  const sourceDateEpoch = Number(git(root, ['show', '-s', '--format=%ct', 'HEAD']).toString().trim());
  if (!Number.isSafeInteger(sourceDateEpoch) || sourceDateEpoch < 0) throw new Error('invalid Git commit timestamp');
  const source = inventory(root, prefix);
  if (!source.index.has('.dockerignore') || !['100644', '100755'].includes(source.index.get('.dockerignore').mode)) {
    throw new Error('tracked regular .dockerignore is required');
  }
  const ignoreFile = readRegular(path.join(frontend, '.dockerignore'));
  if (!ignoreFile) throw new Error('tracked .dockerignore is missing');
  let files = [];
  try {
    const rules = ignoreRules(ignoreFile.bytes);
    const selection = selectInventory(source, rules);
    for (const name of requiredFiles) {
      if (!source.index.has(name)) throw new Error('required build input is not tracked');
    }
    let size = 0;
    let dirty = JSON.stringify(selection.head) !== JSON.stringify(selection.index);
    for (const [name, entry] of selection.index) {
      const file = name === '.dockerignore' ? ignoreFile : readRegular(path.join(frontend, name));
      if (!file) { dirty = true; continue; }
      files.push({ ...file, path: name });
      size += file.bytes.length;
      if (size > MAX_BYTES / 2) throw new Error('context exceeds in-memory size limit');
      const head = source.head.get(name);
      if (!head || head.hash !== blobHash(file.bytes) || head.mode !== file.mode || entry.hash !== head.hash || entry.mode !== head.mode) dirty = true;
    }
    for (const name of requiredFiles) {
      if (!files.some((file) => file.path === name)) throw new Error('required build input is missing');
    }
    if (dirty && !allowDirty) throw new Error('selected frontend context is dirty; use --allow-dirty only for local builds');
    const transforms = files.flatMap((file) => ['--transform',
      `s|^/proc/${process.pid}/fd/${file.fd}$|${file.path.replace(/[\\&|]/g, '\\$&')}|`]);
    const tar = execute('tar', [
      '--create', '--file=-', '--format=ustar', '--owner=0', '--group=0', '--numeric-owner',
      `--mtime=@${sourceDateEpoch}`, '--mode=u=rwX,go=rX', '--dereference', '--hard-dereference',
      '--absolute-names', ...transforms, '--null', '--verbatim-files-from', '--files-from=-',
    ], {
      env: toolEnvironment(), timeout: 30_000,
      input: Buffer.from(files.map((file) => `/proc/${process.pid}/fd/${file.fd}\0`).join('')),
    });
    verifyTar(tar, files, sourceDateEpoch);
    const context = Object.freeze({
      revision: headRevision, sourceDirty: dirty, sourceDateEpoch,
      lockfileSha256: hash(files.find((file) => file.path === 'package-lock.json').bytes),
      contextSha256: hash(tar),
      files: Object.freeze(files.map((file) => Object.freeze(fileIdentity(file.path, file)))),
      get tar() { return Buffer.from(tar); },
    });
    snapshots.set(context, { root, frontend, prefix, rules, selection, tar });
    assertContextUnchanged(context);
    return context;
  } finally {
    const descriptors = new Set([ignoreFile.fd, ...files.map((file) => file.fd)]);
    for (const fd of descriptors) fs.closeSync(fd);
    files = [];
  }
}

/** Recheck both index and working bytes; timestamps alone are not evidence. */
export function assertContextUnchanged(context) {
  const snapshot = snapshots.get(context);
  if (!snapshot) throw new Error('unknown frozen context');
  try {
    if (git(snapshot.root, ['rev-parse', 'HEAD']).toString().trim() !== context.revision ||
        JSON.stringify(selectInventory(inventory(snapshot.root, snapshot.prefix), snapshot.rules)) !== JSON.stringify(snapshot.selection)) {
      throw new Error('Git source changed after freeze');
    }
    for (const [name] of snapshot.selection.index) {
      const file = readRegular(path.join(snapshot.frontend, name));
      const expected = context.files.find((entry) => entry.path === name);
      try {
        if ((!file) !== (!expected) || (file && JSON.stringify(fileIdentity(name, file)) !== JSON.stringify(expected))) {
          throw new Error('source bytes or mode changed after freeze');
        }
      } finally {
        if (file) fs.closeSync(file.fd);
      }
    }
  } catch {
    throw new Error('selected source or index changed after freeze');
  }
}

function localEndpoint(value) {
  if (value === undefined || value === '') return;
  if (!/^unix:\/\/\/[^\0\r\n?#]+$/.test(value) || value.startsWith('unix:////') || value.slice(7).includes('/../')) {
    throw new Error('DOCKER_HOST or builder endpoint must be a local absolute unix socket');
  }
}

function validateOutput(output) {
  if (typeof output !== 'string' || !path.isAbsolute(output) || /[,\0\r\n]/.test(output)) {
    throw new Error('output must be an absolute new directory without exporter delimiters');
  }
  return path.resolve(output);
}

function publicArguments(environment) {
  return Object.fromEntries(Object.entries(publicDefaults).map(([name, fallback]) => {
    const value = environment[name] ?? fallback;
    if (typeof value !== 'string' || /[\0\r\n]/.test(value)) throw new Error('invalid public build argument');
    if (name.endsWith('_PORT')) {
      if (!/^[1-9][0-9]{0,4}$/.test(value) || Number(value) > 65535) throw new Error('public port must be a decimal number from 1 to 65535');
      return [name, value];
    }
    const urls = name === 'NEXT_PUBLIC_SOURCE_FILE_ORIGINS' ? value.split(',') : [value];
    for (const item of urls) {
      if (!item && name !== 'BACKEND_URL') continue;
      if (!/^https?:\/\//i.test(item) || /[\s\\?#\x00-\x1f\x7f]/.test(item) || item.split('/')[2].includes('@')) {
        throw new Error('public build URL requires explicit HTTP(S), without userinfo, whitespace, queries or fragments');
      }
      let url;
      try { url = new URL(item); } catch { throw new Error('public build URL must be absolute HTTP(S)'); }
      const loopback = ['localhost', '[::1]'].includes(url.hostname) || /^127\.(?:[0-9]+\.){2}[0-9]+$/.test(url.hostname);
      if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash ||
          (name === 'NEXT_PUBLIC_SOURCE_FILE_ORIGINS' && url.pathname !== '/') ||
          (name !== 'BACKEND_URL' && url.protocol !== 'https:' && !loopback)) {
        throw new Error('public build URL requires HTTPS except loopback HTTP, without credentials, queries or fragments');
      }
    }
    return [name, value];
  }));
}

function assertBuildOnly(options) {
  if ('pushReference' in options) throw new Error('publication is not supported by this build-only helper');
}

export function createBuildRequest(context, options = {}, environment = {}) {
  assertBuildOnly(options);
  let { output } = options;
  const snapshot = snapshots.get(context);
  if (!snapshot) throw new Error('unknown frozen context');
  output = validateOutput(output);
  localEndpoint(environment.DOCKER_HOST);
  if (environment.DOCKER_CONFIG !== undefined && (!path.isAbsolute(environment.DOCKER_CONFIG) || /[\0\r\n]/.test(environment.DOCKER_CONFIG))) {
    throw new Error('DOCKER_CONFIG must be an explicit absolute directory');
  }
  const metadata = path.join(output, 'metadata.json');
  const args = [
    'buildx', 'build', '--platform', 'linux/amd64', '--provenance=mode=max,version=v0.2',
    '--attest', `type=sbom,generator=${scanner}`,
    '--output', `type=oci,dest=${path.join(output, 'image')},tar=false`, '--metadata-file', metadata,
  ];
  const buildArgs = {
    SOURCE_REVISION: context.revision, SOURCE_DIRTY: String(context.sourceDirty),
    LOCKFILE_SHA256: context.lockfileSha256, CONTEXT_SHA256: context.contextSha256,
    SOURCE_DATE_EPOCH: String(context.sourceDateEpoch), ...publicArguments(environment),
  };
  for (const [name, value] of Object.entries(buildArgs)) args.push('--build-arg', `${name}=${value}`);
  args.push('-');
  return {
    schema_version: 1, source_revision: context.revision, source_dirty: context.sourceDirty,
    source_date_epoch: context.sourceDateEpoch, lockfile_sha256: context.lockfileSha256,
    context_sha256: context.contextSha256, platform: 'linux/amd64', files: context.files,
    public_build_args: publicArguments(environment), build_args: args,
    validation_args: [
      fileURLToPath(new URL('./validate-frontend-image.mjs', import.meta.url)),
      '--layout', path.join(output, 'image'), '--revision', context.revision,
      '--lockfile', path.join(snapshot.frontend, 'package-lock.json'),
      '--context-sha256', context.contextSha256, '--source-dirty', String(context.sourceDirty),
      '--metadata', metadata, '--output', path.join(output, 'identity.json'),
    ],
  };
}

function privateOutputs(directory) {
  for (const name of fs.readdirSync(directory)) {
    const target = path.join(directory, name);
    const stat = fs.lstatSync(target);
    if (stat.isSymbolicLink() || (!stat.isFile() && !stat.isDirectory())) throw new Error('build output must contain only regular files and directories');
    fs.chmodSync(target, stat.isDirectory() ? 0o700 : 0o600);
    if (stat.isDirectory()) privateOutputs(target);
  }
}

function existingLocalBuilder(run, env, logFd) {
  const options = { env, logFd, timeout: 30_000 };
  const version = run('docker', ['buildx', 'version'], options).toString();
  if (!/\bv0\.35\.0\b/.test(version)) throw new Error('buildx v0.35.0 is required; this wrapper never installs or bootstraps builders');
  const inspection = run('docker', ['buildx', 'inspect'], options).toString();
  if (inspection.length > 64 * 1024) throw new Error('builder inspection exceeds the supported size');
  const sections = inspection.split(/^Nodes:\s*$/m);
  if (sections.length !== 2) throw new Error('existing builder inspection must contain nodes');
  const field = (text, name) => {
    const values = [...text.matchAll(new RegExp(`^[ \\t]*${name}:[ \\t]*(\\S+)[ \\t]*$`, 'gm'))];
    if (values.length !== 1) throw new Error('ambiguous existing builder inspection');
    return values[0][1];
  };
  const name = field(sections[0], 'Name');
  const driver = field(sections[0], 'Driver');
  if (!/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$/.test(name) || !['docker', 'docker-container'].includes(driver)) {
    throw new Error('existing builder must use a supported local Docker driver');
  }
  const nodes = sections[1].split(/(?=^[ \t]*Name:)/m).filter((node) => node.trim());
  if (!nodes.length || nodes.length > 32) throw new Error('existing local builder must have bounded explicit nodes');
  for (const node of nodes) {
    let endpoint = field(node, 'Endpoint');
    if (field(node, 'Status') !== 'running') throw new Error('existing builder node must already be running; no bootstrap is performed');
    if (!endpoint.startsWith('unix:')) {
      if (!/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$/.test(endpoint)) throw new Error('builder endpoint must be a local socket or named local context');
      const host = JSON.parse(run('docker', ['context', 'inspect', endpoint, '--format', '{{json .Endpoints.docker.Host}}'], options).toString());
      if (typeof host !== 'string' || !host) throw new Error('Docker context must provide a local endpoint');
      localEndpoint(host);
      // DOCKER_HOST overrides only the default context, not a named context.
      endpoint = endpoint === 'default' && env.DOCKER_HOST ? env.DOCKER_HOST : host;
    }
    localEndpoint(endpoint);
  }
  return name;
}

export function buildFrontendImage(frontendRoot, options, { environment = process.env, run = execute } = {}) {
  assertBuildOnly(options);
  const context = prepareContext(frontendRoot, options);
  const request = createBuildRequest(context, options, environment);
  const output = validateOutput(options.output);
  // Non-recursive mkdir must fail rather than reusing or replacing any output.
  fs.mkdirSync(output, { mode: 0o700 });
  let logFd;
  try {
    fs.writeFileSync(path.join(output, 'request.json'), `${JSON.stringify(request, null, 2)}\n`, { flag: 'wx', mode: 0o600 });
    if (options.prepareOnly) return request;
    const dockerConfig = environment.DOCKER_CONFIG || path.join(output, 'docker-config');
    if (!environment.DOCKER_CONFIG) fs.mkdirSync(dockerConfig, { mode: 0o700 });
    else if (!fs.lstatSync(dockerConfig).isDirectory()) throw new Error('DOCKER_CONFIG must name a directory, not a symlink');
    const env = {
      PATH: environment.PATH || process.env.PATH || '/usr/bin:/bin', HOME: output,
      LC_ALL: 'C', DOCKER_CONFIG: dockerConfig, BUILDX_GIT_INFO: 'false',
      ...(environment.DOCKER_HOST ? { DOCKER_HOST: environment.DOCKER_HOST } : {}),
    };
    logFd = fs.openSync(path.join(output, 'build.log'), 'wx', 0o600);
    const builder = existingLocalBuilder(run, env, logFd);
    request.build_args.splice(2, 0, '--builder', builder);
    fs.writeFileSync(path.join(output, 'request.json'), `${JSON.stringify(request, null, 2)}\n`, { mode: 0o600 });
    assertContextUnchanged(context);
    run('docker', request.build_args, { env, input: context.tar, logFd, captureOutput: false });
    assertContextUnchanged(context);
    run(process.execPath, request.validation_args, { env: { PATH: env.PATH, HOME: output, LC_ALL: 'C' }, logFd, captureOutput: false });
    assertContextUnchanged(context);
    return request;
  } finally {
    if (logFd !== undefined) fs.closeSync(logFd);
    privateOutputs(output);
  }
}

export function parseArguments(args) {
  const names = new Map([
    ['--output', 'output'], ['--revision', 'revision'], ['--allow-dirty', 'allowDirty'],
    ['--prepare-only', 'prepareOnly'],
  ]);
  const options = {};
  for (let index = 0; index < args.length; index++) {
    const name = names.get(args[index]);
    if (!name) throw new Error('unknown build option');
    if (Object.hasOwn(options, name)) throw new Error('duplicate build option');
    if (['allowDirty', 'prepareOnly'].includes(name)) options[name] = true;
    else {
      const value = args[++index];
      if (!value || value.startsWith('--')) throw new Error('build option requires a value');
      options[name] = value;
    }
  }
  validateOutput(options.output);
  return options;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.umask(0o077);
  try {
    const options = parseArguments(process.argv.slice(2));
    const request = buildFrontendImage(fileURLToPath(new URL('../../frontend', import.meta.url)), options);
    process.stdout.write(`${JSON.stringify({
      source_revision: request.source_revision, source_dirty: request.source_dirty,
      context_sha256: request.context_sha256, lockfile_sha256: request.lockfile_sha256,
      request_file: path.join(options.output, 'request.json'),
      ...(!options.prepareOnly ? { identity_file: path.join(options.output, 'identity.json') } : {}),
    })}\n`);
  } catch (error) {
    process.stderr.write(`frontend image build: ${error.message}\n`);
    process.exitCode = 1;
  }
}
