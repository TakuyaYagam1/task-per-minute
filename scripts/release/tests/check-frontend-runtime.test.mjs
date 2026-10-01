import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import * as fs from 'node:fs/promises';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import test from 'node:test';
import { checkRuntime, parseArguments } from '../check-frontend-runtime.mjs';

const run = promisify(execFile);
const checker = fileURLToPath(new URL('../check-frontend-runtime.mjs', import.meta.url));
const CSP = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self' data: https://fonts.gstatic.com; connect-src 'self'; worker-src 'self' blob:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'; object-src 'none'; report-uri /csp-report; upgrade-insecure-requests";
const HEADERS = {
  'content-security-policy': CSP,
  'strict-transport-security': 'max-age=63072000; includeSubDomains; preload',
  'x-frame-options': 'DENY',
  'x-content-type-options': 'nosniff',
  'referrer-policy': 'strict-origin-when-cross-origin',
  'permissions-policy': 'camera=(), microphone=(), geolocation=(), interest-cohort=()',
  'cross-origin-opener-policy': 'same-origin',
};

async function fixture(t, modify = () => {}, host = '127.0.0.1') {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'frontend-runtime-'));
  t.after(() => fs.rm(directory, { recursive: true, force: true }));
  const requests = [];
  const server = http.createServer((request, response) => {
    requests.push({ method: request.method, path: request.url });
    const health = request.url === '/health';
    const data = {
      status: 200,
      headers: { ...HEADERS, 'content-type': health ? 'application/json' : 'text/html; charset=utf-8' },
      body: health ? '{"status":"ok","detail":"body-marker"}' : '<!doctype html><html><body>body-marker</body></html>',
    };
    modify(data, request, response);
    if (data.hang) return;
    if (data.disconnect) { response.destroy(); return; }
    response.writeHead(data.status, data.headers);
    response.end(data.body);
  });
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, host, resolve);
  });
  t.after(() => new Promise((resolve) => {
    server.close(resolve);
    server.closeAllConnections();
  }));
  const address = host === '::1' ? '[::1]' : host;
  return { directory, output: path.join(directory, 'report.json'), url: `http://${address}:${server.address().port}`, requests };
}

async function absent(filename) {
  await assert.rejects(fs.lstat(filename), { code: 'ENOENT' });
}

test('checks only public GETs and publishes a minimal private PASS report', async (t) => {
  const f = await fixture(t);
  const result = await checkRuntime(f);
  assert.deepEqual(f.requests, [{ method: 'GET', path: '/health' }, { method: 'GET', path: '/' }]);
  assert.deepEqual(result, {
    schemaVersion: 1,
    status: 'pass',
    health: { status: 200, statusOk: true },
    page: { status: 200, html: true },
    policy: {
      csp: 'enforced', connectSrc: 'expected', frameOptions: 'deny', contentTypeOptions: 'nosniff',
      hsts: 'matched', referrerPolicy: 'matched', permissionsPolicy: 'matched', crossOriginOpenerPolicy: 'same-origin',
    },
  });
  const report = await fs.readFile(f.output, 'utf8');
  assert.deepEqual(JSON.parse(report), result);
  assert.doesNotMatch(report, /body-marker|127\.0\.0\.1|fonts\.gstatic|http:|headers/i);
  assert.equal((await fs.stat(f.output)).mode & 0o777, 0o600);
  assert.deepEqual(await fs.readdir(f.directory), ['report.json']);
});

test('CLI checks a literal IPv6 loopback target', async (t) => {
  const f = await fixture(t, undefined, '::1');
  const { stdout, stderr } = await run(process.execPath, [checker, '--url', f.url, '--output', f.output]);
  assert.equal(stdout.trim(), 'Frontend runtime check passed.');
  assert.equal(stderr, '');
  assert.equal(JSON.parse(await fs.readFile(f.output, 'utf8')).status, 'pass');
});

test('explicit public HTTPS origins allow only their matching WSS sources', async (t) => {
  const f = await fixture(t, (data) => {
    data.headers['content-security-policy'] = CSP.replace("connect-src 'self'", "connect-src 'self' https://api.example.test wss://api.example.test https://admin.example.test:8443 wss://admin.example.test:8443");
  });
  await checkRuntime({ ...f, apiOrigin: 'https://api.example.test', adminOrigin: 'https://admin.example.test:8443' });
  assert.equal(f.requests.length, 2);
  assert.doesNotMatch(await fs.readFile(f.output, 'utf8'), /example\.test/);
});

