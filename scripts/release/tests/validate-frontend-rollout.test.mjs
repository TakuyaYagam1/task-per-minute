import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { validateRollout, parseArguments } from '../validate-frontend-rollout.mjs';

const cli = fileURLToPath(new URL('../validate-frontend-rollout.mjs', import.meta.url));
const front = `ghcr.io/example/task-per-minute-frontend@sha256:${'a'.repeat(64)}`;
const back = `ghcr.io/example/task-per-minute-backend@sha256:${'b'.repeat(64)}`;
const previous = `ghcr.io/example/task-per-minute-frontend@sha256:${'c'.repeat(64)}`;
const deny = (route) => `handle ${route} {\n respond 404\n}\n`;
const internalDeny = deny('/internal') + deny('/internal/*');
const proxy = (route, upstream) => `handle${route ? ` ${route}` : ''} {\n reverse_proxy ${upstream}\n}\n`;
const site = (address, body) => `${address} {\n import common_headers\n request_body {\n max_size 125MB\n }\n${body}}\n`;

function fixture() {
  const plan = {
    schema_version: 1, phase: 'rollout', source_revision: '1'.repeat(40), frontend_image: front, backend_image: back,
    public_origin: 'https://play.example.org', admin_origin: 'https://admin.example.org', api_origin: '', admin_api_origin: '',
    public_build_args: { NEXT_PUBLIC_API_URL: '', NEXT_PUBLIC_ADMIN_API_URL: '' },
    current_schema: 29, frontend_schema_min: 28, frontend_schema_max: 30,
    rollback: { frontend_image: previous, frontend_schema_min: 29, frontend_schema_max: 29 },
  };
  const health = { test: ['CMD', 'wget', '-q', 'http://localhost/health'] };
  const compose = { services: {
    backend: { image: back, expose: ['8080'], healthcheck: structuredClone(health),
      environment: { HTTP_PORT: '8080', HTTP_ALLOWED_ORIGINS: '', WS_ALLOWED_ORIGINS: '', WS_REQUIRE_ORIGIN: 'true',
        HTTP_TRUSTED_PROXY_CIDRS: '172.30.0.0/24', DB_DSN: 'SYNTHETIC_PRIVATE_VALUE' } },
    frontend: { image: front, expose: ['3000'], healthcheck: structuredClone(health),
      environment: { PORT: '3000', BACKEND_URL: 'http://backend:8080' },
      depends_on: { backend: { condition: 'service_healthy' } } },
    caddy: { image: 'caddy:2.11.4-alpine@sha256:6aeddd44c3078b0f9a35206472a11420648a79c184603ef95957d0a20044cb2b',
      healthcheck: { test: ['CMD-SHELL', 'wget -qO- http://127.0.0.1:2019/config/'] },
      depends_on: { backend: { condition: 'service_healthy' }, frontend: { condition: 'service_healthy' } },
      environment: { APP_DOMAIN: 'play.example.org', ADMIN_DOMAIN: 'admin.example.org', API_DOMAIN: 'api.example.org',
        CADDY_FRONTEND_UPSTREAM: 'frontend:3000', CADDY_BACKEND_UPSTREAM: 'backend:8080' } },
  } };
  const caddyfile = '{\n admin localhost:2019\n}\n' +
    '(common_headers) {\n encode gzip zstd\n header {\n' +
    ' >Strict-Transport-Security "max-age=63072000; includeSubDomains; preload"\n >X-Content-Type-Options "nosniff"\n >Referrer-Policy "strict-origin-when-cross-origin"\n }\n}\n' +
    site('{$APP_DOMAIN}', deny('/admin') + deny('/admin/*') + deny('/api/v1/admin') + deny('/api/v1/admin/*') + internalDeny +
      proxy('/api/*', '{$CADDY_BACKEND_UPSTREAM}') + proxy('', '{$CADDY_FRONTEND_UPSTREAM}')) +
    site('{$ADMIN_DOMAIN}', 'handle / {\n redir * /admin 302\n}\n' + internalDeny + proxy('/api/*', '{$CADDY_BACKEND_UPSTREAM}') + proxy('', '{$CADDY_FRONTEND_UPSTREAM}')) +
    site('{$API_DOMAIN}', internalDeny + proxy('', '{$CADDY_BACKEND_UPSTREAM}'));
  return { plan, compose, caddyfile };
}

