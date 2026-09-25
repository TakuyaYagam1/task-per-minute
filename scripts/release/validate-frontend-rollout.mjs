#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { isDeepStrictEqual } from 'node:util';
import { isIP } from 'node:net';

const check = (condition, message) => { if (!condition) throw new Error(message); };
const record = (value) => value !== null && typeof value === 'object' && !Array.isArray(value);
const sha = (bytes) => createHash('sha256').update(bytes).digest('hex');
const positive = (value) => Number.isSafeInteger(value) && value > 0;
const planKeys = ['schema_version', 'phase', 'source_revision', 'frontend_image', 'backend_image',
  'public_origin', 'admin_origin', 'api_origin', 'admin_api_origin', 'public_build_args',
  'current_schema', 'frontend_schema_min', 'frontend_schema_max', 'rollback'];
const apiKeys = ['NEXT_PUBLIC_API_URL', 'NEXT_PUBLIC_ADMIN_API_URL'];
const sourceOriginsKey = 'NEXT_PUBLIC_SOURCE_FILE_ORIGINS';
const caddyImage = 'caddy:2.11.4-alpine@sha256:6aeddd44c3078b0f9a35206472a11420648a79c184603ef95957d0a20044cb2b';
const auxiliarySites = { FILES_DOMAIN: 'CADDY_FILES_UPSTREAM', TASK_COOKIE_DOMAIN: 'TASK_COOKIE_UPSTREAM',
  TASK_ARCHIVE_DOMAIN: 'TASK_ARCHIVE_UPSTREAM', TASK_SAMOVAR_DOMAIN: 'TASK_SAMOVAR_UPSTREAM',
  TASK_VKONTAKTE_DOMAIN: 'TASK_VKONTAKTE_UPSTREAM', TASK_DEDYS_DOMAIN: 'TASK_DEDYS_UPSTREAM' };

function keys(value, expected, field) {
  check(record(value) && isDeepStrictEqual(Object.keys(value).sort(), [...expected].sort()), `${field} fields are invalid`);
}