test('explicit HTTP origins are supported for literal loopback only', async (t) => {
  const f = await fixture(t, (data) => {
    data.headers['content-security-policy'] = CSP.replace("connect-src 'self'", "connect-src 'self' http://127.0.0.1:8080 ws://127.0.0.1:8080 http://[::1]:8081 ws://[::1]:8081");
  });
  await checkRuntime({ ...f, apiOrigin: 'http://127.0.0.1:8080', adminOrigin: 'http://[::1]:8081' });
  assert.deepEqual(f.requests, [{ method: 'GET', path: '/health' }, { method: 'GET', path: '/' }]);
});

test('identical explicit API and admin origins are one allowed source pair', async (t) => {
  const f = await fixture(t, (data) => {
    data.headers['content-security-policy'] = CSP.replace("connect-src 'self'", "connect-src 'self' https://api.example.test wss://api.example.test");
  });
  await checkRuntime({ ...f, apiOrigin: 'https://api.example.test', adminOrigin: 'https://api.example.test' });
});

const invalidPolicies = [
  ['missing CSP', () => undefined],
  ['empty CSP', () => ''],
  ['duplicate directive', (csp) => `${csp}; default-src 'self'`],
  ['mixed-case duplicate directive', (csp) => `${csp}; DEFAULT-SRC 'self'`],
  ['wildcard source', (csp) => csp.replace("img-src 'self'", "img-src * 'self'")],
  ['wildcard hostname', (csp) => csp.replace('https://fonts.gstatic.com', 'https://*.example.test')],
  ['unsafe eval', (csp) => csp.replace("script-src 'self'", "script-src 'self' 'unsafe-eval'")],
  ['unsafe wasm eval', (csp) => csp.replace("script-src 'self'", "script-src 'self' 'wasm-unsafe-eval'")],
  ['broad script HTTPS source', (csp) => csp.replace("script-src 'self' 'unsafe-inline'", "script-src 'self' https:")],
  ['script element override', (csp) => `${csp}; script-src-elem https://unexpected.example`],
  ['broad style HTTPS source', (csp) => csp.replace("style-src 'self'", "style-src 'self' https:")],
  ['broad image HTTPS source', (csp) => csp.replace("img-src 'self'", "img-src 'self' https:")],
  ['broad media HTTPS source', (csp) => csp.replace("media-src 'self'", "media-src 'self' https:")],
  ['unexpected font origin', (csp) => csp.replace('https://fonts.gstatic.com', 'https://unexpected.example')],
  ['broad worker HTTPS source', (csp) => csp.replace("worker-src 'self'", "worker-src 'self' https:")],
  ['missing style directive', (csp) => csp.replace("style-src 'self' 'unsafe-inline'; ", '')],
  ['unknown directive', (csp) => `${csp}; unrecognized-policy 'self'`],
  ['stale insecure API origin', (csp) => csp.replace("connect-src 'self'", "connect-src 'self' http://stale.example.test ws://stale.example.test")],
  ['connect scheme allowlist', (csp) => csp.replace("connect-src 'self'", "connect-src 'self' https: wss:")],
  ['missing connect self', (csp) => csp.replace("connect-src 'self'", 'connect-src')],
  ['duplicate connect self', (csp) => csp.replace("connect-src 'self'", "connect-src 'self' 'self'")],
  ['default source broadened', (csp) => csp.replace("default-src 'self'", "default-src 'self' https:")],
  ['frame ancestor broadened', (csp) => csp.replace("frame-ancestors 'none'", "frame-ancestors 'self'")],
  ['object source broadened', (csp) => csp.replace("object-src 'none'", "object-src 'self'")],
  ['base URI broadened', (csp) => csp.replace("base-uri 'self'", 'base-uri https:')],
  ['form action broadened', (csp) => csp.replace("form-action 'self'", 'form-action https:')],
  ['missing production upgrade', (csp) => csp.replace('; upgrade-insecure-requests', '')],
  ['invalid upgrade value', (csp) => `${csp} true`],
  ['multiple policies', (csp) => `${csp}, default-src 'self'`],
];
for (const [name, change] of invalidPolicies) {
  test(`refuses ${name} without writing a report`, async (t) => {
    const f = await fixture(t, (data) => {
      const csp = change(CSP);
      if (csp === undefined) delete data.headers['content-security-policy'];
      else data.headers['content-security-policy'] = csp;
    });
    await assert.rejects(checkRuntime(f), /policy|CSP/);
    await absent(f.output);
  });
}

test('report-only is rejected even alongside enforced CSP', async (t) => {
  const f = await fixture(t, (data) => { data.headers['content-security-policy-report-only'] = CSP; });
  await assert.rejects(checkRuntime(f), /report-only/);
  await absent(f.output);
});