function direct(f) {
  f.plan.api_origin = 'https://api.example.org';
  f.plan.admin_api_origin = 'https://api.example.org';
  f.plan.public_build_args = { NEXT_PUBLIC_API_URL: f.plan.api_origin, NEXT_PUBLIC_ADMIN_API_URL: f.plan.admin_api_origin };
  f.compose.services.backend.environment.HTTP_ALLOWED_ORIGINS = 'https://play.example.org,https://admin.example.org';
  f.compose.services.backend.environment.WS_ALLOWED_ORIGINS = 'https://admin.example.org,https://play.example.org';
  return f;
}

test('same-origin config validates without claiming deployment or signature verification', () => {
  const f = fixture(); const before = structuredClone(f); const result = validateRollout(f);
  assert.equal(result.status, 'pass');
  assert.equal(result.configuration_validated, true);
  assert.equal(result.deployment_verified, false);
  assert.equal(result.signature_verified, false);
  assert.equal(result.source_revision, '1'.repeat(40));
  assert.equal(result.frontend_image, front);
  assert.equal(result.current_schema, 29);
  assert.equal(JSON.stringify(result).includes('SYNTHETIC_PRIVATE_VALUE'), false);
  assert.deepEqual(f, before, 'pure API must not mutate inputs');
});

test('direct API with explicit public/admin Origins and recovery phase validates', () => {
  const f = direct(fixture()); f.plan.phase = 'recovery'; f.compose.services.backend.deploy = { replicas: 1 };
  assert.equal(validateRollout(f).phase, 'recovery');
});

test('separate explicit admin API can use the admin host but never the public host', () => {
  const f = direct(fixture());
  f.plan.admin_api_origin = f.plan.admin_origin;
  f.plan.public_build_args.NEXT_PUBLIC_ADMIN_API_URL = f.plan.admin_origin;
  assert.equal(validateRollout(f).status, 'pass');
  f.plan.admin_api_origin = f.plan.public_origin;
  f.plan.public_build_args.NEXT_PUBLIC_ADMIN_API_URL = f.plan.public_origin;
  assert.throws(() => validateRollout(f), /admin_api_origin|Caddy/);
});

test('optional source-file origins permit only empty or explicit canonical HTTPS lists', () => {
  for (const value of ['', 'https://files.example.org', 'https://files.example.org,https://archive.example.org']) {
    const f = fixture(); f.plan.public_build_args.NEXT_PUBLIC_SOURCE_FILE_ORIGINS = value;
    assert.equal(validateRollout(f).status, 'pass');
  }
  for (const value of ['*', 'https://u:p@files.example.org', 'https://files.example.org/path', 'http://files.example.org']) {
    const f = fixture(); f.plan.public_build_args.NEXT_PUBLIC_SOURCE_FILE_ORIGINS = value;
    assert.throws(() => validateRollout(f), /SOURCE_FILE_ORIGINS/);
  }
});

test('WS Origin is mandatory and Caddy proxy trust requires explicit non-default CIDRs', () => {
  for (const changes of [{ WS_REQUIRE_ORIGIN: 'false' }, { WS_REQUIRE_ORIGIN: undefined },
    { HTTP_TRUSTED_PROXY_CIDRS: '' }, { HTTP_TRUSTED_PROXY_CIDRS: '0.0.0.0/0' },
    { HTTP_TRUSTED_PROXY_CIDRS: '::/0' }, { HTTP_TRUSTED_PROXY_CIDRS: '172.30.0.0/24,::/0' },
    { HTTP_TRUSTED_PROXY_CIDRS: 'not-a-network' }]) {
    const f = fixture(); Object.assign(f.compose.services.backend.environment, changes);
    assert.throws(() => validateRollout(f), /WS_REQUIRE_ORIGIN|HTTP_TRUSTED_PROXY_CIDRS/);
  }
});

test('the current nine-site Caddyfile accepts synthetic public, file and task mappings', () => {
  const f = fixture();
  const ce = f.compose.services.caddy.environment;
  Object.assign(ce, { FILES_DOMAIN: 'files.example.org', CADDY_FILES_UPSTREAM: 'seaweedfs:8333' });
  for (const name of ['COOKIE', 'ARCHIVE', 'SAMOVAR', 'VKONTAKTE', 'DEDYS']) {
    ce[`TASK_${name}_DOMAIN`] = `${name.toLowerCase()}.example.org`;
    ce[`TASK_${name}_UPSTREAM`] = 'frontend:3000';
  }
  f.caddyfile = fs.readFileSync(new URL('../../../deployment/caddy/Caddyfile', import.meta.url), 'utf8');
  assert.equal(validateRollout(f).status, 'pass');
  f.caddyfile = f.caddyfile.replace('{$FILES_DOMAIN}', '{$APP_DOMAIN}');
  assert.throws(() => validateRollout(f), /Caddy/);
});