function origin(value, field, empty = false) {
  if (empty && value === '') return '';
  check(typeof value === 'string' && value.length <= 2048 && /^https:\/\//.test(value)
    && !/[\s\\?#@\x00-\x1f\x7f]/.test(value), `${field} must be a canonical HTTPS origin`);
  let url;
  try { url = new URL(value); } catch { throw new Error(`${field} must be a canonical HTTPS origin`); }
  check(url.hostname && !url.username && !url.password && (value === url.origin || value === `${url.origin}/`), `${field} must be a canonical HTTPS origin`);
  return url.origin;
}

function image(value, frontend = false, field = 'image') {
  check(typeof value === 'string' && value.length <= 512 && !/\s/.test(value)
    && /^[a-z0-9][a-z0-9.-]*(?::[1-9][0-9]{0,4})?\/(?:[a-z0-9]+(?:[._-][a-z0-9]+)*\/)*[a-z0-9]+(?:[._-][a-z0-9]+)*@sha256:[a-f0-9]{64}$/.test(value), `${field} requires a canonical registry/repository SHA256 image`);
  const registry = value.split('/')[0];
  check(registry.includes('.') || registry.split(':')[0] === 'localhost' || registry.includes(':'), `${field} requires an explicit image registry`);
  if (frontend) check(/^ghcr\.io\/[a-z0-9][a-z0-9-]*\/task-per-minute-frontend@sha256:[a-f0-9]{64}$/.test(value), `${field} requires the canonical frontend image`);
  return value.split('@')[0];
}

function schemaRange(value, current, field) {
  check(positive(value.frontend_schema_min) && positive(value.frontend_schema_max)
    && value.frontend_schema_min <= current && current <= value.frontend_schema_max, `${field} schema range does not cover current_schema`);
}

function port(value, field) {
  check((typeof value === 'string' || typeof value === 'number') && /^[1-9][0-9]{0,4}$/.test(String(value))
    && Number(value) <= 65535, `${field} port is invalid`);
  return String(value);
}

function environment(service, name) {
  check(record(service?.environment), `${name}.environment must be a rendered object`);
  return service.environment;
}

function health(service, name) {
  const value = service.healthcheck;
  check(record(value) && value.disable !== true && Array.isArray(value.test)
    && ['CMD', 'CMD-SHELL'].includes(value.test[0]) && value.test.length > 1
    && value.test.slice(1).every((part) => typeof part === 'string' && part.trim()), `${name}.healthcheck must be enabled`);
}

function healthyDependency(service, dependency, name) {
  const value = service.depends_on?.[dependency];
  check(record(value) && value.condition === 'service_healthy' && value.required !== false, `${name}.depends_on requires healthy dependencies`);
}

function originList(value, field) {
  check(typeof value === 'string' && value.length <= 8192, `${field} must be an explicit origin list`);
  if (value === '') return [];
  const values = value.split(',').map((item) => item.trim());
  const result = values.map((item) => origin(item, field));
  check(new Set(result).size === result.length, `${field} contains duplicate origins`);
  return result;
}

function trustedProxies(value) {
  check(typeof value === 'string' && value.length > 0 && value.length <= 4096, 'HTTP_TRUSTED_PROXY_CIDRS requires explicit proxy networks');
  const networks = value.split(',').map((item) => item.trim());
  check(networks.length <= 32 && new Set(networks).size === networks.length, 'HTTP_TRUSTED_PROXY_CIDRS list is invalid');
  for (const network of networks) {
    const [address, mask, extra] = network.split('/');
    const family = isIP(address);
    check(extra === undefined && family && !address.includes('%') && /^[1-9][0-9]{0,2}$/.test(mask ?? '')
      && Number(mask) <= (family === 4 ? 32 : 128), 'HTTP_TRUSTED_PROXY_CIDRS must contain non-default CIDRs');
  }
}

// A deliberately bounded parser for this project's Caddy contract. It does
// not evaluate imports, process environment variables or arbitrary directives.
function parseCaddy(source) {
  check(typeof source === 'string' && Buffer.byteLength(source) <= 256 * 1024
    && !/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(source), 'Caddy input is invalid or oversized');
  const tokens = [];
  const pattern = /[ \t\r]+|#[^\n]*|\n|\{\$[A-Z][A-Z0-9_]*\}|"(?:[^"\\\n]|\\.)*"|[{}]|[^\s{}"#]+/y;
  let position = 0;
  while (position < source.length) {
    pattern.lastIndex = position;
    const match = pattern.exec(source);
    check(match && match.index === position, 'unsupported Caddy token');
    position = pattern.lastIndex;
    const token = match[0];
    if (/^[ \t\r#]/.test(token)) continue;
    let word = token;
    if (token.startsWith('"')) { try { word = JSON.parse(token); } catch { throw new Error('unsupported Caddy quoted token'); } }
    check(word.length <= 4096 && tokens.length < 16000, 'Caddy token limit exceeded');
    tokens.push(word);
  }
  let cursor = 0;
  function block(depth = 0) {
    check(depth <= 12, 'Caddy nesting limit exceeded');
    const result = [];
    while (cursor < tokens.length) {
      if (tokens[cursor] === '\n') { cursor++; continue; }
      if (tokens[cursor] === '}') { check(depth > 0, 'unbalanced Caddy block'); cursor++; return result; }
      const words = [];
      while (cursor < tokens.length && !['\n', '{', '}'].includes(tokens[cursor])) words.push(tokens[cursor++]);
      let body = null;
      if (tokens[cursor] === '{') { cursor++; body = block(depth + 1); }
      check(words.length > 0 || (depth === 0 && body !== null), 'invalid Caddy directive');
      result.push({ words, body });
    }
    check(depth === 0, 'unbalanced Caddy block');
    return result;
  }
  return block();
}

const leaf = (...words) => ({ words, body: null });
const handler = (route, child) => ({ words: ['handle', ...(route ? [route] : [])], body: [child] });
const denied = (routes) => routes.map((route) => handler(route, leaf('respond', '404')));

function caddyContract(source, env, publicOrigin, adminOrigin, apiOrigin, adminApiOrigin, frontendPort, backendPort) {
  const siteOrigin = (value) => origin(typeof value === 'string' && !value.includes('://') ? `https://${value}` : value, 'Caddy origin');
  const publicEdge = siteOrigin(env.APP_DOMAIN);
  const adminEdge = siteOrigin(env.ADMIN_DOMAIN);
  const apiEdge = siteOrigin(env.API_DOMAIN);
  check(publicEdge === publicOrigin && adminEdge === adminOrigin && new Set([publicEdge, adminEdge, apiEdge]).size === 3, 'Caddy origins do not match distinct plan origins');
  check(!apiOrigin || [publicEdge, apiEdge].includes(apiOrigin), 'api_origin has no matching Caddy backend route');
  check(!adminApiOrigin || [adminEdge, apiEdge].includes(adminApiOrigin), 'admin_api_origin has no matching Caddy backend route');
  const front = `frontend:${frontendPort}`;
  const back = `backend:${backendPort}`;
  check(env.CADDY_FRONTEND_UPSTREAM === front && env.CADDY_BACKEND_UPSTREAM === back, 'Caddy upstream ports do not match runtime');
  const proxy = (route, upstream) => handler(route, leaf('reverse_proxy', upstream));
  const internal = denied(['/internal', '/internal/*']);
  const definitions = new Map([
    [publicEdge, [...denied(['/admin', '/admin/*', '/api/v1/admin', '/api/v1/admin/*']), ...internal, proxy('/api/*', back), proxy('', front)]],
    [adminEdge, [handler('/', leaf('redir', '*', '/admin', '302')), ...internal, proxy('/api/*', back), proxy('', front)]],
    [apiEdge, [...internal, proxy('', back)]],
  ]);
  for (const [domain, upstream] of Object.entries(auxiliarySites)) {
    if (env[domain] === undefined) continue;
    const address = siteOrigin(env[domain]);
    check(!definitions.has(address), 'Caddy origins must not overlap');
    check(typeof env[upstream] === 'string' && /^[a-z0-9][a-z0-9._-]*:[1-9][0-9]{0,4}$/.test(env[upstream]), 'Caddy auxiliary upstream is invalid');
    port(env[upstream].split(':')[1], 'Caddy auxiliary upstream');
    definitions.set(address, [...internal, proxy('', env[upstream])]);
  }
  const allowedVariables = new Set(['APP_DOMAIN', 'ADMIN_DOMAIN', 'API_DOMAIN', 'CADDY_FRONTEND_UPSTREAM', 'CADDY_BACKEND_UPSTREAM',
    ...Object.keys(auxiliarySites), ...Object.values(auxiliarySites)]);
  function resolved(word) {
    const match = /^\{\$([A-Z][A-Z0-9_]*)\}$/.exec(word);
    if (!match) { check(!/[{}]/.test(word), 'unsupported Caddy placeholder'); return word; }
    check(allowedVariables.has(match[1]) && typeof env[match[1]] === 'string'
      && !/[\s{}]/.test(env[match[1]]), 'Caddy placeholder must have an explicit safe compose value');
    return env[match[1]];
  }
  const normalized = (node) => ({ words: node.words.map(resolved), body: node.body?.map(normalized) ?? null });
  const nodes = parseCaddy(source);
  const globals = nodes.filter((node) => node.words.length === 0);
  check(globals.length === 1 && globals[0].body && globals[0].body.some((node) => isDeepStrictEqual(node, leaf('admin', 'localhost:2019')))
    && globals[0].body.every((node) => node.body === null && node.words.length === 2 && ['admin', 'email', 'acme_ca'].includes(node.words[0]))
    && new Set(globals[0].body.map((node) => node.words[0])).size === globals[0].body.length, 'Caddy global contract mismatch');
  const snippets = nodes.filter((node) => node.words[0]?.startsWith('('));
  check(snippets.length === 1 && isDeepStrictEqual(snippets[0], { words: ['(common_headers)'], body: [leaf('encode', 'gzip', 'zstd'),
    { words: ['header'], body: [leaf('>Strict-Transport-Security', 'max-age=63072000; includeSubDomains; preload'),
      leaf('>X-Content-Type-Options', 'nosniff'), leaf('>Referrer-Policy', 'strict-origin-when-cross-origin')] }] }), 'Caddy common headers contract mismatch');
  const seen = new Set();
  for (const node of nodes.filter((item) => item.words.length > 0 && !item.words[0].startsWith('('))) {
    check(node.words.length === 1 && node.body !== null, 'unsupported Caddy site or import');
    const address = siteOrigin(resolved(node.words[0]));
    check(definitions.has(address) && !seen.has(address), 'Caddy site is unexpected or duplicated'); seen.add(address);
    const imports = node.body.filter((item) => item.words[0] === 'import');
    check(imports.length === 1 && isDeepStrictEqual(imports[0], leaf('import', 'common_headers')), 'Caddy import contract mismatch');
    const requests = node.body.filter((item) => item.words[0] === 'request_body');
    check(requests.length <= 1 && requests.every((item) => item.words.length === 1 && item.body?.length === 1
      && item.body[0].body === null && item.body[0].words.length === 2 && item.body[0].words[0] === 'max_size'
      && /^[1-9][0-9]*(?:KB|MB|GB)$/.test(item.body[0].words[1])), 'Caddy request body contract mismatch');
    const routes = node.body.filter((item) => !['import', 'request_body'].includes(item.words[0])).map(normalized);
    check(isDeepStrictEqual(routes, definitions.get(address)), 'Caddy routes, deny handlers or upstream mapping mismatch');
  }
  check(seen.size === definitions.size, 'Caddy required sites are missing');
}

/** Pure offline contract check. Source revision and build args are declarations,
 * not proof of the running image, signature, schema state or successful deploy. */
export function validateRollout({ plan, compose, caddyfile }) {
  keys(plan, planKeys, 'plan');
  check(plan.schema_version === 1 && ['rollout', 'recovery'].includes(plan.phase), 'plan schema_version or phase is invalid');
  check(typeof plan.source_revision === 'string' && plan.source_revision.length === 40 && /^[a-f0-9]{40}$/.test(plan.source_revision), 'source_revision is invalid');
  const frontendRepository = image(plan.frontend_image, true, 'frontend_image'); image(plan.backend_image, false, 'backend_image');
  const publicOrigin = origin(plan.public_origin, 'public_origin');
  const adminOrigin = origin(plan.admin_origin, 'admin_origin');
  check(publicOrigin !== adminOrigin, 'public_origin and admin_origin must differ');
  const apiOrigin = origin(plan.api_origin, 'api_origin', true);
  const adminApiOrigin = origin(plan.admin_api_origin, 'admin_api_origin', true);
  check(!apiOrigin || adminApiOrigin, 'admin_api_origin must be explicit when api_origin is set');
  const buildArgs = plan.public_build_args;
  check(record(buildArgs) && apiKeys.every((key) => Object.hasOwn(buildArgs, key))
    && Object.keys(buildArgs).every((key) => [...apiKeys, sourceOriginsKey].includes(key)), 'public_build_args fields are invalid');
  if (Object.hasOwn(buildArgs, sourceOriginsKey)) originList(buildArgs[sourceOriginsKey], sourceOriginsKey);
  check(origin(plan.public_build_args.NEXT_PUBLIC_API_URL, 'public_build_args API origin', true) === apiOrigin
    && origin(plan.public_build_args.NEXT_PUBLIC_ADMIN_API_URL, 'public_build_args admin API origin', true) === adminApiOrigin, 'public_build_args API origins mismatch');
  check(positive(plan.current_schema), 'current_schema must be a positive integer'); schemaRange(plan, plan.current_schema, 'frontend');
  keys(plan.rollback, ['frontend_image', 'frontend_schema_min', 'frontend_schema_max'], 'rollback');
  check(image(plan.rollback.frontend_image, true, 'rollback image') === frontendRepository && plan.rollback.frontend_image !== plan.frontend_image, 'rollback requires a different digest in the same frontend image repository');
  schemaRange(plan.rollback, plan.current_schema, 'rollback');
  check(record(compose?.services), 'compose.services must be a rendered object');
  const { frontend, backend, caddy } = compose.services;
  check([frontend, backend, caddy].every(record), 'frontend, backend and caddy services are required');
  check(caddy.image === caddyImage && (caddy.build === undefined || caddy.build === null),
    'Caddy requires the reviewed release image without a build override');
  for (const [name, service] of Object.entries({ frontend, backend })) {
    check(service.image === plan[`${name}_image`], `${name} image does not match plan`);
    check(service.build === undefined || service.build === null, `${name} build is forbidden in release compose`);
    check(service.ports === undefined || (Array.isArray(service.ports) && service.ports.length === 0), `${name} published ports are forbidden`);
    check(service.network_mode === undefined, `${name} network_mode is forbidden`);
  }
  check((backend.deploy?.replicas ?? 1) === 1 && (backend.scale ?? 1) === 1
    && (backend.deploy?.mode ?? 'replicated') === 'replicated', 'backend replicas, scale and mode must preserve one process');
  for (const [name, service] of Object.entries({ frontend, backend, caddy })) health(service, name);
  healthyDependency(caddy, 'backend', 'caddy'); healthyDependency(caddy, 'frontend', 'caddy'); healthyDependency(frontend, 'backend', 'frontend');
  const fe = environment(frontend, 'frontend'); const be = environment(backend, 'backend'); const ce = environment(caddy, 'caddy');
  const frontendPort = port(fe.PORT, 'frontend PORT'); const backendPort = port(be.HTTP_PORT, 'backend HTTP_PORT');
  for (const [service, value] of [[frontend, frontendPort], [backend, backendPort]]) {
    check(Array.isArray(service.expose) && service.expose.length === 1 && [value, `${value}/tcp`].includes(String(service.expose[0])), 'expose port does not match runtime PORT');
  }
  check(fe.BACKEND_URL === `http://backend:${backendPort}`, 'frontend BACKEND_URL must match the internal backend port');
  for (const key of [...apiKeys, sourceOriginsKey]) if (fe[key] !== undefined) check(fe[key] === plan.public_build_args[key], 'frontend runtime API declaration differs from public_build_args');
  check(be.WS_REQUIRE_ORIGIN === 'true' || be.WS_REQUIRE_ORIGIN === true, 'WS_REQUIRE_ORIGIN must be true');
  trustedProxies(be.HTTP_TRUSTED_PROXY_CIDRS);
  const httpOrigins = originList(be.HTTP_ALLOWED_ORIGINS, 'HTTP_ALLOWED_ORIGINS');
  const wsOrigins = originList(be.WS_ALLOWED_ORIGINS, 'WS_ALLOWED_ORIGINS');
  check(wsOrigins.every((value) => httpOrigins.includes(value)), 'WS_ALLOWED_ORIGINS must be a subset of HTTP_ALLOWED_ORIGINS');
  if ((apiOrigin && apiOrigin !== publicOrigin) || (adminApiOrigin && adminApiOrigin !== adminOrigin)) {
    check([publicOrigin, adminOrigin].every((value) => httpOrigins.includes(value) && wsOrigins.includes(value)), 'HTTP_ALLOWED_ORIGINS and WS_ALLOWED_ORIGINS must include both browser origins in direct API mode');
  }
  caddyContract(caddyfile, ce, publicOrigin, adminOrigin, apiOrigin, adminApiOrigin, frontendPort, backendPort);
  return { schema_version: 1, status: 'pass', phase: plan.phase, source_revision: plan.source_revision,
    frontend_image: plan.frontend_image, backend_image: plan.backend_image, rollback_frontend_image: plan.rollback.frontend_image,
    current_schema: plan.current_schema, configuration_validated: true, deployment_verified: false, signature_verified: false,
    source_proof: 'plan_declaration_only', database_downgrade_verified: false };
}

function absolutePath(value) {
  check(typeof value === 'string' && path.isAbsolute(value) && !/[\x00-\x1f\x7f\\]/.test(value)
    && !value.split('/').includes('..'), 'absolute paths without traversal are required');
  return path.resolve(value);
}

function checkedPath(value, directory = false) {
  const absolute = absolutePath(value); let current = path.parse(absolute).root;
  for (const part of absolute.slice(current.length).split('/').filter(Boolean)) {
    current = path.join(current, part); const stat = fs.lstatSync(current);
    check(!stat.isSymbolicLink(), 'symlink paths are forbidden');
    check(current === absolute && !directory ? stat.isFile() : stat.isDirectory(), 'input path type is invalid');
  }
  check(directory || absolute !== path.parse(absolute).root, 'input must be a regular file'); return absolute;
}

const stamp = (stat) => [stat.dev, stat.ino, stat.size, stat.mtimeNs, stat.ctimeNs].join(':');
function readInput(filename, limit, field) {
  const absolute = checkedPath(filename); const before = fs.statSync(absolute, { bigint: true });
  check(before.size <= BigInt(limit), `${field} input exceeds size limit`);
  const fd = fs.openSync(absolute, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW | fs.constants.O_NONBLOCK);
  try {
    const opened = fs.fstatSync(fd, { bigint: true }); check(stamp(opened) === stamp(before) && opened.isFile(), `${field} changed while opening`);
    const bytes = Buffer.alloc(Number(opened.size)); let offset = 0;
    while (offset < bytes.length) { const count = fs.readSync(fd, bytes, offset, bytes.length - offset, null); if (!count) break; offset += count; }
    checkedPath(absolute);
    check(offset === bytes.length && stamp(opened) === stamp(fs.fstatSync(fd, { bigint: true }))
      && stamp(opened) === stamp(fs.statSync(absolute, { bigint: true })), `${field} changed while reading`);
    return { filename: absolute, bytes, hash: sha(bytes), stamp: stamp(opened), limit, field };
  } finally { fs.closeSync(fd); }
}

export function validateRolloutFiles(options) {
  const output = absolutePath(options.output); const parent = checkedPath(path.dirname(output), true);
  const parentStat = fs.statSync(parent, { bigint: true });
  try { fs.lstatSync(output); throw new Error('output already exists; refusing overwrite'); } catch (error) { if (error.code !== 'ENOENT') throw error; }
  const inputs = { plan: readInput(options.plan, 32 * 1024, 'plan'), compose: readInput(options.compose, 4 * 1024 * 1024, 'compose'),
    caddyfile: readInput(options.caddyfile, 256 * 1024, 'caddyfile') };
  function json(input) { try { return JSON.parse(input.bytes.toString('utf8')); } catch { throw new Error(`invalid ${input.field} JSON`); } }
  const report = { ...validateRollout({ plan: json(inputs.plan), compose: json(inputs.compose), caddyfile: inputs.caddyfile.bytes.toString('utf8') }),
    inputs_sha256: Object.fromEntries(Object.entries(inputs).map(([name, input]) => [name, input.hash])) };
  for (const input of Object.values(inputs)) {
    const after = readInput(input.filename, input.limit, input.field);
    check(after.hash === input.hash && after.stamp === input.stamp, 'input changed during validation');
  }
  checkedPath(parent, true); const currentParent = fs.statSync(parent, { bigint: true });
  check(parentStat.dev === currentParent.dev && parentStat.ino === currentParent.ino, 'output parent changed');
  const scratch = fs.mkdtempSync(path.join(parent, '.frontend-rollout-')); const temporary = path.join(scratch, 'report.json');
  try {
    const fd = fs.openSync(temporary, 'wx', 0o600);
    try { fs.writeFileSync(fd, `${JSON.stringify(report, null, 2)}\n`); fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
    checkedPath(parent, true); fs.linkSync(temporary, output);
  } finally { fs.unlinkSync(temporary); fs.rmdirSync(scratch); }
  return report;
}

export function parseArguments(args) {
  const options = {}; const names = new Set(['--plan', '--compose', '--caddyfile', '--output']);
  for (let index = 0; index < args.length; index += 2) {
    const flag = args[index];
    check(names.has(flag) && !Object.hasOwn(options, flag.slice(2)) && typeof args[index + 1] === 'string'
      && !args[index + 1].startsWith('--'), 'unknown, duplicate or incomplete CLI argument');
    options[flag.slice(2)] = args[index + 1];
  }
  check(Object.keys(options).length === 4, 'plan, compose, caddyfile and output arguments are required'); return options;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.umask(0o077);
  try { const report = validateRolloutFiles(parseArguments(process.argv.slice(2))); process.stdout.write(`${JSON.stringify(report)}\n`); }
  catch (error) { process.stderr.write(`frontend rollout validation failed: ${error.code ?? error.message}\n`); process.exitCode = 1; }
}