test('a healthy endpoint cannot hide missing enforced CSP on the page', async (t) => {
  const f = await fixture(t, (data, request) => {
    if (request.url === '/') delete data.headers['content-security-policy'];
  });
  await assert.rejects(checkRuntime(f), /CSP/);
  assert.equal(f.requests.length, 2);
  await absent(f.output);
});

test('a healthy endpoint cannot hide unexpected page connection origins', async (t) => {
  const f = await fixture(t, (data, request) => {
    if (request.url === '/') data.headers['content-security-policy'] = CSP.replace("connect-src 'self'", "connect-src 'self' https://stale.example.test wss://stale.example.test");
  });
  await assert.rejects(checkRuntime(f), /connect-src/);
  await absent(f.output);
});

test('expected explicit origin cannot be missing from connect-src', async (t) => {
  const f = await fixture(t);
  await assert.rejects(checkRuntime({ ...f, apiOrigin: 'https://api.example.test' }), /connect-src/);
  await absent(f.output);
});

for (const name of Object.keys(HEADERS).filter((name) => name !== 'content-security-policy')) {
  for (const missing of [true, false]) {
    test(`refuses ${missing ? 'missing' : 'incorrect'} ${name}`, async (t) => {
      const f = await fixture(t, (data, request) => {
        if (request.url !== '/') return;
        if (missing) delete data.headers[name];
        else data.headers[name] = 'unexpected';
      });
      await assert.rejects(checkRuntime(f), /security header/);
      await absent(f.output);
    });
  }
}

for (const [name, change] of [
  ['health status', (data) => { data.status = 503; }],
  ['health value', (data) => { data.body = '{"status":"not_ready"}'; }],
  ['health JSON', (data) => { data.body = 'body-marker'; }],
  ['health content type', (data) => { data.headers['content-type'] = 'text/html'; }],
  ['health oversized body', (data) => { data.body = 'a'.repeat(64 * 1024 + 1); }],
]) {
  test(`refuses wrong ${name}`, async (t) => {
    const f = await fixture(t, (data, request) => { if (request.url === '/health') change(data); });
    await assert.rejects(checkRuntime(f), /health|body limit/);
    assert.equal(f.requests.length, 1);
    await absent(f.output);
  });
}

for (const [name, change] of [
  ['page status', (data) => { data.status = 500; }],
  ['page content type', (data) => { data.headers['content-type'] = 'application/json'; }],
  ['page body', (data) => { data.body = '{"not":"html"}'; }],
  ['page oversized body', (data) => { data.body = 'a'.repeat(2 * 1024 * 1024 + 1); }],
]) {
  test(`refuses wrong ${name}`, async (t) => {
    const f = await fixture(t, (data, request) => { if (request.url === '/') change(data); });
    await assert.rejects(checkRuntime(f), /page|body limit/);
    await absent(f.output);
  });
}

for (const endpoint of ['/health', '/']) {
  test(`does not follow ${endpoint} redirects`, async (t) => {
    const destination = await fixture(t);
    const f = await fixture(t, (data, request) => {
      if (request.url === endpoint) {
        data.status = 302;
        data.headers.location = `${destination.url}/private-marker`;
      }
    });
    await assert.rejects(checkRuntime(f), /status/);
    assert.equal(destination.requests.length, 0);
    await absent(f.output);
  });
}

test('fetch errors are sanitized and never publish a report', async (t) => {
  const f = await fixture(t, (data) => { data.disconnect = true; });
  await assert.rejects(checkRuntime(f), (error) => {
    assert.match(error.message, /request failed/);
    assert.doesNotMatch(error.message, /127\.0\.0\.1|body-marker/);
    return true;
  });
  await absent(f.output);
});

for (const bodyStarted of [false, true]) {
  test(`timeout covers ${bodyStarted ? 'response body' : 'response headers'}`, async (t) => {
    const f = await fixture(t, (data, _request, response) => {
      data.hang = true;
      if (bodyStarted) { response.writeHead(200, data.headers); response.write('{'); }
    });
    await assert.rejects(checkRuntime({ ...f, timeoutMs: 100 }), /timed out/);
    await absent(f.output);
  });
}