test('custom internal ports must agree across expose, runtime and both Caddy upstreams', () => {
  const f = fixture();
  f.compose.services.frontend.environment.PORT = '3100'; f.compose.services.frontend.expose = ['3100/tcp'];
  f.compose.services.backend.environment.HTTP_PORT = '8180'; f.compose.services.backend.expose = [8180];
  f.compose.services.frontend.environment.BACKEND_URL = 'http://backend:8180';
  f.compose.services.caddy.environment.CADDY_FRONTEND_UPSTREAM = 'frontend:3100';
  f.compose.services.caddy.environment.CADDY_BACKEND_UPSTREAM = 'backend:8180';
  assert.equal(validateRollout(f).status, 'pass');
});

test('mutable, noncanonical or mismatched images and malformed source revision are denied', () => {
  for (const mutate of [
    (f) => { f.plan.frontend_image = 'ghcr.io/example/task-per-minute-frontend:latest'; },
    (f) => { f.plan.backend_image = `backend@sha256:${'b'.repeat(64)}`; },
    (f) => { f.compose.services.frontend.image = previous; },
    (f) => { f.compose.services.backend.image = `ghcr.io/example/backend@sha256:${'d'.repeat(64)}`; },
    (f) => { f.plan.source_revision = 'main'; },
  ]) { const f = fixture(); mutate(f); assert.throws(() => validateRollout(f), /image|revision/); }
});

test('frontend and backend cannot rebuild, publish ports or use host networking', () => {
  for (const service of ['frontend', 'backend']) {
    for (const changes of [{ build: { context: '.' } }, { ports: ['8080:8080'] }, { network_mode: 'host' }]) {
      const f = fixture(); Object.assign(f.compose.services[service], changes);
      assert.throws(() => validateRollout(f), /build|ports|network/);
    }
  }
});

test('Caddy image must retain the reviewed release pin without a build override', () => {
  for (const changes of [{ image: 'caddy:2-alpine' }, { image: `caddy:2.11.4-alpine@sha256:${'0'.repeat(64)}` },
    { build: { context: '.' } }]) {
    const f = fixture(); Object.assign(f.compose.services.caddy, changes);
    assert.throws(() => validateRollout(f), /Caddy requires/);
  }
});

test('backend process-local realtime cannot be scaled through replicas, scale or global mode', () => {
  for (const changes of [{ deploy: { replicas: 2 } }, { scale: 2 }, { deploy: { mode: 'global' } }, { deploy: { replicas: '1' } }]) {
    const f = fixture(); Object.assign(f.compose.services.backend, changes);
    assert.throws(() => validateRollout(f), /replica|scale|mode/);
  }
});

test('all three healthchecks and healthy dependency conditions are required', () => {
  for (const service of ['frontend', 'backend', 'caddy']) {
    for (const healthcheck of [undefined, { disable: true, test: ['CMD', 'true'] }, { test: ['NONE'] }, { test: [] }]) {
      const f = fixture(); f.compose.services[service].healthcheck = healthcheck;
      assert.throws(() => validateRollout(f), /healthcheck/);
    }
  }
  for (const service of ['backend', 'frontend']) {
    const f = fixture(); f.compose.services.caddy.depends_on[service].condition = 'service_started';
    assert.throws(() => validateRollout(f), /depends_on/);
  }
});

test('public/admin origins are distinct canonical HTTPS origins without credentials or path tricks', () => {
  for (const origin of ['http://play.example.org', 'https://u:p@play.example.org', 'https://play.example.org/?x=1',
    'https://play.example.org/#x', 'https://play.example.org/path', 'https://play.example.org/../',
    'https://PLAY.example.org', 'https://play.example.org:443', 'https://play.example.org\\evil', 'https://admin.example.org']) {
    const f = fixture(); f.plan.public_origin = origin; assert.throws(() => validateRollout(f), /origin/);
  }
});

test('API paths, ambiguous admin fallback and mismatched build/runtime API declarations are denied', () => {
  for (const mutate of [
    (f) => { f.plan.api_origin += '/api/v1'; },
    (f) => { f.plan.admin_api_origin = ''; f.plan.public_build_args.NEXT_PUBLIC_ADMIN_API_URL = ''; },
    (f) => { f.plan.public_build_args.NEXT_PUBLIC_API_URL = 'https://other.example.org'; },
    (f) => { f.compose.services.frontend.environment.NEXT_PUBLIC_API_URL = 'https://other.example.org'; },
    (f) => { f.plan.public_build_args.JWT_SECRET = 'SYNTHETIC_PRIVATE_VALUE'; },
  ]) { const f = direct(fixture()); mutate(f); assert.throws(() => validateRollout(f), /origin|build|API/); }
});

