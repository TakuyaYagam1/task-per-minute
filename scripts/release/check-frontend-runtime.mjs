#!/usr/bin/env node

import * as fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const SECURITY_HEADERS = {
  'x-frame-options': 'DENY',
  'x-content-type-options': 'nosniff',
  'strict-transport-security': 'max-age=63072000; includeSubDomains; preload',
  'referrer-policy': 'strict-origin-when-cross-origin',
  'permissions-policy': 'camera=(), microphone=(), geolocation=(), interest-cohort=()',
  'cross-origin-opener-policy': 'same-origin',
};

class RuntimeCheckError extends Error {}

function check(condition, message) {
  if (!condition) throw new RuntimeCheckError(message);
}

function loopbackURL(value) {
  // Check the spelling before URL normalization: alternate numeric hosts and
  // dot segments must not become an apparently valid loopback root.
  check(typeof value === 'string' && !/\s/.test(value)
    && /^http:\/\/(?:127\.0\.0\.1|\[::1\])(?::[1-9][0-9]{0,4})?\/?$/.test(value),
  'target must be a literal HTTP loopback URL with only a root path');
  let parsed;
  try { parsed = new URL(value); } catch { throw new RuntimeCheckError('invalid loopback URL'); }
  return parsed;
}

function publicOrigin(value) {
  check(typeof value === 'string' && !/[\s\\*?#@]/.test(value)
    && /^https?:\/\/[^/]+\/?$/.test(value), 'invalid expected public origin');
  let parsed;
  try { parsed = new URL(value); } catch { throw new RuntimeCheckError('invalid expected public origin'); }
  check(parsed.hostname && !parsed.hostname.includes('*') && !parsed.username && !parsed.password
    && parsed.pathname === '/', 'invalid expected public origin');
  if (parsed.protocol === 'http:') {
    try { loopbackURL(value); } catch { throw new RuntimeCheckError('HTTP public origin must be literal loopback'); }
  }
  return [parsed.origin, `${parsed.protocol === 'https:' ? 'wss:' : 'ws:'}//${parsed.host}`];
}

function expectedConnections(apiOrigin, adminOrigin) {
  const sources = new Set(["'self'"]);
  for (const value of [apiOrigin, adminOrigin]) {
    if (value !== undefined) for (const source of publicOrigin(value)) sources.add(source);
  }
  return sources;
}

function parseCSP(value) {
  check(typeof value === 'string' && value.trim().length > 0 && value.length <= 16 * 1024
    && !/[^\x20-\x7e\t]/.test(value) && !value.includes(','), 'invalid or missing enforced CSP policy');
  const directives = new Map();
  const parts = value.split(';');
  for (let index = 0; index < parts.length; index += 1) {
    const part = parts[index].trim();
    if (part === '' && index === parts.length - 1) continue;
    const [rawName, ...sources] = part.split(/[ \t]+/);
    const name = rawName.toLowerCase();
    check(/^[a-z][a-z0-9-]*$/.test(name) && !directives.has(name), 'invalid or duplicate CSP policy directive');
    check(sources.every((source) => !source.includes('*')
      && !["'unsafe-eval'", "'wasm-unsafe-eval'"].includes(source.toLowerCase())),
    'CSP policy contains a wildcard or unsafe evaluation source');
    directives.set(name, sources);
  }
  return directives;
}

function validatePolicy(headers, expectedSources) {
  check(!headers.has('content-security-policy-report-only'), 'production must not send report-only CSP');
  const directives = parseCSP(headers.get('content-security-policy'));
  const expectedDirectives = new Map([
    ['default-src', ["'self'"]], ['frame-ancestors', ["'none'"]], ['object-src', ["'none'"]],
    ['script-src', ["'self'", "'unsafe-inline'"]], ['style-src', ["'self'", "'unsafe-inline'"]],
    ['img-src', ["'self'", 'data:', 'blob:']], ['media-src', ["'self'", 'blob:']],
    ['font-src', ["'self'", 'data:', 'https://fonts.gstatic.com']],
    ['worker-src', ["'self'", 'blob:']],
    ['base-uri', ["'self'"]], ['form-action', ["'self'"]], ['report-uri', ['/csp-report']],
    ['upgrade-insecure-requests', []],
  ]);
  check([...directives.keys()].every((name) => name === 'connect-src' || expectedDirectives.has(name)),
    'CSP policy contains an unexpected directive');
  for (const [name, expected] of expectedDirectives) {
    const sources = directives.get(name);
    check(sources && sources.length === expected.length && sources.every((source, index) => source === expected[index]),
      `CSP policy ${name} does not match production policy`);
  }
  const connections = directives.get('connect-src');
  check(connections && connections.length === expectedSources.size
    && new Set(connections).size === connections.length
    && connections.every((source) => expectedSources.has(source)), 'CSP policy connect-src differs from expected public origins');
  for (const [name, expected] of Object.entries(SECURITY_HEADERS)) {
    check(headers.get(name) === expected, `production security header ${name} is missing or incorrect`);
  }
}

async function request(url, name, contentType, limit, timeoutMs) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const response = await fetch(url, { method: 'GET', redirect: 'manual', signal: controller.signal, credentials: 'omit' });
    check(response.status === 200, `${name} status must be 200; redirects are not allowed`);
    check(response.headers.get('content-type')?.split(';')[0].trim().toLowerCase() === contentType,
      `${name} content type is incorrect`);
    const chunks = [];
    let size = 0;
    for await (const chunk of response.body) {
      size += chunk.length;
      check(size <= limit, `${name} response exceeds the body limit`);
      chunks.push(chunk);
    }
    return { headers: response.headers, body: Buffer.concat(chunks).toString('utf8') };
  } catch (error) {
    if (error instanceof RuntimeCheckError) throw error;
    throw new RuntimeCheckError(controller.signal.aborted ? `${name} request timed out` : `${name} request failed`);
  } finally {
    clearTimeout(timer);
    controller.abort();
  }
}

