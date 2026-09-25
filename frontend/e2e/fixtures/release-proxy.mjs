import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';

const repository = fileURLToPath(new URL('../../../', import.meta.url));
const originalConfig = path.join(repository, 'deployment/caddy/Caddyfile');
const caddySha256 = 'b7105518e3ed1c0761f232e44fc09345535533c9cb0abf0e12809416c7ac64d9';
const host = '127.0.0.1';
export const releaseOrigins = Object.freeze({
  public: 'http://127.0.0.1:4381', admin: 'http://127.0.0.1:4382', api: 'http://127.0.0.1:4383',
});
const domainNames = ['APP_DOMAIN', 'ADMIN_DOMAIN', 'API_DOMAIN', 'FILES_DOMAIN', 'TASK_COOKIE_DOMAIN',
  'TASK_ARCHIVE_DOMAIN', 'TASK_SAMOVAR_DOMAIN', 'TASK_VKONTAKTE_DOMAIN', 'TASK_DEDYS_DOMAIN'];
const allowedOrigins = new Set([releaseOrigins.public, releaseOrigins.admin]);
const check = (condition, message) => { if (!condition) throw new Error(message); };

function executable(filename) {
  check(typeof filename === 'string' && path.isAbsolute(filename) && !/[\x00-\x1f\x7f]/.test(filename)
    && !filename.split(path.sep).includes('..'), 'CADDY_BIN must name an absolute reviewed executable');
  const stat = fs.lstatSync(filename);
  check(stat.isFile() && !stat.isSymbolicLink(), 'CADDY_BIN must be a regular executable, not a symlink');
  fs.accessSync(filename, fs.constants.X_OK);
  check(stat.size <= 256 * 1024 * 1024 && createHash('sha256').update(fs.readFileSync(filename)).digest('hex') === caddySha256,
    'CADDY_BIN SHA256 does not match the reviewed Caddy 2.11.4 binary');
  return filename;
}