test('direct-mode HTTP and WS lists require exact origins, WS subset and both browser origins', () => {
  for (const [key, value] of [
    ['HTTP_ALLOWED_ORIGINS', '*'], ['HTTP_ALLOWED_ORIGINS', 'https://u:p@play.example.org'],
    ['HTTP_ALLOWED_ORIGINS', 'https://play.example.org/path'], ['WS_ALLOWED_ORIGINS', 'https://other.example.org'],
    ['WS_ALLOWED_ORIGINS', 'https://play.example.org'], ['HTTP_ALLOWED_ORIGINS', ''],
    ['HTTP_ALLOWED_ORIGINS', 'https://play.example.org,https://play.example.org'],
  ]) {
    const f = direct(fixture()); f.compose.services.backend.environment[key] = value;
    assert.throws(() => validateRollout(f), /ORIGINS/);
  }
});

test('Caddy origins and upstreams must match plan and actual internal ports', () => {
  for (const mutate of [
    (f) => { f.compose.services.caddy.environment.APP_DOMAIN = 'other.example.org'; },
    (f) => { f.compose.services.caddy.environment.ADMIN_DOMAIN = 'play.example.org'; },
    (f) => { f.compose.services.caddy.environment.CADDY_FRONTEND_UPSTREAM = 'frontend:3001'; },
    (f) => { f.compose.services.caddy.environment.CADDY_BACKEND_UPSTREAM = 'external:8080'; },
    (f) => { f.compose.services.frontend.environment.BACKEND_URL = 'http://backend:8080/prefix'; },
    (f) => { f.compose.services.frontend.expose = ['3999']; },
    (f) => { f.plan.api_origin = 'https://external.example.org'; f.plan.public_build_args.NEXT_PUBLIC_API_URL = f.plan.api_origin; },
  ]) { const f = direct(fixture()); mutate(f); assert.throws(() => validateRollout(f), /Caddy|upstream|PORT|expose|BACKEND_URL|origin/); }
});

test('public admin and internal deny routes must remain active handlers, not comments', () => {
  for (const route of ['/admin', '/admin/*', '/api/v1/admin', '/api/v1/admin/*', '/internal', '/internal/*']) {
    const f = fixture(); f.caddyfile = f.caddyfile.replace(deny(route), `# removed ${route}\n`);
    assert.throws(() => validateRollout(f), /Caddy/);
  }
  const f = fixture(); f.caddyfile = f.caddyfile.replace('respond 404', 'respond 200');
  assert.throws(() => validateRollout(f), /Caddy/);
});

test('path stripping, forwarded URI overrides, imports and injected routing cannot bypass the gate', () => {
  for (const mutate of [
    (s) => s.replace('handle /api/*', 'handle_path /api/*'),
    (s) => s.replace('reverse_proxy {$CADDY_BACKEND_UPSTREAM}', 'reverse_proxy {$CADDY_BACKEND_UPSTREAM} {\n header_up X-Forwarded-Uri /admin\n }'),
    (s) => s + '\nimport /tmp/external.conf\n',
    (s) => s.replace('import common_headers', 'route {\n reverse_proxy attacker:8080\n }\n import common_headers'),
    (s) => s.replace('{$APP_DOMAIN}', '{$UNREVIEWED_DOMAIN}'),
  ]) { const f = fixture(); f.caddyfile = mutate(f.caddyfile); assert.throws(() => validateRollout(f), /Caddy/); }
});

test('literal HTTPS Caddy site addresses preserve the same route contract', () => {
  const f = fixture(); f.caddyfile = f.caddyfile.replace('{$APP_DOMAIN}', 'https://play.example.org')
    .replace('{$ADMIN_DOMAIN}', 'https://admin.example.org').replace('{$API_DOMAIN}', 'https://api.example.org');
  assert.equal(validateRollout(f).status, 'pass');
});

test('edge security headers must be deferred to replace upstream duplicates', () => {
  for (const name of ['Strict-Transport-Security', 'X-Content-Type-Options', 'Referrer-Policy']) {
    const f = fixture(); f.caddyfile = f.caddyfile.replace(`>${name}`, name);
    assert.throws(() => validateRollout(f), /Caddy common headers/);
  }
});