async function checkedDirectory(directory) {
  const root = path.parse(directory).root;
  let current = root;
  let stat = await fs.lstat(root);
  for (const component of directory.slice(root.length).split(path.sep).filter(Boolean)) {
    current = path.join(current, component);
    stat = await fs.lstat(current);
    check(!stat.isSymbolicLink(), 'report path must not contain symlinks');
    check(stat.isDirectory(), 'report parent must be an existing directory');
  }
  return stat;
}

async function outputPath(value) {
  check(typeof value === 'string' && path.isAbsolute(value) && !value.includes('\0')
    && !value.split(/[\\/]/).includes('..'), 'output must be an absolute path without traversal');
  const absolute = path.resolve(value);
  const parent = await checkedDirectory(path.dirname(absolute));
  try {
    await fs.lstat(absolute);
  } catch (error) {
    if (error.code === 'ENOENT') return { absolute, parent };
    throw error;
  }
  throw new RuntimeCheckError('report output already exists; refusing file or symlink overwrite');
}

async function publishReport(output, report) {
  const { absolute, parent } = await outputPath(output.absolute);
  check(parent.dev === output.parent.dev && parent.ino === output.parent.ino, 'report parent changed during the check');
  const directory = path.dirname(absolute);
  const scratch = await fs.mkdtemp(path.join(directory, '.frontend-runtime-'));
  const temporary = path.join(scratch, 'report.json');
  try {
    const handle = await fs.open(temporary, 'wx', 0o600);
    try {
      await handle.writeFile(`${JSON.stringify(report, null, 2)}\n`);
      await handle.sync();
    } finally {
      await handle.close();
    }
    const current = await checkedDirectory(directory);
    check(current.dev === parent.dev && current.ino === parent.ino, 'report parent changed during publication');
    // Unlike rename, link cannot replace a report created by another owner.
    await fs.link(temporary, absolute);
  } finally {
    await fs.unlink(temporary).catch((error) => { if (error.code !== 'ENOENT') throw error; });
    await fs.rmdir(scratch);
  }
}

// Only HTTP policy is attested here, not image identity, signatures or browser
// behavior. The caller owns the loopback service and the new report path.
export async function checkRuntime({ url, output, apiOrigin, adminOrigin, timeoutMs = 5000 }) {
  const target = loopbackURL(url);
  const sources = expectedConnections(apiOrigin, adminOrigin);
  check(Number.isSafeInteger(timeoutMs) && timeoutMs > 0 && timeoutMs <= 30000, 'invalid request timeout');
  const destination = await outputPath(output);
  const health = await request(new URL('/health', target), 'health', 'application/json', 64 * 1024, timeoutMs);
  let status;
  try { status = JSON.parse(health.body); } catch { throw new RuntimeCheckError('health must return valid JSON'); }
  check(status !== null && typeof status === 'object' && !Array.isArray(status) && status.status === 'ok',
    'health must report status ok');
  validatePolicy(health.headers, sources);
  const page = await request(new URL('/', target), 'page', 'text/html', 2 * 1024 * 1024, timeoutMs);
  check(/^\s*(?:<!doctype\s+html(?:\s|>)|<html(?:\s|>))/i.test(page.body), 'page must return HTML');
  validatePolicy(page.headers, sources);
  const report = {
    schemaVersion: 1,
    status: 'pass',
    health: { status: 200, statusOk: true },
    page: { status: 200, html: true },
    policy: {
      csp: 'enforced', connectSrc: 'expected', frameOptions: 'deny', contentTypeOptions: 'nosniff',
      hsts: 'matched', referrerPolicy: 'matched', permissionsPolicy: 'matched', crossOriginOpenerPolicy: 'same-origin',
    },
  };
  await publishReport(destination, report);
  return report;
}

export function parseArguments(args) {
  const names = new Map([['--url', 'url'], ['--output', 'output'], ['--api-origin', 'apiOrigin'], ['--admin-origin', 'adminOrigin']]);
  const options = {};
  for (let index = 0; index < args.length; index += 2) {
    const name = names.get(args[index]);
    check(name && !Object.hasOwn(options, name), 'unknown or duplicate option');
    const value = args[index + 1];
    check(typeof value === 'string' && value.length > 0 && !value.startsWith('--'), 'option value is required');
    options[name] = value;
  }
  check(options.url && options.output, '--url and --output are required');
  return options;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    await checkRuntime(parseArguments(process.argv.slice(2)));
    console.log('Frontend runtime check passed.');
  } catch (error) {
    // Never print response bodies, raw headers, URLs or filesystem error paths.
    console.error(`Frontend runtime check failed: ${error instanceof RuntimeCheckError ? error.message : 'report filesystem operation failed'}`);
    process.exitCode = 1;
  }
}