function loopbackConfig(source) {
  const text = source.toString('utf8');
  check(Buffer.from(text).equals(source), 'repository Caddyfile must be valid UTF-8');
  const openings = [...text.matchAll(/^[ \t]*\{[ \t]*\r?$/gm)];
  check(openings.length === 1 && openings[0].index === 0, 'expected exactly one leading Caddy global block');
  const global = /^(\{[ \t]*\r?\n)([\s\S]*?)^\}[ \t]*(?:\r?\n|$)/m.exec(text);
  check(global !== null && !/[{}]/.test(global[2].replace(/\{\$[A-Z0-9_]+\}/g, '')),
    'unsupported Caddy global block shape');
  check(!/^[ \t]*(?:default_bind|bind)\b/m.test(text), 'repository Caddyfile already sets a listener binding; review required');
  const newline = global[1].endsWith('\r\n') ? '\r\n' : '\n';
  return Buffer.from(`${global[1]}\tdefault_bind 127.0.0.1${newline}${text.slice(global[1].length)}`);
}

async function freePort(port, address = host) {
  const listener = net.createServer();
  await new Promise((resolve, reject) => {
    listener.once('error', (error) => {
      if (address === '::1' && ['EAFNOSUPPORT', 'EADDRNOTAVAIL'].includes(error.code)) resolve();
      else reject(new Error(`release proxy requires free local port ${port}; no existing process was stopped`, { cause: error }));
    });
    listener.listen({ host: address, port, exclusive: true }, () => listener.close((error) => error ? reject(error) : resolve()));
  });
}

function localGet(url, timeout = 1000) {
  const target = new URL(url);
  check(target.protocol === 'http:' && target.hostname === host, 'readiness probes must stay on literal loopback HTTP');
  return new Promise((resolve, reject) => {
    const request = http.get(target, { agent: false, timeout }, (response) => {
      let bytes = 0;
      response.on('data', (chunk) => {
        bytes += chunk.length;
        if (bytes > 4096) response.destroy(new Error('readiness response exceeds limit'));
      });
      response.on('error', reject);
      response.on('end', () => resolve(response.statusCode));
    });
    request.once('timeout', () => request.destroy(new Error('loopback readiness probe timed out')));
    request.once('error', reject);
  });
}

// This is only a routing probe. It does not implement tournament auth, CSRF,
// membership or backend policy and must never be cited as backend security proof.
function syntheticBackend() {
  const sockets = new Set();
  const counts = new Map();
  const probePaths = new Set(['/internal', '/internal/release-smoke', '/api/release-smoke',
    '/api/release-smoke/ws', '/api/v1/admin', '/api/v1/admin/release-smoke']);
  const remember = (url) => {
    if (probePaths.has(url)) {
      counts.set(url, (counts.get(url) ?? 0) + 1);
    }
  };
  const backend = http.createServer({ requestTimeout: 5000, headersTimeout: 5000, maxHeaderSize: 8192 }, (request, response) => {
    const url = (request.url ?? '').split('?')[0];
    const origin = request.headers.origin;
    remember(url);
    const reply = (status, value) => {
      response.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' });
      response.end(JSON.stringify(value));
    };
    request.on('error', () => response.destroy());
    request.resume();
    if (origin !== undefined && !allowedOrigins.has(origin)) {
      reply(403, { synthetic: true, error: 'origin denied by routing fixture' });
      return;
    }
    if (origin !== undefined) {
      response.setHeader('Access-Control-Allow-Origin', origin);
      response.setHeader('Access-Control-Allow-Credentials', 'true');
      response.setHeader('Vary', 'Origin');
    }
    if (request.method === 'OPTIONS') {
      response.setHeader('Access-Control-Allow-Methods', 'GET, POST, OPTIONS');
      response.setHeader('Access-Control-Allow-Headers', 'Content-Type');
      response.writeHead(204); response.end(); return;
    }
    if (url === '/health') {
      reply(200, { status: 'ok', synthetic: true }); return;
    }
    if (url === '/api/release-smoke' && ['GET', 'POST'].includes(request.method)) {
      reply(200, { synthetic: true, probe: 'release-routing', path: url, method: request.method,
        request_origin: origin ?? null, request_host: request.headers.host });
      return;
    }
    // Positive upstream sentinels make Caddy's deny-path assertions meaningful:
    // a bypass reaches 200 here, rather than passing due to a backend 404.
    if (url === '/internal' || url.startsWith('/internal/') || url === '/api/v1/admin' || url === '/api/v1/admin/release-smoke') {
      reply(200, { synthetic: true, probe: 'upstream-boundary-sentinel' }); return;
    }
    reply(url.startsWith('/api/v1/') ? 401 : 404, { synthetic: true, error: 'no synthetic application session' });
  });
  backend.maxConnections = 64;
  backend.keepAliveTimeout = 1000;
  backend.maxHeadersCount = 32;
  backend.on('connection', (socket) => {
    sockets.add(socket); socket.on('error', () => socket.destroy()); socket.once('close', () => sockets.delete(socket));
  });
  backend.on('upgrade', (request, socket) => {
    const url = request.url ?? '';
    const origin = request.headers.origin;
    remember(url);
    const reject = (status) => socket.end(`HTTP/1.1 ${status}\r\nConnection: close\r\nContent-Length: 0\r\n\r\n`);
    if (!allowedOrigins.has(origin)) { reject('403 Forbidden'); return; }
    if (url !== '/api/release-smoke/ws') { reject('404 Not Found'); return; }
    const key = request.headers['sec-websocket-key'];
    if (request.headers.upgrade?.toLowerCase() !== 'websocket' || request.headers['sec-websocket-version'] !== '13'
      || typeof key !== 'string' || key.length !== 24 || !/^[A-Za-z0-9+/]{22}==$/.test(key)) {
      reject('400 Bad Request'); return;
    }
    const accept = createHash('sha1').update(`${key}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`).digest('base64');
    socket.write(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`);
    const payload = Buffer.from(`release-routing:${origin}`);
    check(payload.length < 126, 'fixture websocket frame must remain bounded');
    // One unmasked server text frame followed by a normal close frame. There is
    // no application protocol, message reflection or arbitrary frame parser.
    socket.end(Buffer.concat([Buffer.from([0x81, payload.length]), payload, Buffer.from([0x88, 2, 0x03, 0xe8])]));
    socket.setTimeout(2000, () => socket.destroy());
  });
  return { backend, sockets, count: (url) => counts.get(url) ?? 0 };
}

/** No work runs on import: Playwright --list never launches a listener.
 * @param {{ caddyBin?: string, frontendUrl?: string }} options
 */
export async function startReleaseProxy({ caddyBin, frontendUrl = 'http://127.0.0.1:3188' } = {}) {
  const binary = executable(caddyBin);
  check(['http://127.0.0.1:3188', 'http://127.0.0.1:3188/'].includes(frontendUrl),
    'release frontend must be the owner-managed production frontend runtime at http://127.0.0.1:3188');
  const frontend = new URL(frontendUrl);
  const sourceConfig = fs.readFileSync(originalConfig);
  const configHash = createHash('sha256').update(sourceConfig).digest('hex');
  const adaptedConfigBytes = loopbackConfig(sourceConfig);
  const adaptedConfigHash = createHash('sha256').update(adaptedConfigBytes).digest('hex');
  for (const port of [2019, ...domainNames.map((_, index) => 4381 + index)]) await freePort(port);
  await freePort(2019, '::1');
  check(await localGet(`${frontend.origin}/health`, 3000) === 200, 'production frontend runtime /health is not ready');
  const scratchRoot = path.join(os.homedir(), '.codex', '.tmp');
  fs.mkdirSync(scratchRoot, { mode: 0o700, recursive: true });
  check(fs.lstatSync(scratchRoot).isDirectory() && !fs.lstatSync(scratchRoot).isSymbolicLink(), 'scratch root must be a regular directory');
  const scratch = fs.mkdtempSync(path.join(scratchRoot, 'release-proxy-'));
  fs.chmodSync(scratch, 0o700);
  const logPath = path.join(scratch, 'caddy.log');
  const adaptedConfig = path.join(scratch, 'Caddyfile');
  const configEvidencePath = path.join(scratch, 'config-evidence.json');
  const log = fs.openSync(logPath, 'wx', 0o600);
  let loggedBytes = 0;
  const logChunk = (chunk) => {
    const remaining = 128 * 1024 - loggedBytes;
    if (remaining > 0) loggedBytes += fs.writeSync(log, chunk.subarray(0, remaining));
  };
  const { backend, sockets, count } = syntheticBackend();
  let child;
  let childClosed;
  let childError;
  let stopping;
  let configEvidence;
  const stop = ({ keepLog = false } = {}) => {
    if (stopping) return stopping;
    stopping = (async () => {
      process.off('SIGINT', interrupt); process.off('SIGTERM', terminate);
      if (child) {
        await new Promise((resolve, reject) => {
          const hardStop = setTimeout(() => child.kill('SIGKILL'), 2000);
          const deadline = setTimeout(() => reject(new Error('owned Caddy process did not exit')), 5000);
          childClosed.then(() => { clearTimeout(hardStop); clearTimeout(deadline); resolve(); });
          if (child.exitCode === null && child.signalCode === null) child.kill('SIGTERM');
        });
      }
      for (const socket of sockets) socket.destroy();
      if (backend.listening) await new Promise((resolve, reject) => backend.close((error) => error ? reject(error) : resolve()));
      fs.closeSync(log);
      const unchanged = createHash('sha256').update(fs.readFileSync(originalConfig)).digest('hex') === configHash;
      if (configEvidence) {
        configEvidence.original_unchanged_after_stop = unchanged;
        fs.writeFileSync(configEvidencePath, `${JSON.stringify(configEvidence, null, 2)}\n`, { mode: 0o600 });
      }
      if (!keepLog && unchanged) fs.rmSync(scratch, { recursive: true, force: false });
      check(unchanged, 'repository Caddyfile changed during smoke');
    })();
    return stopping;
  };
  const interrupt = () => { void stop().finally(() => process.exit(130)); };
  const terminate = () => { void stop().finally(() => process.exit(143)); };
  process.once('SIGINT', interrupt); process.once('SIGTERM', terminate);
  try {
    // This task-owned disposable copy changes only the listener default. All
    // route blocks and the original repository file remain byte-for-byte intact.
    fs.writeFileSync(adaptedConfig, adaptedConfigBytes, { flag: 'wx', mode: 0o600 });
    await new Promise((resolve, reject) => {
      backend.once('error', reject);
      backend.listen(0, host, () => { backend.off('error', reject); resolve(); });
    });
    const address = backend.address();
    check(address !== null && typeof address === 'object', 'synthetic backend did not bind a loopback port');
    const backendUpstream = `${host}:${address.port}`;
    const environment = {
      PATH: '/usr/bin:/bin', HOME: scratch, TMPDIR: scratch, XDG_CONFIG_HOME: scratch, XDG_DATA_HOME: scratch,
      CADDY_ACME_EMAIL: 'release@example.invalid', CADDY_ACME_CA: `http://${backendUpstream}/acme/directory`,
      CADDY_FRONTEND_UPSTREAM: frontend.host, CADDY_BACKEND_UPSTREAM: backendUpstream, CADDY_FILES_UPSTREAM: backendUpstream,
      TASK_COOKIE_UPSTREAM: backendUpstream, TASK_ARCHIVE_UPSTREAM: backendUpstream, TASK_SAMOVAR_UPSTREAM: backendUpstream,
      TASK_VKONTAKTE_UPSTREAM: backendUpstream, TASK_DEDYS_UPSTREAM: backendUpstream,
      ...Object.fromEntries(domainNames.map((name, index) => [name, `http://${host}:${4381 + index}`])),
    };
    const launch = (command) => {
      childError = undefined;
      child = spawn(binary, [command, '--config', adaptedConfig, '--adapter', 'caddyfile'], {
        cwd: scratch, env: environment, stdio: ['ignore', 'pipe', 'pipe'],
      });
      childClosed = new Promise((resolve) => child.once('close', resolve));
      child.stdout.on('data', logChunk); child.stderr.on('data', logChunk);
      child.once('error', (error) => { childError = error; });
    };
    launch('validate');
    await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error('Caddy config validation deadline exceeded')), 10_000);
      childClosed.then(() => { clearTimeout(timeout); resolve(); });
    });
    check(!childError && child.exitCode === 0, 'adapted Caddy config validation failed');
    check(createHash('sha256').update(fs.readFileSync(originalConfig)).digest('hex') === configHash,
      'repository Caddyfile changed before startup');
    check(createHash('sha256').update(fs.readFileSync(adaptedConfig)).digest('hex') === adaptedConfigHash,
      'adapted Caddy config changed during validation');
    configEvidence = {
      original_sha256: configHash, adapted_sha256: adaptedConfigHash,
      default_bind: host, validated: true, original_unchanged: true,
      original_unchanged_after_stop: null,
    };
    fs.writeFileSync(configEvidencePath, `${JSON.stringify(configEvidence, null, 2)}\n`, { flag: 'wx', mode: 0o600 });
    launch('run');
    const deadline = Date.now() + 15_000;
    let ready = false;
    while (Date.now() < deadline) {
      check(!childError && child.exitCode === null && child.signalCode === null, 'owned Caddy process failed during startup');
      try {
        ready = await localGet(`${releaseOrigins.api}/health`) === 200
          && await localGet(`${releaseOrigins.public}/health`) === 200;
      } catch { ready = false; }
      if (ready) break;
      await delay(100);
    }
    check(ready, 'Caddy loopback readiness deadline exceeded');
    return { origins: releaseOrigins, allOrigins: domainNames.map((_, index) => `http://${host}:${4381 + index}`),
      backendUrl: `http://${backendUpstream}`, configEvidence, count, stop };
  } catch (error) {
    await stop({ keepLog: true });
    throw new Error(`release proxy startup failed; bounded private Caddy log: ${logPath}`, { cause: error });
  }
}