test('current and rollback schema ranges must cover the current positive schema with distinct pinned image', () => {
  for (const mutate of [
    (f) => { f.plan.current_schema = 0; }, (f) => { f.plan.frontend_schema_min = 30; },
    (f) => { f.plan.frontend_schema_max = 28; }, (f) => { f.plan.rollback.frontend_schema_max = 28; },
    (f) => { f.plan.rollback.frontend_image = front; },
    (f) => { f.plan.rollback.frontend_image = previous.replace('example/', 'other/'); },
    (f) => { delete f.plan.rollback.frontend_schema_min; }, (f) => { delete f.plan.rollback; },
  ]) { const f = fixture(); mutate(f); assert.throws(() => validateRollout(f), /schema|rollback|plan fields/); }
});

test('unknown plan keys, invalid phases and non-rendered environment shapes fail without value disclosure', () => {
  for (const mutate of [
    (f) => { f.plan.phase = 'deploy'; }, (f) => { f.plan.DB_DSN = 'SYNTHETIC_PRIVATE_VALUE'; },
    (f) => { f.compose.services.backend.environment = ['HTTP_PORT=8080']; },
  ]) {
    const f = fixture(); mutate(f);
    assert.throws(() => validateRollout(f), (error) => !error.message.includes('SYNTHETIC_PRIVATE_VALUE'));
  }
});

function files(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'frontend-rollout-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const f = fixture();
  const names = { plan: path.join(root, 'plan.json'), compose: path.join(root, 'compose.json'), caddyfile: path.join(root, 'Caddyfile'), output: path.join(root, 'report.json') };
  fs.writeFileSync(names.plan, JSON.stringify(f.plan)); fs.writeFileSync(names.compose, JSON.stringify(f.compose)); fs.writeFileSync(names.caddyfile, f.caddyfile);
  const run = (overrides = {}, extra = []) => spawnSync(process.execPath, [cli,
    ...Object.entries({ ...names, ...overrides }).flatMap(([name, value]) => [`--${name}`, value]), ...extra],
  { encoding: 'utf8', timeout: 5000, env: { PATH: '/usr/bin:/bin', APP_DOMAIN: 'SYNTHETIC_INHERITED_VALUE' } });
  return { root, f, names, run };
}

test('offline CLI publishes a private new report and hashes inputs without reading inherited env', (t) => {
  const f = files(t); const result = f.run();
  assert.equal(result.status, 0, result.stderr);
  const report = JSON.parse(fs.readFileSync(f.names.output));
  assert.equal(report.configuration_validated, true);
  assert.equal(report.deployment_verified, false);
  assert.match(report.inputs_sha256.compose, /^[a-f0-9]{64}$/);
  assert.equal(fs.statSync(f.names.output).mode & 0o777, 0o600);
  assert.equal((result.stdout + result.stderr + JSON.stringify(report)).includes('SYNTHETIC_'), false);
});

test('CLI refuses malformed JSON without raw parse errors or copied environment values', (t) => {
  const f = files(t); fs.writeFileSync(f.names.compose, '{"DB_DSN":"SYNTHETIC_PRIVATE_VALUE",broken');
  const result = f.run(); assert.equal(result.status, 1);
  assert.equal((result.stdout + result.stderr).includes('SYNTHETIC_PRIVATE_VALUE'), false);
  assert.equal(fs.existsSync(f.names.output), false);
});

test('CLI refuses input symlinks, traversal and oversized inputs', (t) => {
  for (const name of ['plan', 'compose', 'caddyfile']) {
    const f = files(t); const alias = path.join(f.root, 'alias'); fs.symlinkSync(f.names[name], alias);
    assert.equal(f.run({ [name]: alias }).status, 1); assert.equal(fs.existsSync(f.names.output), false);
  }
  const f = files(t); assert.equal(f.run({ output: `${f.root}/../unexpected.json` }).status, 1);
  fs.writeFileSync(f.names.plan, Buffer.alloc(32 * 1024 + 1)); assert.equal(f.run().status, 1);
});

test('CLI refuses existing files, symlink outputs and unknown or duplicate flags', (t) => {
  for (const mode of ['existing', 'symlink']) {
    const f = files(t); const saved = path.join(f.root, 'saved'); fs.writeFileSync(saved, 'preserve');
    if (mode === 'existing') fs.writeFileSync(f.names.output, 'preserve'); else fs.symlinkSync(saved, f.names.output);
    assert.equal(f.run().status, 1); assert.equal(fs.readFileSync(f.names.output, 'utf8'), 'preserve');
  }
  const f = files(t);
  for (const flag of ['--plan', '--config', '--deploy']) assert.equal(f.run({}, [flag, 'untrusted']).status, 1);
  // Exercise parser rejection directly: Node itself consumes --env-file even
  // after the script name, before the CLI gets an opportunity to reject it.
  assert.throws(() => parseArguments(['--env-file', 'untrusted']), /unknown/);
  assert.equal(fs.existsSync(f.names.output), false);
});