test('rejects nonliteral or decorated target URLs before any request', async (t) => {
  const f = await fixture(t);
  const port = new URL(f.url).port;
  for (const url of [
    'https://example.test', 'http://localhost', `http://127.1:${port}`,
    `http://2130706433:${port}`, `http://0x7f000001:${port}`, 'http://[::ffff:127.0.0.1]',
    `${f.url}/health`, `${f.url}/./`, `${f.url}/%2e`, `${f.url}?`, `${f.url}#`, `${f.url}/?query=value`,
    `http://user@127.0.0.1:${port}`, `http://@127.0.0.1:${port}`, `${f.url}\n`, ` ${f.url}`,
    'http://127.0.0.1:0', 'http://127.0.0.1:65536', 'http://127.0.0.1:00080',
  ]) await assert.rejects(checkRuntime({ ...f, url }), /loopback URL/);
  assert.equal(f.requests.length, 0);
  await absent(f.output);
});

test('rejects malformed public origins without network or output', async (t) => {
  const f = await fixture(t);
  for (const apiOrigin of [
    '', 'ws://api.example.test', 'https://api.example.test/path', 'https://api.example.test?query=value',
    'https://user:password@api.example.test', 'https://api.example.test#', 'https://*.example.test',
    'https://api.example.test\n', 'https://api.example.test/./',
    'http://admin.example.test:8080', 'http://localhost:8080', 'http://127.1:8080',
  ]) await assert.rejects(checkRuntime({ ...f, apiOrigin }), /public origin/);
  assert.equal(f.requests.length, 0);
  await absent(f.output);
});

test('rejects relative output, traversal and nonexistent parent before requests', async (t) => {
  const f = await fixture(t);
  for (const output of ['report.json', `${f.directory}/../report.json`, `${f.directory}/missing/report.json`]) {
    await assert.rejects(checkRuntime({ ...f, output }));
  }
  assert.equal(f.requests.length, 0);
});

test('never overwrites an existing report or follows output symlinks', async (t) => {
  const f = await fixture(t);
  const existing = path.join(f.directory, 'existing.json');
  await fs.writeFile(existing, 'unchanged');
  await assert.rejects(checkRuntime({ ...f, output: existing }), /exists/);
  await fs.symlink(existing, f.output);
  await assert.rejects(checkRuntime(f), /exists|symlink/);
  assert.equal(await fs.readFile(existing, 'utf8'), 'unchanged');
  assert.equal(f.requests.length, 0);
});

test('rejects symlink parent and dangling symlink output', async (t) => {
  const f = await fixture(t);
  const linked = path.join(f.directory, 'linked');
  await fs.symlink(f.directory, linked);
  await assert.rejects(checkRuntime({ ...f, output: path.join(linked, 'report.json') }), /symlink/);
  await fs.symlink(path.join(f.directory, 'missing'), f.output);
  await assert.rejects(checkRuntime(f), /exists|symlink/);
  assert.equal(f.requests.length, 0);
});

test('a report created during requests is preserved', async (t) => {
  const f = await fixture(t);
  // The second live server holds its response until the other owner has written.
  const blocker = await fixture(t, (data, request, response) => {
    if (request.url !== '/') return;
    data.hang = true;
    fs.writeFile(f.output, 'other owner', { flag: 'wx' }).then(() => {
      response.writeHead(data.status, data.headers);
      response.end(data.body);
    }).catch((error) => response.destroy(error));
  });
  await assert.rejects(checkRuntime({ url: blocker.url, output: f.output }), /exists/);
  assert.equal(await fs.readFile(f.output, 'utf8'), 'other owner');
});

test('CLI accepts only the documented unique options', () => {
  assert.deepEqual(parseArguments(['--url', 'http://127.0.0.1:3000', '--output', '/work/report.json', '--api-origin', 'https://api.example.test', '--admin-origin', 'https://admin.example.test']), {
    url: 'http://127.0.0.1:3000', output: '/work/report.json', apiOrigin: 'https://api.example.test', adminOrigin: 'https://admin.example.test',
  });
  for (const args of [[], ['--url'], ['--url', 'http://127.0.0.1'], ['--output', '/work/report.json'], ['--unknown', 'x'], ['--url', 'a', '--url', 'b'], ['--output', '--url']]) {
    assert.throws(() => parseArguments(args), /option|required/);
  }
});

test('CLI failure is nonzero, sanitized and does not write a report', async (t) => {
  const f = await fixture(t, (data) => { data.body = 'body-marker'; });
  await assert.rejects(run(process.execPath, [checker, '--url', f.url, '--output', f.output]), (error) => {
    assert.equal(error.code, 1);
    assert.equal(error.stdout, '');
    assert.match(error.stderr, /health/);
    assert.doesNotMatch(error.stderr, /body-marker|127\.0\.0\.1|at checkRuntime/);
    return true;
  });
  await absent(f.output);
});
